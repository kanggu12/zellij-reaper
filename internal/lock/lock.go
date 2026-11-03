// Package lock keeps two passes from running at once: the timer's, a manual
// `run`, and an action taken in the TUI all take the same lock file.
package lock

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrBusy is returned when another pass still holds the lock after the wait.
var ErrBusy = errors.New("another zellij-reaper pass is running")

// Lock is a held lock; Release frees it.
type Lock struct{ f *os.File }

// Acquire takes the lock at path, waiting up to wait for another holder to
// finish. flock(2) is released by the kernel when the process exits, so a
// crashed pass never leaves a stale lock behind.
func Acquire(path string, wait time.Duration) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		// Anything but "held by someone else" is a real error.
		if !errors.Is(err, syscall.EWOULDBLOCK) {
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
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}
