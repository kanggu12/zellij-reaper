package procfs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UnixSocket is one Unix socket as the kernel's sock_diag interface reports it.
type UnixSocket struct {
	// Inode is the socket's own inode, the N in a process's "socket:[N]" fd.
	Inode uint32
	// State is the TCP-style state: 10 is listening, 1 is connected.
	State uint8
	// Name is the path the socket was bound to. It does not change when the
	// socket file is renamed afterwards, which is what zellij does when a
	// session is renamed.
	Name string
	// VFSIno and VFSDev identify the socket file on disk, and do follow a
	// rename; they are zero for sockets without a file.
	VFSIno uint32
	VFSDev uint32
	// Peer is the inode of the other end of a connected socket.
	Peer uint32
}

// Listening reports whether the socket accepts connections.
func (s UnixSocket) Listening() bool { return s.State == 10 }

// ErrNoSockDiag means the kernel offers no unix sock_diag (the unix_diag
// module is missing), so sockets cannot be matched to their files.
var ErrNoSockDiag = errors.New("unix sock_diag is not available")

// SocketInodes returns the socket inodes a process holds open, read from its
// fd directory ("socket:[N]" links). Unreadable processes give none.
func (t *Table) SocketInodes(pid int) map[uint32]bool {
	// A native reader that has no /proc fills in the sockets itself.
	if t.sockets != nil {
		return t.sockets[pid]
	}
	out := map[uint32]bool{}
	dir := filepath.Join(t.Root, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		link, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 32)
		if err == nil {
			out[uint32(n)] = true
		}
	}
	return out
}
