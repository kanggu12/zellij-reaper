package zellij

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBornTellsWhichFolderWasMadeFirst(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatal(err)
	}
	// Apart by more than any file system's step in birth times that has one
	// finer than a second.
	time.Sleep(20 * time.Millisecond)
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatal(err)
	}
	a, ok := Born(first)
	if !ok {
		t.Skip("this file system keeps no birth times")
	}
	b, ok := Born(second)
	if !ok || !a.Before(b) {
		t.Errorf("first %v, second %v (%v)", a, b, ok)
	}
	// Made just now.
	if age := time.Since(a); age < 0 || age > time.Minute {
		t.Errorf("first made %v ago", age)
	}
	// A session's files give their folder's.
	f, err := ReadFiles(dir, "first")
	if err != nil || !f.Born.Equal(a) {
		t.Errorf("ReadFiles: %v, %v; want %v", f.Born, err, a)
	}
	// Nothing there, nothing to tell.
	if _, ok := Born(filepath.Join(dir, "none")); ok {
		t.Error("a missing folder has a birth time")
	}
}
