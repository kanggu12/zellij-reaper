package procfs

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SettleAge is how old the youngest zellij server must be before anything
// connects to every session's socket. zellij 0.44 and 0.45 panic when a
// connection to a new server closes before its first client has set the session
// up (zellij issue 5632), and `zellij list-sessions` makes exactly such a
// connection to every socket. Measured on 0.44.3 and 0.45.1, one listing 0.1 s
// after `attach --create-background` killed the new session 5 times out of 5,
// and none at 0.2 s or later; a second leaves room for a slow machine.
const SettleAge = time.Second

// clockTicks is USER_HZ, the unit of the start time in /proc/<pid>/stat. It is
// 100 on every Linux build that matters (the kernel's ABI fixes it).
const clockTicks = 100

// YoungestServer returns the age of the youngest zellij server of uid, and
// false when there is none. It reads only stat for every process, and the
// command line only for zellij processes started in the last SettleAge, so a
// pass that finds none spends about a millisecond.
func YoungestServer(root string, uid int) (time.Duration, bool) {
	up, ok := uptime(root)
	if !ok {
		return 0, false
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, false
	}
	var youngest time.Duration
	found := false
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		// Only the numeric entries are processes.
		if err != nil {
			continue
		}
		comm, start, ok := commAndStart(root, pid)
		if !ok || !strings.Contains(comm, "zellij") {
			continue
		}
		age := up - start
		// An older server is past the danger; only a young one is looked at closer.
		if age >= SettleAge || (found && age >= youngest) {
			continue
		}
		p := &Proc{PID: pid, Comm: comm, UID: -1}
		readDetails(root, p)
		if p.UID != uid {
			continue
		}
		if _, ok := ServerOf(p); !ok {
			continue
		}
		youngest, found = max(age, 0), true
	}
	return youngest, found
}

// Settle waits, at most a little over SettleAge, until every zellij server of
// uid is at least SettleAge old. A caller that is about to run
// `zellij list-sessions` calls it, so the listing cannot kill a session that is
// starting at that moment.
func Settle(ctx context.Context, root string, uid int) {
	deadline := time.Now().Add(SettleAge + 200*time.Millisecond)
	for time.Now().Before(deadline) {
		age, ok := YoungestServer(root, uid)
		if !ok {
			return
		}
		wait := min(SettleAge-age, time.Until(deadline))
		if wait <= 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// uptime reads how long the machine has been up, from /proc/uptime.
func uptime(root string) (time.Duration, bool) {
	b, err := os.ReadFile(filepath.Join(root, "uptime"))
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// commAndStart reads a process's kernel name and when it started, counted from
// boot like /proc/uptime.
func commAndStart(root string, pid int) (string, time.Duration, bool) {
	b, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, false
	}
	// stat is "pid (comm) state ppid ...", and comm may itself contain spaces
	// and parentheses, so split at the last ")".
	s := string(b)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return "", 0, false
	}
	fields := strings.Fields(s[closing+1:])
	// starttime is field 22 of stat; the fields after comm start at field 3.
	if len(fields) < 20 {
		return "", 0, false
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return s[open+1 : closing], time.Duration(ticks) * time.Second / clockTicks, true
}
