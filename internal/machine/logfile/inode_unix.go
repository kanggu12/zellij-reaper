//go:build unix

package logfile

import (
	"os"
	"syscall"
)

// inode returns a file's inode number, which changes when the log is rotated
// and a new file takes its name.
func inode(_ string, info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
