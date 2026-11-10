package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func cmdInstall() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the timers that run a pass every interval",
		Long: `Set up the timers that run the passes: on Linux, systemd user units in
~/.config/systemd/user with lingering turned on, so they run without a login; on
macOS, launch agents in ~/Library/LaunchAgents; on Windows, two tasks in the Task
Scheduler. A settings file is written if there is none. On Linux an install of the
earlier shell version is moved over: its settings go to the settings file, its log
next to the new one. Running install again after changing the interval applies it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			return install(cmd, a, load)
		},
	}
}

// install writes and starts the timer for a, then prints what the next pass
// will do. reload reads the settings again after a migration.
func install(cmd *cobra.Command, a *app.App, reload func() (*app.App, error)) error {
	exe, err := app.Executable()
	if err != nil {
		return err
	}
	// Put zellij's own directory first in the service's PATH.
	zdir := ""
	if p, err := exec.LookPath(a.Zellij.Bin); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			zdir = filepath.Dir(abs)
		}
	}
	// The timer runs with this shell's places for the settings, the state and
	// zellij's sessions, so it reads what the dashboard here reads.
	res, err := a.System.Install(timers.InstallOptions{Exe: exe, ZellijDir: zdir, Config: a.Config, ConfigFile: chosenConfig(), Env: timers.CarriedEnv(a.Env)})
	printResult(cmd, res)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	t := theme()
	// Reload: a migrated settings file changes the interval.
	if b, err := reload(); err == nil {
		a = b
	}
	println(w, ok(t)+i18n.T("install.enabled", duration.Format(a.Config.Interval)))
	// Lingering is systemd's alone.
	if linger := res.Linger; linger != "" {
		switch linger {
		case "already":
			linger = i18n.T("linger.already")
		case "enabled":
			linger = i18n.T("linger.enabled")
		}
		println(w, ok(t)+i18n.T("install.linger", linger))
	}
	// When the next pass runs and what it would reap: on a fresh
	// install there is time to protect a session first.
	if next := a.System.Status().NextPass(); !next.IsZero() {
		r, err := reaper.Plan(cmd.Context(), a.Reaper, reaper.Options{})
		println(w, nextPassLine(t, r, err, next, a.Config.DryRun, time.Now()))
	}
	// What this shell set through the environment applies here only.
	for _, m := range envOnly(a.Config, settingsPath()) {
		println(w, warn(t)+m)
	}
	println(w)
	println(w, i18n.T("install.done"))
	return nil
}

// envOnly words, one line each, the settings taken from ZELLIJ_REAPER_*
// variables. The timer's service does not run in this shell, so its passes
// use the file's value instead; file is where the setting should go.
func envOnly(c *config.Config, file string) []string {
	var out []string
	for _, k := range config.Keys {
		if c.Sources[k] == config.FromEnv {
			out = append(out, i18n.T("env.unseen", config.EnvNames[k], k, tilde(file)))
		}
	}
	return out
}

// printResult lists what an install or an uninstall did, then what it has to
// say about it.
func printResult(cmd *cobra.Command, r timers.Result) {
	printDone(cmd, r)
	printNotes(cmd, r)
}

// printDone lists what an install or an uninstall wrote, moved and removed,
// and returns how many lines that took.
func printDone(cmd *cobra.Command, r timers.Result) int {
	w, t, n := cmd.OutOrStdout(), theme(), 0
	// line prints one of them and counts it.
	line := func(text string) {
		println(w, ok(t)+text)
		n++
	}
	for _, p := range r.Wrote {
		line(i18n.T("install.wrote", shown(p)))
	}
	if r.Migrated != "" {
		line(i18n.T("install.migrated", tilde(r.Migrated)))
	}
	if r.Created != "" {
		line(i18n.T("install.created", tilde(r.Created)))
	}
	for _, m := range r.Moved {
		// "from -> to", each shortened to ~ like the other paths.
		if from, to, found := strings.Cut(m, " -> "); found {
			m = tilde(from) + " → " + tilde(to)
		}
		line(i18n.T("install.moved", m))
	}
	for _, p := range r.Removed {
		line(i18n.T("install.removed", shown(p)))
	}
	return n
}

// printNotes lists what an install or an uninstall has to say about what it
// did: its warnings, then each directory a purge left in place with what is
// still in it. It returns how many lines that took.
func printNotes(cmd *cobra.Command, r timers.Result) int {
	w, t := cmd.OutOrStdout(), theme()
	for _, m := range r.Warnings {
		println(w, warn(t)+m)
	}
	for _, l := range r.Left {
		println(w, warn(t)+i18n.T("uninstall.left", tilde(l.Dir), someOf(l.Names)))
	}
	return len(r.Warnings) + len(r.Left)
}

// shown is something a Result names, as a person reads it: a path with the
// home directory as "~", and the crontab, which is not a file of the
// reaper's to name, in words that say only its own lines changed.
func shown(p string) string {
	if p == timers.CronJobs {
		return i18n.T("install.crontab")
	}
	return tilde(p)
}

// leftShown is how many of the names in a directory a purge left are listed
// before the rest is counted.
const leftShown = 3

// someOf lists file names on one line: the first few, then how many more.
func someOf(names []string) string {
	shown := make([]string, 0, leftShown)
	for _, name := range names[:min(len(names), leftShown)] {
		// A name with a line break or an escape in it would redraw the
		// terminal, not name the file.
		shown = append(shown, strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '?'
			}
			return r
		}, name))
	}
	out := strings.Join(shown, ", ")
	if more := len(names) - len(shown); more > 0 {
		// The setup screen's words for the rest of a list.
		out += " " + i18n.T("setup.more", more)
	}
	return out
}

// chosenConfig is a settings file picked with --config or
// ZELLIJ_REAPER_CONFIG, for the timer to read too; "" for the default.
func chosenConfig() string {
	p := configPath
	if p == "" {
		p = os.Getenv("ZELLIJ_REAPER_CONFIG")
	}
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func cmdUninstall() *cobra.Command {
	var purge bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the timers",
		Long: `Stop the timers and remove them. On Linux that is both the systemd user units and
the reaper's lines in the crontab, whichever of the two runs the passes today. The
settings, the log and the lists stay, for an install later to go on with, and so
does lingering, which other services of yours may rely on.

--purge removes the settings, the log and the lists too, and takes the prompt hook
out of the shells' startup files: the line setup adds, where it still reads as
setup wrote it. The settings and state directories go when nothing else is in them;
a file that is not the reaper's is left, and named. The program itself stays:
delete its file, or remove the package it came in.

uninstall also works where the settings file does not load.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Not load(): what stops it, a settings file or a protected list
			// that cannot be read, must not keep the timers on a machine
			// somebody wants them off.
			a, unread := app.ForUninstall(configPath)
			return uninstall(cmd, a, purge, unread)
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the settings, the log, the protected list, the lists of given and kept names, and the prompt hook")
	return cmd
}

// uninstall removes the timers and says what it did. unread is why the
// settings did not load, where they did not: a then holds the defaults.
func uninstall(cmd *cobra.Command, a *app.App, purge bool, unread error) error {
	res, err := a.System.Uninstall(purge, a.Config.LogPath)
	if err != nil {
		// What it got done before it stopped, then why it stopped. The
		// startup files are left alone: the reaper is still half installed.
		printResult(cmd, res)
		return err
	}
	w, t := cmd.OutOrStdout(), theme()
	// First everything that is gone, then everything there is to say about
	// it, so the marks read in two runs and not mixed.
	lines := printDone(cmd, res)
	var hook unhooked
	if purge {
		// The home of the settings' environment, which is where setup looked
		// for the startup files too.
		hook = removeHooks(a.Env.Home())
		for _, file := range hook.removed {
			println(w, ok(t)+i18n.T("uninstall.unhooked", tilde(file)))
		}
		lines += len(hook.removed)
		// The log that went is the one the settings name. Without the
		// settings that is the default one, and another may be left.
		if unread != nil {
			println(w, warn(t)+i18n.T("uninstall.nosettings"))
			lines++
		}
	}
	lines += printNotes(cmd, res)
	for _, file := range hook.stays {
		println(w, warn(t)+i18n.T("uninstall.hook.stays", tilde(file)))
	}
	for _, err := range hook.failed {
		// A startup file that could not be changed fails nothing: the timers
		// are gone, and the line it keeps is named.
		println(w, warn(t)+i18n.T("uninstall.hook.failed", err))
	}
	// The list stands apart from the words that close it, as install's does.
	if lines+len(hook.stays)+len(hook.failed) > 0 {
		println(w)
	}
	// The timers are said to be removed only where there were some: a second
	// uninstall, or one on a machine that never had them, finds none.
	if res.Timers > 0 {
		println(w, i18n.T("uninstall.done"))
	} else {
		println(w, i18n.T("uninstall.none"))
	}
	muted := ui.Fg(t.Muted)
	if !purge {
		println(w, muted.Render(i18n.T("uninstall.kept")))
		return nil
	}
	// A purge leaves one thing, which it cannot take itself: a running
	// program's file cannot be deleted on Windows, and a package's file is
	// the package manager's to remove.
	if exe, err := app.Executable(); err == nil {
		println(w, muted.Render(i18n.T("uninstall.program", tilde(exe))))
	}
	return nil
}

// ok, warn and bad prefix a line with a colored mark.
func ok(t ui.Theme) string   { return "  " + ui.Fg(t.Cat[ui.CatAttached]).Render("✓") + " " }
func warn(t ui.Theme) string { return "  " + ui.Fg(t.Cat[ui.CatReap]).Render("!") + " " }
func bad(t ui.Theme) string  { return "  " + ui.Fg(t.Cat[ui.CatUnknown]).Render("✗") + " " }

// tilde shortens the home directory to "~" for display.
func tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && !filepath.IsAbs(rel) && rel != ".." && !startsWithDotDot(rel) {
		return filepath.Join("~", rel)
	}
	return p
}

func startsWithDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)
}

// nextPassLine is what install says about the next pass: when it runs, and
// what it would reap then, counted at that time as plan and the setup screen
// count, so a session a few minutes short of its wait now is in it.
func nextPassLine(t ui.Theme, r reaper.Report, planErr error, next time.Time, dry bool, now time.Time) string {
	at, in := ui.Clock(next), duration.Format(max(time.Minute, next.Sub(now).Round(time.Minute)))
	due := r.DueAtNext(next, now)
	switch {
	case dry:
		return ok(t) + i18n.T("install.next.dry", at, in)
	case planErr != nil:
		return ok(t) + i18n.T("install.next.at", at, in)
	case due == 0:
		return ok(t) + i18n.T("install.next.none", at, in)
	}
	return warn(t) + i18n.TN("install.next", due, at, in, due)
}
