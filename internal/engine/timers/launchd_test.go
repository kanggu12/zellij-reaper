package timers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func TestALaunchdUninstallCountsItsAgents(t *testing.T) {
	// `uninstall --purge` says whether there were timers by this count: the
	// settings and the state a purge removes are not timers.
	env := config.MapEnv{"HOME": t.TempDir()}
	// A launchd that takes every command, as one does for a logged-in user.
	l := Launchd{Env: env, UID: 501, Run: func(string, ...string) (string, error) { return "", nil }}
	if _, err := l.Install(InstallOptions{Exe: "/usr/local/bin/zellij-reaper", Config: config.Default(env)}); err != nil {
		t.Fatal(err)
	}
	// Both agents are files now, next to the settings install wrote.
	for _, label := range []string{LaunchdLabel, LaunchdNamesLabel} {
		if _, err := os.Stat(filepath.Join(env.Home(), "Library", "LaunchAgents", label+".plist")); err != nil {
			t.Fatal(err)
		}
	}
	r, err := l.Uninstall(true, config.Default(env).LogPath)
	if err != nil {
		t.Fatal(err)
	}
	// Two agents went, and with them the settings file: three files, two timers.
	if r.Timers != 2 || len(r.Removed) != 3 {
		t.Errorf("timers %d, removed %q", r.Timers, r.Removed)
	}
	if _, err := os.Stat(config.ConfigFile(env)); err == nil {
		t.Error("the purge left the settings file")
	}
	// With nothing installed there is no timer to count, purge or not.
	if err := os.MkdirAll(config.ConfigDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigFile(env), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = l.Uninstall(true, config.Default(env).LogPath)
	if err != nil || r.Timers != 0 || len(r.Removed) != 1 {
		t.Errorf("a second uninstall: timers %d, removed %q, err %v", r.Timers, r.Removed, err)
	}
}
