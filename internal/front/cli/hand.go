package cli

// The commands for what the dashboard does by hand to one session: reap it,
// rename it. They go through the dashboard's own backend, so they make the
// same checks, and the decision log says they came from the command line.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

func cmdDelete() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete name...",
		Short: "Reap these sessions now, as d does in the dashboard",
		Long: `Reap sessions by hand. Each is asked about first, as the dashboard asks, saying
what protects it or still runs in it, and zellij is asked right before whether a
client is attached: a session somebody is attached to, one whose clients nobody can
tell, or one zellij no longer lists is kept. The waits do not apply.

--yes reaps without asking, for scripts; without a terminal to answer in, delete
needs it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ask := !yes
			// A question nobody can answer would hang a script, or take
			// the next line of its input for a yes.
			if ask && (!isTerminal(os.Stdin) || !isTerminal(os.Stdout)) {
				return errors.New(i18n.T("delete.needyes"))
			}
			a, err := load()
			if err != nil {
				return err
			}
			return reapByHand(cmd, tui.NewCommandLine(a), args, ask)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "reap without asking")
	return cmd
}

// reapByHand reaps each named session through b, asking first when ask is
// set. It fails when any session it was not told to keep was kept.
func reapByHand(cmd *cobra.Command, b tui.Backend, names []string, ask bool) error {
	// Not theme(): its question to the terminal would read the answers.
	ctx, w, t := cmd.Context(), cmd.OutOrStdout(), themeWithoutAsking()
	// The question says what the plan knows of the session, as the
	// dashboard's does.
	var listed map[string]bool
	var question func(string) string
	if ask {
		report, err := b.Plan(ctx)
		if err != nil {
			return err
		}
		listed = map[string]bool{}
		for _, o := range report.Outcomes {
			listed[o.Session.Name()] = true
		}
		question = func(name string) string {
			q, _ := tui.ReapQuestion(report, name)
			return q
		}
	}
	in := bufio.NewReader(cmd.InOrStdin())
	failed := 0
	for _, name := range names {
		if ask {
			if !listed[name] {
				println(w, bad(t)+i18n.T("confirm.gone", name))
				failed++
				continue
			}
			fmt.Fprint(w, question(name)+" [y/N] ")
			answer, _ := in.ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				println(w, warn(t)+i18n.T("delete.skipped", name))
				continue
			}
		}
		if err := b.Delete(ctx, name); err != nil {
			if errors.Is(err, lock.ErrBusy) {
				err = errors.New(i18n.T("toast.busy"))
			}
			println(w, bad(t)+i18n.T("toast.delete.failed", name, err))
			failed++
			continue
		}
		println(w, ok(t)+i18n.T("toast.deleted", name))
	}
	if failed > 0 {
		return errors.New(i18n.T("delete.some", failed, len(names)))
	}
	return nil
}

func cmdRename() *cobra.Command {
	return &cobra.Command{
		Use:   "rename name new-name",
		Short: "Rename a session and keep the new name, as r does in the dashboard",
		Long: `Rename a running session. A name given by hand is someone's choice, so passes
leave it alone from then on, and a protection follows the session to its new name.
A name too long for the socket path, or one another session has, is refused, and so
is an exited session. Renaming a session to the name it has keeps that name, as
keep-name does. zellij on Windows cannot reach a session after a rename, so rename
refuses there.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !zellij.RenameReachable() {
				return errors.New(i18n.T("rename.windows"))
			}
			a, err := load()
			if err != nil {
				return err
			}
			return renameByHand(cmd, a, tui.NewCommandLine(a), args[0], strings.TrimSpace(args[1]))
		},
	}
}

// renameByHand renames one running session through b, or keeps its name
// when the new one is the same.
func renameByHand(cmd *cobra.Command, a *app.App, b tui.Backend, name, to string) error {
	ctx, w, t := cmd.Context(), cmd.OutOrStdout(), theme()
	if to == "" {
		return errors.New(i18n.T("rename.empty"))
	}
	// The dashboard offers a rename only for a running session it lists.
	sessions, err := a.Zellij.ListSessions(ctx)
	if err != nil && !errors.Is(err, zellij.ErrNoSessions) {
		return err
	}
	i := -1
	for j, s := range sessions {
		if s.Name == name {
			i = j
		}
	}
	switch {
	case i < 0:
		return errors.New(i18n.T("rename.none", name))
	case sessions[i].Exited:
		return errors.New(i18n.T("rename.exited"))
	}
	if to == name {
		if err := b.KeepName(ctx, name); err != nil {
			return err
		}
		println(w, ok(t)+i18n.T("toast.kept", name))
		return nil
	}
	note, err := b.Rename(ctx, name, to)
	switch {
	case errors.Is(err, lock.ErrBusy):
		return errors.New(i18n.T("toast.busy"))
	case err != nil:
		return errors.New(i18n.T("toast.rename.failed", err))
	case note != "":
		// Renamed, with something to know about it, such as a lost
		// protection.
		println(w, warn(t)+note)
	default:
		println(w, ok(t)+i18n.T("toast.renamed", name, to))
	}
	return nil
}
