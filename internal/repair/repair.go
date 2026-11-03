// Package repair repairs sessions zellij can no longer reach: names too long
// for a Unix socket path, and servers whose socket file is gone.
//
// A session name becomes part of its socket path. Linux caps that path at 107
// bytes, and a session renamed past it keeps its server running with no
// socket, so the CLI cannot reach it, and its cache directory comes back after
// every delete. repair deletes such sessions, clears their files and, when
// asked, stops the servers that serve no socket, unless somebody may still be
// using one: a server keeps its clients after its socket file is gone.
package repair

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/inspect"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// Zellij is what recover asks of zellij.
type Zellij interface {
	ListSessions(ctx context.Context) ([]zellij.Listed, error)
	Delete(ctx context.Context, name string) error
}

// Inputs locate everything recover looks at.
type Inputs struct {
	Zellij     Zellij
	ProcRoot   string
	InfoDir    string
	SocketDirs []string
	UID        int
	Sockets    func() ([]procfs.UnixSocket, error)
	// Protected reports names never to touch (the protect pattern and list).
	Protected func(name string) bool
	// ProtectCommands are programs that keep a server running while they
	// run under it (config.ProtectCommands).
	ProtectCommands []string
	// Shells are extra prompt programs (config.Shells).
	Shells []string
	// Signal sends a signal to a process; nil means syscall.Kill. Tests
	// record the signals instead of sending any.
	Signal func(pid int, sig syscall.Signal) error
}

// signal is the function that sends signals.
func (in Inputs) signal() func(int, syscall.Signal) error {
	if in.Signal != nil {
		return in.Signal
	}
	return syscall.Kill
}

// Broken is a session whose socket path would be too long.
type Broken struct {
	Name string
	// PathBytes is the length the socket path would have.
	PathBytes int
}

// Orphan is a server that serves no socket file.
type Orphan struct {
	PID int
	// FirstName is the session name on its command line (its first name).
	FirstName string
	// Socket is the socket path on its command line; it tells this server
	// apart from a process that took over its pid.
	Socket string
	// Kept says why the server must not be stopped, "" when nothing keeps
	// it: a protected first name or program, a client connected, a busy
	// pane, or a fact that could not be read.
	Kept string
}

// Scan is what recover found.
type Scan struct {
	// SocketDir is the directory socket paths were measured against.
	SocketDir string
	// Budget is how many bytes a name may have there.
	Budget int
	// Broken sessions, by name.
	Broken []Broken
	// Orphans are servers with no socket file.
	Orphans []Orphan
	// OrphansKnown is false when sockets could not be matched to their files,
	// so orphans cannot be told from renamed sessions and none are listed.
	OrphansKnown bool
	// AllOrphaned is true when every server looked like an orphan, which
	// points at the socket table rather than at the servers: Repair then
	// stops none of them.
	AllOrphaned bool
}

// Find looks for broken sessions and orphan servers. It changes nothing.
func Find(ctx context.Context, in Inputs) (Scan, error) {
	var sc Scan
	table, err := procfs.Read(in.ProcRoot)
	if err != nil {
		return sc, err
	}
	servers := table.Servers(in.UID)
	// Measure against the directory the servers use, else the configured one.
	sc.SocketDir = firstNonEmpty(in.SocketDirs...)
	if len(servers) > 0 {
		sc.SocketDir = filepath.Dir(servers[0].Socket)
	}
	sc.Budget = zellij.SocketPathLimit - len(sc.SocketDir) - 1

	// Names come from zellij's list and from the directories it keeps, which
	// can outlive a session zellij no longer lists.
	seen := map[string]bool{}
	var names []string
	if listed, err := in.Zellij.ListSessions(ctx); err == nil {
		for _, l := range listed {
			if !seen[l.Name] {
				seen[l.Name] = true
				names = append(names, l.Name)
			}
		}
	}
	for _, n := range zellij.SessionDirs(in.InfoDir) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)
	dirs := socketDirs(sc.SocketDir, in.SocketDirs, servers)
	// protectedBroken is a protected session renamed past the socket path's
	// limit: its server is one of the orphans, and which one cannot be told.
	protectedBroken := ""
	for _, n := range names {
		// A protected name is never touched, broken or not.
		if in.Protected != nil && in.Protected(n) {
			if !socketExists(dirs, n) && len(sc.SocketDir)+1+len(n) > zellij.SocketPathLimit {
				protectedBroken = n
			}
			continue
		}
		// A session with a socket file can be reached, so it is not broken,
		// whatever the length of the directory it lives in.
		if socketExists(dirs, n) {
			continue
		}
		if total := len(sc.SocketDir) + 1 + len(n); total > zellij.SocketPathLimit {
			sc.Broken = append(sc.Broken, Broken{Name: n, PathBytes: total})
		}
	}

	sc.Orphans, sc.OrphansKnown = orphans(in, table, servers, dirs)
	sc.AllOrphaned = len(servers) > 1 && len(sc.Orphans) == len(servers)
	// Any orphan may be the protected session's server: keep them all.
	if protectedBroken != "" {
		for i := range sc.Orphans {
			if sc.Orphans[i].Kept == "" {
				sc.Orphans[i].Kept = "protected session " + protectedBroken + " has no socket file and may be this one"
			}
		}
	}
	return sc, nil
}

// socketDirs is every directory a session's socket file can be in: the one
// names are measured against, the configured ones, and those running
// servers name on their command lines.
func socketDirs(measured string, extra []string, servers []procfs.Server) []string {
	dirs := append([]string{measured}, extra...)
	for _, s := range servers {
		dirs = append(dirs, filepath.Dir(s.Socket))
	}
	return dirs
}

// orphans finds, in one look at the processes and the socket table, the
// servers none of whose listening sockets has a file in dirs, each with what
// keeps it, if anything. known is false when the socket table cannot be read:
// orphans then cannot be told from renamed sessions, and none are listed.
func orphans(in Inputs, table *procfs.Table, servers []procfs.Server, dirs []string) (found []Orphan, known bool) {
	// A server is an orphan when none of its listening sockets has a file in
	// a socket directory. A renamed session's file has a new name but the
	// same inode, so it still counts as served.
	if in.Sockets == nil {
		return nil, false
	}
	socks, err := in.Sockets()
	if err != nil {
		return nil, false
	}
	files := map[[2]uint32]bool{}
	for _, d := range dirs {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if ino, dev, ok := procfs.FileID(filepath.Join(d, e.Name())); ok {
				files[[2]uint32{ino, dev}] = true
			}
		}
	}
	listening := map[uint32]procfs.UnixSocket{}
	for _, u := range socks {
		if u.Listening() {
			listening[u.Inode] = u
		}
	}
	for _, s := range servers {
		// Only a server whose listening socket the table shows, with a file
		// identity, can be judged; one the table does not show is left alone.
		seen, served := false, false
		for ino := range table.SocketInodes(s.PID) {
			u, ok := listening[ino]
			if !ok || u.VFSIno == 0 {
				continue
			}
			seen = true
			if files[[2]uint32{u.VFSIno, u.VFSDev}] {
				served = true
				break
			}
		}
		if seen && !served {
			o := Orphan{PID: s.PID, FirstName: s.Session(), Socket: s.Socket}
			o.Kept = keep(in, table, socks, o)
			found = append(found, o)
		}
	}
	return found, true
}

// keep says why an orphan server must not be stopped, or "" when nothing
// keeps it. These are a pass's rules, in a pass's order: losing the socket
// file does not mean nobody uses the session, and a fact that cannot be read
// keeps the server.
func keep(in Inputs, t *procfs.Table, socks []procfs.UnixSocket, o Orphan) string {
	// Its name now is unknown (no socket file names it), so the name it
	// was started with is the one to check.
	if in.Protected != nil && in.Protected(o.FirstName) {
		return "its first name is protected"
	}
	if want := inspect.ProtectedProgram(t, o.PID, in.ProtectCommands); want != "" {
		return "protected program running (" + want + ")"
	}
	// A client stays connected after the socket file is removed.
	switch attached, detail := inspect.ListenerClients(t, o.PID, socks); attached {
	case policy.Yes:
		return "client attached (" + detail + ")"
	case policy.Unknown:
		return "attach-check uncertain (" + detail + ")"
	}
	for _, p := range inspect.Panes(t, o.PID, in.Shells) {
		if p.Busy != "" {
			return "running in a pane: " + p.Busy
		}
	}
	return ""
}

// socketExists reports whether any of dirs has a socket file named name.
func socketExists(dirs []string, name string) bool {
	for _, d := range dirs {
		if info, err := os.Lstat(filepath.Join(d, name)); err == nil && info.Mode()&os.ModeSocket != 0 {
			return true
		}
	}
	return false
}

// Step is one thing Repair did or tried.
type Step struct {
	// Target is a session name or "pid N".
	Target string
	// What is a short description ("deleted with zellij", "removed cache").
	What string
	// Err is set when the step failed.
	Err error
}

// Repair deletes broken sessions and clears their files; with stopOrphans it
// first stops orphan servers (SIGTERM, then SIGKILL after a second), which
// otherwise rewrite the files being cleared. An orphan the scan kept is left
// alone; one that a second look right before the stop does not clear is kept
// too, and reported as a step.
func Repair(ctx context.Context, in Inputs, sc Scan, stopOrphans bool) []Step {
	var steps []Step
	if stopOrphans && sc.OrphansKnown && sc.AllOrphaned {
		steps = append(steps, Step{Target: "servers", What: "not stopped: every server looked orphaned, which points at the socket table"})
	}
	if stopOrphans && sc.OrphansKnown && !sc.AllOrphaned {
		steps = append(steps, stopAll(in, sc)...)
	}
	for _, b := range sc.Broken {
		if err := in.Zellij.Delete(ctx, b.Name); err == nil {
			steps = append(steps, Step{Target: b.Name, What: "deleted with zellij"})
		} else {
			// zellij often cannot reach a broken session; the files are
			// cleared below either way, so this is reported, not fatal.
			steps = append(steps, Step{Target: b.Name, What: "zellij could not delete it", Err: err})
		}
		dir := filepath.Join(in.InfoDir, b.Name)
		if _, err := os.Stat(dir); err == nil {
			// filepath.Join cleans the path; a name with "/" or ".." cannot
			// leave InfoDir because zellij names are single path elements,
			// and the check below makes that a guarantee.
			if filepath.Dir(dir) != filepath.Clean(in.InfoDir) {
				steps = append(steps, Step{Target: b.Name, What: "skipped: name is not a plain directory name"})
				continue
			}
			err := os.RemoveAll(dir)
			steps = append(steps, Step{Target: b.Name, What: "removed " + dir, Err: err})
		}
	}
	return steps
}

// stopAll stops the orphans the scan did not keep, after one more look: the
// scan is older than the stop, and since then a client may have connected or
// the pid may have passed to another process.
func stopAll(in Inputs, sc Scan) []Step {
	var todo []Orphan
	for _, o := range sc.Orphans {
		if o.Kept == "" {
			todo = append(todo, o)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	now, doubt := lookAgain(in, sc)
	var steps []Step
	stopped := false
	for _, o := range todo {
		why := doubt
		if why == "" {
			why = stillStoppable(o, now)
		}
		if why != "" {
			steps = append(steps, Step{Target: "pid " + itoa(o.PID), What: "kept orphan server: " + why})
			continue
		}
		steps = append(steps, stop(in.signal(), o.PID))
		stopped = true
	}
	// Let the stopped servers' last writes land before clearing files.
	if stopped {
		time.Sleep(500 * time.Millisecond)
	}
	return steps
}

// lookAgain scans for orphans once more, by pid. doubt is set when that scan
// cannot be trusted, which keeps every server.
func lookAgain(in Inputs, sc Scan) (map[int]Orphan, string) {
	table, err := procfs.Read(in.ProcRoot)
	if err != nil {
		return nil, "could not read the processes again: " + err.Error()
	}
	servers := table.Servers(in.UID)
	found, known := orphans(in, table, servers, socketDirs(sc.SocketDir, in.SocketDirs, servers))
	if !known {
		return nil, "could not read the socket table again"
	}
	// The same check the scan makes: every server orphaned points at the
	// socket table, not at the servers.
	if len(servers) > 1 && len(found) == len(servers) {
		return nil, "every server looked orphaned on a second look"
	}
	byPID := map[int]Orphan{}
	for _, o := range found {
		byPID[o.PID] = o
	}
	return byPID, ""
}

// stillStoppable says why the second look keeps an orphan, or "" when it
// may be stopped.
func stillStoppable(o Orphan, now map[int]Orphan) string {
	n, ok := now[o.PID]
	switch {
	// Exited, serving a socket file again, or the pid is another process's
	// now: there is nothing to stop.
	case !ok || n.Socket != o.Socket:
		return "it is no longer an orphan server"
	case n.Kept != "":
		return n.Kept
	}
	return ""
}

// stop ends one process: SIGTERM, then SIGKILL if it is still there.
func stop(kill func(int, syscall.Signal) error, pid int) Step {
	st := Step{Target: "pid " + itoa(pid), What: "stopped orphan server"}
	if err := kill(pid, syscall.SIGTERM); err != nil {
		st.Err = err
		return st
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		// Signal 0 only asks whether the process is still there.
		if kill(pid, 0) != nil {
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	st.What = "stopped orphan server (SIGKILL)"
	st.Err = kill(pid, syscall.SIGKILL)
	return st
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
