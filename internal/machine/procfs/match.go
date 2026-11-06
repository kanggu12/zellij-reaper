package procfs

import "time"

// ByStartTime picks the server that started when a socket file was born: the
// nearest within a second, if every other server started clearly farther from
// it (more than four times as far, plus 100 ms). Otherwise it picks none.
func ByStartTime(born time.Time, servers []Server) (Server, bool) {
	best, second := time.Duration(-1), time.Duration(-1)
	var match Server
	for _, s := range servers {
		if s.Started.IsZero() {
			continue
		}
		d := born.Sub(s.Started)
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
	if second >= 0 && second < 4*best+100*time.Millisecond {
		return Server{}, false
	}
	return match, true
}
