package systemd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// Result says what an install or an uninstall did.
type Result struct {
	// Wrote and Removed list the files written and deleted.
	Wrote, Removed []string
	// Moved lists "from -> to" for files moved (legacy logs).
	Moved []string
	// Migrated is the settings file written from a legacy install, if any.
	Migrated string
	// Created is the settings file written fresh, if any.
	Created string
	// Linger is "already", "enabled" or the error that stopped it.
	Linger string
	// Warnings are things install did not do and the person should know.
	Warnings []string
}

// InstallOptions are the inputs to Install.
type InstallOptions struct {
	// Exe is the absolute path of the zellij-reaper binary.
	Exe string
	// ZellijDir is the directory holding zellij, if known.
	ZellijDir string
	// Config is the effective settings (for the interval and paths).
	Config *config.Config
	// ConfigFile is a settings file chosen with --config or
	// ZELLIJ_REAPER_CONFIG; empty for the default location.
	ConfigFile string
}

// Install writes the units, moves a legacy install over, writes a settings
// file if there is none, and enables the timer and lingering.
func (s System) Install(o InstallOptions) (Result, error) {
	var r Result
	// Refused before anything is written: a unit that cannot hold these
	// paths would run something else, or nothing.
	if err := quotable("the zellij-reaper binary", o.Exe); err != nil {
		return r, err
	}
	if err := quotable("the settings file", o.ConfigFile); err != nil {
		return r, err
	}
	// zellij's directory goes inside the quoted PATH assignment.
	if err := quotable("zellij's directory", o.ZellijDir); err != nil {
		return r, err
	}
	if !s.Available() {
		return r, errors.New("no systemd user instance answers (on WSL, set systemd=true in /etc/wsl.conf)")
	}
	// The shell version's settings live in its service file, which is about
	// to be overwritten: read them first.
	legacy := DetectLegacy(s.Env)
	// The log the timer will write: the one the settings in effect name,
	// unless the settings are about to be moved over from the shell version.
	logPath := o.Config.LogPath
	// The settings come first: if they cannot be written, the shell
	// version's service (and its settings) must stay as they are.
	cfgPath := config.ConfigFile(s.Env)
	if o.ConfigFile != "" {
		cfgPath = o.ConfigFile
	}
	if _, err := os.Stat(cfgPath); err == nil && legacy != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("kept the settings in %s; the shell version's own settings were not moved over", cfgPath))
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		var text string
		if legacy != nil {
			migrated := legacy.Config(s.Env)
			text = config.Render(migrated, "Moved from the settings of the earlier shell version's service file.")
			r.Migrated = cfgPath
			// The moved settings keep a log path the shell version was given
			// on purpose; its log has to end up there, not at the default.
			logPath = migrated.LogPath
		} else {
			text = config.Render(config.Default(s.Env), "zellij-reaper settings. Run `zellij-reaper config show` to see the values in effect.")
			r.Created = cfgPath
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return r, err
		}
		if err := writeFile(cfgPath, text); err != nil {
			return r, err
		}
	}
	if err := os.MkdirAll(config.UnitDir(s.Env), 0o755); err != nil {
		return r, err
	}
	if err := writeFile(s.servicePath(), ServiceUnit(o.Exe, o.ZellijDir, o.ConfigFile)); err != nil {
		return r, err
	}
	if err := writeFile(s.timerPath(), TimerUnit(o.Config.Interval)); err != nil {
		return r, err
	}
	r.Wrote = append(r.Wrote, s.servicePath(), s.timerPath())
	if legacy != nil {
		moved, removed := legacy.Retire(s.Env, logPath)
		r.Moved, r.Removed = append(r.Moved, moved...), append(r.Removed, removed...)
	}
	if out, err := s.systemctl("daemon-reload"); err != nil {
		return r, fmt.Errorf("systemctl --user daemon-reload: %s", firstLine(out, err))
	}
	if out, err := s.systemctl("enable", "--now", TimerName); err != nil {
		return r, fmt.Errorf("systemctl --user enable --now %s: %s", TimerName, firstLine(out, err))
	}
	// Without lingering, a user's timers stop when their last login ends.
	r.Linger = s.enableLinger()
	return r, nil
}

// enableLinger turns on lingering for the user, if it is not on yet.
func (s System) enableLinger() string {
	if out, err := s.Run("loginctl", "show-user", s.User, "--property=Linger"); err == nil && strings.Contains(out, "Linger=yes") {
		return "already"
	}
	if out, err := s.Run("loginctl", "enable-linger", s.User); err != nil {
		return firstLine(out, err)
	}
	return "enabled"
}

// Uninstall stops and removes the timer and the service. With purge it also
// removes the settings, the log and the protected list.
func (s System) Uninstall(purge bool, logPath string) (Result, error) {
	var r Result
	if s.Available() {
		// "not loaded" is fine: there may be nothing to disable.
		s.systemctl("disable", "--now", TimerName)
	}
	for _, p := range []string{s.timerPath(), s.servicePath()} {
		if err := os.Remove(p); err == nil {
			r.Removed = append(r.Removed, p)
		}
	}
	if s.Available() {
		s.systemctl("daemon-reload")
	}
	if purge {
		// The protected list's own lock file goes too, or it alone would
		// keep the state directory from being removed.
		for _, p := range []string{config.ConfigFile(s.Env), logPath, logPath + ".1", config.ProtectedFile(s.Env),
			protect.LockPath(config.ProtectedFile(s.Env)), config.LockFile(s.Env)} {
			if err := os.Remove(p); err == nil {
				r.Removed = append(r.Removed, p)
			}
		}
		// Remove the directories too, but only if nothing else is in them.
		os.Remove(config.ConfigDir(s.Env))
		os.Remove(config.StateDir(s.Env))
	}
	return r, nil
}
