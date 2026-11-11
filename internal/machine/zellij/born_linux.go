//go:build linux

package zellij

import (
	"time"

	"golang.org/x/sys/unix"
)

// Born is when a file or folder was made, as the file system keeps it
// (statx's birth time, which ext4, xfs, btrfs and tmpfs keep); ok is false
// where it keeps none. A link is not followed: the link itself is asked about.
func Born(path string) (time.Time, bool) {
	var st unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &st); err != nil {
		return time.Time{}, false
	}
	// A file system without birth times leaves the bit out of the mask.
	if st.Mask&unix.STATX_BTIME == 0 {
		return time.Time{}, false
	}
	return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec)), true
}
