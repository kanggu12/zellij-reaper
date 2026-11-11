package inspect

import (
	"sort"
	"strconv"
	"strings"

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
	// ID tells this agent from one started later in the same session: its
	// pid and when it started. "" when its start cannot be read.
	ID string
	// Started is the repository of the directory the agent was started in,
	// "" when that is in none.
	Started string
	// Work lists the repositories the agent's commands run in now, newest
	// command first: each with when its newest command there started.
	Work []Sighting
}

// Sighting is a repository an agent's command runs in, and when that command
// started (procfs.StartMark).
type Sighting struct {
	Repo string
	Mark int64
}

// agentWork finds the first coding agent under the session's panes and what
// it works on. Several agents in one session are rare, and the first pane's
// alone speaks for it: two agents at work in two projects would otherwise
// take the session's name back and forth between them.
func agentWork(t *procfs.Table, panes []Pane, agents []string, repos *repo.Finder, root string) AgentWork {
	var w AgentWork
	var a *procfs.Proc
	for _, p := range panes {
		if a = findAgent(t, p.PID, agents); a != nil {
			break
		}
	}
	if a == nil {
		return w
	}
	w.Program = agentName(a, agents)
	// The agent as it was found names it: the process it may have started
	// again under itself comes and goes with what it does.
	if mark, ok := procfs.StartMarkOf(root, a); ok {
		w.ID = strconv.Itoa(a.PID) + ":" + strconv.FormatInt(mark, 10)
	}
	w.Started = repos.Root(t.Cwd(a.PID))
	a = innermost(t, a, w.Program)
	// newest holds, per repository, the start of the newest command there.
	newest := map[string]int64{}
	budget := maxAgentProcs
	// Each child of the agent's is a command it runs, or a helper it keeps
	// (a language server, a tool's server), and what runs under it is part
	// of it. A command counts from when it started: a loop or a watcher that
	// keeps starting processes is one command, however long it runs.
	for _, c := range t.Children(a.PID) {
		if budget <= 0 {
			break
		}
		if c == nil {
			continue
		}
		// The command and what runs under it, within what is left to read.
		procs := append([]*procfs.Proc{c}, t.Descendants(c.PID, budget-1)...)
		budget -= len(procs)
		// A command whose start cannot be read cannot be told new from
		// old, and is left out rather than guessed at.
		mark, ok := procfs.StartMarkOf(root, c)
		if !ok {
			continue
		}
		for _, d := range procs {
			// A process that ended and waits to be collected runs nowhere.
			if d == nil || d.IsZombie() {
				continue
			}
			r := repos.Root(t.Cwd(d.PID))
			if r == "" {
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
	// Newest first. One command may run in two repositories, and then the
	// two keep an order that does not change between runs.
	sort.Slice(w.Work, func(i, j int) bool {
		if w.Work[i].Mark != w.Work[j].Mark {
			return w.Work[i].Mark > w.Work[j].Mark
		}
		return w.Work[i].Repo < w.Work[j].Repo
	})
	return w
}

// maxRestarts bounds how far innermost goes down: a process tree has no
// loops, and the bound keeps a broken table from making one.
const maxRestarts = 8

// innermost is the process an agent's commands run under. An agent may start
// itself again as its only child, with other settings (Gemini CLI does, for
// more memory), or start its real program from a launcher of the same name;
// its commands then run under that child.
func innermost(t *procfs.Table, a *procfs.Proc, program string) *procfs.Proc {
	for range maxRestarts {
		var only *procfs.Proc
		n := 0
		for _, c := range t.Children(a.PID) {
			// A child that ended runs nothing.
			if c == nil || c.IsZombie() {
				continue
			}
			only, n = c, n+1
		}
		// With other children next to it, the agent runs its commands
		// itself, and a child of the same name is one of them.
		if n != 1 || agentName(only, []string{program}) != program {
			return a
		}
		a = only
	}
	return a
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

// isAgent reports whether a process is one of the agents.
func isAgent(p *procfs.Proc, agents []string) bool {
	return agentName(p, agents) != ""
}

// agentName is the agent a process is, "" when it is none: by its kernel name
// or its program, or, for an interpreter, by the script it runs, by its name
// or the package it is in ("node /usr/bin/claude", "node .../claude-code/
// cli.js"). Another program's argument names nothing: "vim codex" edits a
// file.
func agentName(p *procfs.Proc, agents []string) string {
	script := ""
	if isInterpreter(p.Name()) {
		script = scriptOf(p.Argv)
	}
	for _, a := range agents {
		if p.Comm == a || p.Name() == a || (script != "" && wordMatch(script, a)) {
			return a
		}
	}
	return ""
}

// isInterpreter reports whether a program runs a script it is given: a coding
// agent written in JavaScript or Python runs as one.
func isInterpreter(name string) bool {
	switch name {
	case "node", "nodejs", "bun", "deno", "sh", "bash", "dash", "zsh", "ruby", "perl":
		return true
	}
	return strings.HasPrefix(name, "python")
}

// scriptOf is the script an interpreter's command line runs: the first word
// after the interpreter's long options ("node --max-old-space-size=8192
// /usr/bin/gemini"), or the module of "python3 -m aider". Another short
// option may give code to run in place of a script ("sh -c ..."), and then
// there is none.
func scriptOf(argv []string) string {
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "-m" && i+1 < len(argv):
			return argv[i+1]
		case strings.HasPrefix(a, "--"):
			continue
		case strings.HasPrefix(a, "-"):
			return ""
		default:
			return a
		}
	}
	return ""
}
