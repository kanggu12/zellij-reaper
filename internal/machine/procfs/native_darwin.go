package procfs

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// macOS has no /proc. The process table comes from sysctl(kern.proc.all), a
// command line from sysctl(kern.procargs2), and the directories panes work in
// from one lsof call. What /proc gives through fd 0 comes here from the
// controlling terminal: a pane's input is its terminal.

// szomb is the p_stat of a zombie in <sys/proc.h>.
const szomb = 5

// readNative reads every process from sysctl. With sessions, only zellij's
// processes and everything under them get their command line and directory.
func readNative(sessions bool) (*Table, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	ttys := ttyNames()
	t := &Table{ByPID: map[int]*Proc{}, children: map[int][]int{}, Taken: time.Now(), cwds: map[int]string{}}
	for i := range kps {
		p := fromKinfo(&kps[i], ttys)
		t.ByPID[p.PID] = p
	}
	t.index()
	// zellij's processes and their descendants, the only ones a pass reads in
	// full; a full read takes every process.
	var want []int
	if sessions {
		want = zellijTree(t)
	} else {
		for pid := range t.ByPID {
			want = append(want, pid)
		}
	}
	for _, pid := range want {
		t.ByPID[pid].Argv = procArgs(pid)
	}
	for pid, dir := range cwdsOf(want) {
		t.cwds[pid] = dir
	}
	return t, nil
}

// zellijTree lists zellij's processes and everything under them.
func zellijTree(t *Table) []int {
	var queue, out []int
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
		out = append(out, pid)
		queue = append(queue, t.children[pid]...)
	}
	return out
}

// processNative reads one process in full.
func processNative(pid int) (*Proc, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid {
		return nil, false
	}
	p := fromKinfo(kp, ttyNames())
	p.Argv = procArgs(pid)
	return p, true
}

// fromKinfo turns one sysctl record into a Proc: everything but the command line.
func fromKinfo(kp *unix.KinfoProc, ttys map[int32]string) *Proc {
	p := &Proc{
		PID:  int(kp.Proc.P_pid),
		PPID: int(kp.Eproc.Ppid),
		UID:  int(kp.Eproc.Pcred.P_ruid),
		Comm: cString(kp.Proc.P_comm[:]),
		// The start time, kept for the starting-server check.
		started: time.Unix(kp.Proc.P_starttime.Sec, int64(kp.Proc.P_starttime.Usec)*1000),
	}
	if kp.Proc.P_stat == szomb {
		p.State = "Z"
	} else {
		p.State = "S"
	}
	// NODEV (-1) is no controlling terminal, which /proc says as 0.
	if tdev := kp.Eproc.Tdev; tdev != -1 && tdev != 0 {
		p.CTTY = int(tdev)
		p.Stdin = ttys[tdev]
	} else {
		// A process with no terminal is what a server starts for itself:
		// the same as a helper reading /dev/null on Linux.
		p.Stdin = "/dev/null"
	}
	return p
}

// ttyNames maps terminal devices to their paths under /dev, for the panes'
// last input and output.
func ttyNames() map[int32]string {
	out := map[int32]string{}
	matches, _ := filepath.Glob("/dev/ttys*")
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			out[st.Rdev] = m
		}
	}
	return out
}

// procArgs reads a process's command line from sysctl(kern.procargs2): the
// argument count, the executable's path, padding, then the arguments, each
// ending in a NUL. Another user's process gives nothing.
func procArgs(pid int) []string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(b[:4]))
	rest := b[4:]
	// Skip the executable's path and the NULs that pad it.
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	var argv []string
	for len(argv) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			argv = append(argv, string(rest))
			break
		}
		argv = append(argv, string(rest[:j]))
		rest = rest[j+1:]
	}
	return argv
}

// cwdsOf asks lsof, once, for the directory each process works in. lsof comes
// with macOS; without it the directories stay unknown, and a session is then
// named after what its panes show instead.
func cwdsOf(pids []int) map[int]string {
	out := map[int]string{}
	if len(pids) == 0 {
		return out
	}
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	b, _ := exec.Command("lsof", "-nP", "-w", "-a", "-d", "cwd", "-p", strings.Join(list, ","), "-F", "pn").Output()
	return parseLsofCwd(b)
}

// parseLsofCwd reads lsof's field output: a "p<pid>" line, then "n<path>".
func parseLsofCwd(b []byte) map[int]string {
	out := map[int]string{}
	pid := 0
	for _, line := range strings.Split(string(b), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case strings.HasPrefix(line, "n") && pid != 0:
			out[pid] = line[1:]
		}
	}
	return out
}

// youngestNative is the age of the youngest zellij server of uid.
func youngestNative(uid int) (time.Duration, bool) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.uid", uid)
	if err != nil {
		return 0, false
	}
	now := time.Now()
	var youngest time.Duration
	found := false
	for i := range kps {
		kp := &kps[i]
		if !strings.Contains(cString(kp.Proc.P_comm[:]), "zellij") {
			continue
		}
		age := now.Sub(time.Unix(kp.Proc.P_starttime.Sec, int64(kp.Proc.P_starttime.Usec)*1000))
		if age >= SettleAge || (found && age >= youngest) {
			continue
		}
		p := &Proc{PID: int(kp.Proc.P_pid), Argv: procArgs(int(kp.Proc.P_pid))}
		if _, ok := ServerOf(p); !ok {
			continue
		}
		youngest, found = max(age, 0), true
	}
	return youngest, found
}

// readZellijNative reads every process's parent and name, and the command line
// of zellij's processes and those under them, without lsof.
func readZellijNative() (*Table, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	ttys := ttyNames()
	t := &Table{ByPID: map[int]*Proc{}, children: map[int][]int{}, Taken: time.Now(), cwds: map[int]string{}}
	for i := range kps {
		p := fromKinfo(&kps[i], ttys)
		t.ByPID[p.PID] = p
	}
	t.index()
	for _, pid := range zellijTree(t) {
		t.ByPID[pid].Argv = procArgs(pid)
	}
	return t, nil
}

// bornNear and aloneWithin bound the match of a socket file to a server by
// time: zellij binds the socket right after the server starts, so the file is
// born within bornNear of it, and no other server may have started within
// aloneWithin, or the match could pick the wrong one.
const (
	bornNear    = 2 * time.Second
	aloneWithin = 5 * time.Second
)

// fileOwner matches a socket file to the server that started when the file was
// created. macOS keeps a file's creation time through a rename, which is all a
// rename changes, so this still holds for a renamed session. Two servers
// started within seconds of each other match neither.
func fileOwner(path string, servers []Server) (Server, bool) {
	born, ok := Born(path)
	if !ok {
		return Server{}, false
	}
	var match Server
	n := 0
	for _, s := range servers {
		if s.Started.IsZero() {
			continue
		}
		d := born.Sub(s.Started)
		if d < 0 {
			d = -d
		}
		if d <= aloneWithin {
			n++
			if d <= bornNear {
				match = s
			}
		}
	}
	if n != 1 || match.PID == 0 {
		return Server{}, false
	}
	return match, true
}

// nativeRoot is the directory a table reads from; this system has no /proc.
func nativeRoot(root string) string { return root }

// cString is a C string in a fixed buffer: up to its first NUL. The kernel
// does not clear what comes after it.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
