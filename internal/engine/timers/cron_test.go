package timers

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// fakeCrontab is a crontab in memory.
type fakeCrontab struct{ lines []string }

func (f *fakeCrontab) run(stdin string, args ...string) (string, error) {
	switch args[0] {
	case "-l":
		if f.lines == nil {
			return "no crontab for me", errExit
		}
		return strings.Join(f.lines, "\n"), nil
	case "-r":
		f.lines = nil
	case "-":
		f.lines = strings.Split(strings.TrimRight(stdin, "\n"), "\n")
	}
	return "", nil
}

var errExit = &exitErr{}

type exitErr struct{}

func (*exitErr) Error() string { return "exit status 1" }

func TestCronSchedules(t *testing.T) {
	for _, c := range []struct {
		in    time.Duration
		sched string
		every time.Duration
	}{
		{time.Hour, "5 * * * *", time.Hour},
		{5 * time.Minute, "*/5 * * * *", 5 * time.Minute},
		{time.Minute, "* * * * *", time.Minute},
		{2 * time.Hour, "5 */2 * * *", 2 * time.Hour},
		{24 * time.Hour, "5 0 * * *", 24 * time.Hour},
		{7 * time.Minute, "*/6 * * * *", 6 * time.Minute},
		{90 * time.Minute, "5 * * * *", time.Hour},
	} {
		sched, every := CronSchedule(c.in)
		if sched != c.sched || every != c.every {
			t.Errorf("%v: %q %v, want %q %v", c.in, sched, every, c.sched, c.every)
		}
		// What it writes reads back as the same interval.
		if got, next := cronEvery(strings.Fields(sched), time.Now()); got != every || next.IsZero() {
			t.Errorf("%q reads back as %v, next %v", sched, got, next)
		}
	}
}

func TestCronInstallKeepsTheOwnersLines(t *testing.T) {
	f := &fakeCrontab{lines: []string{"MAILTO=me", "0 3 * * * backup.sh"}}
	c := Cron{Env: config.MapEnv{"HOME": t.TempDir()}, Run: f.run, Running: func() bool { return true }}
	cfg := config.Default(config.MapEnv{"HOME": "/h"})
	o := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", ZellijDir: "/opt/zellij", Config: cfg}
	for range 2 { // a second install replaces the first one's lines
		if _, err := c.Install(o); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.lines) != 4 || !slices.Equal(f.lines[:2], []string{"MAILTO=me", "0 3 * * * backup.sh"}) {
		t.Fatalf("crontab:\n%s", strings.Join(f.lines, "\n"))
	}
	if !strings.HasPrefix(f.lines[2], "5 * * * * env PATH='/opt/zellij:") || !strings.Contains(f.lines[2], "'/h/.local/bin/zellij-reaper' run --quiet") {
		t.Errorf("pass line: %s", f.lines[2])
	}
	st, ns := c.Status(), c.NamesStatus()
	if !st.Installed || !st.Active || st.Interval != time.Hour || st.Next.IsZero() || !ns.Installed || ns.Interval != 5*time.Minute {
		t.Errorf("status %+v, names %+v", st, ns)
	}
	if _, err := c.Uninstall(false, ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.lines, []string{"MAILTO=me", "0 3 * * * backup.sh"}) {
		t.Errorf("after uninstall:\n%s", strings.Join(f.lines, "\n"))
	}
	// An install refuses where no daemon would run it.
	c.Running = func() bool { return false }
	if _, err := c.Install(o); err == nil {
		t.Error("installed with no cron daemon")
	}
}
