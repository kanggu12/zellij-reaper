package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetKeysKeepsWhatWasWrittenByHand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	in := "# my notes\n# idle_after = \"12h\"\nprotect = \"^keep-\"  # mine\ndry_run = false\n"
	os.WriteFile(p, []byte(in), 0o644)
	c := Default(MapEnv{"HOME": "/h"})
	if err := SetKeys(p, c, map[string]string{"idle_after": `"4h"`, "dry_run": "true", "auto_rename": "false"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	want := "# my notes\nidle_after = \"4h\"\nprotect = \"^keep-\"  # mine\ndry_run = true\nauto_rename = false\n"
	if string(b) != want {
		t.Errorf("got\n%s\nwant\n%s", b, want)
	}
	// What it wrote reads back.
	got, err := Load(p, MapEnv{"HOME": "/h"})
	if err != nil || got.IdleAfter.Hours() != 4 || !got.DryRun || got.AutoRename || got.Protect != "^keep-" {
		t.Errorf("read back %+v, %v", got, err)
	}
}

func TestSetKeysWritesAWholeFileWhenThereIsNone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	c := Default(MapEnv{"HOME": "/h"})
	if err := SetKeys(p, c, map[string]string{"exited_after": `"1w"`}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "\nexited_after = \"1w\"\n") || !strings.Contains(string(b), "# idle_after = ") {
		t.Errorf("file:\n%s", b)
	}
	if got, err := Load(p, MapEnv{"HOME": "/h"}); err != nil || got.ExitedAfter.Hours() != 168 {
		t.Errorf("read back %v, %v", got.ExitedAfter, err)
	}
}
