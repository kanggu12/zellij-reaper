//go:build !linux && !darwin

package zellij

import "time"

// Born is when a file or folder was made. On Windows, where nothing is
// renamed (RenamesReachable), and elsewhere, nothing asks: ok is false.
func Born(path string) (time.Time, bool) {
	_ = path
	return time.Time{}, false
}
