package cli

import (
	"errors"
	"io"
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
		Long: `A session name is part of its socket path, which Linux caps at 107 bytes and macOS
at 103. A session renamed past that keeps running with no socket: zellij's own
commands cannot reach it and its files come back after every delete. recover
deletes such sessions and clears their files. With --aggressive it first stops
servers that serve no socket file at all; a server whose session was renamed still
serves its socket and is left alone, and so is one with a client still connected,
a busy pane, or a protected name or program. --dry-run reports only. The status is
1 when a step failed, or when zellij's session index cannot be read, which leaves
every session as it is.`,
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
			return inWords(err)
		}
		defer l.Release()
	}
	sc, err := repair.Find(cmd.Context(), in)
	if err != nil {
		return err
	}
	println(w, ui.Fg(t.Muted).Render(i18n.T("recover.dir", sc.SocketDir, sc.Budget)))
	// No session can be told from another without the index: a scan that
	// went on would take live sessions for broken ones. Nothing asked for
	// could be done, and the status says so.
	if sc.IndexErr != nil {
		println(w, warn(t)+i18n.T("recover.noindex", sc.IndexErr))
		return errSilentExit
	}
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
				println(w, ok(t)+i18n.T("recover.orphan.kept", o.PID, orphanName(o), o.Kept))
				continue
			}
			stoppable++
			println(w, warn(t)+i18n.T("recover.orphan", o.PID, orphanName(o)))
		}
	}
	// Servers this run leaves that --aggressive would stop are said last,
	// with the command for them: a list of them that ended in "nothing to
	// repair" told nobody there was one.
	left := stoppable > 0 && !aggressive
	hint := func() {
		if left {
			println(w, ui.Fg(t.Muted).Render(i18n.TN("recover.stoppable", stoppable, stoppable)))
		}
	}
	if len(sc.Broken) == 0 && (stoppable == 0 || !aggressive || sc.AllOrphaned) {
		if !left {
			println(w, ok(t)+i18n.T("recover.nothing"))
		}
		hint()
		return nil
	}
	if dry {
		println(w, ui.Fg(t.Muted).Render(i18n.T("recover.dry")))
		hint()
		return nil
	}
	failed := false
	for _, st := range repair.Repair(cmd.Context(), in, sc, aggressive) {
		if st.Err != nil {
			println(w, bad(t)+st.Target+": "+st.What+": "+st.Err.Error())
			failed = true
		} else {
			println(w, ok(t)+st.Target+": "+st.What)
		}
	}
	hint()
	// Each step that failed has said why above: the status tells a script.
	if failed {
		return errSilentExit
	}
	return nil
}

// showResized prints a line for each process resize signalled, or would, and
// ends with status 1 when one was not reached: its line says why.
func showResized(w io.Writer, t ui.Theme, targets []resize.Target) error {
	failed := false
	for _, tg := range targets {
		line := strconv.Itoa(tg.PID) + "  " + tg.Role + "  " + ui.Fg(t.Muted).Render(tg.Command)
		if tg.Err != nil {
			println(w, bad(t)+line+": "+tg.Err.Error())
			failed = true
		} else {
			println(w, ok(t)+line)
		}
	}
	if failed {
		return errSilentExit
	}
	return nil
}

// busyError is lock.ErrBusy in the words the dashboard uses for it, in the
// reader's language; errors.Is still finds lock.ErrBusy in it.
type busyError struct{}

func (busyError) Error() string { return i18n.T("toast.busy") }
func (busyError) Unwrap() error { return lock.ErrBusy }

// inWords is err, with a busy lock said in the reader's language.
func inWords(err error) error {
	if errors.Is(err, lock.ErrBusy) {
		return busyError{}
	}
	return err
}

// orphanName says which session an orphan server is: by the name it has now
// when zellij's index still has one, else by the name it started under.
func orphanName(o repair.Orphan) string {
	switch {
	case o.Name != "":
		return i18n.T("recover.orphan.name", o.Name)
	case o.FirstName != "":
		return i18n.T("recover.orphan.first", o.FirstName)
	}
	return i18n.T("recover.orphan.unnamed")
}

func cmdResize() *cobra.Command {
	var dry, shallow bool
	cmd := &cobra.Command{
		Use:     "resize",
		Aliases: []string{"refresh"},
		Short:   "Make zellij and the programs in its panes redraw at the right size",
		Long: `Send SIGWINCH to every zellij client, every server and every program in a pane, so each
re-reads its terminal size and redraws. Use it when a program in a pane is stuck at an
old size after the window changed. --shallow signals the clients only. The status
is 1 when a process could not be signalled.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			t := theme()
			table, err := procfs.Read(procfs.Native)
			if err != nil {
				return err
			}
			targets := resize.Targets(table, procfs.CurrentUID(), os.Getpid(), shallow)
			if len(targets) == 0 {
				println(w, warn(t)+i18n.T("resize.none"))
				return nil
			}
			if !dry {
				targets = resize.Send(targets)
			}
			return showResized(w, t, targets)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "list the processes, send nothing")
	cmd.Flags().BoolVar(&shallow, "shallow", false, "signal the clients only")
	return cmd
}
