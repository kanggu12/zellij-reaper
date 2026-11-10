//go:build unix

package logfile

import (
	"os"
	"syscall"
)

// dirWritable says why a file could not be made in dir, or nil. access(2)
// asks without making one, and it answers for a read-only file system too.
func dirWritable(dir string) error {
	// 2 is W_OK, which the syscall package does not name on every system.
	if err := syscall.Access(dir, 2); err != nil {
		return &os.PathError{Op: "write", Path: dir, Err: err}
	}
	return nil
}
