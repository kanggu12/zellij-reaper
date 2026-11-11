//go:build !linux && !darwin

package zellij

import "time"

// Born is when a file or folder was made. On Windows, where nothing is
// renamed (RenamesReachable), and elsewhere, birth times are not read: ok is
// false, and no session reads as brought back or made anew.
func Born(path string) (time.Time, bool) {
	_ = path
	return time.Time{}, false
}
