package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func TestCtrlCOnTheSetupScreenEndsAsAnInterrupt(t *testing.T) {
	home := isolate(t)
	env := config.MapEnv{"HOME": home}
	a := fakeApp(t, env)
	// The screen answers ctrl+c.
	defer func(f func(tui.SetupStart) (tui.SetupChoice, tui.SetupOutcome, error)) { runSetup = f }(runSetup)
	runSetup = func(tui.SetupStart) (tui.SetupChoice, tui.SetupOutcome, error) {
		return tui.SetupChoice{}, tui.SetupQuit, nil
	}
	for _, installer := range []bool{true, false} {
		cmd, out := output()
		err := setup(cmd, a, installer)
		// Opened by an install script, too: a status of 0 ended the script as
		// a finished install, with no timer running.
		if !errors.Is(err, errInterrupted) || !strings.HasPrefix(out.String(), "Nothing changed.") {
			t.Errorf("installer %v: %v\n%s", installer, err, out)
		}
		var w strings.Builder
		if got := report(err, &w); got != 130 || w.Len() != 0 {
			t.Errorf("installer %v: status %d, printed %q", installer, got, w.String())
		}
	}
	// Nothing was set up: no settings file, no timer.
	for _, p := range []string{config.ConfigFile(env), filepath.Join(config.UnitDir(env), timers.TimerName)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written", p)
		}
	}
}

func TestReportSaysWhatAnErrorMeans(t *testing.T) {
	isolate(t)
	for _, c := range []struct {
		err    error
		status int
		said   string
	}{
		{nil, 0, ""},
		{errSilentExit, 1, ""},
		// The engine's English words for a missing zellij are not shown: the
		// screen's language, and what to do.
		{reaper.ErrNoZellij, 1, "zellij-reaper: zellij was not found in PATH\nInstall zellij, or name it in the settings: zellij = \"/path/to/zellij\" (zellij-reaper config edit).\n"},
		{errors.New("unknown flag: --nope"), 1, "zellij-reaper: unknown flag: --nope\nRun `zellij-reaper --help` for the commands.\n"},
	} {
		var w strings.Builder
		if got := report(c.err, &w); got != c.status || w.String() != c.said {
			t.Errorf("%v: status %d, said %q", c.err, got, w.String())
		}
	}
}

func TestSetupTakesItsChoicesAsFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the timer here is systemd's and the hook bash's, which Windows does not have")
	}
	home := isolate(t)
	t.Setenv("SHELL", "/bin/bash")
	a := fakeApp(t, config.MapEnv{"HOME": home})
	reload := func() (*app.App, error) { return a, nil }
	// A file someone wrote: their note and a value set by hand stay.
	path := settingsPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("# my notes\nexited_after = \"2w\"\n"), 0o644)
	cmd, out := output()
	f := setupFlags{idle: "4h", dry: true, hook: true, protect: []string{"api", " notes "},
		set: []string{"idle-after", "dry-run", "hook", "protect"}}
	if err := setupWith(cmd, a, f, reload); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"# my notes", `exited_after = "2w"`, `idle_after = "4h"`, "dry_run = true"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings lack %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "auto_rename") {
		t.Errorf("wrote auto_rename, which no flag named:\n%s", b)
	}
	for _, name := range []string{"api", "notes"} {
		if !a.Protected.Has(name) {
			t.Errorf("%s not protected: %v", name, a.Protected.Names())
		}
	}
	rc, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if !strings.Contains(string(rc), "zellij-reaper shell-hook bash") {
		t.Errorf(".bashrc:\n%s", rc)
	}
	// Then the timers, as Enter starts them.
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "zellij-reaper.timer")); err != nil {
		t.Errorf("no timer written: %v\n%s", err, out)
	}

	// Again: the hook is there, and no second one is added.
	cmd, out = output()
	if err := setupWith(cmd, a, setupFlags{hook: true, set: []string{"hook"}}, reload); err != nil {
		t.Fatal(err)
	}
	rc, _ = os.ReadFile(filepath.Join(home, ".bashrc"))
	if n := strings.Count(string(rc), "shell-hook bash"); n != 1 || !strings.Contains(out.String(), "prompt hook: already in") {
		t.Errorf("hooks %d, output:\n%s", n, out)
	}
	// A shell with no hook says so, and the timers start all the same.
	t.Setenv("SHELL", "/usr/bin/tcsh")
	cmd, out = output()
	if err := setupWith(cmd, a, setupFlags{hook: true, set: []string{"hook"}}, reload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no prompt hook for the shell in $SHELL (tcsh)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestSetupFlagsCheckEveryValueBeforeWriting(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	reload := func() (*app.App, error) { return a, nil }
	for _, f := range []setupFlags{
		{idle: "banana", set: []string{"idle-after"}},
		{exited: "-1d", set: []string{"exited-after"}},
		// One good value with a bad one: neither is written.
		{idle: "4h", exited: "soon", set: []string{"idle-after", "exited-after"}},
	} {
		cmd, _ := output()
		if err := setupWith(cmd, a, f, reload); err == nil {
			t.Errorf("%+v accepted", f)
		}
	}
	if _, err := os.Stat(settingsPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a settings file was written: %v", err)
	}
	// A name the protected list cannot hold, beside a setting: the setting
	// stays as it was and nothing is protected. It used to be written first.
	path := settingsPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("dry_run = true\n"), 0o644)
	cmd, _ := output()
	f := setupFlags{dry: false, protect: []string{"#scratch", "api"}, set: []string{"dry-run", "protect"}}
	if err := setupWith(cmd, a, f, reload); err == nil || !strings.Contains(err.Error(), "--protect") {
		t.Errorf("a name starting with # accepted: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "dry_run = true\n" || a.Protected.Has("api") {
		t.Errorf("settings %q, protected %v", b, a.Protected.Names())
	}
	// The flags reach setup without a terminal: the value is what fails.
	// (Only commands that stop before the timers run here: --installer would
	// start this machine's own.)
	for args, want := range map[string]string{
		"setup --idle-after banana":               "--idle-after",
		"setup --snapshot 100x30 --idle-after 4h": "take none of the choices",
	} {
		root := newRoot()
		root.SetArgs(strings.Fields(args))
		root.SetOut(&strings.Builder{})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
	// The hidden modes would drop a choice without a word.
	if err := choicesInHiddenMode([]string{"dry-run"}, "", true); err == nil {
		t.Error("--installer took --dry-run")
	}
	if err := choicesInHiddenMode(nil, "", true); err != nil {
		t.Errorf("--installer alone: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "dry_run = true\n" {
		t.Errorf("settings changed: %q", b)
	}
}

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
