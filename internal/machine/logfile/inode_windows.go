package logfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// inode returns the file's index on its volume, which changes when the log is
// rotated and a new file takes its name, as an inode does elsewhere. The
// FileInfo from os.Stat does not carry it, so the file is opened by name.
func inode(path string, _ os.FileInfo) uint64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var d windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &d) != nil {
		return 0
	}
	return uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)
}
