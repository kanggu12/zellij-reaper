// Package cli is the zellij-reaper command line.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/version"
)

// configPath is the --config flag shared by every command.
var configPath string

// Execute runs the command line and returns the process exit status.
func Execute() int {
	i18n.Init()
	return report(newRoot().Execute(), os.Stderr)
}

// report says on w what the error a command ended with means, and gives the
// exit status for it.
func report(err error, w io.Writer) int {
	switch {
	case err == nil:
		return 0
	// ctrl+c on a screen that has said what it had to say.
	case errors.Is(err, errInterrupted):
		return 130
	// doctor has already printed its findings.
	case err == errSilentExit:
		return 1
	// zellij missing is said in the language on screen, with what to do
	// about it; the engine's words for it are English.
	case errors.Is(err, reaper.ErrNoZellij):
		fmt.Fprintln(w, "zellij-reaper:", i18n.T("error.zellij"))
		fmt.Fprintln(w, i18n.T("error.zellij.fix"))
		return 1
	}
	fmt.Fprintln(w, "zellij-reaper:", err)
	// A settings file that does not load stops every command: say how to
	// get past it.
	var fe *config.FileError
	if errors.As(err, &fe) {
		fmt.Fprintln(w, i18n.T("config.broken", fe.Path))
	}
	// A mistyped command or flag: say where the right ones are.
	if msg := err.Error(); strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "unknown flag") || strings.HasPrefix(msg, "unknown shorthand flag") {
		fmt.Fprintln(w, i18n.T("help.hint"))
	}
	return 1
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
				// The first time, with no settings file, the setup
				// screen comes first: nothing runs until it is done.
				if firstTime() {
					a, err := load()
					if err != nil {
						return err
					}
					return setup(cmd, a, false)
				}
				return runTUI()
			}
			return runPlan(cmd.Context(), cmd.OutOrStdout(), false)
		},
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "settings file, instead of the default (see config path)")
	root.SetVersionTemplate("zellij-reaper {{.Version}}\n")
	root.AddCommand(
		cmdTUI(), cmdSetup(), cmdPlan(), cmdRun(), cmdLogs(), cmdInstall(), cmdUninstall(), cmdConfig(),
		cmdProtect(true), cmdProtect(false), cmdKeepName(true), cmdKeepName(false), cmdDelete(), cmdRename(), cmdHere(), cmdShellHook(),
		cmdDoctor(), cmdRecover(), cmdResize(), cmdVersion(),
	)
	fitHelp(root)
	return root
}

// load reads the settings for a command.
func load() (*app.App, error) { return app.Load(configPath) }

// isTerminal reports whether f is a terminal; nil (output that is not a
// file) is not.
func isTerminal(f *os.File) bool { return f != nil && term.IsTerminal(f.Fd()) }

// background is what the terminal said about its background, asked once.
var background struct {
	// once guards the asking.
	once sync.Once
	// dark is the answer; a terminal that gives none counts as dark.
	dark bool
}

// theme picks colors for the terminal on stdout. Output that is not a
// terminal is printed without colors, so the choice does not matter there.
func theme() ui.Theme {
	if !isTerminal(os.Stdout) {
		return ui.NewTheme(true)
	}
	// One question for the whole command: a terminal that answers none makes
	// each asking wait out its time limit, and a command that picks its colors
	// in three places waited three times.
	background.once.Do(func() { background.dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout) })
	return ui.NewTheme(background.dark)
}

// themeWithoutAsking picks colors without asking the terminal, for a command
// that reads answers from it: the background query reads its reply from the
// same input, and took an answer typed ahead for it, so the question then
// waited for an answer already given. COLORFGBG says the background where a
// terminal sets it; otherwise the colors are the dark ones.
func themeWithoutAsking() ui.Theme {
	dark := true
	if v := os.Getenv("COLORFGBG"); v != "" {
		f := strings.Split(v, ";")
		if bg, err := strconv.Atoi(f[len(f)-1]); err == nil {
			// 7 and 9 to 15 are the light ones.
			dark = bg != 7 && bg < 9
		}
	}
	return ui.NewTheme(dark)
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
				// Without a terminal the screen library fails with its own
				// words about /dev/tty; say what to run instead.
				if !isTerminal(os.Stdout) || !isTerminal(os.Stdin) {
					return errors.New(i18n.T("tui.notty"))
				}
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
	// launchd does not say when it runs a job next: its interval does.
	case st.Next.IsZero() && st.Interval > 0:
		return ui.Fg(t.Muted).Render(i18n.T("header.every", duration.Format(st.Interval)))
	case st.Next.IsZero():
		return ui.Fg(t.Muted).Render(i18n.T("header.next", "?"))
	}
	return ui.Fg(t.Muted).Render(i18n.T("header.next", ui.Clock(st.Next)))
}
