//go:build darwin

package zellij

import (
	"syscall"
	"time"
)

// Born is when a file or folder was made, as the file system keeps it (APFS
// and HFS+ keep a birth time); ok is false when it cannot be read. A link is
// not followed: the link itself is asked about.
func Born(path string) (time.Time, bool) {
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		return time.Time{}, false
	}
	// A file system that keeps none reports the start of the epoch.
	if st.Birthtimespec.Sec == 0 && st.Birthtimespec.Nsec == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
}
