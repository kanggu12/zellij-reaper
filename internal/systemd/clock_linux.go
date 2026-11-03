package systemd

import (
	"time"

	"golang.org/x/sys/unix"
)

// monotonicNow reads CLOCK_MONOTONIC, the clock systemd's monotonic timers
// run on (it stops while the machine sleeps, unlike /proc/uptime).
func monotonicNow() (time.Duration, bool) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, false
	}
	return time.Duration(ts.Nano()), true
}
