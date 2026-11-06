package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	res, err := a.System.Install(timers.InstallOptions{Exe: exe, ZellijDir: zdir, Config: a.Config, ConfigFile: chosenConfig()})
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
	if st := a.System.Status(); !st.Next.IsZero() {
		at, in := ui.Clock(st.Next), duration.Format(max(time.Minute, time.Until(st.Next).Round(time.Minute)))
		r, err := reaper.Plan(cmd.Context(), a.Reaper, reaper.Options{})
		switch {
		case a.Config.DryRun:
			println(w, ok(t)+i18n.T("install.next.dry", at, in))
		case err != nil:
			println(w, ok(t)+i18n.T("install.next.at", at, in))
		case r.Due() == 0:
			println(w, ok(t)+i18n.T("install.next.none", at, in))
		default:
			println(w, warn(t)+i18n.TN("install.next", r.Due(), at, in, r.Due()))
		}
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

// printResult lists what an install or uninstall wrote, moved and removed.
func printResult(cmd *cobra.Command, r timers.Result) {
	w := cmd.OutOrStdout()
	t := theme()
	for _, p := range r.Wrote {
		println(w, ok(t)+i18n.T("install.wrote", tilde(p)))
	}
	if r.Migrated != "" {
		println(w, ok(t)+i18n.T("install.migrated", tilde(r.Migrated)))
	}
	if r.Created != "" {
		println(w, ok(t)+i18n.T("install.created", tilde(r.Created)))
	}
	for _, m := range r.Moved {
		// "from -> to", each shortened to ~ like the other paths.
		if from, to, found := strings.Cut(m, " -> "); found {
			m = tilde(from) + " → " + tilde(to)
		}
		println(w, ok(t)+i18n.T("install.moved", m))
	}
	for _, p := range r.Removed {
		println(w, ok(t)+i18n.T("install.removed", tilde(p)))
	}
	for _, m := range r.Warnings {
		println(w, warn(t)+m)
	}
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
		Short: "Remove the timer and the service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			res, err := a.System.Uninstall(purge, a.Config.LogPath)
			printResult(cmd, res)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			println(w, i18n.T("uninstall.done"))
			if !purge {
				println(w, ui.Fg(theme().Muted).Render(i18n.T("uninstall.kept")))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the settings, the log, the protected list and the lists of given and kept names")
	return cmd
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
