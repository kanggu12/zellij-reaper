package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// stoppedTimer is a systemd whose timer is installed and does not run.
type stoppedTimer struct{ timers.Systemd }

func (stoppedTimer) Status() timers.TimerStatus {
	return timers.TimerStatus{Installed: true, Enabled: true}
}

// disabledTimer is a systemd timer disabled without --now: it runs, its next
// pass at next, and will not start again.
type disabledTimer struct {
	timers.Systemd
	next time.Time
}

func (d disabledTimer) Status() timers.TimerStatus {
	return timers.TimerStatus{Installed: true, Active: true, Next: d.next}
}

// The line above plan's and run's tables says what passes do as the
// dashboard's header does: never "reaping on" beside a stopped timer, the
// next pass with how long until then, that a running timer will not start
// again, and the mode of the pass the table is about.
func TestTheHeadlineSaysWhatRunsOnItsOwn(t *testing.T) {
	i18n.Set(i18n.English)
	a := fakeApp(t, config.MapEnv{"HOME": t.TempDir()})
	th := ui.NewTheme(true)
	line := func(dry bool) string {
		return ansi.Strip(strings.Join(headline(context.Background(), a, th, 1000, dry), " "))
	}
	a.System = stoppedTimer{}
	if l := line(false); !strings.Contains(l, "● timer off") || strings.Contains(l, "reaping on") || strings.Contains(l, "next pass") {
		t.Errorf("with the timer stopped: %q", l)
	}
	// run --dry-run with the timer stopped and dry_run off: the pass only
	// logged, which the line says.
	if l := line(true); !strings.Contains(l, "● timer off · dry run") {
		t.Errorf("run --dry-run with the timer stopped: %q", l)
	}
	soon := time.Now().Add(41*time.Minute + 30*time.Second)
	a.System = runningTimer{next: soon}
	if l := line(false); !strings.Contains(l, "● reaping on · next pass ") || !strings.Contains(l, ", in 41m") || strings.Contains(l, "timer") {
		t.Errorf("with the timer running: %q", l)
	}
	// run --dry-run under a timer that reaps: the line says the run's mode,
	// and not the timer's next pass, which would read as a dry one.
	if l := line(true); !strings.Contains(l, "● dry run") || strings.Contains(l, "next pass") {
		t.Errorf("run --dry-run under a timer that reaps: %q", l)
	}
	a.System = disabledTimer{next: soon}
	if l := line(false); !strings.Contains(l, "● reaping on · next pass ") || !strings.Contains(l, " · off after a restart") {
		t.Errorf("with the timer disabled and running: %q", l)
	}
}

// plan's line gives the mode of the timer's passes, the settings'; run's the
// mode its own pass ran in, which --dry-run sets.
func TestPlanAndRunTellTheModeOfTheirPass(t *testing.T) {
	i18n.Set(i18n.English)
	a := fakeApp(t, config.MapEnv{"HOME": t.TempDir()})
	a.System = runningTimer{next: time.Now().Add(41*time.Minute + 30*time.Second)}
	a.Config.DryRun = true
	var plan strings.Builder
	showPlan(context.Background(), &plan, a, reaper.Report{}, 1000)
	if out := ansi.Strip(plan.String()); !strings.Contains(out, "● dry run · next pass ") {
		t.Errorf("plan with dry_run on:\n%s", out)
	}
	a.Config.DryRun = false
	var run strings.Builder
	if err := showPass(context.Background(), &run, a, reaper.Report{DryRun: true}, false, false); err != nil {
		t.Fatal(err)
	}
	if out := ansi.Strip(run.String()); !strings.Contains(out, "● dry run") || strings.Contains(out, "next pass") {
		t.Errorf("run --dry-run with dry_run off:\n%s", out)
	}
	// Nor the note that comes after the timer's next pass.
	a.System = disabledTimer{next: time.Now().Add(41 * time.Minute)}
	run.Reset()
	if err := showPass(context.Background(), &run, a, reaper.Report{DryRun: true}, false, false); err != nil {
		t.Fatal(err)
	}
	if out := ansi.Strip(run.String()); strings.Contains(out, "restart") {
		t.Errorf("run --dry-run under a disabled timer that runs:\n%s", out)
	}
}
