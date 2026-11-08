package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

func cmdSetup() *cobra.Command {
	var snap, look, keys string
	var installer bool
	var f setupFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Choose the waits, the mode and the sessions to keep, then start the timers",
		Long: `Open a screen that shows what the first pass would reap and lets you choose: how
long idle and exited sessions wait, whether passes reap or only log what they would
reap, whether sessions are named after what they show, the prompt hook, and which
of the sessions it lists to keep. Enter writes the choices into the settings file,
leaving the rest of it as it is, puts the kept sessions on the protected list and
starts the timers, as install does. Esc changes nothing.

With any of the flags below it opens no screen: the flags are the choices, what
they leave out stays as the settings have it, and it then does what Enter does.
That works anywhere, in a script or a shell without a terminal too:

  zellij-reaper setup --idle-after 4h --exited-after 1w --dry-run --protect api

The installers open it in a terminal on a first install, and with no settings file
zellij-reaper opens it instead of the dashboard.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range setupFlagNames {
				if cmd.Flags().Changed(name) {
					f.set = append(f.set, name)
				}
			}
			if err := choicesInHiddenMode(f.set, snap, installer); err != nil {
				return err
			}
			if snap != "" {
				return setupSnapshot(cmd, snap, look, keys)
			}
			if installer {
				return fromInstaller(cmd)
			}
			// Choices given as flags: no screen, so no terminal needed.
			if len(f.set) > 0 {
				a, err := load()
				if err != nil {
					return err
				}
				return setupWith(cmd, a, f, load)
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
	cmd.Flags().StringVar(&f.idle, "idle-after", "", "reap running sessions idle this long, like 4h (idle_after)")
	cmd.Flags().StringVar(&f.exited, "exited-after", "", "reap exited sessions kept this long, like 1w (exited_after)")
	cmd.Flags().BoolVar(&f.dry, "dry-run", false, "only log what passes would reap (dry_run); --dry-run=false reaps")
	cmd.Flags().BoolVar(&f.rename, "auto-rename", false, "name sessions after what they show (auto_rename); --auto-rename=false stops it")
	cmd.Flags().BoolVar(&f.hook, "hook", false, "add the prompt hook to your shell's startup file")
	cmd.Flags().StringSliceVar(&f.protect, "protect", nil, "put these sessions on the protected list (repeat it, or separate names with commas)")
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

// setupFlagNames are the flags that give setup's choices on the command line.
var setupFlagNames = []string{"idle-after", "exited-after", "dry-run", "auto-rename", "hook", "protect"}

// setupFlags are the setup screen's choices, given as flags.
type setupFlags struct {
	idle, exited      string
	dry, rename, hook bool
	protect           []string
	// set lists the flags given: one left out changes nothing.
	set []string
}

// choicesInHiddenMode refuses choices given with --snapshot or --installer,
// whose modes would drop them without a word.
func choicesInHiddenMode(set []string, snap string, installer bool) error {
	if len(set) > 0 && (snap != "" || installer) {
		return fmt.Errorf("--snapshot and --installer take none of the choices (--%s)", set[0])
	}
	return nil
}

// given reports whether a flag was given.
func (f setupFlags) given(name string) bool { return slices.Contains(f.set, name) }

// setupWith is setup without the screen: the flags given are the choices, and
// what they leave out stays as the settings have it. Every value is checked
// before anything is written; then it does what Enter does in the screen.
func setupWith(cmd *cobra.Command, a *app.App, f setupFlags, reload func() (*app.App, error)) error {
	// The settings in effect with the new values, to check them together.
	c := *a.Config
	// A new settings file starts from the defaults, as the screen's does.
	base := *config.Default(config.OSEnv{})
	values := map[string]string{}
	for _, d := range []struct {
		flag, key, value string
		to               []*time.Duration
	}{
		{"idle-after", "idle_after", f.idle, []*time.Duration{&c.IdleAfter, &base.IdleAfter}},
		{"exited-after", "exited_after", f.exited, []*time.Duration{&c.ExitedAfter, &base.ExitedAfter}},
	} {
		if !f.given(d.flag) {
			continue
		}
		v, err := duration.Parse(d.value)
		if err != nil {
			return fmt.Errorf("--%s: %w", d.flag, err)
		}
		for _, p := range d.to {
			*p = v
		}
		values[d.key] = strconv.Quote(duration.Setting(v))
	}
	if f.given("dry-run") {
		c.DryRun, base.DryRun = f.dry, f.dry
		values["dry_run"] = strconv.FormatBool(f.dry)
	}
	if f.given("auto-rename") {
		c.AutoRename, base.AutoRename = f.rename, f.rename
		values["auto_rename"] = strconv.FormatBool(f.rename)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	// The names too: one the list file cannot hold would stop the setup
	// halfway, with the settings written and nothing protected.
	var names []string
	for _, name := range f.protect {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		if err := protect.Storable(name); err != nil {
			return fmt.Errorf("--protect: %w", err)
		}
		names = append(names, name)
	}
	w, t := cmd.OutOrStdout(), theme()
	if len(values) > 0 {
		path := settingsPath()
		if err := config.SetKeys(path, &base, values); err != nil {
			return err
		}
		println(w, ok(t)+i18n.T("setup.saved", tilde(path)))
	}
	for _, name := range names {
		if err := a.Protected.Set(name, true); err != nil {
			return err
		}
		println(w, ok(t)+i18n.T("setup.protected", name))
	}
	if f.given("hook") && f.hook {
		shell := os.Getenv("SHELL")
		file, line := shellHook(shell, homeDir())
		switch {
		case file == "":
			// No hook to add is no reason to leave the timers off.
			name := filepath.Base(shell)
			if shell == "" {
				name = "-"
			}
			println(w, warn(t)+i18n.T("setup.hook.none", name))
		case hasHook(file):
			println(w, ok(t)+i18n.T("setup.hook.there", tilde(file)))
		default:
			if err := appendLine(file, line); err != nil {
				return err
			}
			println(w, ok(t)+i18n.T("setup.hooked", tilde(file)))
		}
	}
	// install reads the settings again, so the timers and its summary of the
	// next pass go by what was just written.
	b, err := reload()
	if err != nil {
		return err
	}
	return install(cmd, b, reload)
}

// shellHook returns the startup file of the shell in $SHELL and the line that
// adds the prompt hook to it, or "" for a shell that has no hook.
func shellHook(shell, home string) (file, line string) {
	if home == "" {
		return "", ""
	}
	switch filepath.Base(shell) {
	case "bash":
		return filepath.Join(home, ".bashrc"), `command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook bash)"`
	case "zsh":
		return filepath.Join(home, ".zshrc"), `command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook zsh)"`
	case "fish":
		return filepath.Join(home, ".config", "fish", "config.fish"), `zellij-reaper shell-hook fish | source`
	}
	return "", ""
}

// hasHook reports whether a startup file has the prompt hook already.
func hasHook(file string) bool {
	b, err := os.ReadFile(file)
	return err == nil && strings.Contains(string(b), "zellij-reaper shell-hook")
}

// hookFor is what the setup screen offers: the startup file and the hook's
// line, or "" when the shell has no hook or its file has one already.
func hookFor(shell, home string) (file, line string) {
	file, line = shellHook(shell, home)
	if file == "" || hasHook(file) {
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
// time the schedule comes round with cron. Where the timers run already, the
// next of their passes can come sooner, with the settings as they are, and
// an install does not always move it.
func firstPassAt(a *app.App) time.Time {
	now := time.Now()
	at := a.System.FirstPass(now, a.Config.Interval)
	if st := a.System.Status(); st.Active && st.Next.After(now) && st.Next.Before(at) {
		at = st.Next
	}
	return at
}
