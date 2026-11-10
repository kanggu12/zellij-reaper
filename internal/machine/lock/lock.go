// Package lock keeps two passes from running at once: the timer's, a manual
// `run`, and an action taken in the TUI all take the same lock file.
package lock

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrBusy is returned when another pass still holds the lock after the wait.
var ErrBusy = errors.New("another zellij-reaper pass is running")

// Lock is a held lock; Release frees it.
type Lock struct{ f *os.File }

// Acquire takes the lock at path, waiting up to wait for another holder to
// finish. The system frees the lock when the process exits (flock(2), or
// LockFileEx on Windows), so a crashed pass never leaves a stale lock behind.
func Acquire(path string, wait time.Duration) (*Lock, error) {
	// The state folder and the lock in it are the user's own.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		got, err := tryLock(f)
		if got {
			return &Lock{f: f}, nil
		}
		// Anything but "held by someone else" is a real error.
		if err != nil {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, ErrBusy
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Release frees the lock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	unlock(l.f)
	l.f.Close()
	l.f = nil
}
