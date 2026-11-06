//go:build unix

package lock

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive flock(2) without waiting. got is false, with no
// error, when another process holds it.
func tryLock(f *os.File) (got bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}

// unlock frees the lock.
func unlock(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
