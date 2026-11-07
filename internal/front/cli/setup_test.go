package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// runningTimer is a systemd whose timer runs already, its next pass at next.
type runningTimer struct {
	timers.Systemd
	next time.Time
}

func (r runningTimer) Status() timers.TimerStatus {
	return timers.TimerStatus{Installed: true, Enabled: true, Active: true, Next: r.next}
}

func TestSetupNamesTheRunningTimersPassWhenItComesFirst(t *testing.T) {
	// Opened where the timers run already, the screen's first pass is the
	// timer's next one when that comes before the ten minutes an install
	// gives: that pass runs with the settings as they are, and an install
	// does not always move it.
	a := fakeApp(t, config.MapEnv{"HOME": t.TempDir()})
	soon := time.Now().Add(3 * time.Minute)
	a.System = runningTimer{next: soon}
	if got := firstPassAt(a); !got.Equal(soon) {
		t.Errorf("first pass %s, want the timer's %s", got.Format("15:04:05"), soon.Format("15:04:05"))
	}
	// A pass further off than an install's first does not move it later.
	a.System = runningTimer{next: time.Now().Add(40 * time.Minute)}
	if got := time.Until(firstPassAt(a)); got > timers.FirstPassDelay || got < timers.FirstPassDelay-time.Minute {
		t.Errorf("first pass in %v, want %v", got, timers.FirstPassDelay)
	}
}

func TestHookForOffersTheShellsFileOnce(t *testing.T) {
	home := t.TempDir()
	file, line := hookFor("/bin/bash", home)
	if file != filepath.Join(home, ".bashrc") || line == "" {
		t.Fatalf("bash: %q %q", file, line)
	}
	if f, _ := hookFor("/usr/bin/tcsh", home); f != "" {
		t.Errorf("tcsh offered %q", f)
	}
	// A file without a final newline gets the hook on a line of its own.
	os.WriteFile(file, []byte("alias ll='ls -l'"), 0o644)
	if err := appendLine(file, line); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	if string(b) != "alias ll='ls -l'\n"+line+"\n" {
		t.Errorf("file:\n%s", b)
	}
	if f, _ := hookFor("/bin/bash", home); f != "" {
		t.Error("offered again once the hook is there")
	}
	// fish's file is made with its directory.
	file, line = hookFor("/usr/bin/fish", home)
	if err := appendLine(file, line); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "fish", "config.fish")); string(b) != line+"\n" {
		t.Errorf("fish: %q", b)
	}
}
