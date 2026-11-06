package procfs

import (
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no /proc. The process table comes from a Toolhelp snapshot, a
// command line from NtQueryInformationProcess, and whose a process is from its
// token. A process of this user gets UID 0 (CurrentUID), anyone else's -1.
// Windows keeps no controlling terminal and no fd 0 to read: a pane's input
// and its last use are unknown here, and the session files say when it was
// last used instead.

// readNative reads every process. With sessions, only zellij's processes and
// everything under them get their command line, owner and start time.
func readNative(sessions bool) (*Table, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	t := &Table{ByPID: map[int]*Proc{}, children: map[int][]int{}, Taken: time.Now(), cwds: map[int]string{}}
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := windows.UTF16ToString(e.ExeFile[:])
		p := &Proc{PID: int(e.ProcessID), PPID: int(e.ParentProcessID), UID: -1, State: "S",
			Comm: strings.TrimSuffix(strings.ToLower(name), ".exe")}
		t.ByPID[p.PID] = p
	}
	// Windows does not hand a process to a new parent when its parent exits,
	// so a parent ID can name a process that is gone, or a newer one that
	// took the number. A parent that started after its child is not its parent.
	me := CurrentSID()
	var want []int
	if sessions {
		want = zellijTreeWin(t)
	} else {
		for pid := range t.ByPID {
			want = append(want, pid)
		}
	}
	for _, pid := range want {
		fillDetails(t.ByPID[pid], me)
	}
	for _, p := range t.ByPID {
		if parent, ok := t.ByPID[p.PPID]; ok && !p.started.IsZero() && !parent.started.IsZero() && parent.started.After(p.started) {
			p.PPID = 0
		}
	}
	t.index()
	return t, nil
}

// zellijTreeWin lists zellij's processes and everything under them, before
// the child index exists.
func zellijTreeWin(t *Table) []int {
	kids := map[int][]int{}
	for pid, p := range t.ByPID {
		kids[p.PPID] = append(kids[p.PPID], pid)
	}
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
		if seen[pid] || pid == 0 {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, kids[pid]...)
	}
	return out
}

// fillDetails reads a process's command line, start time and owner. A process
// that cannot be opened (another user's, or a protected one) keeps none.
func fillDetails(p *Proc, me *windows.SID) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		p.started = time.Unix(0, created.Nanoseconds())
	}
	if me != nil {
		var tok windows.Token
		if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) == nil {
			if u, err := tok.GetTokenUser(); err == nil && u.User.Sid.Equals(me) {
				p.UID = 0
			}
			tok.Close()
		}
	}
	p.Argv = commandLine(h)
}

// commandLine reads a process's command line (Windows 8.1 and later) and
// splits it the way the C runtime does.
func commandLine(h windows.Handle) []string {
	var need uint32
	// The first call says how big the answer is.
	_ = windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, nil, 0, &need)
	if need == 0 {
		return nil
	}
	buf := make([]byte, need)
	if windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), need, &need) != nil {
		return nil
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	line := us.String()
	if line == "" {
		return nil
	}
	argv, err := windows.DecomposeCommandLine(line)
	if err != nil {
		return strings.Fields(line)
	}
	return argv
}

// processNative reads one process in full: its entry in a Toolhelp snapshot,
// which has its parent and name, then its own command line, start and owner.
func processNative(pid int) (*Proc, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) != pid {
			continue
		}
		p := &Proc{PID: pid, PPID: int(e.ParentProcessID), UID: -1, State: "S",
			Comm: strings.TrimSuffix(strings.ToLower(windows.UTF16ToString(e.ExeFile[:])), ".exe")}
		fillDetails(p, CurrentSID())
		return p, true
	}
	return nil, false
}

// youngestNative needs no answer on Windows: zellij lists its sessions there
// from the files that hold their servers' process IDs and never connects to a
// starting server, so nothing waits.
func youngestNative(uid int) (time.Duration, bool) {
	_ = uid
	return 0, false
}

// CurrentSID is the user this program runs as.
func CurrentSID() *windows.SID {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return nil
	}
	return u.User.Sid
}

// CurrentUID is the UID this user's processes get in a Table: Windows has no
// UIDs, so it is 0, and every other user's is -1.
func CurrentUID() int { return 0 }

// readZellijNative is the pass's read: Windows has no lighter one.
func readZellijNative() (*Table, error) { return readNative(true) }

// fileOwner reads the server's process ID that zellij writes into the socket
// file, which keeps it through a rename.
func fileOwner(path string, servers []Server) (Server, bool) {
	pid, ok := PIDFile(path)
	if !ok {
		return Server{}, false
	}
	for _, s := range servers {
		if s.PID == pid {
			return s, true
		}
	}
	return Server{}, false
}

// nativeRoot is the directory a table reads from; this system has no /proc.
func nativeRoot(root string) string { return root }
