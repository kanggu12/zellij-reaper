package config

import (
	"os"
	"path/filepath"
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

// ConfigDir is where the settings file lives.
func ConfigDir(env Env) string {
	return filepath.Join(xdg(env, "XDG_CONFIG_HOME", ".config"), "zellij-reaper")
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
	return filepath.Join(xdg(env, "XDG_STATE_HOME", ".local/state"), "zellij-reaper")
}

// LockFile serializes passes: the timer's, a manual `run` and the TUI's actions.
func LockFile(env Env) string { return filepath.Join(StateDir(env), "lock") }

// ProtectedFile lists session names protected from the TUI or `protect`.
func ProtectedFile(env Env) string { return filepath.Join(StateDir(env), "protected") }

// UnitDir is where systemd looks for the user's own unit files.
func UnitDir(env Env) string {
	return filepath.Join(xdg(env, "XDG_CONFIG_HOME", ".config"), "systemd", "user")
}

// CacheDir is the user's cache directory, which zellij uses for session data.
func CacheDir(env Env) string { return xdg(env, "XDG_CACHE_HOME", ".cache") }
