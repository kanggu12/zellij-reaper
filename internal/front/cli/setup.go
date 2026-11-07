package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func cmdSetup() *cobra.Command {
	var snap, look, keys string
	var installer bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Choose the waits, the mode and the sessions to keep, then start the timers",
		Long: `Open a screen that shows what the first pass would reap and lets you choose: how
long idle and exited sessions wait, whether passes reap or only log what they would
reap, whether sessions are named after what they show, the prompt hook, and which
of the sessions it lists to keep. Enter writes the choices into the settings file,
leaving the rest of it as it is, puts the kept sessions on the protected list and
starts the timers, as install does. Esc changes nothing.

The installers open it in a terminal on a first install, and with no settings file
zellij-reaper opens it instead of the dashboard.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if snap != "" {
				return setupSnapshot(cmd, snap, look, keys)
			}
			if installer {
				return fromInstaller(cmd)
			}
			if !isTerminal(os.Stdout) || !isTerminal(os.Stdin) {
				return errors.New(i18n.T("setup.notty"))
			}
			a, err := load()
			if err != nil {
				return err
			}
			return setup(cmd, a, false)
		},
	}
	cmd.Flags().StringVar(&snap, "snapshot", "", "print one frame of WIDTHxHEIGHT and exit")
	cmd.Flags().StringVar(&look, "theme", "dark", "colors for --snapshot: dark or light")
	cmd.Flags().StringVar(&keys, "keys", "", "keys to press before --snapshot prints")
	cmd.Flags().BoolVar(&installer, "installer", false, "as the install scripts run it: the screen on a first install, install otherwise")
	for _, f := range []string{"snapshot", "theme", "keys", "installer"} {
		cmd.Flags().MarkHidden(f)
	}
	return cmd
}

// setupSnapshot prints one frame of the setup screen, for the
// documentation's screenshots.
func setupSnapshot(cmd *cobra.Command, snap, look, keys string) error {
	var w, h int
	if _, err := fmt.Sscanf(snap, "%dx%d", &w, &h); err != nil || w < 40 || h < 10 {
		return fmt.Errorf("--snapshot wants WIDTHxHEIGHT, like 100x30")
	}
	a, err := load()
	if err != nil {
		return err
	}
	hook, _ := hookFor(os.Getenv("SHELL"), homeDir())
	start, err := tui.StartFrom(cmd.Context(), a, firstPassAt(a), zellij.RenameReachable(), tilde(hook))
	if err != nil {
		return err
	}
	if hook == "" {
		start.HookFile = ""
	}
	fmt.Fprint(cmd.OutOrStdout(), tui.SetupSnapshot(start, w, h, look != "light", keys))
	return nil
}

// firstTime reports whether the dashboard should open the setup screen
// first: there is no settings file yet. install and setup always write one,
// so this is a machine where nothing has been set up.
func firstTime() bool {
	_, err := os.Stat(settingsPath())
	return errors.Is(err, os.ErrNotExist)
}

// fromInstaller is setup as the install scripts run it: an install must end
// with the timers running whatever happens on the way. An upgrade, with
// settings already there, only rewrites the timers; a first install in a
// terminal gets the screen, where esc keeps the defaults; anything that
// keeps the screen from opening falls back to the plain install.
func fromInstaller(cmd *cobra.Command) error {
	a, err := load()
	if err != nil {
		return err
	}
	if !firstTime() || !isTerminal(os.Stdout) || !isTerminal(os.Stdin) {
		return install(cmd, a, load)
	}
	if err := setup(cmd, a, true); err != nil {
		if errors.Is(err, errNoScreen) {
			println(cmd.ErrOrStderr(), i18n.T("setup.fallback", err))
			return install(cmd, a, load)
		}
		return err
	}
	return nil
}

// errNoScreen wraps a failure to show the setup screen, as opposed to one
// applying what was chosen.
var errNoScreen = errors.New("the setup screen did not open")

// setup shows the setup screen and applies what was chosen. From the
// installer, esc keeps the defaults and still starts the timers.
func setup(cmd *cobra.Command, a *app.App, installer bool) error {
	hook, hookLine := hookFor(os.Getenv("SHELL"), homeDir())
	start, err := tui.StartFrom(cmd.Context(), a, firstPassAt(a), zellij.RenameReachable(), tilde(hook))
	if err != nil {
		return err
	}
	if hook == "" {
		start.HookFile = ""
	}
	start.Installer = installer
	choice, how, err := tui.RunSetup(start)
	if err != nil {
		return fmt.Errorf("%w: %v", errNoScreen, err)
	}
	w := cmd.OutOrStdout()
	switch {
	case how == tui.SetupQuit:
		println(w, i18n.T("setup.left"))
		return nil
	case how == tui.SetupLeft && installer:
		// The defaults, as an install without the screen would set up.
		println(w, i18n.T("setup.defaults"))
		return install(cmd, a, load)
	case how == tui.SetupLeft:
		println(w, i18n.T("setup.left"))
		return nil
	}
	t := theme()
	// The values as the settings file words them.
	// A new file starts from the defaults: values from ZELLIJ_REAPER_*
	// variables apply to this shell only and must not become settings.
	c := *config.Default(config.OSEnv{})
	c.IdleAfter, c.ExitedAfter, c.DryRun, c.AutoRename = choice.IdleAfter, choice.ExitedAfter, choice.DryRun, choice.AutoRename
	values := map[string]string{
		"idle_after":   strconv.Quote(duration.Setting(choice.IdleAfter)),
		"exited_after": strconv.Quote(duration.Setting(choice.ExitedAfter)),
		"dry_run":      strconv.FormatBool(choice.DryRun),
	}
	if start.CanRename {
		values["auto_rename"] = strconv.FormatBool(choice.AutoRename)
	}
	path := settingsPath()
	if err := config.SetKeys(path, &c, values); err != nil {
		return err
	}
	println(w, ok(t)+i18n.T("setup.saved", tilde(path)))
	for _, name := range choice.Protect {
		if err := a.Protected.Set(name, true); err != nil {
			return err
		}
		println(w, ok(t)+i18n.T("setup.protected", name))
	}
	if choice.Hook {
		if err := appendLine(hook, hookLine); err != nil {
			return err
		}
		println(w, ok(t)+i18n.T("setup.hooked", tilde(hook)))
	}
	// install reads the settings again, so the timers and its summary of the
	// next pass go by what was just chosen.
	b, err := load()
	if err != nil {
		return err
	}
	return install(cmd, b, load)
}

// hookFor returns the startup file of the shell in $SHELL and the line that
// adds the prompt hook to it, or "" when the shell has no hook or its file
// has one already.
func hookFor(shell, home string) (file, line string) {
	if home == "" {
		return "", ""
	}
	switch filepath.Base(shell) {
	case "bash":
		file, line = filepath.Join(home, ".bashrc"), `command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook bash)"`
	case "zsh":
		file, line = filepath.Join(home, ".zshrc"), `command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook zsh)"`
	case "fish":
		file, line = filepath.Join(home, ".config", "fish", "config.fish"), `zellij-reaper shell-hook fish | source`
	default:
		return "", ""
	}
	if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), "zellij-reaper shell-hook") {
		return "", ""
	}
	return file, line
}

// appendLine adds a line at the end of a file, on a line of its own, making
// the file and its directory when they are not there.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	// A file that does not end in a newline would run the hook into its
	// last line.
	if len(b) > 0 && b[len(b)-1] != '\n' {
		line = "\n" + line
	}
	_, err = f.WriteString(line + "\n")
	return err
}

// homeDir is the home directory, or "".
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// firstPassAt is when the first pass after an install runs, as the scheduler
// that will run the passes starts them: ten minutes after the timers start
// with systemd and the Task Scheduler, one interval with launchd, the next
// time the schedule comes round with cron.
func firstPassAt(a *app.App) time.Time {
	return a.System.FirstPass(time.Now(), a.Config.Interval)
}
