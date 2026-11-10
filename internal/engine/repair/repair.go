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
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
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
	// Signal sends a signal to a process; nil means procfs.Signal. Tests
	// record the signals instead of sending any.
	Signal func(pid int, sig syscall.Signal) error
	// Index maps names to the ids zellij 0.46 and later name sockets and
	// directories by; nil reads zellij's own, next to InfoDir.
	Index *zellij.Index
	// Log is given every step of a repair as a line for the decision log,
	// when the step ends; nil keeps no record. A scan and a dry run write
	// nothing: they change nothing.
	Log func(line string)
}

// index is the session index recover goes by, and why it could not be read:
// nil when it could, or when there is none (a zellij from before ids).
func (in Inputs) index() (zellij.Index, error) {
	if in.Index != nil {
		return *in.Index, nil
	}
	return zellij.ReadIndex(in.InfoDir)
}

// signal is the function that sends signals.
func (in Inputs) signal() func(int, syscall.Signal) error {
	if in.Signal != nil {
		return in.Signal
	}
	return procfs.Signal
}

// Step is one thing Repair did or tried.
type Step struct {
	// Target is a session name or "pid N".
	Target string
	// Session says which session a pid's server is, in Orphan.said's words;
	// "" for a step about a session, whose name is the target.
	Session string
	// What says what came of it, failed or not ("deleted with zellij",
	// "could not remove DIR").
	What string
	// Err is set when the step failed.
	Err error
}

// Line is the step as the decision log holds it. A server is written with
// its session beside the pid: on the terminal the scan above the steps names
// it, and the log has no scan.
func (s Step) Line() string {
	line := s.Target
	if s.Session != "" {
		line += " (" + s.Session + ")"
	}
	line += " :: recover: " + s.What
	if s.Err != nil {
		line += ": " + s.Err.Error()
	}
	return line
}

// said names an orphan's session as the log does: by the name it has now
// where zellij's index still holds one, else by the name it started under.
func (o Orphan) said() string {
	switch {
	case o.Name != "":
		return "name: " + o.Name
	case o.FirstName != "":
		return "first name: " + o.FirstName
	}
	return "name unknown"
}

// Repair deletes broken sessions and clears their files; with stopOrphans it
// first stops orphan servers (SIGTERM, then SIGKILL after a second), which
// otherwise rewrite the files being cleared. An orphan the scan kept is left
// alone; one that a second look right before the stop does not clear is kept
// too, and reported as a step.
func Repair(ctx context.Context, in Inputs, sc Scan, stopOrphans bool) []Step {
	var steps []Step
	// note keeps a step and, where there is a log, writes it there at once:
	// a recover cut short has then put down what it got done.
	note := func(st Step) {
		steps = append(steps, st)
		if in.Log != nil {
			in.Log(st.Line())
		}
	}
	if stopOrphans && sc.OrphansKnown && sc.AllOrphaned {
		note(Step{Target: "servers", What: "not stopped: every server looked orphaned, which points at the socket table"})
	}
	if stopOrphans && sc.OrphansKnown && !sc.AllOrphaned {
		stopAll(in, sc, note)
	}
	for _, b := range sc.Broken {
		if err := in.Zellij.Delete(ctx, b.Name); err == nil {
			note(Step{Target: b.Name, What: "deleted with zellij"})
		} else {
			// zellij often cannot reach a broken session; the files are
			// cleared below either way, so this is reported, not fatal.
			note(Step{Target: b.Name, What: "zellij could not delete it", Err: err})
		}
		dir := filepath.Join(in.InfoDir, b.Name)
		if _, err := os.Stat(dir); err == nil {
			// filepath.Join cleans the path; a name with "/" or ".." cannot
			// leave InfoDir because zellij names are single path elements,
			// and the check below makes that a guarantee.
			if filepath.Dir(dir) != filepath.Clean(in.InfoDir) {
				note(Step{Target: b.Name, What: "skipped: name is not a plain directory name"})
				continue
			}
			// The words say what came of it: a step that failed must not
			// read as done with a reason after it. The reason names the
			// entry inside that would not go.
			if err := os.RemoveAll(dir); err != nil {
				note(Step{Target: b.Name, What: "could not remove " + dir, Err: err})
			} else {
				note(Step{Target: b.Name, What: "removed " + dir})
			}
		}
	}
	return steps
}

// stopAll stops the orphans the scan did not keep, after one more look: the
// scan is older than the stop, and since then a client may have connected or
// the pid may have passed to another process. Every server it stops or keeps
// is a step handed to note.
func stopAll(in Inputs, sc Scan, note func(Step)) {
	var todo []Orphan
	for _, o := range sc.Orphans {
		if o.Kept == "" {
			todo = append(todo, o)
		}
	}
	if len(todo) == 0 {
		return
	}
	now, doubt := lookAgain(in, sc)
	stopped := false
	for _, o := range todo {
		why := doubt
		if why == "" {
			why = stillStoppable(o, now)
		}
		if why != "" {
			note(Step{Target: "pid " + itoa(o.PID), Session: o.said(), What: "kept orphan server: " + why})
			continue
		}
		st := stop(in.signal(), o.PID, time.Second, in.sameProcess(o.PID))
		st.Session = o.said()
		note(st)
		stopped = true
	}
	// Let the stopped servers' last writes land before clearing files.
	if stopped {
		time.Sleep(500 * time.Millisecond)
	}
}

// lookAgain scans for orphans once more, by pid. doubt is set when that scan
// cannot be trusted, which keeps every server.
func lookAgain(in Inputs, sc Scan) (map[int]Orphan, string) {
	table, err := procfs.Read(in.ProcRoot)
	if err != nil {
		return nil, "could not read the processes again: " + err.Error()
	}
	servers := table.Servers(in.UID)
	idx, err := in.index()
	if err != nil {
		return nil, "could not read zellij's session index again"
	}
	found, known := orphans(in, idx, table, servers, socketDirs(sc.SocketDir, in.SocketDirs, servers))
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

// sameProcess returns a check that pid is still the process it is now: one
// that ended may give its pid to another process of the same user, which
// must not get the SIGKILL meant for the server. The process's start time
// tells them apart. Where it cannot be read, the check passes, as before.
func (in Inputs) sameProcess(pid int) func() bool {
	mark, ok := procfs.StartMark(in.ProcRoot, pid)
	if !ok {
		return func() bool { return true }
	}
	return func() bool {
		now, ok := procfs.StartMark(in.ProcRoot, pid)
		return ok && now == mark
	}
}

// stop ends one process: SIGTERM, then SIGKILL if it is still there after
// wait. same says whether pid is still the process signalled; once it is
// not, the process ended, and no signal follows. The step's words say which
// of the two it took, or that the signal was refused.
func stop(kill func(int, syscall.Signal) error, pid int, wait time.Duration, same func() bool) Step {
	st := Step{Target: "pid " + itoa(pid), What: "stopped orphan server"}
	if err := kill(pid, syscall.SIGTERM); err != nil {
		st.What, st.Err = "could not stop orphan server", err
		return st
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		// Signal 0 only asks whether a process has the pid; the start time
		// says whether it is still this one.
		if kill(pid, 0) != nil || !same() {
			return st
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Looked at once more right before the SIGKILL: the pid may have gone
	// to another process since the last look.
	if !same() {
		return st
	}
	if err := kill(pid, syscall.SIGKILL); err != nil {
		// Gone after the last look and before SIGKILL: SIGTERM ended it.
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return st
		}
		st.What, st.Err = "could not stop orphan server (SIGKILL)", err
		return st
	}
	st.What = "stopped orphan server (SIGKILL)"
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
