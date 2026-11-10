//go:build linux || darwin

package logfile

import "syscall"

// spaceLeft is how many bytes this user may still write on the file system
// dir is on: the blocks free to unprivileged users, as df shows them.
func spaceLeft(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bsize is an int64 on Linux and a uint32 on macOS.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
