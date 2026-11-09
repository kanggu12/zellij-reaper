package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
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
// only need attention.
func doctor(ctx context.Context, a *app.App, cmd *cobra.Command) (int, int) {
	w := cmd.OutOrStdout()
	t := theme()
	problems, warnings := 0, 0
	pass := func(s string) { println(w, ok(t)+s) }
	note := func(s string) { println(w, warn(t)+s); warnings++ }
	fail := func(s string) { println(w, bad(t)+s); problems++ }

	// zellij, and whether its version is one CI covers.
	if v, err := a.Zellij.Version(ctx); err != nil {
		fail(i18n.T("doctor.zellij.none"))
	} else {
		tested := false
		for _, m := range testedMinors {
			tested = tested || strings.HasPrefix(v, m)
		}
		if tested {
			pass(i18n.T("doctor.zellij", v))
		} else {
			note(i18n.T("doctor.zellij.tested", v, TestedZellij))
		}
	}

	// The timer.
	switch {
	case !a.System.Available() && a.System.Kind() == "systemd":
		fail(i18n.T("doctor.systemd.none"))
	case !a.System.Available():
		fail(i18n.T("doctor.scheduler.none", a.System.Kind()))
	default:
		if a.System.Kind() == "systemd" {
			pass(i18n.T("doctor.systemd"))
		} else {
			pass(i18n.T("doctor.scheduler", a.System.Kind()))
		}
		st := a.System.Status()
		switch {
		case !st.Installed:
			fail(i18n.T("doctor.timer.none"))
		case !st.Enabled || !st.Active:
			fail(i18n.T("doctor.timer.off"))
		// The interval can be unreadable (0): then it is left unsaid
		// rather than shown as "every 0s".
		case st.Interval == 0 && st.Next.IsZero():
			pass(i18n.T("doctor.timer.on"))
		case st.Interval == 0:
			pass(i18n.T("doctor.timer.next", ui.Clock(st.Next)))
		case st.Next.IsZero():
			pass(i18n.T("doctor.timer.idle", duration.Format(st.Interval)))
		default:
			pass(i18n.T("doctor.timer", duration.Format(st.Interval), ui.Clock(st.Next)))
		}
		// A unit written by another copy of the binary keeps running that copy.
		if exe, err := app.Executable(); err == nil && st.ExecStart != "" && !strings.Contains(st.ExecStart, exe) {
			note(i18n.T("doctor.timer.stale", st.ExecStart))
		}
		if st.Installed && st.Interval != 0 && st.Interval != a.Config.Interval {
			note(i18n.T("doctor.interval", duration.Format(st.Interval), duration.Format(a.Config.Interval)))
		}
		// The names timer, which matters only when passes rename; zellij on
		// Windows cannot reach a renamed session, so nothing is renamed there.
		if a.Config.AutoRename && !zellij.RenameReachable() {
			note(i18n.T("doctor.rename.windows"))
		} else if a.Config.AutoRename && a.Config.RenameInterval > 0 {
			ns := a.System.NamesStatus()
			switch {
			case !ns.Installed || !ns.Enabled || !ns.Active:
				note(i18n.T("doctor.names.off"))
			case ns.Interval != a.Config.RenameInterval:
				note(i18n.T("doctor.names.interval", duration.Format(ns.Interval), duration.Format(a.Config.RenameInterval)))
			default:
				pass(i18n.T("doctor.names", duration.Format(ns.Interval)))
			}
			if exe, err := app.Executable(); err == nil && ns.ExecStart != "" && !strings.Contains(ns.ExecStart, exe) {
				note(i18n.T("doctor.timer.stale", ns.ExecStart))
			}
		}
		// Turned off in the settings, but still installed from before.
		if a.Config.RenameInterval <= 0 {
			if ns := a.System.NamesStatus(); ns.Installed && ns.Active {
				note(i18n.T("doctor.names.leftover"))
			}
		}
		// Only systemd can be asked whether the timers outlive a login.
		if on, known := a.System.Persists(); known && on {
			pass(i18n.T("doctor.linger"))
		} else if known {
			note(i18n.T("doctor.linger.off"))
		}
	}

	// The settings.
	if a.Config.File != "" {
		pass(i18n.T("doctor.config", tilde(a.Config.File)))
	} else {
		pass(i18n.T("doctor.config.none", tilde(settingsPath())))
	}
	// A setting from this shell's environment is not the timer's.
	for _, m := range envOnly(a.Config, settingsPath()) {
		note(m)
	}

	// Sessions and the socket table.
	if listed, err := a.Zellij.ListSessions(ctx); err == nil {
		pass(i18n.T("doctor.sessions", len(listed), tilde(a.InfoDir)))
	} else if errors.Is(err, zellij.ErrNoSessions) {
		pass(i18n.T("doctor.sessions.none", tilde(a.InfoDir)))
	} else if a.Zellij.Available() {
		fail(i18n.T("error.list", err))
	}
	// How a renamed session is matched to its server: the kernel's socket
	// table on Linux, the socket file itself elsewhere.
	switch runtime.GOOS {
	case "darwin":
		pass(i18n.T("doctor.match.darwin"))
	case "windows":
		pass(i18n.T("doctor.match.windows"))
	default:
		if _, err := procfs.UnixSockets(); err == nil {
			pass(i18n.T("doctor.sockdiag"))
		} else {
			note(i18n.T("doctor.sockdiag.none"))
		}
	}

	// The log.
	if info, err := os.Stat(a.Config.LogPath); err == nil {
		pass(i18n.T("doctor.log", tilde(a.Config.LogPath), size(info.Size()), size(a.Config.LogMaxBytes)))
	} else {
		pass(i18n.T("doctor.log.none", tilde(a.Config.LogPath)))
	}
	if n := len(a.Protected.Names()); n > 0 {
		pass(i18n.TN("doctor.protected", n, n))
	}
	return problems, warnings
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
