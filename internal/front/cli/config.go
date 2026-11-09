package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func cmdConfig() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, locate, create or edit the settings",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return showConfig(cmd) },
	}
	cmd.AddCommand(
		&cobra.Command{Use: "show", Short: "Print every setting with where its value comes from", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error { return showConfig(cmd) }},
		&cobra.Command{Use: "path", Short: "Print the settings file's path", Args: cobra.NoArgs,
			Run: func(cmd *cobra.Command, args []string) { fmt.Fprintln(cmd.OutOrStdout(), settingsPath()) }},
		&cobra.Command{Use: "init", Short: "Write a settings file with every key and its default", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p := settingsPath()
				if _, err := os.Stat(p); err == nil {
					return fmt.Errorf("%s already exists", p)
				}
				if err := writeDefaults(p); err != nil {
					return err
				}
				// Say where it went: the next step is to open it.
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("config.written", p))
				return nil
			}},
		&cobra.Command{Use: "edit", Short: "Open the settings in $VISUAL or $EDITOR (writing defaults first if needed)", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				p := settingsPath()
				if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
					if err := writeDefaults(p); err != nil {
						return err
					}
				}
				editor := os.Getenv("VISUAL")
				if editor == "" {
					editor = os.Getenv("EDITOR")
				}
				// vi is everywhere but on Windows, where Notepad is.
				if strings.TrimSpace(editor) == "" {
					editor = "vi"
					if runtime.GOOS == "windows" {
						editor = "notepad"
					}
				}
				// $EDITOR may carry arguments ("code --wait"), as shells and
				// git take it.
				words := strings.Fields(editor)
				c := exec.Command(words[0], append(words[1:], p)...)
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := c.Run(); err != nil {
					return err
				}
				// Check what was saved, so a typo is caught now, not at the
				// next timer run.
				if _, err := config.Load(p, config.OSEnv{}); err != nil {
					return fmt.Errorf("the settings have a problem: %w", err)
				}
				return nil
			}},
	)
	return cmd
}

// settingsPath is --config, or the default location.
func settingsPath() string {
	if configPath != "" {
		return configPath
	}
	return config.ConfigFile(config.OSEnv{})
}

// writeDefaults writes a fresh, fully commented settings file.
func writeDefaults(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(config.Render(config.Default(config.OSEnv{}), "zellij-reaper settings.")), 0o644)
}

// showConfig prints each setting, its value and its source.
func showConfig(cmd *cobra.Command) error {
	a, err := load()
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	t := theme()
	file := a.Config.File
	if file == "" {
		file = i18n.T("config.notcreated", settingsPath())
	}
	println(w, ui.Fg(t.Muted).Render("# "+tilde(file)))
	// The values line up after the longest key, and the sources in one
	// column after the longest value.
	keyWidth := 0
	for _, k := range config.Keys {
		keyWidth = max(keyWidth, len(k))
	}
	lines := make([]string, len(config.Keys))
	width := 0
	for i, k := range config.Keys {
		value := a.Config.Value(k)
		// A path under the home directory reads as "~/...", as above.
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			value = strings.Replace(value, `"`+home+"/", `"~/`, 1)
		}
		lines[i] = fmt.Sprintf("%-*s = %s", keyWidth, k, value)
		width = max(width, len(lines[i]))
	}
	for i, k := range config.Keys {
		src := string(a.Config.Sources[k])
		if src == string(config.FromEnv) {
			src += " " + config.EnvNames[k]
		}
		println(w, ui.Fg(t.Ink).Render(fmt.Sprintf("%-*s", width, lines[i]))+"  "+ui.Fg(t.Muted).Render("# "+src))
	}
	return nil
}
