package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// TestedZellij is the range of zellij releases CI runs against.
const TestedZellij = "0.43 – 0.45"

// testedMinors are the zellij minor versions the integration tests cover.
var testedMinors = []string{"0.43.", "0.44.", "0.45."}

func cmdDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that everything zellij-reaper needs is in place",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			problems, warnings := doctor(cmd.Context(), a, cmd)
			println(cmd.OutOrStdout())
			t := theme()
			switch {
			case problems > 0:
				println(cmd.OutOrStdout(), bad(t)+i18n.TN("doctor.problems", problems, problems))
				return errSilentExit
			case warnings > 0:
				println(cmd.OutOrStdout(), warn(t)+i18n.TN("doctor.ok.warnings", warnings, warnings))
			default:
				println(cmd.OutOrStdout(), ok(t)+i18n.T("doctor.ok"))
			}
			return nil
		},
	}
}

// errSilentExit makes the command exit with status 1 without printing more.
var errSilentExit = errors.New("problems found")

// doctor prints one line per check and returns how many failed and how many
// only need attention. Each part of the machine is one method below, in the
// order the lines come out.
func doctor(ctx context.Context, a *app.App, cmd *cobra.Command) (int, int) {
	c := &checkup{w: cmd.OutOrStdout(), t: theme()}
	c.checkZellij(ctx, a)
	every := c.checkScheduler(a)
	c.checkSettings(a)
	c.checkSessions(ctx, a)
	c.checkLog(a, every)
	if n := len(a.Protected.Names()); n > 0 {
		c.pass(i18n.TN("doctor.protected", n, n))
	}
	return c.problems, c.warnings
}

// checkup is doctor as it goes: where its lines go, and how many of them
// were problems and how many only need attention.
type checkup struct {
	w                  io.Writer
	t                  ui.Theme
	problems, warnings int
}

// pass prints a check that is fine.
func (c *checkup) pass(s string) { println(c.w, ok(c.t)+s) }

// note prints a check that needs attention, and counts it.
func (c *checkup) note(s string) { println(c.w, warn(c.t)+s); c.warnings++ }

// fail prints a check that is broken, and counts it.
func (c *checkup) fail(s string) { println(c.w, bad(c.t)+s); c.problems++ }

// checkZellij checks zellij, and whether its version is one CI covers.
func (c *checkup) checkZellij(ctx context.Context, a *app.App) {
	v, err := a.Zellij.Version(ctx)
	if err != nil {
		c.fail(i18n.T("doctor.zellij.none"))
		return
	}
	tested := false
	for _, m := range testedMinors {
		tested = tested || strings.HasPrefix(v, m)
	}
	if tested {
		c.pass(i18n.T("doctor.zellij", v))
	} else {
		c.note(i18n.T("doctor.zellij.tested", v, TestedZellij))
	}
}

// checkScheduler checks the scheduler and the timers it runs, and returns how
// often a pass runs, for the last pass to be measured by; 0 where no timer
// runs one.
func (c *checkup) checkScheduler(a *app.App) time.Duration {
	switch {
	case !a.System.Available() && a.System.Kind() == "systemd":
		c.fail(i18n.T("doctor.systemd.none"))
		return 0
	case !a.System.Available():
		c.fail(i18n.T("doctor.scheduler.none", a.System.Kind()))
		return 0
	}
	if a.System.Kind() == "systemd" {
		c.pass(i18n.T("doctor.systemd"))
	} else {
		c.pass(i18n.T("doctor.scheduler", a.System.Kind()))
	}
	st := a.System.Status()
	var every time.Duration
	if st.Installed && st.Enabled && st.Active {
		// An interval the timer does not tell is the settings' one.
		every = cmp.Or(st.Interval, a.Config.Interval)
	}
	c.checkTimer(a, st)
	c.checkNamesTimer(a)
	// Only systemd can be asked whether the timers outlive a login.
	if on, known := a.System.Persists(); known && on {
		c.pass(i18n.T("doctor.linger"))
	} else if known {
		c.note(i18n.T("doctor.linger.off"))
	}
	return every
}

// checkTimer checks the timer of the passes: that it is installed and runs,
// the binary it runs, the places it reads, and its interval.
func (c *checkup) checkTimer(a *app.App, st timers.TimerStatus) {
	switch {
	case !st.Installed:
		c.fail(i18n.T("doctor.timer.none"))
	case !st.Enabled || !st.Active:
		c.fail(i18n.T("doctor.timer.off"))
	// The interval can be unreadable (0): then it is left unsaid rather than
	// shown as "every 0s".
	case st.Interval == 0 && st.Next.IsZero():
		c.pass(i18n.T("doctor.timer.on"))
	case st.Interval == 0:
		c.pass(i18n.T("doctor.timer.next", ui.Clock(st.Next)))
	case st.Next.IsZero():
		c.pass(i18n.T("doctor.timer.idle", duration.Format(st.Interval)))
	default:
		c.pass(i18n.T("doctor.timer", duration.Format(st.Interval), ui.Clock(st.Next)))
	}
	// A unit written by another copy of the binary keeps running that copy.
	if exe, err := app.Executable(); err == nil && st.ExecStart != "" && !strings.Contains(st.ExecStart, exe) {
		c.note(i18n.T("doctor.timer.stale", st.ExecStart))
	}
	// The timer runs with the places install saw: one this shell sets
	// otherwise has the timer read other settings, another protected list, or
	// look for zellij's sessions elsewhere.
	if st.Installed && st.EnvKnown {
		here := timers.CarriedEnv(a.Env)
		for _, k := range timers.Carried {
			if st.Env[k] != here[k] {
				c.note(i18n.T("doctor.timer.env", k, unsetOr(st.Env[k]), unsetOr(here[k])))
			}
		}
	}
	if st.Installed && st.Interval != 0 && st.Interval != a.Config.Interval {
		c.note(i18n.T("doctor.interval", duration.Format(st.Interval), duration.Format(a.Config.Interval)))
	}
}

// checkNamesTimer checks the timer that names sessions, which matters only
// when passes rename; zellij on Windows cannot reach a renamed session, so
// nothing is renamed there.
func (c *checkup) checkNamesTimer(a *app.App) {
	if a.Config.AutoRename && !zellij.RenameReachable() {
		c.note(i18n.T("doctor.rename.windows"))
	} else if a.Config.AutoRename && a.Config.RenameInterval > 0 {
		ns := a.System.NamesStatus()
		switch {
		case !ns.Installed || !ns.Enabled || !ns.Active:
			c.note(i18n.T("doctor.names.off"))
		case ns.Interval != a.Config.RenameInterval:
			c.note(i18n.T("doctor.names.interval", duration.Format(ns.Interval), duration.Format(a.Config.RenameInterval)))
		default:
			c.pass(i18n.T("doctor.names", duration.Format(ns.Interval)))
		}
		if exe, err := app.Executable(); err == nil && ns.ExecStart != "" && !strings.Contains(ns.ExecStart, exe) {
			c.note(i18n.T("doctor.timer.stale", ns.ExecStart))
		}
	}
	// Turned off in the settings, but still installed from before.
	if a.Config.RenameInterval <= 0 {
		if ns := a.System.NamesStatus(); ns.Installed && ns.Active {
			c.note(i18n.T("doctor.names.leftover"))
		}
	}
}

// checkSettings checks the settings file, and the settings only this shell
// sees.
func (c *checkup) checkSettings(a *app.App) {
	if a.Config.File != "" {
		c.pass(i18n.T("doctor.config", tilde(a.Config.File)))
	} else {
		c.pass(i18n.T("doctor.config.none", tilde(settingsPath())))
	}
	// A setting from this shell's environment is not the timer's.
	for _, m := range envOnly(a.Config, settingsPath()) {
		c.note(m)
	}
}

// checkSessions checks that zellij lists the sessions, its session index, and
// how a renamed session is matched to its server.
func (c *checkup) checkSessions(ctx context.Context, a *app.App) {
	if listed, err := a.Zellij.ListSessions(ctx); err == nil {
		c.pass(i18n.TN("doctor.sessions", len(listed), len(listed), tilde(a.InfoDir)))
	} else if errors.Is(err, zellij.ErrNoSessions) {
		c.pass(i18n.T("doctor.sessions.none", tilde(a.InfoDir)))
	} else if a.Zellij.Available() {
		// A list that gave no answer names the server that held it up, as a
		// pass's does, where the process table shows one stopped.
		if zellij.CutShort(err) {
			table, _ := inspect.ReadProcesses(a.Sources())
			idx, _ := zellij.ReadIndex(a.InfoDir)
			err = reaper.Stuck(err, table, a.Sources().UID, idx)
		}
		c.fail(i18n.T("error.list", err))
	}
	// Since 0.46 zellij names a session's files by id, and keeps the names in
	// an index: one that cannot be read leaves those sessions unjudged.
	if _, err := os.Stat(zellij.IndexFile(a.InfoDir)); err == nil {
		if x, err := zellij.ReadIndex(a.InfoDir); err == nil {
			c.pass(i18n.TN("doctor.index", x.Len(), x.Len()))
		} else {
			c.fail(i18n.T("doctor.index.bad", err))
		}
	}
	// How a renamed session is matched to its server: the kernel's socket
	// table on Linux, the socket file itself elsewhere.
	switch runtime.GOOS {
	case "darwin":
		c.pass(i18n.T("doctor.match.darwin"))
	case "windows":
		c.pass(i18n.T("doctor.match.windows"))
	default:
		if _, err := procfs.UnixSockets(); err == nil {
			c.pass(i18n.T("doctor.sockdiag"))
		} else {
			c.note(i18n.T("doctor.sockdiag.none"))
		}
	}
}

// checkLog checks the log, and what it says of the last pass; every is how
// often the timer runs one.
func (c *checkup) checkLog(a *app.App, every time.Duration) {
	if info, err := os.Stat(a.Config.LogPath); err == nil {
		c.pass(i18n.T("doctor.log", tilde(a.Config.LogPath), size(info.Size()), size(a.Config.LogMaxBytes)))
	} else {
		c.pass(i18n.T("doctor.log.none", tilde(a.Config.LogPath)))
	}
	// A log that cannot be written leaves the passes without a record, and
	// nothing else says so: they go on, and end with status 0.
	if err := logfile.Writable(a.Config.LogPath); err != nil {
		c.fail(i18n.T("doctor.log.unwritable", err))
	}
	// The last pass, as the log tells it. Passes that stop each time, or that
	// never get the lock, say so nowhere else either: the timer runs them,
	// and they end with status 0.
	lines, _ := logfile.Tail(a.Config.LogPath, passLines)
	mark, text := passWords(logfile.LastPass(lines), every, lockHeld(a.Reaper.LockPath), tilde(a.Reaper.LockPath), time.Now())
	switch mark {
	case "ok":
		c.pass(text)
	case "warn":
		c.note(text)
	case "bad":
		c.fail(text)
	}
}

// passLines is how many of the log's last lines are read for the last pass:
// a pass writes a line or two for each session, and a few hundred sessions
// is many.
const passLines = 4000

// stillStarting is how long a pass may run before doctor takes it for stuck:
// a pass takes well under a second, and systemd stops one after a minute.
const stillStarting = time.Minute

// passWords is what doctor says about the last pass p: a mark ("ok", "warn"
// or "bad", or "" for nothing to say) and the words. every is how often the
// timer runs a pass, 0 where none does; held is whether the pass lock at
// lockPath is held now.
func passWords(p logfile.Pass, every time.Duration, held bool, lockPath string, now time.Time) (mark, text string) {
	at := ui.Clock(p.Started)
	ago := duration.Format(max(time.Minute, now.Sub(p.Started).Round(time.Minute)))
	switch {
	// A pass a moment old holds the lock while it runs.
	case held && p.Found && !p.Ended && now.Sub(p.Started) < stillStarting:
		return "ok", i18n.T("doctor.pass.running", at)
	// One that has run for longer is stuck, and keeps every pass after it out.
	case held && p.Found && !p.Ended:
		return "bad", i18n.T("doctor.pass.stuck", at, ago, lockPath)
	case held:
		return "bad", i18n.T("doctor.pass.held", lockPath)
	case !p.Found:
		// The log's own line says whether there is one.
		return "", ""
	// Passes stopped coming. A machine that was off or asleep says this too,
	// until its timer runs again: a note, with the times to judge it by.
	case every > 0 && now.Sub(p.Started) > 2*every+10*time.Minute:
		return "warn", i18n.T("doctor.pass.late", at, ago, duration.Format(every))
	case !p.Ended:
		return "warn", i18n.T("doctor.pass.unfinished", at, ago)
	// zellij refused a delete: the session is still there, and the log says
	// why.
	case p.Acted && p.Failed > 0:
		return "warn", i18n.T("doctor.pass", at, ago, p.Reaped, p.Kept, p.Renamed) + i18n.TN("doctor.pass.failed", p.Failed, p.Failed)
	case p.Acted:
		return "ok", i18n.T("doctor.pass", at, ago, p.Reaped, p.Kept, p.Renamed)
	// With no sessions there was nothing to do.
	case p.Stopped == "no sessions":
		return "ok", i18n.T("doctor.pass.nosessions", at, ago)
	}
	return "bad", i18n.T("doctor.pass.stopped", at, ago, p.Stopped)
}

// lockLook is how long doctor waits for the pass lock before it says the lock
// is held: a names pass, a dashboard action, keep-name or recover hold it for
// a moment, and that is not worth a failure.
var lockLook = 3 * time.Second

// lockHeld reports whether something holds the pass lock at path now. A lock
// file that is not there is held by nobody, and is not made here.
func lockHeld(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	l, err := lock.Acquire(path, lockLook)
	if err != nil {
		return errors.Is(err, lock.ErrBusy)
	}
	l.Release()
	return false
}

// size prints a byte count the way people read it.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// unsetOr is a variable's value, or a word for one that is not set.
func unsetOr(v string) string {
	if v == "" {
		return i18n.T("doctor.env.unset")
	}
	return tilde(v)
}
