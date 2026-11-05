package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// home points the settings and state directories at a new, empty home.
func home(t *testing.T) config.Env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
	t.Setenv("ZELLIJ_REAPER_CONFIG", "")
	t.Setenv("ZELLIJ_REAPER_ZELLIJ", filepath.Join(dir, "no-zellij"))
	return config.OSEnv{}
}

func TestAListThatCannotBeReadStopsOnlyWhatItGuards(t *testing.T) {
	// A directory where the file should be: reading it fails.
	broken := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Without the names a pass gave, those names stay as they are: nothing to stop for.
	t.Run("the given names", func(t *testing.T) {
		broken(t, config.GivenFile(home(t)))
		if _, err := Load(""); err != nil {
			t.Errorf("load: %v", err)
		}
	})
	// Without the kept names, a pass renames nothing; the rest goes on.
	t.Run("the kept names", func(t *testing.T) {
		broken(t, config.KeptFile(home(t)))
		if _, err := Load(""); err != nil {
			t.Errorf("load: %v", err)
		}
	})
	// Without the protected list, nobody can tell which sessions it protects.
	t.Run("the protected list", func(t *testing.T) {
		broken(t, config.ProtectedFile(home(t)))
		if _, err := Load(""); err == nil {
			t.Error("loaded without the protected list")
		}
	})
}
