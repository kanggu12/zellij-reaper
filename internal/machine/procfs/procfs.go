// Package procfs takes one consistent look at the user's processes through
// /proc: who is whose child, what each one runs, and which terminal it reads.
// The reaper decides from this snapshot instead of calling pgrep and ps many
// times, so every check in a pass sees the same process tree.
package procfs

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Proc is one process.
type Proc struct {
	// PID and PPID are the process and its parent.
	PID, PPID int
	// UID is the real user id.
	UID int
	// Comm is the kernel's short name (at most 15 bytes), e.g. "bash".
	Comm string
	// Argv is the command line; empty for kernel threads and zombies.
	Argv []string
	// Stdin is where file descriptor 0 points, e.g. "/dev/pts/3".
	Stdin string
	// State is the one-letter state from /proc/<pid>/stat ("Z" for a zombie).
	State string
	// CTTY is the device of its controlling terminal, 0 when it has none.
	CTTY int
	// started is when it started, where the reader knows it.
	started time.Time
}

// Name is the program's name as people know it: the command line's first
// word without its directory or a login shell's leading "-", falling back to
// Comm. For a script run through an interpreter ("sh /usr/bin/claude"),
// Name is the interpreter; ScriptName finds the script.
func (p *Proc) Name() string {
	if len(p.Argv) > 0 && p.Argv[0] != "" {
		// A Windows program's name goes without its ".exe", as its kernel
		// name does there.
		return strings.TrimSuffix(strings.TrimPrefix(baseName(p.Argv[0]), "-"), ".exe")
	}
	return p.Comm
}

// Names returns every name the process can be recognized by: its kernel name,
// its program and, for an interpreter, the script it runs. A coding agent
// started as "node .../claude-code/cli.js" or "/bin/sh ./claude" is still
// found by "claude" this way.
func (p *Proc) Names() []string {
	names := []string{p.Comm, p.Name()}
	if len(p.Argv) > 1 && !strings.HasPrefix(p.Argv[1], "-") {
		names = append(names, baseName(p.Argv[1]))
	}
	return names
}

// CommandLine joins Argv for display, falling back to Comm.
func (p *Proc) CommandLine() string {
	if len(p.Argv) == 0 {
		return p.Comm
	}
	return strings.Join(p.Argv, " ")
}

// Table is a snapshot of processes.
type Table struct {
	// ByPID holds every process that could be read.
	ByPID map[int]*Proc
	// children maps a parent to its children, sorted by PID.
	children map[int][]int
	// Root is the /proc directory the snapshot came from, empty for a native
	// reader on a system without one.
	Root string
	// cwds and sockets, when a native reader set them, answer Cwd and
	// SocketInodes in place of Root.
	cwds    map[int]string
	sockets map[int]map[uint32]bool
	// Taken is when the snapshot was made.
	Taken time.Time
}

// index builds the child index once, in PID order: a session's first pane is
// its oldest, whichever order the map gives (ReadSessions once left this out,
// and a session's name changed from pass to pass with its first pane).
func (t *Table) index() {
	for pid, p := range t.ByPID {
		t.children[p.PPID] = append(t.children[p.PPID], pid)
	}
	for _, kids := range t.children {
		sort.Ints(kids)
	}
}

// Children returns the direct children of pid.
func (t *Table) Children(pid int) []*Proc {
	var out []*Proc
	for _, c := range t.children[pid] {
		out = append(out, t.ByPID[c])
	}
	return out
}

// Descendants returns every process below pid, breadth first, up to max
// processes (a runaway fork bomb must not make a pass slow).
func (t *Table) Descendants(pid, max int) []*Proc {
	var out []*Proc
	queue := []int{pid}
	seen := map[int]bool{pid: true}
	for len(queue) > 0 && len(out) < max {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range t.children[cur] {
			if seen[c] {
				continue
			}
			// Stop as soon as the limit is reached, not after a whole level.
			if len(out) == max {
				break
			}
			seen[c] = true
			out = append(out, t.ByPID[c])
			queue = append(queue, c)
		}
	}
	return out
}

// Server is a running zellij server.
type Server struct {
	// PID is the server process.
	PID int
	// Socket is the socket path from its command line.
	Socket string
	// Started is when the server started, zero where the reader does not
	// know it.
	Started time.Time
}

// Session is the session name the server serves: its socket file's name.
func (s Server) Session() string { return filepath.Base(s.Socket) }

// Servers finds the zellij servers owned by uid. A server's command line is
// "zellij --server <socket path>", which names both the session and the
// socket directory, whatever directory this zellij build uses.
func (t *Table) Servers(uid int) []Server {
	var out []Server
	for _, p := range t.ByPID {
		if p.UID != uid || len(p.Argv) < 3 {
			continue
		}
		// The program must be zellij (a path or a renamed copy still has
		// "zellij" in its name); a shell running `grep zellij --server`
		// would otherwise look like one.
		if !strings.Contains(baseName(p.Argv[0]), "zellij") {
			continue
		}
		for i := 1; i+1 < len(p.Argv); i++ {
			if p.Argv[i] == "--server" {
				out = append(out, Server{PID: p.PID, Socket: p.Argv[i+1], Started: p.started})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// Started is when a process started, zero where the reader does not know it.
func (p *Proc) Started() time.Time { return p.started }

// IsZombie reports whether the process has exited and waits to be reaped by
// its parent.
func (p *Proc) IsZombie() bool { return p.State == "Z" }

// ServerOf reports whether p is a zellij server, and the socket it was started on.
func ServerOf(p *Proc) (Server, bool) {
	// The same test Servers makes: zellij in the program's name, then --server.
	if len(p.Argv) < 3 || !strings.Contains(baseName(p.Argv[0]), "zellij") {
		return Server{}, false
	}
	for i := 1; i+1 < len(p.Argv); i++ {
		if p.Argv[i] == "--server" {
			return Server{PID: p.PID, Socket: p.Argv[i+1], Started: p.started}, true
		}
	}
	return Server{}, false
}

// Native is the root that means "this machine": /proc on Linux, and the
// system's own process interfaces elsewhere. Any other root is read as a
// directory laid out like /proc, which is how the tests fake a machine.
const Native = ""

// Read takes a snapshot of every process.
func Read(root string) (*Table, error) {
	if root == Native {
		return readNative(false)
	}
	return readDirAll(root)
}

// ReadSessions is Read for a pass: every process's parent and name, and the
// rest (uid, command line, input) only for zellij's own processes and
// everything under them, the only processes a pass looks at. The others keep
// a UID of -1 and no command line. On a machine with 166 processes this read
// took 11.9 ms in full and about a third of that this way.
func ReadSessions(root string) (*Table, error) {
	if root == Native {
		return readNative(true)
	}
	return readDirSessions(root)
}

// Process reads one process in full, as Read would. ok is false when it is gone.
func Process(root string, pid int) (*Proc, bool) {
	if root == Native {
		return processNative(pid)
	}
	return readProc(root, pid)
}

// ReadZellij reads only what finds zellij's servers: their command lines and
// start times, without the directories panes work in. `here` reads this at
// every prompt.
func ReadZellij(root string) (*Table, error) {
	if root == Native {
		return readZellijNative()
	}
	return readDirSessions(root)
}

// FileOwner reports which of servers a session's socket file belongs to, from
// the file itself where the system allows it: the process ID zellij writes into
// it on Windows, its creation time on macOS. On Linux the kernel's socket table
// answers instead, so this says nothing there.
func FileOwner(path string, servers []Server) (Server, bool) { return fileOwner(path, servers) }

// Empty is a table with no process in it yet, which still reads a process's
// sockets and directory from root as they are asked for.
func Empty(root string) *Table {
	return &Table{ByPID: map[int]*Proc{}, children: map[int][]int{}, Root: nativeRoot(root)}
}

// baseName is the last element of a path written with either separator: a
// Windows command line names its program with backslashes, which
// filepath.Base reads as part of a name on other systems.
func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
