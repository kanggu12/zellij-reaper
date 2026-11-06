package inspect

import (
	"errors"
	"path/filepath"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// ErrNotInSession says pid runs under no zellij server.
var ErrNotInSession = errors.New("not inside a zellij session")

// ErrNameUnknown says the session was found but not the name it has now.
var ErrNameUnknown = errors.New("the session's name cannot be told")

// Here is the name the zellij session pid runs in has now. A pane keeps the
// name its session had when the pane opened, in ZELLIJ_SESSION_NAME, and a
// zellij command sent under an old name waits for a session by it. This reads
// only what that takes: the parents of pid up to the session's server, the
// server's sockets, the kernel's socket table and the socket directory, with no
// zellij process started, so a shell can afford it at every prompt.
func Here(src Sources, pid int) (string, error) {
	var server procfs.Server
	found := false
	// A pane's process tree is shallow; the bound ends a walk through a
	// table that changes as it is read.
	var child *procfs.Proc
	for range 64 {
		p, ok := procfs.Process(src.ProcRoot, pid)
		if !ok {
			break
		}
		// Windows keeps a parent's ID after the parent exits, and may give
		// it to a newer process: a "parent" that started after its child
		// is not its parent.
		if child != nil && !child.Started().IsZero() && p.Started().After(child.Started()) {
			break
		}
		if s, ok := procfs.ServerOf(p); ok && (p.UID == src.UID) {
			server, found = s, true
			break
		}
		if p.PPID <= 0 || p.PPID == pid {
			break
		}
		child, pid = p, p.PPID
	}
	if !found {
		return "", ErrNotInSession
	}
	// The socket file the server listens on, whatever it is called now: only
	// a match through the socket table or the file itself counts, since after
	// a rename another session may have taken the name on the server's
	// command line.
	dir := filepath.Dir(server.Socket)
	var socks []procfs.UnixSocket
	if src.Sockets != nil {
		socks, _ = src.Sockets()
	}
	var table *procfs.Table
	servers := []procfs.Server{server}
	if len(socks) > 0 {
		// The socket table needs only this server's sockets.
		table = procfs.Empty(src.ProcRoot)
	} else {
		// Matching by the file needs every server, to tell them apart.
		t, err := procfs.ReadZellij(src.ProcRoot)
		if err != nil {
			return "", ErrNameUnknown
		}
		table, servers = t, t.Servers(src.UID)
	}
	owners := socketOwners(table, servers)
	for _, name := range zellij.Sockets(dir) {
		s, how := findServer(filepath.Join(dir, name), servers, socks, owners)
		if s != nil && s.PID == server.PID && how == "socket file" {
			return name, nil
		}
	}
	return "", ErrNameUnknown
}
