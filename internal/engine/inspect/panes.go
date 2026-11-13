package inspect

import (
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
)

// shellSet is KnownShells plus the configured extras.
func shellSet(extra []string) map[string]bool {
	set := map[string]bool{}
	for _, s := range KnownShells {
		set[s] = true
	}
	for _, s := range extra {
		set[s] = true
	}
	return set
}

// Panes describes the pane processes of a server, judged by the same rule a
// pass uses; shells are extra prompt programs on top of KnownShells.
func Panes(t *procfs.Table, server int, shells []string) []Pane {
	return panes(t, server, shellSet(shells))
}

// panes describes the pane processes of a server.
func panes(t *procfs.Table, server int, shells map[string]bool) []Pane {
	var out []Pane
	for _, p := range t.Children(server) {
		// A pane whose process has exited and waits to be collected has
		// nothing running and no terminal to read.
		if p.IsZombie() {
			continue
		}
		// Not every child of a server is a pane: zellij runs `ps -ao ppid,args`
		// to save its layouts, and plugins run commands of their own. Caught
		// in the few milliseconds they live, they would make an idle session
		// look busy for that pass.
		if isHelper(p) {
			continue
		}
		pane := Pane{PID: p.PID, Command: short(p.CommandLine()), TTY: p.Stdin, Cwd: t.Cwd(p.PID)}
		pane.Prompt = isPrompt(p, shells)
		if !pane.Prompt {
			// A pane that runs a program directly (a command pane, or an
			// editor started from a layout) is busy as long as it runs.
			pane.Busy = short(p.CommandLine())
		} else if kids := t.Children(p.PID); len(kids) > 0 {
			// A shell with a child is running a command in the foreground or
			// the background.
			pane.Busy = short(kids[0].CommandLine())
		}
		if tty := p.Stdin; tty != "" && strings.HasPrefix(tty, "/") && tty != "/dev/null" {
			pane.LastIO = procfs.LastIO(tty)
		}
		out = append(out, pane)
	}
	return out
}

// consoleHosts are the programs Windows runs behind every pseudoconsole: one
// per pane, and none of them a program the pane runs.
var consoleHosts = map[string]bool{"conhost": true, "openconsole": true}

// isHelper reports whether a server's child is one of its helpers. Every pane
// has its terminal as its controlling terminal, whatever its input was pointed
// at afterwards: `nohup`, `exec </dev/null` and a pipe all keep it, and those are
// real jobs a pass must not take for nothing. A helper has no controlling
// terminal, because the server has none to pass on, and reads no terminal
// either. A stdin that cannot be read says nothing, so it does not count: a
// process of another user is no reason to call a session idle.
func isHelper(p *procfs.Proc) bool {
	// On Windows every pane's pseudoconsole runs as a child of the server.
	if consoleHosts[p.Comm] {
		return true
	}
	if p.CTTY != 0 {
		return false
	}
	// zellij's own ps (0.43 runs `ps -ao ppid,args` to save its layouts,
	// every second with serialization_interval 1), caught as it exits: its
	// input can no longer be read, and it was taken for a pane running ps,
	// which named a session after it (handbook-ps). A ps someone runs in a
	// pane has the pane's terminal.
	if p.Comm == "ps" {
		return true
	}
	return p.Stdin == "/dev/null" || strings.HasPrefix(p.Stdin, "pipe:") || strings.HasPrefix(p.Stdin, "socket:")
}

// isPrompt reports whether a process is an interactive shell: both its kernel
// name and its program are shells, and it was not given a script to run. A
// script started from its shebang has the script's name as its kernel name,
// so it is not mistaken for a prompt.
func isPrompt(p *procfs.Proc, shells map[string]bool) bool {
	if !shells[p.Comm] || !shells[p.Name()] {
		return false
	}
	for _, a := range p.Argv[min(1, len(p.Argv)):] {
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}

// short keeps a command line to one readable line.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}
