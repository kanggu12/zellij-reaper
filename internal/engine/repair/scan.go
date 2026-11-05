package repair

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

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
	sc.Budget = zellij.NameRoom(sc.SocketDir)

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
