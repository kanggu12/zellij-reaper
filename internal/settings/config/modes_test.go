package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestTheModesOfOlderReleasesAreTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	home := t.TempDir()
	env := MapEnv{"HOME": home}
	cfg, state := ConfigDir(env), StateDir(env)
	// As 0.12 left them, with one file the user made private, one made
	// readable to a group, and a link to a file of their own elsewhere.
	elsewhere := filepath.Join(home, "notes.txt")
	for p, mode := range map[string]os.FileMode{
		filepath.Join(cfg, "config.toml"):    0o644,
		filepath.Join(state, "reaper.log"):   0o644,
		filepath.Join(state, "protected"):    0o644,
		filepath.Join(state, "kept"):         0o600,
		filepath.Join(state, "reaper.log.1"): 0o640,
		elsewhere:                            0o644,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{cfg, state} {
		if err := os.Chmod(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(elsewhere, filepath.Join(state, "link")); err != nil {
		t.Fatal(err)
	}
	changed := TightenOld(env)
	for p, want := range map[string]os.FileMode{
		cfg:                                  0o700,
		state:                                0o700,
		filepath.Join(cfg, "config.toml"):    0o600,
		filepath.Join(state, "reaper.log"):   0o600,
		filepath.Join(state, "protected"):    0o600,
		filepath.Join(state, "kept"):         0o600,
		filepath.Join(state, "reaper.log.1"): 0o640,
		elsewhere:                            0o644,
	} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, want %v (%v)", p, info.Mode().Perm(), want, err)
		}
	}
	if len(changed) != 5 || !slices.Contains(changed, state) {
		t.Errorf("changed %v", changed)
	}
	// Once done, nothing more to do.
	if again := TightenOld(env); len(again) != 0 {
		t.Errorf("a second time: %v", again)
	}
}
