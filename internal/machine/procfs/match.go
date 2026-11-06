package procfs

import "time"

// bornBefore is how much earlier than its server a socket file may look born:
// the two clocks are read at different grains (microseconds and nanoseconds).
const bornBefore = 20 * time.Millisecond

// ByStartTime picks the server that started when a socket file was born: the
// nearest within a second, if every other server started clearly farther from
// it (more than ten times as far, plus 20 ms). Otherwise it picks none.
// Measured on macOS 26 with zellij 0.45.1, a server makes its socket within
// 5 ms of starting, while sessions started one after another by a script are
// 65 ms or more apart.
func ByStartTime(born time.Time, servers []Server) (Server, bool) {
	best, second := time.Duration(-1), time.Duration(-1)
	var match Server
	for _, s := range servers {
		if s.Started.IsZero() {
			continue
		}
		// A server makes its socket after it starts: one that started
		// later than the file (beyond the clocks' grain) did not make it.
		d := born.Sub(s.Started)
		if d < -bornBefore {
			continue
		}
		if d < 0 {
			d = -d
		}
		switch {
		case best < 0 || d < best:
			best, second, match = d, best, s
		case second < 0 || d < second:
			second = d
		}
	}
	if best < 0 || best > time.Second {
		return Server{}, false
	}
	if second >= 0 && second < 10*best+20*time.Millisecond {
		return Server{}, false
	}
	return match, true
}
