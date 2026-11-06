//go:build unix

package procfs

import (
	"os"
	"syscall"
)

// Signal sends sig to a process.
func Signal(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// SupportsResize reports whether SIGWINCH exists here, for `resize`.
const SupportsResize = true

// Resize sends SIGWINCH, which makes a program re-read its terminal's size.
func Resize(pid int) error { return syscall.Kill(pid, syscall.SIGWINCH) }

// CurrentUID is the user this program runs as, which owns the sessions it
// looks after.
func CurrentUID() int { return os.Getuid() }
