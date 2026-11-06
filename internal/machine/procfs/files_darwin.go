package procfs

import (
	"os"
	"syscall"
	"time"
)

// FileID returns a file's inode and device, as stat(2) gives them. Nothing on
// macOS compares them with a socket table; they tell two files apart.
func FileID(path string) (ino, dev uint32, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return uint32(st.Ino), uint32(st.Dev), true
}

// LastIO is the later of a terminal's last write (mtime) and last read (atime).
func LastIO(tty string) time.Time {
	info, err := os.Stat(tty)
	if err != nil {
		return time.Time{}
	}
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if a := time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec); a.After(t) {
			t = a
		}
	}
	return t
}

// Born is when a file was created. macOS keeps it through a rename, which is
// how a renamed session's socket file is matched to its server here, with no
// socket table to read.
func Born(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
}
