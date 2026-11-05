package inspect

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// attachChecks decides, for every running session, whether a client is
// attached. The kernel's socket table answers first: it is read once for the
// whole pass, it starts no process, and it does not depend on what zellij
// prints. `list-clients` asks only when the table cannot tell.
//
// Sessions are asked one at a time on purpose. zellij 0.44.2 answers
// list-clients calls that overlap with nothing at all after a second, even
// when they go to different servers: four at a time took a pass over five
// sessions from 0.17 s to 1.1 s, and left about one call in eight without an
// answer. Later releases do not do this, but a pass that rarely asks has no
// need to find out which release it runs on.
func attachChecks(ctx context.Context, src Sources, sessions []Session, socks []procfs.UnixSocket, owners map[uint32]int) {
	open := openSockets(socks)
	for i := range sessions {
		s := &sessions[i]
		// An exited session has no server, and a session whose server was
		// not found has nobody to ask: the policy keeps it for that reason.
		if s.Listed.Exited || s.Server == nil {
			continue
		}
		pid := s.Server.PID
		c := connectionsTo(func(ino uint32) bool { return owners[ino] == pid }, socks, open)
		attachCheck(ctx, src, s, c)
	}
}

// attachCheck settles one session, from the table's answer down to the last
// source that can say anything. Whatever nothing can tell stays Unknown, and
// the policy keeps a session in that state.
func attachCheck(ctx context.Context, src Sources, s *Session, c connections) {
	md := s.Files.Metadata
	switch {
	// Somebody holds the other end of a connection the server accepted. A
	// browser client reaches the server the same way, so the file's count
	// of those can only say there are more, never add to these.
	case c.clients > 0:
		s.Clients = max(c.clients, md.WebClients)
		s.Facts.Attached = policy.Yes
		s.Facts.AttachDetail = fmt.Sprintf("%d client(s) attached", c.clients)
		return
	// The table shows the server listening and no connection at all.
	case c.seen && c.unaccounted == 0:
		noClientOnTheSocket(s, md)
		return
	}
	// The table cannot tell: no sock_diag on this kernel, another network
	// namespace, or a connection whose other end nobody can see.
	rows, err := askServer(ctx, src, s.Name())
	if err == nil {
		s.Clients = len(rows) + md.WebClients
		if len(rows) > 0 {
			s.Facts.Attached = policy.Yes
			s.Facts.AttachDetail = fmt.Sprintf("%d client(s) attached", len(rows))
			return
		}
		noClientOnTheSocket(s, md)
		return
	}
	// The server did not answer either: zellij's own count in the metadata
	// file. It lags, so it can only add a client, never rule one out.
	if md.HasClientCount && md.ConnectedClients > 0 {
		s.Clients = md.ConnectedClients
		s.Facts.Attached = policy.Yes
		s.Facts.AttachDetail = fmt.Sprintf("zellij reports %d connected client(s)", md.ConnectedClients)
		return
	}
	// Nothing could tell.
	s.Facts.Attached = policy.Unknown
	s.Facts.AttachDetail = "list-clients failed: " + err.Error()
}

// noClientOnTheSocket records a session without a terminal client, which a
// browser client still counts against: the file is the only place that names
// those separately.
func noClientOnTheSocket(s *Session, md zellij.Metadata) {
	s.Clients = md.WebClients
	if md.WebClients > 0 {
		s.Facts.Attached = policy.Yes
		s.Facts.AttachDetail = fmt.Sprintf("%d web client(s) attached", md.WebClients)
		return
	}
	s.Facts.Attached, s.Facts.AttachDetail = policy.No, "no client attached"
}

// askServer runs list-clients for a session, or says why it cannot.
func askServer(ctx context.Context, src Sources, name string) ([]zellij.ClientRow, error) {
	if src.Clients == nil {
		return nil, errors.New("no client lister")
	}
	return src.Clients.ListClients(ctx, name)
}

// openSockets is the set of sockets the table lists, which is what makes the
// other end of a connection visible.
func openSockets(socks []procfs.UnixSocket) map[uint32]bool {
	open := make(map[uint32]bool, len(socks))
	for _, u := range socks {
		open[u.Inode] = true
	}
	return open
}

// connections is what the socket table says about one server's clients.
type connections struct {
	// seen is true when the table shows the server listening. Without that
	// (a table from another network namespace, or one missing entries) the
	// other fields prove nothing.
	seen bool
	// clients counts the connections the server accepted whose other end
	// is open: one per attached client.
	clients int
	// unaccounted counts accepted connections with no visible other end.
	unaccounted int
	// peers are the inodes of every other end, for naming who holds them.
	peers map[uint32]bool
}

// connectionsTo reads a server's clients from the socket table; held says
// which sockets the server holds. A connection the server accepted carries the
// listening socket's name and file, and keeps them after the socket file is
// removed or renamed. The socket pairs a zellij server keeps for itself carry
// neither, so they are not mistaken for clients.
func connectionsTo(held func(uint32) bool, socks []procfs.UnixSocket, open map[uint32]bool) connections {
	var listeners []procfs.UnixSocket
	for _, u := range socks {
		if held(u.Inode) && u.Listening() {
			listeners = append(listeners, u)
		}
	}
	c := connections{seen: len(listeners) > 0, peers: map[uint32]bool{}}
	if !c.seen {
		return c
	}
	// accepted reports whether a socket came from one of the listeners: the
	// same name, or the same file.
	accepted := func(u procfs.UnixSocket) bool {
		for _, l := range listeners {
			if (u.Name != "" && u.Name == l.Name) || (u.VFSIno != 0 && u.VFSIno == l.VFSIno && u.VFSDev == l.VFSDev) {
				return true
			}
		}
		return false
	}
	for _, u := range socks {
		if !held(u.Inode) || u.Listening() || !accepted(u) {
			continue
		}
		if u.Peer != 0 {
			c.peers[u.Peer] = true
		}
		// The other end is listed when it is still open in this namespace.
		if u.Peer != 0 && open[u.Peer] {
			c.clients++
			continue
		}
		// A connection without a visible other end still is one: it ends as
		// one nobody can account for, and the caller asks elsewhere.
		c.unaccounted++
	}
	return c
}

// ListenerClients finds, through the kernel's socket table, the clients
// connected to a server's listening socket. Yes names a connected process;
// Unknown means the table does not show the server listening, or a
// connection's other end belongs to nobody visible.
func ListenerClients(t *procfs.Table, server int, socks []procfs.UnixSocket) (policy.Tri, string) {
	held := t.SocketInodes(server)
	c := connectionsTo(func(ino uint32) bool { return held[ino] }, socks, openSockets(socks))
	if !c.seen {
		return policy.Unknown, "the socket table does not show the server listening"
	}
	if c.clients == 0 && c.unaccounted == 0 {
		return policy.No, "no client connected to its socket"
	}
	// Who holds the other ends? `recover` says so, since it may stop this
	// server: look through the user's processes in a stable order.
	var pids []int
	for pid := range t.ByPID {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	for _, pid := range pids {
		if pid == server {
			continue
		}
		for ino := range t.SocketInodes(pid) {
			if c.peers[ino] {
				return policy.Yes, fmt.Sprintf("pid %d is connected to its socket", pid)
			}
		}
	}
	return policy.Unknown, fmt.Sprintf("%d connection(s) to its socket with no visible owner", c.clients+c.unaccounted)
}
