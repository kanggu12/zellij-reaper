//go:build !linux

package timers

import "time"

// monotonicNow has no systemd clock to read outside Linux.
func monotonicNow() (time.Duration, bool) { return 0, false }
