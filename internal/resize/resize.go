// Package resize sends SIGWINCH to zellij's clients, its servers and the
// programs in its panes, so each re-reads its terminal size and redraws.
//
// A program in a pane can be left with a stale size when the window changes
// size while the chain client -> server -> pane terminal -> program drops the
// signal somewhere. SIGWINCH is harmless to any process whose size is already
// right, so sending it to the whole tree is safe.
package resize

import (
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/kanggu12/zellij-reaper/internal/procfs"
)

// Target is a process that gets the signal.
type Target struct {
	PID int
	// Role is "client", "server" or "pane".
	Role string
	// Command is its command line, for the report.
	Command string
	// Err is set when sending failed.
	Err error
}

// Targets finds the processes to signal: every zellij client and, unless
// shallow, every server and everything running below it. self is excluded.
func Targets(t *procfs.Table, uid, self int, shallow bool) []Target {
	var out []Target
	seen := map[int]bool{self: true}
	add := func(p *procfs.Proc, role string) {
		if seen[p.PID] || p.IsZombie() {
			return
		}
		seen[p.PID] = true
		out = append(out, Target{PID: p.PID, Role: role, Command: p.CommandLine()})
	}
	servers := t.Servers(uid)
	isServer := map[int]bool{}
	for _, s := range servers {
		isServer[s.PID] = true
	}
	for _, p := range t.ByPID {
		if p.UID != uid || isServer[p.PID] || len(p.Argv) == 0 {
			continue
		}
		base := filepath.Base(p.Argv[0])
		// zellij itself, not this tool (whose name also starts with it).
		if base == "zellij" || (strings.HasPrefix(base, "zellij") && !strings.HasPrefix(base, "zellij-reap")) {
			add(p, "client")
		}
	}
	if !shallow {
		for _, s := range servers {
			add(t.ByPID[s.PID], "server")
			for _, d := range t.Descendants(s.PID, 4096) {
				add(d, "pane")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// Send signals every target, recording failures (a process that just exited).
func Send(targets []Target) []Target {
	for i := range targets {
		targets[i].Err = syscall.Kill(targets[i].PID, syscall.SIGWINCH)
	}
	return targets
}
