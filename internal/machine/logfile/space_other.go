//go:build !linux && !darwin && !windows

package logfile

import "errors"

// spaceLeft cannot be read on this system: the space is taken to be there.
func spaceLeft(string) (uint64, error) {
	return 0, errors.New("free space is not known on this system")
}
