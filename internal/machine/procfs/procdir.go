package procfs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// readDirAll takes a snapshot of a directory laid out like /proc. Processes
// that exit while it is being read are skipped, not reported as errors.
func readDirAll(root string) (*Table, error) {
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
	t.index()
	return t, nil
}

// readDirSessions is readDirAll for a pass: every process's stat, which gives the tree,
// and the rest (uid, command line, fd 0) only for zellij's own processes and
// everything under them, the only processes a pass looks at. The others keep a
// UID of -1 and no command line. On a machine with 166 processes this read took
// 11.9 ms in full and about a third of that this way.
func readDirSessions(root string) (*Table, error) {
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
		if p, ok := readStat(root, pid); ok {
			p.UID = -1
			t.ByPID[pid] = p
		}
	}
	t.index()
	// zellij's processes (servers and clients) and their descendants: panes,
	// the programs in them, a protected agent however deep.
	var queue []int
	for pid, p := range t.ByPID {
		if strings.Contains(p.Comm, "zellij") {
			queue = append(queue, pid)
		}
	}
	seen := map[int]bool{}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		readDetails(root, t.ByPID[pid])
		queue = append(queue, t.children[pid]...)
	}
	return t, nil
}

// readProc reads one process's stat, status, cmdline and fd 0.
func readProc(root string, pid int) (*Proc, bool) {
	p, ok := readStat(root, pid)
	if ok {
		readDetails(root, p)
	}
	return p, ok
}

// readStat reads one process's stat: its name, state, parent and terminal.
func readStat(root string, pid int) (*Proc, bool) {
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
	return p, true
}

// readDetails reads a process's real uid, command line and fd 0.
func readDetails(root string, p *Proc) {
	dir := filepath.Join(root, strconv.Itoa(p.PID))
	// The real uid is the first number on the Uid: line of status.
	if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		for line := range strings.SplitSeq(string(status), "\n") {
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
}

// Cwd is the directory a process works in, "" when it cannot be read or the
// directory is gone.
func (t *Table) Cwd(pid int) string {
	// A native reader that has no /proc fills in the directories itself.
	if t.cwds != nil {
		return t.cwds[pid]
	}
	p, err := os.Readlink(filepath.Join(t.Root, strconv.Itoa(pid), "cwd"))
	if err != nil || strings.HasSuffix(p, " (deleted)") {
		return ""
	}
	return p
}

// envDir reads a variable from root/<pid>/environ: the strings the process
// started with, each ending in a NUL.
func envDir(root string, pid int, key string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "environ"))
	if err != nil {
		return "", false
	}
	return lookupEnv(strings.Split(string(b), "\x00"), key)
}

// lookupEnv finds key=value among strings like an environment's.
func lookupEnv(env []string, key string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v, true
		}
	}
	return "", false
}
