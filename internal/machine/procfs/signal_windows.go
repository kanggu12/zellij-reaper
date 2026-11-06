package procfs

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess gives a running process.
const stillActive = 259

// Signal stops a process. Windows has no signals to send another process: any
// signal but 0 ends it, as zellij itself does on Windows (TerminateProcess).
// Signal 0 only asks whether the process is still running, as on Unix.
func Signal(pid int, sig syscall.Signal) error {
	if sig == 0 {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			return err
		}
		defer windows.CloseHandle(h)
		var code uint32
		if err := windows.GetExitCodeProcess(h, &code); err != nil {
			return err
		}
		if code != stillActive {
			return errors.New("process has exited")
		}
		return nil
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
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
