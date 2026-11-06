package procfs

import (
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// FileID returns a file's index on its volume and the volume's serial number,
// which tell two files apart as an inode and a device do elsewhere.
func FileID(path string) (ino, dev uint32, ok bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, false
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, 0, false
	}
	defer windows.CloseHandle(h)
	var d windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &d) != nil {
		return 0, 0, false
	}
	return d.FileIndexLow, d.VolumeSerialNumber, true
}

// LastIO has no terminal to look at on Windows, where a pane is a console
// with no file: the session files say when the session was last used.
func LastIO(tty string) time.Time {
	_ = tty
	return time.Time{}
}

// PIDFile reads the process ID zellij writes into a session's socket file on
// Windows. The file keeps it through a rename, so it leads to the server of a
// renamed session as well.
func PIDFile(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid, err == nil && pid > 0
}
