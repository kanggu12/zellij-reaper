// Package protect keeps the lists of names in the state directory, one name per
// line: the sessions protected by name from the TUI or `zellij-reaper protect`, and
// the names auto_rename gave.
//
// Several processes share the file (the timer's pass, the dashboard, the
// command line), so a change is made to the file as it is on disk at that
// moment, under a lock file next to it, never to a copy read earlier.
package protect

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
)

// lockWait is how long a change waits for another writer of the list. A
// writer holds the lock for one read and one write of a small file.
var lockWait = 5 * time.Second

// List is the set of protected names, read from a file. One List is shared
// by everything in a process (the dashboard reads it while its commands
// change it), so every access goes through mu.
type List struct {
	// Path is the file.
	Path string
	// kind says what the list holds.
	kind kind
	// mu guards names.
	mu sync.RWMutex
	// names holds the entries.
	names map[string]bool
}

// kind is what a list holds, as its file's first line and its messages say it.
type kind struct{ header, noun string }

var (
	protectedKind = kind{"# Sessions zellij-reaper never reaps or renames, one per line.", "the protected list"}
	givenKind     = kind{"# Names zellij-reaper gave, which it may change again, one per line. Take one off to keep it.", "the list of given names"}
)

// Load reads the protected list at path; a missing file is an empty list.
func Load(path string) (*List, error) {
	names, err := read(path)
	if err != nil {
		return nil, err
	}
	return &List{Path: path, kind: protectedKind, names: names}, nil
}

// LoadGiven reads the list of names auto_rename gave at path. A list that cannot be
// read starts empty and stops nothing: all it costs is that those names stay as they
// are, where a protected list that cannot be read could cost a session.
func LoadGiven(path string) *List {
	l := &List{Path: path, kind: givenKind, names: map[string]bool{}}
	_ = l.Reload()
	return l
}

// LockPath is the lock file that serializes changes to the list at path.
func LockPath(path string) string { return path + ".lock" }

// read parses the file at path; a missing file is an empty list.
func read(path string) (map[string]bool, error) {
	names := map[string]bool{}
	f, err := os.Open(path)
	// No file yet: nothing is protected by name.
	if errors.Is(err, os.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		// Blank lines and "#" comments let people edit the file by hand.
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		names[name] = true
	}
	// A read cut short must not pass for a shorter list.
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

// Reload re-reads the file into this same List, so everything holding it
// sees what other processes wrote since. On an error the List keeps what it
// had, and the caller decides what an unreadable list means.
func (l *List) Reload() error {
	// Held across the read: a change made in this process meanwhile can
	// then not be overwritten by an older read.
	l.mu.Lock()
	defer l.mu.Unlock()
	names, err := read(l.Path)
	if err != nil {
		return err
	}
	l.names = names
	return nil
}

// Has reports whether name is protected.
func (l *List) Has(name string) bool {
	if l == nil {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.names[name]
}

// Names returns the protected names, sorted.
func (l *List) Names() []string {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return sorted(l.names)
}

// sorted returns the keys of names in order.
func sorted(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Set adds (on) or removes (off) a name and saves the file.
func (l *List) Set(name string, on bool) error {
	// Refused before anything is touched: a name the file cannot hold would
	// read back as another name, or as nothing.
	if on {
		if err := Storable(name); err != nil {
			return err
		}
	}
	return l.update(func(names map[string]bool) (bool, error) {
		// Nothing to write when the file already says so.
		if names[name] == on {
			return false, nil
		}
		if on {
			names[name] = true
		} else {
			delete(names, name)
		}
		return true, nil
	})
}

// Storable reports whether a name can be kept in the list file, which holds
// one trimmed name per line and treats "#" lines as comments. A name that
// would read back differently is refused instead of silently not protected.
func Storable(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("an empty name cannot be protected")
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("%q starts or ends with a space; protect it with the protect pattern instead", name)
	case strings.HasPrefix(name, "#"):
		return fmt.Errorf("%q starts with #; protect it with the protect pattern instead", name)
	case strings.ContainsAny(name, "\n\r"):
		return fmt.Errorf("a name with a line break cannot be protected")
	}
	return nil
}

// Rename moves a protection along with a session that was renamed by hand.
func (l *List) Rename(from, to string) error {
	return l.update(func(names map[string]bool) (bool, error) {
		// Only a name the file holds now has a protection to move.
		if !names[from] {
			return false, nil
		}
		// The new name must read back as itself, or the move would drop
		// the protection.
		if err := Storable(to); err != nil {
			return false, err
		}
		delete(names, from)
		names[to] = true
		return true, nil
	})
}

// update applies change to the list as the file holds it now, saves the
// result when change says it changed anything, and makes this List match the
// file. The lock file keeps two writers (two processes, or two commands of
// the dashboard) from both reading the old list and one losing the other's
// change.
func (l *List) update(change func(names map[string]bool) (changed bool, err error)) error {
	lk, err := lock.Acquire(LockPath(l.Path), lockWait)
	if errors.Is(err, lock.ErrBusy) {
		return fmt.Errorf("another zellij-reaper is changing %s; try again", l.kind.noun)
	}
	if err != nil {
		return err
	}
	defer lk.Release()
	// Taken after the file lock, so readers in this process are never held
	// up while another process writes; held through the save, so a Reload
	// here cannot put an older read in place of this change.
	l.mu.Lock()
	defer l.mu.Unlock()
	disk, err := read(l.Path)
	if err != nil {
		return err
	}
	// The change works on a copy, so a refusal or a failed save leaves the
	// list exactly as the file still holds it.
	next := make(map[string]bool, len(disk)+1)
	for n := range disk {
		next[n] = true
	}
	changed, err := change(next)
	if err != nil {
		l.names = disk
		return err
	}
	if changed {
		if err := save(l.Path, l.kind.header, next); err != nil {
			l.names = disk
			return err
		}
	}
	l.names = next
	return nil
}

// save writes the file atomically: a reader never sees half a list.
func save(path, header string, names map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, n := range sorted(names) {
		b.WriteString(n + "\n")
	}
	// A temporary file of its own, so two writers never write into the same
	// one, renamed over the list in one step.
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	os.Chmod(f.Name(), 0o644)
	return os.Rename(f.Name(), path)
}
