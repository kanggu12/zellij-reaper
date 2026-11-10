package inspect

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// socketDirs collects the directories servers keep their sockets in, plus the
// configured candidates, without repeats, in a stable order.
func socketDirs(servers []procfs.Server, extra []string) []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, s := range servers {
		add(filepath.Dir(s.Socket))
	}
	for _, d := range extra {
		add(d)
	}
	return dirs
}

// findSocket returns the socket file for name in the first directory that has
// one.
func findSocket(dirs []string, name string) string {
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if info, err := os.Lstat(p); err == nil && zellij.IsSocket(info.Mode()) {
			return p
		}
	}
	return ""
}

// socketOwners maps each socket inode held by a server to that server's PID.
// A server's sockets are read from its fd directory, one readlink per fd:
// with thirty sessions that was most of a survey's own time, one server after
// the other. The servers are read side by side now, by up to eight workers,
// and merged in their order, so an inode two of them hold goes to the later
// one, as before.
func socketOwners(t *procfs.Table, servers []procfs.Server) map[uint32]int {
	held := make([]map[uint32]bool, len(servers))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(len(servers), runtime.GOMAXPROCS(0), 8) {
		wg.Go(func() {
			for i := range next {
				held[i] = t.SocketInodes(servers[i].PID)
			}
		})
	}
	for i := range servers {
		next <- i
	}
	close(next)
	wg.Wait()
	owners := map[uint32]int{}
	for i, s := range servers {
		for ino := range held[i] {
			owners[ino] = s.PID
		}
	}
	return owners
}

// cmdlineTrusted reports whether every server's command line still names its
// own session's socket file: true while no session has been renamed. A rename
// moves the socket file and leaves the command line, so after one a server's
// command line can name a file another session moved into its place, and the
// command-line match below would judge one session by the other's panes. A
// command line naming no socket file, or a socket file no command line names,
// says a rename happened (or a server left its file behind), and then no
// match by command line is made: the sessions it would have found are kept.
func cmdlineTrusted(servers []procfs.Server, dirs []string) bool {
	named := map[string]bool{}
	for _, s := range servers {
		if _, err := os.Lstat(s.Socket); err != nil {
			return false
		}
		named[s.Socket] = true
	}
	for _, d := range dirs {
		for _, f := range zellij.Sockets(d) {
			// zellij's own sockets are no session's.
			if !zellij.IsAppName(f) && !named[filepath.Join(d, f)] {
				return false
			}
		}
	}
	return true
}

// findServer finds the server listening on socket. The socket file's inode
// leads to the listening socket (sock_diag) and from there to the process
// holding it, which stays right after a rename. The command line is the
// fallback, used only where byCmdline says no session was renamed: it still
// names the session's first name after a rename.
func findServer(socket string, servers []procfs.Server, socks []procfs.UnixSocket, owners map[uint32]int, byCmdline bool) (*procfs.Server, string) {
	if ino, dev, ok := procfs.FileID(socket); ok {
		for _, u := range socks {
			if !u.Listening() || u.VFSIno != ino || (u.VFSDev != 0 && u.VFSDev != dev) {
				continue
			}
			if pid, ok := owners[u.Inode]; ok {
				for i := range servers {
					if servers[i].PID == pid {
						return &servers[i], "socket file"
					}
				}
			}
		}
	}
	// From the socket file itself, where the system allows it: the process ID
	// zellij writes into it on Windows, its creation time on macOS.
	if s, ok := procfs.FileOwner(socket, servers); ok {
		for i := range servers {
			if servers[i].PID == s.PID {
				return &servers[i], "socket file"
			}
		}
	}
	// By command line: only when no session was renamed, and exactly one
	// server names this socket. After a rename and a new session under the
	// old name, two servers name it, and picking either could judge one
	// session by the other's panes.
	if !byCmdline {
		return nil, ""
	}
	var match *procfs.Server
	for i := range servers {
		if servers[i].Socket == socket {
			if match != nil {
				return nil, ""
			}
			match = &servers[i]
		}
	}
	if match != nil {
		return match, "command line"
	}
	return nil, ""
}

// startName is the name a session kept by id started under, "" when it cannot
// be read. zellij sets ZELLIJ_SESSION_NAME before it starts a server, and the
// environment a process began with stays as it was: a rename changes the
// variable only for the panes opened after it. A pane's own environment would
// not do. The pane opened first has the first name, but once it is gone the
// oldest pane left has a later one, and a session someone renamed would read
// as never renamed: its name, if zellij could have made it up, would go.
func startName(t *procfs.Table, server int) string {
	name, _ := procfs.EnvVar(t.Root, server, "ZELLIJ_SESSION_NAME")
	return name
}
