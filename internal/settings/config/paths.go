package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Env is the process environment as the settings see it. Tests pass a MapEnv
// so they never read or write the real home directory.
type Env interface {
	// Lookup returns a variable and whether it is set.
	Lookup(key string) (string, bool)
	// Home returns the home directory.
	Home() string
}

// OSEnv reads the real environment.
type OSEnv struct{}

// Lookup implements Env with os.LookupEnv.
func (OSEnv) Lookup(key string) (string, bool) { return os.LookupEnv(key) }

// Home implements Env. $HOME wins, as it does for zellij itself; the passwd
// entry is the fallback for the rare service started without HOME.
func (OSEnv) Home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// MapEnv is a fixed environment for tests. HOME comes from the map too.
type MapEnv map[string]string

// Lookup implements Env.
func (m MapEnv) Lookup(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

// Home implements Env.
func (m MapEnv) Home() string { return m["HOME"] }

// xdg returns $name if it is an absolute path, else home/fallback, following
// the XDG Base Directory rule that relative values are ignored.
func xdg(env Env, name, fallback string) string {
	if v, ok := env.Lookup(name); ok && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(env.Home(), fallback)
}

// ConfigDir is where the settings file lives: ~/.config/zellij-reaper on Linux
// and macOS, as most command-line tools keep theirs, and %APPDATA% on Windows.
func ConfigDir(env Env) string {
	if GOOS == "windows" {
		if _, ok := env.Lookup("XDG_CONFIG_HOME"); !ok {
			return filepath.Join(appData(env), "zellij-reaper")
		}
	}
	return filepath.Join(xdg(env, "XDG_CONFIG_HOME", ".config"), "zellij-reaper")
}

// appData is %APPDATA%, or where Windows puts it by default.
func appData(env Env) string {
	if v, ok := env.Lookup("APPDATA"); ok && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(env.Home(), "AppData", "Roaming")
}

// ConfigFile is the settings file, or the one named by ZELLIJ_REAPER_CONFIG.
func ConfigFile(env Env) string {
	if v, ok := env.Lookup("ZELLIJ_REAPER_CONFIG"); ok && v != "" {
		return expandHome(v, env)
	}
	return filepath.Join(ConfigDir(env), "config.toml")
}

// StateDir holds the log, the lock and the list of protected sessions:
// things the tool writes that are not settings.
func StateDir(env Env) string {
	if GOOS == "windows" {
		if _, ok := env.Lookup("XDG_STATE_HOME"); !ok {
			return filepath.Join(localAppData(env), "zellij-reaper")
		}
	}
	return filepath.Join(xdg(env, "XDG_STATE_HOME", ".local/state"), "zellij-reaper")
}

// LockFile serializes passes: the timer's, a manual `run` and the TUI's actions.
func LockFile(env Env) string { return filepath.Join(StateDir(env), "lock") }

// ProtectedFile lists session names protected from the TUI or `protect`.
func ProtectedFile(env Env) string { return filepath.Join(StateDir(env), "protected") }

// GivenFile lists the session names auto_rename gave, which it may replace again.
// A name that is not on it, and that zellij did not make up, was chosen by someone.
func GivenFile(env Env) string { return filepath.Join(StateDir(env), "named") }

// KeptFile lists the session names someone kept with r and Enter in the dashboard,
// which auto_rename never replaces.
func KeptFile(env Env) string { return filepath.Join(StateDir(env), "kept") }

// UnitDir is where systemd looks for the user's own unit files.
func UnitDir(env Env) string {
	return filepath.Join(xdg(env, "XDG_CONFIG_HOME", ".config"), "systemd", "user")
}

// GOOS is the system the paths follow; tests set it to check the others.
var GOOS = runtime.GOOS

// ZellijCacheDir is zellij's own cache directory, which holds its session
// data. zellij takes it from the directories crate: the XDG cache directory on
// Linux, ~/Library/Caches on macOS, %LOCALAPPDATA% on Windows.
func ZellijCacheDir(env Env) string {
	switch GOOS {
	case "darwin":
		return filepath.Join(env.Home(), "Library", "Caches", "org.Zellij-Contributors.Zellij")
	case "windows":
		return filepath.Join(localAppData(env), "Zellij", "cache")
	}
	return filepath.Join(xdg(env, "XDG_CACHE_HOME", ".cache"), "zellij")
}

// localAppData is %LOCALAPPDATA%, or where Windows puts it by default.
func localAppData(env Env) string {
	if v, ok := env.Lookup("LOCALAPPDATA"); ok && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(env.Home(), "AppData", "Local")
}
