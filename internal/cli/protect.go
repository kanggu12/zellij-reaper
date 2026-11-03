package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/i18n"
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
				return fmt.Errorf("name a session to unprotect")
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
				for _, n := range a.Protected.Names() {
					fmt.Fprintln(w, n)
				}
				return nil
			}
			for _, name := range args {
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
