package inspect

import (
	"os"
	"path/filepath"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
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
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSocket != 0 {
			return p
		}
	}
	return ""
}

// socketOwners maps each socket inode held by a server to that server's PID.
func socketOwners(t *procfs.Table, servers []procfs.Server) map[uint32]int {
	owners := map[uint32]int{}
	for _, s := range servers {
		for ino := range t.SocketInodes(s.PID) {
			owners[ino] = s.PID
		}
	}
	return owners
}

// findServer finds the server listening on socket. The socket file's inode
// leads to the listening socket (sock_diag) and from there to the process
// holding it, which stays right after a rename. The command line is the
// fallback; it still names the session's first name after a rename.
func findServer(socket string, servers []procfs.Server, socks []procfs.UnixSocket, owners map[uint32]int) (*procfs.Server, string) {
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
	// By command line: only when exactly one server names this socket. After
	// a rename and a new session under the old name, two servers name it, and
	// picking either could judge one session by the other's panes.
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
