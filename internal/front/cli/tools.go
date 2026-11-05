package cli

import (
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/repair"
	"github.com/kanggu12/zellij-reaper/internal/engine/resize"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
)

func cmdRecover() *cobra.Command {
	var dry, aggressive bool
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Repair sessions whose name is too long for their socket path",
		Long: `A session name is part of its socket path, which Linux caps at 107 bytes. A session
renamed past that keeps running with no socket: zellij's own commands cannot reach
it and its files come back after every delete. recover deletes such sessions and
clears their files. With --aggressive it first stops servers that serve no socket
file at all; a server whose session was renamed still serves its socket and is
left alone, and so is one with a client still connected, a busy pane, or a
protected name or program. --dry-run reports only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			return recoverSessions(cmd, a.Reaper.LockPath, a.RepairInputs(), dry, aggressive)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "report only")
	cmd.Flags().BoolVar(&aggressive, "aggressive", false, "also stop servers that serve no socket file")
	return cmd
}

// recoverSessions is `recover` on the given inputs: the machine's, or a fake
// one in tests. lockPath is the pass lock.
func recoverSessions(cmd *cobra.Command, lockPath string, in repair.Inputs, dry, aggressive bool) error {
	w := cmd.OutOrStdout()
	t := theme()
	// A real recover deletes sessions and stops servers: it waits for a
	// pass, and a pass waits for it, as two passes do. The lock is taken
	// before the scan, so nothing changes between the two.
	if !dry {
		l, err := lock.Acquire(lockPath, reaper.LockWait)
		if err != nil {
			return err
		}
		defer l.Release()
	}
	sc, err := repair.Find(cmd.Context(), in)
	if err != nil {
		return err
	}
	println(w, ui.Fg(t.Muted).Render(i18n.T("recover.dir", sc.SocketDir, sc.Budget)))
	for _, b := range sc.Broken {
		println(w, warn(t)+i18n.T("recover.broken", b.Name, b.PathBytes))
	}
	// Orphans nothing keeps: the ones --aggressive would stop.
	stoppable := 0
	switch {
	case !sc.OrphansKnown:
		println(w, warn(t)+i18n.T("recover.nodiag"))
	case len(sc.Orphans) == 0:
		println(w, ok(t)+i18n.T("recover.served"))
	case sc.AllOrphaned:
		println(w, warn(t)+i18n.T("recover.allorphaned"))
	default:
		for _, o := range sc.Orphans {
			// One somebody may still use is kept, and says why.
			if o.Kept != "" {
				println(w, ok(t)+i18n.T("recover.orphan.kept", o.PID, o.FirstName, o.Kept))
				continue
			}
			stoppable++
			println(w, warn(t)+i18n.T("recover.orphan", o.PID, o.FirstName))
		}
	}
	if len(sc.Broken) == 0 && (stoppable == 0 || !aggressive || sc.AllOrphaned) {
		println(w, ok(t)+i18n.T("recover.nothing"))
		return nil
	}
	if dry {
		println(w, ui.Fg(t.Muted).Render(i18n.T("recover.dry")))
		return nil
	}
	for _, st := range repair.Repair(cmd.Context(), in, sc, aggressive) {
		if st.Err != nil {
			println(w, bad(t)+st.Target+": "+st.What+": "+st.Err.Error())
		} else {
			println(w, ok(t)+st.Target+": "+st.What)
		}
	}
	return nil
}

func cmdResize() *cobra.Command {
	var dry, shallow bool
	cmd := &cobra.Command{
		Use:     "resize",
		Aliases: []string{"refresh"},
		Short:   "Make zellij and the programs in its panes redraw at the right size",
		Long: `Send SIGWINCH to every zellij client, every server and every program in a pane, so each
re-reads its terminal size and redraws. Use it when a program in a pane is stuck at an
old size after the window changed. --shallow signals the clients only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			t := theme()
			table, err := procfs.Read("/proc")
			if err != nil {
				return err
			}
			targets := resize.Targets(table, os.Getuid(), os.Getpid(), shallow)
			if len(targets) == 0 {
				println(w, warn(t)+i18n.T("resize.none"))
				return nil
			}
			if !dry {
				targets = resize.Send(targets)
			}
			for _, tg := range targets {
				line := strconv.Itoa(tg.PID) + "  " + tg.Role + "  " + ui.Fg(t.Muted).Render(tg.Command)
				if tg.Err != nil {
					println(w, bad(t)+line+": "+tg.Err.Error())
				} else {
					println(w, ok(t)+line)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "list the processes, send nothing")
	cmd.Flags().BoolVar(&shallow, "shallow", false, "signal the clients only")
	return cmd
}
