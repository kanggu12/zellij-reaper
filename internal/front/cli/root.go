// Package cli is the zellij-reaper command line.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/version"
)

// configPath is the --config flag shared by every command.
var configPath string

// Execute runs the command line and returns the process exit status.
func Execute() int {
	i18n.Init()
	root := newRoot()
	if err := root.Execute(); err != nil {
		// doctor has already printed its findings.
		if err != errSilentExit {
			fmt.Fprintln(os.Stderr, "zellij-reaper:", err)
			// A mistyped command or flag: say where the right ones are.
			if msg := err.Error(); strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "unknown flag") || strings.HasPrefix(msg, "unknown shorthand flag") {
				fmt.Fprintln(os.Stderr, i18n.T("help.hint"))
			}
		}
		return 1
	}
	return 0
}

// newRoot builds the command tree.
func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "zellij-reaper",
		Short: "Reap idle and exited zellij sessions, safely",
		Long: `zellij-reaper deletes zellij sessions nobody uses: running ones with no client
attached and every pane at a shell prompt, and exited ones, each after a wait you
set. A pass keeps every session with a client attached, a busy pane, or a
protected name or program.

Run it without a command in a terminal to open the dashboard.`,
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// No command: the dashboard in a terminal, the plan otherwise.
		RunE: func(cmd *cobra.Command, args []string) error {
			if isTerminal(os.Stdout) && isTerminal(os.Stdin) {
				return runTUI()
			}
			return runPlan(cmd.Context(), cmd.OutOrStdout(), false)
		},
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "settings file (default ~/.config/zellij-reaper/config.toml)")
	root.SetVersionTemplate("zellij-reaper {{.Version}}\n")
	root.AddCommand(
		cmdTUI(), cmdPlan(), cmdRun(), cmdLogs(), cmdInstall(), cmdUninstall(), cmdConfig(),
		cmdProtect(true), cmdProtect(false), cmdKeepName(true), cmdKeepName(false), cmdHere(), cmdShellHook(),
		cmdDoctor(), cmdRecover(), cmdResize(), cmdVersion(),
	)
	return root
}

// load reads the settings for a command.
func load() (*app.App, error) { return app.Load(configPath) }

// isTerminal reports whether f is a terminal; nil (output that is not a
// file) is not.
func isTerminal(f *os.File) bool { return f != nil && term.IsTerminal(f.Fd()) }

// theme picks colors for the terminal on stdout. Output that is not a
// terminal is printed without colors, so the choice does not matter there.
func theme() ui.Theme {
	if !isTerminal(os.Stdout) {
		return ui.NewTheme(true)
	}
	return ui.NewTheme(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
}

// println prints styled text, downsampled to what the output can show:
// colors are dropped for pipes, files and NO_COLOR.
func println(w io.Writer, s ...any) { lipgloss.Fprintln(w, s...) }

// runTUI opens the dashboard.
func runTUI() error {
	a, err := load()
	if err != nil {
		return err
	}
	return tui.Run(a)
}

func cmdTUI() *cobra.Command {
	var snap, look, pick, keys string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if snap == "" {
				return runTUI()
			}
			// One frame to stdout, for the documentation's screenshots.
			var w, h int
			if _, err := fmt.Sscanf(snap, "%dx%d", &w, &h); err != nil || w < 40 || h < 10 {
				return fmt.Errorf("--snapshot wants WIDTHxHEIGHT, like 132x34")
			}
			a, err := load()
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), tui.Snapshot(a, w, h, look != "light", pick, keys))
			return nil
		},
	}
	cmd.Flags().StringVar(&snap, "snapshot", "", "print one frame of WIDTHxHEIGHT and exit")
	cmd.Flags().StringVar(&look, "theme", "dark", "colors for --snapshot: dark or light")
	cmd.Flags().StringVar(&pick, "select", "", "session to select in --snapshot")
	cmd.Flags().StringVar(&keys, "keys", "", "keys to press before --snapshot prints, like \"r tab\"")
	cmd.Flags().MarkHidden("select")
	cmd.Flags().MarkHidden("keys")
	cmd.Flags().MarkHidden("snapshot")
	cmd.Flags().MarkHidden("theme")
	return cmd
}

func cmdVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "zellij-reaper", version.String())
		},
	}
}

// headline is the one-line summary at the top of `plan` and the dashboard:
// the zellij version, the mode, the waits and the next timer run.
func headline(ctx context.Context, a *app.App, t ui.Theme) string {
	sep := ui.Fg(t.Rule).Render(" · ")
	parts := []string{lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render("zellij-reaper")}
	if v, err := a.Zellij.Version(ctx); err == nil {
		parts = append(parts, ui.Fg(t.Muted).Render("zellij "+v))
	}
	mode := ui.Fg(t.Cat[ui.CatAttached]).Render(i18n.T("header.live"))
	if a.Config.DryRun {
		mode = ui.Fg(t.Cat[ui.CatReap]).Render(i18n.T("header.dry"))
	}
	parts = append(parts, mode,
		ui.Fg(t.Muted).Render(i18n.T("settings.waits", duration.Format(a.Config.IdleAfter), duration.Format(a.Config.ExitedAfter))))
	parts = append(parts, timerWords(a.System.Status(), t))
	return strings.Join(parts, sep)
}

// timerWords describes the timer: when it runs next, or that it is off.
func timerWords(st timers.TimerStatus, t ui.Theme) string {
	switch {
	case !st.Installed:
		return ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("header.timer.none"))
	case !st.Enabled || !st.Active:
		return ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("header.timer.off"))
	case st.Next.IsZero():
		return ui.Fg(t.Muted).Render(i18n.T("header.next", "?"))
	}
	return ui.Fg(t.Muted).Render(i18n.T("header.next", ui.Clock(st.Next)))
}
