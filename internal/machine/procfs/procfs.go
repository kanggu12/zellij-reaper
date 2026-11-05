// Package procfs takes one consistent look at the user's processes through
// /proc: who is whose child, what each one runs, and which terminal it reads.
// The reaper decides from this snapshot instead of calling pgrep and ps many
// times, so every check in a pass sees the same process tree.
package procfs

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
}

// Name is the program's name as people know it: the command line's first
// word without its directory or a login shell's leading "-", falling back to
// Comm. For a script run through an interpreter ("sh /usr/bin/claude"),
// Name is the interpreter; ScriptName finds the script.
func (p *Proc) Name() string {
	if len(p.Argv) > 0 && p.Argv[0] != "" {
		return strings.TrimPrefix(filepath.Base(p.Argv[0]), "-")
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
		names = append(names, filepath.Base(p.Argv[1]))
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
	// Root is the /proc directory the snapshot came from.
	Root string
	// Taken is when the snapshot was made.
	Taken time.Time
}

// Read takes a snapshot of root (normally "/proc"). Processes that exit while
// it is being read are skipped, not reported as errors.
func Read(root string) (*Table, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	t := &Table{ByPID: map[int]*Proc{}, children: map[int][]int{}, Root: root, Taken: time.Now()}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		// Only the numeric entries are processes.
		if err != nil {
			continue
		}
		p, ok := readProc(root, pid)
		if !ok {
			continue
		}
		t.ByPID[pid] = p
	}
	// Build the child index once, in PID order for stable output.
	for pid, p := range t.ByPID {
		t.children[p.PPID] = append(t.children[p.PPID], pid)
	}
	for _, kids := range t.children {
		sort.Ints(kids)
	}
	return t, nil
}

// readProc reads one process's stat, status, cmdline and fd 0.
func readProc(root string, pid int) (*Proc, bool) {
	dir := filepath.Join(root, strconv.Itoa(pid))
	stat, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return nil, false
	}
	// stat is "pid (comm) state ppid ...", and comm may itself contain spaces
	// and parentheses, so split at the last ")".
	s := string(stat)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return nil, false
	}
	p := &Proc{PID: pid, Comm: s[open+1 : closing]}
	fields := strings.Fields(s[closing+1:])
	if len(fields) < 2 {
		return nil, false
	}
	p.State = fields[0]
	p.PPID, _ = strconv.Atoi(fields[1])
	// After the state and the parent: process group, session, tty_nr.
	if len(fields) > 4 {
		p.CTTY, _ = strconv.Atoi(fields[4])
	}
	// The real uid is the first number on the Uid: line of status.
	if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				if f := strings.Fields(line); len(f) > 1 {
					p.UID, _ = strconv.Atoi(f[1])
				}
				break
			}
		}
	}
	// cmdline separates arguments with NUL and usually ends with one.
	if raw, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil && len(raw) > 0 {
		p.Argv = strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	}
	// fd 0 of another user's process is unreadable; that leaves Stdin empty.
	p.Stdin, _ = os.Readlink(filepath.Join(dir, "fd", "0"))
	return p, true
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

// Cwd is the directory a process works in, "" when it cannot be read or the
// directory is gone.
func (t *Table) Cwd(pid int) string {
	p, err := os.Readlink(filepath.Join(t.Root, strconv.Itoa(pid), "cwd"))
	if err != nil || strings.HasSuffix(p, " (deleted)") {
		return ""
	}
	return p
}

// Server is a running zellij server.
type Server struct {
	// PID is the server process.
	PID int
	// Socket is the socket path from its command line.
	Socket string
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
		if !strings.Contains(filepath.Base(p.Argv[0]), "zellij") {
			continue
		}
		for i := 1; i+1 < len(p.Argv); i++ {
			if p.Argv[i] == "--server" {
				out = append(out, Server{PID: p.PID, Socket: p.Argv[i+1]})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// IsZombie reports whether the process has exited and waits to be reaped by
// its parent.
func (p *Proc) IsZombie() bool { return p.State == "Z" }
