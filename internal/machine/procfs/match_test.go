package procfs_test

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
)

func TestByStartTimePicksOnlyAClearMatch(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 18, 33, 55, 0, time.UTC)
	ms := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Millisecond) }
	two := []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(250)}}
	for _, c := range []struct {
		name    string
		born    time.Time
		servers []procfs.Server
		want    int
	}{
		// As measured on macOS: the socket 3 ms after its server, the next
		// session a quarter of a second later.
		{"measured", ms(3), two, 1},
		// Sessions with a command pane, started by a script: 66 ms apart.
		{"close neighbours", ms(1), []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(66)}}, 1},
		{"the second one", ms(255), two, 2},
		{"alone", ms(400), two[:1], 1},
		// Too far from any server.
		{"too late", ms(1500), two[:1], 0},
		// Two servers about as near: none.
		// The server that started after the file was made is no candidate,
		// so the other one, 125 ms before, is the only one.
		{"between, the later one too late", ms(125), two, 1},
		{"just before it", ms(125), []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(120)}}, 2},
		{"two about as near", ms(125), []procfs.Server{{PID: 1, Started: ms(100)}, {PID: 2, Started: ms(120)}}, 0},
		{"made before every server", ms(0), []procfs.Server{{PID: 1, Started: ms(300)}}, 0},
		{"the only one that started before it", ms(40), []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(400)}}, 1},
		{"nearer, not clearly", ms(40), []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(-300)}}, 0},
		{"started together", ms(5), []procfs.Server{{PID: 1, Started: ms(0)}, {PID: 2, Started: ms(20)}}, 0},
		// A server whose start is unknown is no candidate.
		{"unknown start", ms(3), []procfs.Server{{PID: 1}, {PID: 2, Started: ms(0)}}, 2},
	} {
		got, ok := procfs.ByStartTime(c.born, c.servers)
		if (c.want == 0 && ok) || (c.want != 0 && (!ok || got.PID != c.want)) {
			t.Errorf("%s: got %d %v, want %d", c.name, got.PID, ok, c.want)
		}
	}
}
