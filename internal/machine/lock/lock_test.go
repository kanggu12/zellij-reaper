package lock

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTheLockIsTheUsersOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "lock")
	l, err := Acquire(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	// The state folder is made here when it is not there yet.
	for p, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, %v", p, info.Mode().Perm(), err)
		}
	}
}
