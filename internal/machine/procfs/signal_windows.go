package procfs

import (
	"errors"
	"os"
	"syscall"
)

// Signal stops a process. Windows has no signals to send another process: any
// signal ends it, as zellij itself does on Windows (TerminateProcess).
func Signal(pid int, sig syscall.Signal) error {
	_ = sig
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// SupportsResize reports whether SIGWINCH exists here, for `resize`.
const SupportsResize = false

// Resize has nothing to send on Windows, where a console's size reaches the
// programs in it without a signal.
func Resize(pid int) error {
	_ = pid
	return errors.New("Windows has no SIGWINCH")
}
