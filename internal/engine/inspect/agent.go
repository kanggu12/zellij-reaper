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

// agentsAt finds the coding agent under each of the session's panes, the
// first under each, in the panes' order, and what each works on, each on its
// own: two agents at work in two projects would otherwise take the session's
// name back and forth between them. Two in one session are rare; a pass lets
// one speak for the session (reaper's project book).
func agentsAt(t *procfs.Table, panes []Pane, agents []string, repos *repo.Finder, root string) []AgentWork {
	var out []AgentWork
	for _, p := range panes {
		if a := findAgent(t, p.PID, agents); a != nil {
			out = append(out, workOf(t, a, agents, repos, root))
		}
	}
	return out
}

// workOf is where an agent works, as the commands it runs now show.
func workOf(t *procfs.Table, a *procfs.Proc, agents []string, repos *repo.Finder, root string) AgentWork {
	var w AgentWork
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
	for _, c := range newestFirst(t.Children(a.PID), root) {
		if budget <= 0 {
			break
		}
		// The command and what runs under it, within what is left to read.
		procs := append([]*procfs.Proc{c.proc}, t.Descendants(c.proc.PID, budget-1)...)
		budget -= len(procs)
		mark := c.mark
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

// command is a child of an agent's, and when it started.
type command struct {
	proc *procfs.Proc
	mark int64
}

// newestFirst orders an agent's children by when they started, the newest
// first, so a big old command, such as a build server with many workers,
// cannot take what a survey reads from the newer ones. A child whose start
// cannot be read cannot be told new from old, and is left out rather than
// guessed at.
func newestFirst(children []*procfs.Proc, root string) []command {
	var out []command
	for _, c := range children {
		// An agent with more children than a survey reads has a fork bomb
		// under it, not commands.
		if len(out) == maxAgentProcs {
			break
		}
		if c == nil {
			continue
		}
		if mark, ok := procfs.StartMarkOf(root, c); ok {
			out = append(out, command{c, mark})
		}
	}
	// Equal starts keep the table's order, by pid.
	sort.SliceStable(out, func(i, j int) bool { return out[i].mark > out[j].mark })
	return out
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
// or its program, or, for an interpreter, by the script it runs (scriptIs).
// Another program's argument names nothing: "vim codex" edits a file.
func agentName(p *procfs.Proc, agents []string) string {
	script := ""
	if isInterpreter(p.Name()) {
		script = scriptOf(p.Argv)
	}
	for _, a := range agents {
		if p.Comm == a || p.Name() == a || (script != "" && scriptIs(script, a)) {
			return a
		}
	}
	return ""
}

// scriptIs reports whether a script is the agent: by its file's name, without
// what follows a dot ("/usr/local/bin/claude", ".../bin/codex.js", the module
// "aider.main"), or by the package it is installed in (".../node_modules/
// @anthropic-ai/claude-code/cli.js"). A folder of someone's own with the
// agent's name in it ("~/claude-workspace/server.py") names nothing.
func scriptIs(script, agent string) bool {
	parts := strings.FieldsFunc(script, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return false
	}
	file := parts[len(parts)-1]
	if i := strings.IndexByte(file, '.'); i > 0 {
		file = file[:i]
	}
	if file == agent {
		return true
	}
	// The package is the folder after the last node_modules, past its
	// @scope.
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] != "node_modules" {
			continue
		}
		pkg := parts[i+1]
		if strings.HasPrefix(pkg, "@") && i+2 < len(parts)-1 {
			pkg = parts[i+2]
		}
		return wordMatch(pkg, agent)
	}
	return false
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
