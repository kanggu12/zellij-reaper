package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookForOffersTheShellsFileOnce(t *testing.T) {
	home := t.TempDir()
	file, line := hookFor("/bin/bash", home)
	if file != filepath.Join(home, ".bashrc") || line == "" {
		t.Fatalf("bash: %q %q", file, line)
	}
	if f, _ := hookFor("/usr/bin/tcsh", home); f != "" {
		t.Errorf("tcsh offered %q", f)
	}
	// A file without a final newline gets the hook on a line of its own.
	os.WriteFile(file, []byte("alias ll='ls -l'"), 0o644)
	if err := appendLine(file, line); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(file)
	if string(b) != "alias ll='ls -l'\n"+line+"\n" {
		t.Errorf("file:\n%s", b)
	}
	if f, _ := hookFor("/bin/bash", home); f != "" {
		t.Error("offered again once the hook is there")
	}
	// fish's file is made with its directory.
	file, line = hookFor("/usr/bin/fish", home)
	if err := appendLine(file, line); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".config", "fish", "config.fish")); string(b) != line+"\n" {
		t.Errorf("fish: %q", b)
	}
}
