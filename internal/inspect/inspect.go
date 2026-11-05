// Package inspect gathers the facts the policy decides on: for every session
// zellij lists, its files, its server, its panes and what runs in them, whether
// a client is attached, and when it was last used.
package inspect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// KnownShells are the programs that count as a prompt waiting for input.
var KnownShells = []string{
	"sh", "bash", "zsh", "fish", "dash", "ash", "ksh", "mksh", "oksh", "pdksh", "yash",
	"csh", "tcsh", "nu", "elvish", "xonsh", "pwsh", "ion", "osh", "oil", "hush", "rc", "es", "murex",
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
	// Facts is what the policy reads.
	Facts policy.Facts
}

// Name is the session's name.
func (s Session) Name() string { return s.Listed.Name }

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
	table, err := procfs.Read(src.ProcRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", src.ProcRoot, err)
	}
	servers := table.Servers(src.UID)
	// The socket directory is whatever running servers use; the configured
	// candidates cover sessions whose server is gone.
	dirs := socketDirs(servers, src.SocketDirs)
	// sock_diag can fail on kernels without unix_diag: fall back to matching
	// servers by command line, which works for sessions never renamed.
	var socks []procfs.UnixSocket
	if src.Sockets != nil {
		socks, _ = src.Sockets()
	}
	owners := socketOwners(table, servers)
	shells := shellSet(rules.Shells)

	out := make([]Session, len(listed))
	for i, l := range listed {
		s := Session{Listed: l, Clients: -1}
		s.Facts.Name, s.Facts.Exited = l.Name, l.Exited
		if rules.Protected != nil {
			s.Facts.Protected, s.Facts.ProtectedBy = rules.Protected(l.Name)
		}
		files, err := zellij.ReadFiles(src.InfoDir, l.Name)
		if err != nil {
			// An unreadable directory is treated like a missing one: kept.
			files = zellij.Files{Dir: filepath.Join(src.InfoDir, l.Name)}
		}
		s.Files = files
		s.Facts.HasInfo = files.Exists
		if l.Exited {
			// An exited session has no processes; its files stopped changing
			// when its server died, which is a fair "last used" time.
			s.Facts.LastActive, s.Facts.ActivitySource = files.Newest, "session files"
			// A socket file can outlive a server that was killed; only a
			// server still listening on it means the session is not gone.
			if sock := findSocket(dirs, l.Name); sock != "" {
				if srv, _ := findServer(sock, servers, socks, owners); srv != nil {
					s.Socket, s.Server = sock, srv
					s.Facts.ServerPID = srv.PID
				}
			}
			out[i] = s
			continue
		}
		s.Socket = findSocket(dirs, l.Name)
		s.Facts.SocketOK = s.Socket != ""
		if s.Socket != "" {
			s.Server, s.ServerMatch = findServer(s.Socket, servers, socks, owners)
		}
		if s.Server != nil {
			s.Facts.ServerPID = s.Server.PID
			s.Panes = panes(table, s.Server.PID, shells)
		}
		s.Facts.PaneCount = len(s.Panes)
		for _, p := range s.Panes {
			if p.Busy != "" {
				s.Facts.BusyWith = p.Busy
				break
			}
		}
		// Last activity: the newest read or write on any pane's terminal. The
		// session files are rewritten every minute as a heartbeat, so they
		// are only a fallback when no terminal can be read.
		for _, p := range s.Panes {
			if p.LastIO.After(s.Facts.LastActive) {
				s.Facts.LastActive, s.Facts.ActivitySource = p.LastIO, "pane terminals"
			}
		}
		if s.Facts.LastActive.IsZero() {
			s.Facts.LastActive, s.Facts.ActivitySource = files.Newest, "session files"
		}
		s.Facts.ProtectedCommand = protectedCommand(table, s.Server, files, rules.ProtectCommands)
		out[i] = s
	}
	attachChecks(ctx, src, out, socks, owners)
	for i := range out {
		out[i].Facts.Clients = out[i].Clients
	}
	return out, nil
}

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

// shellSet is KnownShells plus the configured extras.
func shellSet(extra []string) map[string]bool {
	set := map[string]bool{}
	for _, s := range KnownShells {
		set[s] = true
	}
	for _, s := range extra {
		set[s] = true
	}
	return set
}

// Panes describes the pane processes of a server, judged by the same rule a
// pass uses; shells are extra prompt programs on top of KnownShells.
func Panes(t *procfs.Table, server int, shells []string) []Pane {
	return panes(t, server, shellSet(shells))
}

// panes describes the pane processes of a server.
func panes(t *procfs.Table, server int, shells map[string]bool) []Pane {
	var out []Pane
	for _, p := range t.Children(server) {
		// A pane whose process has exited and waits to be collected has
		// nothing running and no terminal to read.
		if p.IsZombie() {
			continue
		}
		// Not every child of a server is a pane. zellij runs `ps -ao ppid,args`
		// to save its layouts, once a minute in every session, and plugins
		// run commands of their own. Those read no terminal; caught in the
		// few milliseconds they live, they would make an idle session look
		// busy for that pass.
		if !readsTerminal(p) {
			continue
		}
		pane := Pane{PID: p.PID, Command: short(p.CommandLine()), TTY: p.Stdin, Cwd: t.Cwd(p.PID)}
		pane.Prompt = isPrompt(p, shells)
		if !pane.Prompt {
			// A pane that runs a program directly (a command pane, or an
			// editor started from a layout) is busy as long as it runs.
			pane.Busy = short(p.CommandLine())
		} else if kids := t.Children(p.PID); len(kids) > 0 {
			// A shell with a child is running a command in the foreground or
			// the background.
			pane.Busy = short(kids[0].CommandLine())
		}
		if tty := p.Stdin; tty != "" && strings.HasPrefix(tty, "/") && tty != "/dev/null" {
			pane.LastIO = lastIO(tty)
		}
		out = append(out, pane)
	}
	return out
}

// readsTerminal reports whether a server's child could be a pane: every pane
// process gets a terminal as its input. A stdin that cannot be read counts as
// one, because a process of another user is no reason to call a session idle.
func readsTerminal(p *procfs.Proc) bool {
	return p.Stdin != "/dev/null" && !strings.HasPrefix(p.Stdin, "pipe:") && !strings.HasPrefix(p.Stdin, "socket:")
}

// lastIO is the later of a terminal's last write (mtime) and last read
// (atime): output and input both mean someone or something used the pane.
func lastIO(tty string) time.Time {
	info, err := os.Stat(tty)
	if err != nil {
		return time.Time{}
	}
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if a := time.Unix(st.Atim.Sec, st.Atim.Nsec); a.After(t) {
			t = a
		}
	}
	return t
}

// isPrompt reports whether a process is an interactive shell: both its kernel
// name and its program are shells, and it was not given a script to run. A
// script started from its shebang has the script's name as its kernel name,
// so it is not mistaken for a prompt.
func isPrompt(p *procfs.Proc, shells map[string]bool) bool {
	if !shells[p.Comm] || !shells[p.Name()] {
		return false
	}
	for _, a := range p.Argv[min(1, len(p.Argv)):] {
		if !strings.HasPrefix(a, "-") {
			return false
		}
	}
	return true
}

// short keeps a command line to one readable line.
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:59]) + "…"
	}
	return s
}

// protectedCommand finds a protected program in a running session: in its
// process tree, or among the commands its layout starts.
func protectedCommand(t *procfs.Table, server *procfs.Server, f zellij.Files, protect []string) string {
	if len(protect) == 0 {
		return ""
	}
	if server != nil {
		if want := ProtectedProgram(t, server.PID, protect); want != "" {
			return want
		}
	}
	for _, want := range protect {
		for _, c := range f.Layout.Commands {
			if filepath.Base(c) == want || wordMatch(c, want) {
				return want
			}
		}
		for _, args := range f.Layout.Args {
			for _, a := range args {
				if wordMatch(a, want) {
					return want
				}
			}
		}
	}
	return ""
}

// ProtectedProgram finds a protected program running under a server, by the
// names a pass recognizes it by; "" when none runs.
func ProtectedProgram(t *procfs.Table, server int, protect []string) string {
	for _, p := range t.Descendants(server, 512) {
		for _, want := range protect {
			for _, n := range p.Names() {
				if n == want {
					return want
				}
			}
			// An interpreter running a script from a package named after
			// the program ("node .../claude-code/cli.js").
			if len(p.Argv) > 1 && !strings.HasPrefix(p.Argv[1], "-") && wordMatch(p.Argv[1], want) {
				return want
			}
		}
	}
	return ""
}

// wordMatch reports whether word appears in s as a whole word: not preceded or
// followed by a letter, a digit or "_" ("claude-code" contains "claude",
// "claudette" does not).
func wordMatch(s, word string) bool {
	isWord := func(b byte) bool {
		return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !isWord(s[start-1])) && (end == len(s) || !isWord(s[end])) {
			return true
		}
		i = start + 1
	}
}
