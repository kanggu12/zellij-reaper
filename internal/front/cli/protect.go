package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

// cmdProtect builds `protect NAME...` (on) or `unprotect NAME...` (off).
// With no name, `protect` lists the protected sessions.
func cmdProtect(on bool) *cobra.Command {
	use, short := "protect [name...]", "Keep these sessions out of every pass (no name: list them)"
	if !on {
		use, short = "unprotect name...", "Let passes reap these sessions again"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if !on && len(args) == 0 {
				return errors.New(i18n.T("args.unprotect"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if on && len(args) == 0 {
				names := a.Protected.Names()
				// An empty list prints nothing to a script; a person is told.
				if len(names) == 0 && isTerminal(os.Stdout) {
					println(w, ui.Fg(theme().Muted).Render(i18n.T("protect.empty")))
				}
				for _, n := range names {
					fmt.Fprintln(w, n)
				}
				return nil
			}
			for _, name := range args {
				// A name the list never had: nothing to lift, and the
				// pattern may still be what protects it.
				if !on && !slices.Contains(a.Protected.Names(), name) {
					fmt.Fprintln(w, i18n.T("protect.notlisted", name))
					if a.Config.ProtectMatches(name) {
						fmt.Fprintln(w, i18n.T("toast.pattern", name))
					}
					continue
				}
				if err := a.Protected.Set(name, on); err != nil {
					return err
				}
				key := "toast.protected"
				if !on {
					key = "toast.unprotected"
				}
				fmt.Fprintln(w, i18n.T(key, name))
				// The pattern still protects a name removed from the list.
				if !on && a.Config.ProtectMatches(name) {
					fmt.Fprintln(w, i18n.T("toast.pattern", name))
				}
			}
			return nil
		},
	}
}
