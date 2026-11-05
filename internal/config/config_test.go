package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tempEnv is an environment rooted in a fresh home directory.
func tempEnv(t *testing.T) MapEnv {
	t.Helper()
	return MapEnv{"HOME": t.TempDir()}
}

// writeConfig writes the settings file for env.
func writeConfig(t *testing.T, env MapEnv, body string) {
	t.Helper()
	p := ConfigFile(env)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	env := tempEnv(t)
	c, err := Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	if c.DryRun || c.AutoRename || c.IdleAfter != 12*time.Hour || c.ExitedAfter != 72*time.Hour || c.Interval != time.Hour {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if want := filepath.Join(env.Home(), ".local/state/zellij-reaper/reaper.log"); c.LogPath != want {
		t.Errorf("LogPath = %q, want %q", c.LogPath, want)
	}
	if c.File != "" {
		t.Errorf("File = %q with no settings file", c.File)
	}
	for _, k := range Keys {
		if c.Sources[k] != FromDefault {
			t.Errorf("source of %s = %s", k, c.Sources[k])
		}
	}
}

func TestFileThenEnv(t *testing.T) {
	env := tempEnv(t)
	writeConfig(t, env, `
dry_run = true
idle_after = "1h"
exited_after = "1d12h"
protect = "^keep-"
protect_commands = ["claude", " vim ", "claude", ""]
auto_rename = true
interval = "30m"
zellij = "~/bin/zellij"
[log]
path = "~/reaper.log"
max_bytes = 2048
`)
	// The environment beats the file.
	env["ZELLIJ_REAPER_IDLE_AFTER"] = "2h"
	env["ZELLIJ_REAPER_DRY_RUN"] = "off"
	c, err := Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	if c.DryRun || c.IdleAfter != 2*time.Hour || c.ExitedAfter != 36*time.Hour || c.Interval != 30*time.Minute {
		t.Errorf("values: %+v", c)
	}
	if c.Sources["dry_run"] != FromEnv || c.Sources["idle_after"] != FromEnv || c.Sources["exited_after"] != FromFile {
		t.Errorf("sources: %v", c.Sources)
	}
	if strings.Join(c.ProtectCommands, ",") != "claude,vim" {
		t.Errorf("ProtectCommands = %q (want trimmed, deduplicated)", c.ProtectCommands)
	}
	if c.ZellijPath != filepath.Join(env.Home(), "bin/zellij") || c.LogPath != filepath.Join(env.Home(), "reaper.log") {
		t.Errorf("~ not expanded: %q %q", c.ZellijPath, c.LogPath)
	}
	if !c.ProtectMatches("keep-notes") || c.ProtectMatches("notes") {
		t.Error("protect pattern not applied")
	}
}

func TestUnknownKeyWarns(t *testing.T) {
	env := tempEnv(t)
	writeConfig(t, env, "idle_afer = \"1h\"\n")
	c, err := Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "idle_afer") {
		t.Errorf("Warnings = %q", c.Warnings)
	}
}

func TestBadValuesFail(t *testing.T) {
	cases := map[string]string{
		"duration":  `idle_after = "soon"`,
		"regex":     `protect = "("`,
		"interval":  `interval = "10s"`,
		"negative":  "[log]\nmax_bytes = -1",
		"syntax":    `dry_run = maybe`,
		"wrongtype": `dry_run = "yes"`,
	}
	for name, body := range cases {
		env := tempEnv(t)
		writeConfig(t, env, body)
		if _, err := Load("", env); err == nil {
			t.Errorf("%s: Load accepted %q", name, body)
		}
	}
	env := tempEnv(t)
	env["ZELLIJ_REAPER_DRY_RUN"] = "maybe"
	if _, err := Load("", env); err == nil {
		t.Error("a malformed environment value was accepted")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	env := tempEnv(t)
	c := Default(env)
	c.DryRun, c.IdleAfter, c.ExitedAfter, c.AutoRename, c.Protect = true, time.Hour, time.Hour, true, "^keep-"
	text := Render(c, "Written by a test.")
	writeConfig(t, env, text)
	back, err := Load("", env)
	if err != nil {
		t.Fatalf("Load(Render(c)): %v\n%s", err, text)
	}
	for _, k := range Keys {
		if back.Value(k) != c.Value(k) {
			t.Errorf("%s: %s after the round trip, %s before", k, back.Value(k), c.Value(k))
		}
	}
	// Defaults are written commented out so a later default still applies.
	if !strings.Contains(text, "# interval = \"1h\"") || !strings.Contains(text, "\ndry_run = true") {
		t.Errorf("unexpected rendering:\n%s", text)
	}
	if len(back.Warnings) != 0 {
		t.Errorf("rendered file has unknown keys: %v", back.Warnings)
	}
}

func TestXDGPaths(t *testing.T) {
	env := MapEnv{"HOME": "/home/u", "XDG_CONFIG_HOME": "/cfg", "XDG_STATE_HOME": "relative/ignored"}
	if got := ConfigFile(env); got != "/cfg/zellij-reaper/config.toml" {
		t.Errorf("ConfigFile = %q", got)
	}
	// A relative XDG value is ignored, as the spec says.
	if got := StateDir(env); got != "/home/u/.local/state/zellij-reaper" {
		t.Errorf("StateDir = %q", got)
	}
	env["ZELLIJ_REAPER_CONFIG"] = "~/other.toml"
	if got := ConfigFile(env); got != "/home/u/other.toml" {
		t.Errorf("ConfigFile with override = %q", got)
	}
}

func TestAutoRenameStamp(t *testing.T) {
	if c := Default(MapEnv{"HOME": "/h"}); c.AutoRenameStamp != "none" {
		t.Errorf("default: %q", c.AutoRenameStamp)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("auto_rename_stamp = \"date\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p, MapEnv{"HOME": dir})
	if err != nil || c.AutoRenameStamp != "date" || c.Sources["auto_rename_stamp"] != FromFile {
		t.Errorf("from the file: %v, %+v", err, c)
	}
	c, err = Load(p, MapEnv{"HOME": dir, "ZELLIJ_REAPER_AUTO_RENAME_STAMP": "time"})
	if err != nil || c.AutoRenameStamp != "time" || c.Sources["auto_rename_stamp"] != FromEnv {
		t.Errorf("from the environment: %v, %+v", err, c)
	}
	// A typo stops loading, like a malformed duration.
	if _, err := Load(p, MapEnv{"HOME": dir, "ZELLIJ_REAPER_AUTO_RENAME_STAMP": "hour"}); err == nil {
		t.Error("a bad value in the environment was accepted")
	}
	if err := os.WriteFile(p, []byte("auto_rename_stamp = \"hour\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p, MapEnv{"HOME": dir}); err == nil {
		t.Error("a bad value in the file was accepted")
	}
}
