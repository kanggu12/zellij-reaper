//go:build darwin || windows

package procfs

// startedMark is StartMark from the start time a native reader gave: macOS
// and Windows tell when a process started, where Linux's /proc tells clock
// ticks since boot.
func startedMark(p *Proc, ok bool) (int64, bool) {
	if !ok || p.started.IsZero() {
		return 0, false
	}
	return p.started.UnixNano(), true
}
