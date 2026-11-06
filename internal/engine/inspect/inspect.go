// Package inspect gathers the facts the policy decides on: for every session
// zellij lists, its files, its server, its panes and what runs in them, whether
// a client is attached, and when it was last used.
package inspect

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// KnownShells are the programs that count as a prompt waiting for input.
var KnownShells = []string{
	"sh", "bash", "zsh", "fish", "dash", "ash", "ksh", "mksh", "oksh", "pdksh", "yash",
	"csh", "tcsh", "nu", "elvish", "xonsh", "pwsh", "ion", "osh", "oil", "hush", "rc", "es", "murex",
	// Windows's own.
	"powershell", "cmd",
}

// ClientLister asks a running session for its attached clients.
type ClientLister interface {
	ListClients(ctx context.Context, name string) ([]zellij.ClientRow, error)
}

// Sources are where the facts come from. Tests point them at a fake /proc
// tree, a fake socket table and a fake zellij.
type Sources struct {
	// ProcRoot is the /proc directory.
	ProcRoot string
	// InfoDir is zellij's session_info directory.
	InfoDir string
	// SocketDirs are extra places to look for session sockets, on top of the
	// directories running servers name on their command lines.
	SocketDirs []string
	// UID is the user whose sessions are inspected.
	UID int
	// Clients asks sessions which clients are attached.
	Clients ClientLister
	// Sockets lists the kernel's Unix sockets (procfs.UnixSockets).
	Sockets func() ([]procfs.UnixSocket, error)
	// Now is the clock.
	Now func() time.Time
}

// Rules are the settings that shape the facts.
type Rules struct {
	// Protected reports whether a name is protected, and by what.
	Protected func(name string) (bool, string)
	// ProtectCommands are programs that keep a running session.
	ProtectCommands []string
	// Shells are extra prompt programs (config.Shells).
	Shells []string
}

// Pane is one pane process of a running session.
type Pane struct {
	// PID is the pane's process, a child of the server.
	PID int
	// Command is what the pane runs ("bash", "htop", "claude --resume").
	Command string
	// Prompt is true when the pane is a shell waiting at its prompt.
	Prompt bool
	// Busy is what keeps the pane busy, empty when it is at a prompt.
	Busy string
	// TTY is the terminal the pane reads, and LastIO its last read or write
	// (the later of its atime and mtime).
	TTY    string
	LastIO time.Time
	// Cwd is the directory the pane works in, "" when it cannot be read.
	Cwd string
}

// Session is everything known about one listed session.
type Session struct {
	// Listed is zellij's own line for it.
	Listed zellij.Listed
	// Files are its directory under session_info.
	Files zellij.Files
	// Socket is the socket file of a running session ("" if not found).
	Socket string
	// Server is its server process (nil if none was found).
	Server *procfs.Server
	// ServerMatch says how the server was found: "socket file" (sock_diag)
	// or "command line".
	ServerMatch string
	// Panes are the pane processes of a running session.
	Panes []Pane
	// Clients is the number of attached clients, or -1 when unknown.
	Clients int
	// ServerSilent is set when the server did not answer list-clients: what
	// is known of its clients came from elsewhere, if from anywhere.
	ServerSilent bool
	// Facts is what the policy reads.
	Facts policy.Facts
}

// Name is the session's name.
func (s Session) Name() string { return s.Listed.Name }

// StartedAs is the name the session's server was started under, which its command
// line keeps through every rename; "" when its server was not found.
func (s Session) StartedAs() string {
	if s.Server == nil || s.Server.Socket == "" {
		return ""
	}
	return filepath.Base(s.Server.Socket)
}

// Titles returns the session's terminal pane titles.
func (s Session) Titles() []string { return s.Files.Metadata.TerminalTitles() }

// WorkDir is the directory the session works in: where its first pane is now,
// which follows a cd, else where the session was started.
func (s Session) WorkDir() string {
	for _, p := range s.Panes {
		if p.Cwd != "" {
			return p.Cwd
		}
	}
	return s.Files.Layout.Cwd
}

// Gather inspects every listed session with one snapshot of /proc and of the
// kernel's socket table, so every session is judged against the same moment.
func Gather(ctx context.Context, src Sources, rules Rules, listed []zellij.Listed) ([]Session, error) {
	table, err := ReadProcesses(src)
	if err != nil {
		return nil, err
	}
	return GatherFrom(ctx, src, rules, listed, table)
}

// ReadProcesses takes the snapshot of /proc a survey judges by.
func ReadProcesses(src Sources) (*procfs.Table, error) {
	table, err := procfs.ReadSessions(src.ProcRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", src.ProcRoot, err)
	}
	return table, nil
}

// GatherFrom is Gather with the snapshot of /proc already taken. The kernel's
// socket table is still read here, after zellij listed the sessions: listing
// connects to every session's socket, and a table read meanwhile would count
// those connections as clients.
func GatherFrom(ctx context.Context, src Sources, rules Rules, listed []zellij.Listed, table *procfs.Table) ([]Session, error) {
	servers := table.Servers(src.UID)
	// sock_diag can fail on kernels without unix_diag: fall back to matching
	// servers by command line, which works for sessions never renamed.
	var socks []procfs.UnixSocket
	if src.Sockets != nil {
		socks, _ = src.Sockets()
	}
	v := snapshot{
		src:     src,
		rules:   rules,
		table:   table,
		servers: servers,
		// The socket directory is whatever running servers use; the configured
		// candidates cover sessions whose server is gone.
		dirs:   socketDirs(servers, src.SocketDirs),
		socks:  socks,
		owners: socketOwners(table, servers),
		shells: shellSet(rules.Shells),
	}
	out := make([]Session, len(listed))
	for i, l := range listed {
		out[i] = v.session(l)
	}
	attachChecks(ctx, src, out, socks, v.owners)
	for i := range out {
		out[i].Facts.Clients = out[i].Clients
	}
	return out, nil
}

// snapshot is what a survey read once, for all its sessions to be judged by.
type snapshot struct {
	src     Sources
	rules   Rules
	table   *procfs.Table
	servers []procfs.Server
	dirs    []string
	socks   []procfs.UnixSocket
	owners  map[uint32]int
	shells  map[string]bool
}

// session gathers the facts of one listed session.
func (v snapshot) session(l zellij.Listed) Session {
	s := Session{Listed: l, Clients: -1}
	s.Facts.Name, s.Facts.Exited = l.Name, l.Exited
	if v.rules.Protected != nil {
		s.Facts.Protected, s.Facts.ProtectedBy = v.rules.Protected(l.Name)
	}
	files, err := zellij.ReadFiles(v.src.InfoDir, l.Name)
	if err != nil {
		// An unreadable directory is treated like a missing one: kept.
		files = zellij.Files{Dir: filepath.Join(v.src.InfoDir, l.Name)}
	}
	s.Files = files
	s.Facts.HasInfo = files.Exists
	if l.Exited {
		v.exited(&s)
		return s
	}
	v.running(&s)
	return s
}

// exited fills in a session whose server is gone: its files stopped changing
// when the server died, which is a fair "last used" time.
func (v snapshot) exited(s *Session) {
	s.Facts.LastActive, s.Facts.ActivitySource = s.Files.Newest, "session files"
	// A socket file can outlive a server that was killed; only a server still
	// listening on it means the session is not gone.
	if sock := findSocket(v.dirs, s.Name()); sock != "" {
		if srv, _ := findServer(sock, v.servers, v.socks, v.owners); srv != nil {
			s.Socket, s.Server = sock, srv
			s.Facts.ServerPID = srv.PID
		}
	}
}

// running fills in a session with a server: its socket, its panes and what
// runs in them, and when they were last used.
func (v snapshot) running(s *Session) {
	s.Socket = findSocket(v.dirs, s.Name())
	s.Facts.SocketOK = s.Socket != ""
	if s.Socket != "" {
		s.Server, s.ServerMatch = findServer(s.Socket, v.servers, v.socks, v.owners)
	}
	if s.Server != nil {
		s.Facts.ServerPID = s.Server.PID
		s.Panes = panes(v.table, s.Server.PID, v.shells)
	}
	s.Facts.PaneCount = len(s.Panes)
	for _, p := range s.Panes {
		if p.Busy != "" {
			s.Facts.BusyWith = p.Busy
			break
		}
	}
	// Last activity: the newest read or write on any pane's terminal. The
	// session files are rewritten every minute as a heartbeat, so they are
	// only a fallback when no terminal can be read.
	for _, p := range s.Panes {
		if p.LastIO.After(s.Facts.LastActive) {
			s.Facts.LastActive, s.Facts.ActivitySource = p.LastIO, "pane terminals"
		}
	}
	if s.Facts.LastActive.IsZero() {
		s.Facts.LastActive, s.Facts.ActivitySource = s.Files.Newest, "session files"
	}
	s.Facts.ProtectedCommand = protectedCommand(v.table, s.Server, s.Files, v.rules.ProtectCommands)
}
