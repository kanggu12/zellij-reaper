package protect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
)

func TestSetLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected")
	l, _ := Load(path)
	for _, n := range []string{"notes", "한글 세션", "a#b"} {
		if err := l.Set(n, true); err != nil {
			t.Fatalf("Set(%q): %v", n, err)
		}
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"notes", "한글 세션", "a#b"} {
		if !back.Has(n) {
			t.Errorf("%q lost on reload", n)
		}
	}
	if err := back.Rename("notes", "journal"); err != nil || !back.Has("journal") || back.Has("notes") {
		t.Errorf("rename: %v %v", back.Names(), err)
	}
}

func TestEachListSaysWhatItHolds(t *testing.T) {
	dir := t.TempDir()
	protected, _ := Load(filepath.Join(dir, "protected"))
	given := LoadGiven(filepath.Join(dir, "named"))
	kept := LoadKept(filepath.Join(dir, "kept"))
	for _, l := range []*List{protected, given, kept} {
		if err := l.Set("notes", true); err != nil {
			t.Fatal(err)
		}
	}
	first := func(path string) string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		line, _, _ := strings.Cut(string(b), "\n")
		return line
	}
	if got := first(protected.Path); !strings.Contains(got, "never reaps") {
		t.Errorf("protected list says %q", got)
	}
	if got := first(given.Path); strings.Contains(got, "never") || !strings.Contains(got, "gave") {
		t.Errorf("list of given names says %q", got)
	}
	if got := first(kept.Path); !strings.Contains(got, "keep") || strings.Contains(got, "reaps") {
		t.Errorf("list of kept names says %q", got)
	}
	// Its file reads back the same way, through either loader.
	if back := LoadGiven(given.Path); !back.Has("notes") {
		t.Errorf("read back %v", back.Names())
	}
	// Whoever waits for the lock hears which list it is.
	held, err := lock.Acquire(LockPath(given.Path), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	old := lockWait
	lockWait = 50 * time.Millisecond
	defer func() { lockWait = old }()
	if err := given.Set("other", true); err == nil || !strings.Contains(err.Error(), "given names") {
		t.Errorf("waiting for the lock: %v", err)
	}
}

func TestAListOfGivenNamesThatCannotBeReadStartsEmpty(t *testing.T) {
	// A directory where the file should be: reading it fails.
	path := filepath.Join(t.TempDir(), "named")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	l := LoadGiven(path)
	if len(l.Names()) != 0 {
		t.Errorf("names %v", l.Names())
	}
	if err := l.Set("x", true); err == nil {
		t.Error("a list that cannot be read took a change")
	}
}

func TestUnstorableNamesAreRefused(t *testing.T) {
	l, _ := Load(filepath.Join(t.TempDir(), "protected"))
	// Each of these would read back differently, so it would not be protected.
	for _, n := range []string{"", " notes", "notes ", "#notes", "two\nlines"} {
		if err := l.Set(n, true); err == nil {
			t.Errorf("Set(%q) was accepted", n)
		}
	}
	if b, err := os.ReadFile(l.Path); err == nil && len(b) > 0 {
		t.Errorf("a refused name was written: %q", b)
	}
}

func TestAChangeKeepsWhatAnotherProcessWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected")
	// Two processes with the list loaded: the dashboard, and a
	// `zellij-reaper protect` run in another terminal.
	dashboard, _ := Load(path)
	cli, _ := Load(path)
	if err := cli.Set("from-cli", true); err != nil {
		t.Fatal(err)
	}
	// The dashboard's copy predates that; its own change must not drop it.
	if err := dashboard.Set("from-dashboard", true); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(path)
	if !back.Has("from-cli") || !back.Has("from-dashboard") {
		t.Errorf("file holds %v, want both names", back.Names())
	}
	// The dashboard's List now matches the file.
	if !dashboard.Has("from-cli") {
		t.Errorf("dashboard list %v after its change", dashboard.Names())
	}
	// A rename moves a protection the file holds, even one this copy
	// never saw.
	stale, _ := Load(filepath.Join(filepath.Dir(path), "other"))
	stale.Path = path
	if err := stale.Rename("from-cli", "renamed"); err != nil {
		t.Fatal(err)
	}
	back, _ = Load(path)
	if back.Has("from-cli") || !back.Has("renamed") || !back.Has("from-dashboard") {
		t.Errorf("after the rename the file holds %v", back.Names())
	}
}

func TestReloadRefreshesTheSameList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected")
	shared, _ := Load(path)
	other, _ := Load(path)
	if err := other.Set("later", true); err != nil {
		t.Fatal(err)
	}
	if err := shared.Reload(); err != nil {
		t.Fatal(err)
	}
	if !shared.Has("later") {
		t.Errorf("reloaded list %v", shared.Names())
	}
	// A list that cannot be read keeps what it had.
	os.Remove(path)
	os.Mkdir(path, 0o755)
	if err := shared.Reload(); err == nil || !shared.Has("later") {
		t.Errorf("unreadable file: err %v, names %v", err, shared.Names())
	}
}

func TestReadsAndWritesFromManyGoroutines(t *testing.T) {
	// The dashboard reads the list while its commands change it; the race
	// detector (go test -race) fails this without the lock.
	l, _ := Load(filepath.Join(t.TempDir(), "protected"))
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := l.Set(fmt.Sprintf("s%d", i), true); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = l.Has("s0")
			_ = l.Names()
			_ = l.Reload()
		}()
	}
	wg.Wait()
	if n := len(l.Names()); n != 8 {
		t.Errorf("%d names after 8 changes: %v", n, l.Names())
	}
}

func TestAChangeWaitsForTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protected")
	l, _ := Load(path)
	// Another writer holds the lock for 300ms.
	first, err := lock.Acquire(LockPath(path), 0)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		first.Release()
	}()
	start := time.Now()
	if err := l.Set("waited", true); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Errorf("the change went ahead after %v, while the lock was held", time.Since(start))
	}
	<-done
	// A lock held past the wait is an error, and nothing is written.
	defer func(w time.Duration) { lockWait = w }(lockWait)
	lockWait = 200 * time.Millisecond
	second, err := lock.Acquire(LockPath(path), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if err := l.Set("blocked", true); err == nil {
		t.Error("a change went ahead while another writer held the lock")
	}
	if back, _ := Load(path); back.Has("blocked") {
		t.Error("a change was written without the lock")
	}
}
