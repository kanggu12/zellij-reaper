package inspect

import (
	"slices"
	"sort"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/repo"
)

// A coding agent's session says little about what it works on: the title the
// agent sets is the topic its conversation began with, its program is the same
// in every session, and so is the directory it was started in, often a folder
// of many projects. The commands it runs say more: each runs in the directory
// of the project the agent is working on at that moment. AgentWork is what a
// survey sees of that, for a pass to name the session after the project (see
// reaper's project book).

// maxAgentProcs bounds how many processes under one agent a survey reads: an
// agent running a build can have many, and the newest few say where it works.
const maxAgentProcs = 512

// AgentWork is where a coding agent in a session works, as its processes show
// at one moment.
type AgentWork struct {
	// Program is the agent's name ("claude"), "" when no agent runs.
	Program string
	// Started is the repository of the directory the agent was started in,
	// "" when that is in none.
	Started string
	// Work lists the repositories the agent's commands run in now, newest
	// first: each with the start mark of its newest process there.
	Work []Sighting
}

// Sighting is a repository an agent's command runs in, and when the newest of
// its processes there started (procfs.StartMark).
type Sighting struct {
	Repo string
	Mark int64
}

// agentWork finds the first coding agent under the session's panes and what
// it works on. Several agents in one session are rare; the first pane's leads.
func agentWork(t *procfs.Table, panes []Pane, agents []string, repos *repo.Finder, root string) AgentWork {
	var w AgentWork
	// newest holds, per repository, the newest mark seen there.
	newest := map[string]int64{}
	for _, p := range panes {
		a := findAgent(t, p.PID, agents)
		if a == nil {
			continue
		}
		// The first agent found names the program and where it started.
		if w.Program == "" {
			w.Program = agentName(a, agents)
			w.Started = repos.Root(t.Cwd(a.PID))
		}
		for _, d := range t.Descendants(a.PID, maxAgentProcs) {
			// A process that ended and waits to be collected runs nowhere.
			if d == nil || d.IsZombie() {
				continue
			}
			r := repos.Root(t.Cwd(d.PID))
			if r == "" {
				continue
			}
			// A process whose start cannot be read cannot be told new from
			// old, and is left out rather than guessed at.
			mark, ok := procfs.StartMark(root, d.PID)
			if !ok {
				continue
			}
			if m, seen := newest[r]; !seen || mark > m {
				newest[r] = mark
			}
		}
	}
	for r, m := range newest {
		w.Work = append(w.Work, Sighting{Repo: r, Mark: m})
	}
	// Newest first; one repository per mark, so equal marks keep an order
	// that does not change between runs.
	sort.Slice(w.Work, func(i, j int) bool {
		if w.Work[i].Mark != w.Work[j].Mark {
			return w.Work[i].Mark > w.Work[j].Mark
		}
		return w.Work[i].Repo < w.Work[j].Repo
	})
	return w
}

// findAgent is the first coding agent at or under a pane's process: the pane
// may run the agent itself, or a shell the agent was started from.
func findAgent(t *procfs.Table, pid int, agents []string) *procfs.Proc {
	if len(agents) == 0 {
		return nil
	}
	if p := t.ByPID[pid]; p != nil && isAgent(p, agents) {
		return p
	}
	// Nearest first: an agent's own commands may run another agent.
	for _, d := range t.Descendants(pid, maxAgentProcs) {
		if d != nil && isAgent(d, agents) {
			return d
		}
	}
	return nil
}

// isAgent reports whether a process is one of the agents, by the names a pass
// knows a program by (procfs.Proc.Names), as the protected programs are.
func isAgent(p *procfs.Proc, agents []string) bool {
	return agentName(p, agents) != ""
}

// agentName is the agent a process is, "" when it is none.
func agentName(p *procfs.Proc, agents []string) string {
	for _, n := range p.Names() {
		if slices.Contains(agents, n) {
			return n
		}
	}
	return ""
}
