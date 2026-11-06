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
	for range 64 {
		p, ok := procfs.Process(src.ProcRoot, pid)
		if !ok {
			break
		}
		if s, ok := procfs.ServerOf(p); ok && (p.UID == src.UID) {
			server, found = s, true
			break
		}
		if p.PPID <= 0 || p.PPID == pid {
			break
		}
		pid = p.PPID
	}
	if !found {
		return "", ErrNotInSession
	}
	if src.Sockets == nil {
		return "", ErrNameUnknown
	}
	socks, err := src.Sockets()
	if err != nil {
		return "", ErrNameUnknown
	}
	// The socket file the server listens on, whatever it is called now:
	// only a match through the socket table counts, since after a rename
	// another session may have taken the name on the server's command line.
	table := &procfs.Table{Root: src.ProcRoot}
	servers := []procfs.Server{server}
	owners := socketOwners(table, servers)
	dir := filepath.Dir(server.Socket)
	for _, name := range zellij.Sockets(dir) {
		if s, how := findServer(filepath.Join(dir, name), servers, socks, owners); s != nil && how == "socket file" {
			return name, nil
		}
	}
	return "", ErrNameUnknown
}
