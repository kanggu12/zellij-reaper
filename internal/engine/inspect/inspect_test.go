package inspect_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/core/rename"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

var now = time.Now()

// clients is a fake `list-clients`: rows per session, or an error.
type clients map[string]any

func (c clients) ListClients(_ context.Context, name string) ([]zellij.ClientRow, error) {
	switch v := c[name].(type) {
	case int:
		rows := make([]zellij.ClientRow, v)
		return rows, nil
	case error:
		return nil, v
	}
	return nil, nil
}

// machine is one fake machine: /proc, a socket directory, session_info.
type machine struct {
	proc    *testutil.FakeProc
	sockDir string
	info    string
	ttys    string
	socks   []procfs.UnixSocket
	clients clients
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	return &machine{
		proc:    testutil.NewFakeProc(t),
		sockDir: testutil.SocketDir(t),
		info:    filepath.Join(t.TempDir(), "session_info"),
		ttys:    t.TempDir(),
		clients: clients{},
	}
}

// server adds a running session: its socket, its server process (pid) and
// its session files. argvName is the name on the server's command line,
// which differs from name after a rename.
func (m *machine) server(t *testing.T, name, argvName string, pid int, metadata, layout string) string {
	t.Helper()
	sock := testutil.Socket(t, m.sockDir, name)
	m.proc.Add(testutil.Proc{PID: pid, PPID: 1, Comm: "zellij", Argv: []string{"/usr/bin/zellij", "--server", filepath.Join(m.sockDir, argvName)}, Sockets: []uint32{uint32(pid * 10)}})
	// The kernel's view: a listening socket whose file is sock.
	ino, dev, _ := procfs.FileID(sock)
	m.socks = append(m.socks, procfs.UnixSocket{Inode: uint32(pid * 10), State: 10, Name: filepath.Join(m.sockDir, argvName), VFSIno: ino, VFSDev: dev})
	testutil.SessionFiles(t, m.info, name, metadata, layout, now.Add(-time.Minute))
	return sock
}

// pane adds a pane process under a server, reading a terminal last used at.
func (m *machine) pane(t *testing.T, pid, server int, comm string, argv []string, at time.Time) {
	t.Helper()
	tty := testutil.TTY(t, m.ttys, filepath.Base(t.Name())+"-"+comm+"-"+time.Duration(pid).String(), at)
	m.proc.Add(testutil.Proc{PID: pid, PPID: server, Comm: comm, Argv: argv, Stdin: tty})
}

func (m *machine) gather(t *testing.T, withSocks bool, listed ...zellij.Listed) []inspect.Session {
	t.Helper()
	src := inspect.Sources{ProcRoot: m.proc.Root, InfoDir: m.info, SocketDirs: []string{m.sockDir}, UID: -1, Clients: m.clients}
	src.UID = osUID()
	if withSocks {
		src.Sockets = func() ([]procfs.UnixSocket, error) { return m.socks, nil }
	}
	rules := inspect.Rules{ProtectCommands: []string{"claude", "codex"}, Protected: func(n string) (bool, string) { return n == "keep", "protected list" }}
	out, err := inspect.Gather(context.Background(), src, rules, listed)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func running(name string) zellij.Listed { return zellij.Listed{Name: name} }

func TestIdleAndBusyPanes(t *testing.T) {
	m := newMachine(t)
	m.server(t, "idle", "idle", 100, testutil.Metadata(0, "Pane #1"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now.Add(-3*time.Hour))
	m.pane(t, 102, 100, "zsh", []string{"/usr/bin/zsh", "-l"}, now.Add(-2*time.Hour))

	m.server(t, "shellcmd", "shellcmd", 200, testutil.Metadata(0, "Pane #1"), "")
	m.pane(t, 201, 200, "bash", []string{"/bin/bash"}, now)
	m.proc.Add(testutil.Proc{PID: 202, PPID: 201, Comm: "sleep", Argv: []string{"sleep", "99"}})

	m.server(t, "cmdpane", "cmdpane", 300, testutil.Metadata(0, "htop"), "")
	m.pane(t, 301, 300, "htop", []string{"htop"}, now)

	m.server(t, "script", "script", 400, testutil.Metadata(0, "build"), "")
	m.pane(t, 401, 400, "build.sh", []string{"/bin/bash", "./build.sh"}, now)

	m.server(t, "zombie", "zombie", 500, testutil.Metadata(0, "x"), "")
	m.proc.Add(testutil.Proc{PID: 501, PPID: 500, Comm: "bash", State: "Z"})

	got := m.gather(t, true, running("idle"), running("shellcmd"), running("cmdpane"), running("script"), running("zombie"))
	want := map[string]string{"idle": "", "shellcmd": "sleep 99", "cmdpane": "htop", "script": "/bin/bash ./build.sh"}
	for _, s := range got {
		f := s.Facts
		if s.Name() == "zombie" {
			if f.PaneCount != 0 {
				t.Errorf("zombie pane counted: %+v", f)
			}
			continue
		}
		if f.BusyWith != want[s.Name()] || f.ServerPID == 0 || !f.SocketOK || f.PaneCount == 0 {
			t.Errorf("%s: %+v", s.Name(), f)
		}
	}
	// Last activity is the newest pane terminal, not the session files.
	idle := got[0].Facts
	if idle.ActivitySource != "pane terminals" || now.Sub(idle.LastActive) > 2*time.Hour+time.Second || now.Sub(idle.LastActive) < 2*time.Hour-time.Second {
		t.Errorf("idle activity: %v from %s", now.Sub(idle.LastActive), idle.ActivitySource)
	}
}

func TestAServersHelpersAreNotPanes(t *testing.T) {
	m := newMachine(t)
	m.server(t, "s", "s", 100, testutil.Metadata(0, "x"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now.Add(-3*time.Hour))
	// zellij saving its layout, and a plugin's command with its input piped.
	// Neither has a controlling terminal: the server has none to pass on.
	m.proc.Add(testutil.Proc{PID: 102, PPID: 100, Comm: "ps", Argv: []string{"ps", "-ao", "ppid,args"}, Stdin: "/dev/null"})
	m.proc.Add(testutil.Proc{PID: 103, PPID: 100, Comm: "wl-copy", Argv: []string{"wl-copy"}, Stdin: "pipe:[77]"})
	got := m.gather(t, false, running("s"))
	if len(got[0].Panes) != 1 || got[0].Facts.BusyWith != "" {
		t.Errorf("helpers taken for panes: %+v, busy with %q", got[0].Panes, got[0].Facts.BusyWith)
	}
	// A child whose input cannot be read still counts: it might be a pane.
	m.proc.Add(testutil.Proc{PID: 104, PPID: 100, Comm: "htop", Argv: []string{"htop"}})
	if got := m.gather(t, false, running("s")); got[0].Facts.BusyWith != "htop" {
		t.Errorf("unreadable input: busy with %q", got[0].Facts.BusyWith)
	}
}

func TestAJobWithItsInputRedirectedIsStillAPane(t *testing.T) {
	// `nohup sleep 99999` and `exec sleep 1 </dev/null` in a command pane: real
	// jobs, whose input is not the terminal. The terminal stays their controlling
	// terminal, which is what tells them from a helper.
	for name, stdin := range map[string]string{"nohup": "/dev/null", "piped": "pipe:[77]"} {
		m := newMachine(t)
		m.server(t, "s", "s", 100, testutil.Metadata(0, "x"), "")
		m.pane(t, 101, 100, "bash", []string{"-bash"}, now.Add(-3*time.Hour))
		m.proc.Add(testutil.Proc{PID: 102, PPID: 100, Comm: "sleep", Argv: []string{"sleep", "99999"}, Stdin: stdin, CTTY: 34816})
		got := m.gather(t, false, running("s"))
		if len(got[0].Panes) != 2 || got[0].Facts.BusyWith != "sleep 99999" {
			t.Errorf("%s: panes %+v, busy with %q", name, got[0].Panes, got[0].Facts.BusyWith)
		}
	}
}

func TestProtectedCommands(t *testing.T) {
	m := newMachine(t)
	// The agent as its own process, as Claude Code's native binary runs.
	m.server(t, "native", "native", 100, testutil.Metadata(0, "x"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	m.proc.Add(testutil.Proc{PID: 102, PPID: 101, Comm: "claude", Argv: []string{"claude"}})
	// Through node, as the npm package runs.
	m.server(t, "node", "node", 200, testutil.Metadata(0, "x"), "")
	m.pane(t, 201, 200, "node", []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, now)
	// Only in the layout: the pane's process has exited, the layout keeps it.
	m.server(t, "layout", "layout", 300, testutil.Metadata(0, "x"), "layout {\n    pane command=\"codex\" {\n    }\n}\n")
	m.pane(t, 301, 300, "bash", []string{"-bash"}, now)
	// A word that only contains the name is not the program.
	m.server(t, "lookalike", "lookalike", 400, testutil.Metadata(0, "x"), "")
	m.pane(t, 401, 400, "bash", []string{"-bash"}, now)
	m.proc.Add(testutil.Proc{PID: 402, PPID: 401, Comm: "vim", Argv: []string{"vim", "claudette.txt"}})

	got := m.gather(t, true, running("native"), running("node"), running("layout"), running("lookalike"))
	want := []string{"claude", "claude", "codex", ""}
	for i, s := range got {
		if s.Facts.ProtectedCommand != want[i] {
			t.Errorf("%s: protected command %q, want %q", s.Name(), s.Facts.ProtectedCommand, want[i])
		}
	}
}

func TestAttachChecks(t *testing.T) {
	m := newMachine(t)
	m.server(t, "meta", "meta", 100, testutil.Metadata(2, "x"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	m.server(t, "listed", "listed", 200, testutil.Metadata(0, "x"), "")
	m.pane(t, 201, 200, "bash", []string{"-bash"}, now)
	m.server(t, "alone", "alone", 300, testutil.Metadata(0, "x"), "")
	m.pane(t, 301, 300, "bash", []string{"-bash"}, now)
	m.server(t, "silent", "silent", 400, testutil.Metadata(0, "x"), "")
	m.pane(t, 401, 400, "bash", []string{"-bash"}, now)
	m.clients["listed"] = 1
	m.clients["alone"] = 0
	m.clients["silent"] = errors.New("no answer within 10s")
	// The metadata only counts when the server does not answer.
	m.clients["meta"] = errors.New("no answer within 10s")

	got := m.gather(t, false, running("meta"), running("listed"), running("alone"), running("silent"))
	want := []policy.Tri{policy.Yes, policy.Yes, policy.No, policy.Unknown}
	for i, s := range got {
		if s.Facts.Attached != want[i] {
			t.Errorf("%s: attached %v (%s), want %v", s.Name(), s.Facts.Attached, s.Facts.AttachDetail, want[i])
		}
	}
	if got[0].Clients != 2 || got[2].Clients != 0 || got[3].Clients != -1 {
		t.Errorf("client counts: %d %d %d", got[0].Clients, got[2].Clients, got[3].Clients)
	}
}

// attachedVia builds a session whose server holds its listening socket and one
// accepted connection, as the kernel's table shows them.
func attachedVia(t *testing.T, m *machine, peerListed bool) {
	t.Helper()
	sock := testutil.Socket(t, m.sockDir, "s")
	ino, dev, _ := procfs.FileID(sock)
	// The server holds its listening socket (1000) and one accepted
	// connection (4001) whose other end (5001) a client process holds.
	m.proc.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"/usr/bin/zellij", "--server", sock}, Sockets: []uint32{1000, 4001}})
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	m.proc.Add(testutil.Proc{PID: 900, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "attach", "s"}, Sockets: []uint32{5001}})
	m.socks = []procfs.UnixSocket{
		{Inode: 1000, State: 10, Name: sock, VFSIno: ino, VFSDev: dev},
		{Inode: 4001, State: 1, Name: sock, Peer: 5001},
	}
	if peerListed {
		m.socks = append(m.socks, procfs.UnixSocket{Inode: 5001, State: 1, Peer: 4001})
	}
	testutil.SessionFiles(t, m.info, "s", testutil.Metadata(0, "x"), "", now)
}

func TestTheSocketTableAnswersWithoutAsking(t *testing.T) {
	m := newMachine(t)
	attachedVia(t, m, true)
	// list-clients would say something else; it must not be asked.
	m.clients["s"] = errors.New("asked anyway")
	got := m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.Yes || got[0].Clients != 1 {
		t.Errorf("a client in the table: %v, %d (%s)", got[0].Facts.Attached, got[0].Clients, got[0].Facts.AttachDetail)
	}
	if got[0].Facts.AttachDetail != "1 client(s) attached" {
		t.Errorf("the log's words changed: %s", got[0].Facts.AttachDetail)
	}
	// A server with only its listening socket: nobody is attached, and nobody
	// was asked.
	m = newMachine(t)
	m.server(t, "s", "s", 100, testutil.Metadata(0, "x"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	m.clients["s"] = errors.New("asked anyway")
	got = m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.No || got[0].Clients != 0 {
		t.Errorf("no connection: %v, %d (%s)", got[0].Facts.Attached, got[0].Clients, got[0].Facts.AttachDetail)
	}
}

func TestAConnectionNobodyCanSeeIsAskedAbout(t *testing.T) {
	// The other end is not in this table: a client leaving, whose end has closed.
	// list-clients decides, and failing that nothing can tell.
	m := newMachine(t)
	attachedVia(t, m, false)
	m.clients["s"] = 0
	got := m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.No {
		t.Errorf("server says nobody: %v (%s)", got[0].Facts.Attached, got[0].Facts.AttachDetail)
	}
	m.clients["s"] = 1
	got = m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.Yes {
		t.Errorf("server says one: %v (%s)", got[0].Facts.Attached, got[0].Facts.AttachDetail)
	}
	m.clients["s"] = errors.New("no answer within 10s")
	got = m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.Unknown || got[0].Clients != -1 {
		t.Errorf("no answer: %v, %d (%s)", got[0].Facts.Attached, got[0].Clients, got[0].Facts.AttachDetail)
	}
}

func TestAClientFromAnotherNetworkNamespaceIsAskedAbout(t *testing.T) {
	// The kernel lists such a connection only in that namespace: this table shows
	// the server listening and no connection, and the server holds a socket the
	// table does not list. That must not read as "nobody attached".
	m := newMachine(t)
	sock := testutil.Socket(t, m.sockDir, "s")
	ino, dev, _ := procfs.FileID(sock)
	m.proc.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"/usr/bin/zellij", "--server", sock}, Sockets: []uint32{1000, 4001}})
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	m.socks = []procfs.UnixSocket{{Inode: 1000, State: 10, Name: sock, VFSIno: ino, VFSDev: dev}}
	testutil.SessionFiles(t, m.info, "s", testutil.Metadata(0, "x"), "", now)
	m.clients["s"] = 1
	got := m.gather(t, true, running("s"))
	if got[0].Facts.Attached != policy.Yes || got[0].Clients != 1 {
		t.Errorf("a client the table cannot see: %v, %d (%s)", got[0].Facts.Attached, got[0].Clients, got[0].Facts.AttachDetail)
	}
	if tri, detail := inspect.ListenerClients(procTable(t, m), 100, m.socks); tri != policy.Unknown {
		t.Errorf("recover would call it %v (%s)", tri, detail)
	}
}

func TestABrowserClientCountsWithNoConnection(t *testing.T) {
	// The table shows the server listening and nothing connected; zellij's file
	// counts a browser client. It counts whichever way the rest is read.
	m := newMachine(t)
	m.server(t, "web", "web", 100, testutil.Metadata(0, "x")+"web_client_count 1\n", "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	for _, withSocks := range []bool{true, false} {
		got := m.gather(t, withSocks, running("web"))
		if got[0].Facts.Attached != policy.Yes || got[0].Clients != 1 {
			t.Errorf("browser client, table %v: %v, %d", withSocks, got[0].Facts.Attached, got[0].Clients)
		}
	}
}

// procTable reads a fake machine's process table.
func procTable(t *testing.T, m *machine) *procfs.Table {
	t.Helper()
	table, err := procfs.Read(m.proc.Root)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// overlaps fails a test when two list-clients calls run at once.
type overlaps struct {
	t       *testing.T
	running atomic.Int32
}

func (o *overlaps) ListClients(context.Context, string) ([]zellij.ClientRow, error) {
	if o.running.Add(1) > 1 {
		o.t.Error("list-clients calls overlap, which makes zellij 0.44.2 answer nothing")
	}
	defer o.running.Add(-1)
	time.Sleep(5 * time.Millisecond)
	return nil, nil
}

func TestServersAreAskedOneAtATime(t *testing.T) {
	m := newMachine(t)
	var listed []zellij.Listed
	for i, name := range []string{"a", "b", "c", "d", "e", "f"} {
		pid := 100 + 10*i
		m.server(t, name, name, pid, testutil.Metadata(0, "x"), "")
		m.pane(t, pid+1, pid, "bash", []string{"-bash"}, now)
		listed = append(listed, running(name))
	}
	src := inspect.Sources{ProcRoot: m.proc.Root, InfoDir: m.info, SocketDirs: []string{m.sockDir}, UID: -1, Clients: &overlaps{t: t}, Now: time.Now}
	if _, err := inspect.Gather(context.Background(), src, inspect.Rules{}, listed); err != nil {
		t.Fatal(err)
	}
}

func TestRenamedSessionFindsItsServer(t *testing.T) {
	// After `rename-session`, the socket file has the new name but the
	// server's command line still has the first one.
	m := newMachine(t)
	m.server(t, "renamed", "original", 100, testutil.Metadata(0, "x"), "")
	m.pane(t, 101, 100, "bash", []string{"-bash"}, now)
	got := m.gather(t, true, running("renamed"))
	if got[0].Facts.ServerPID != 100 || got[0].ServerMatch != "socket file" {
		t.Errorf("with sock_diag: server %d via %q", got[0].Facts.ServerPID, got[0].ServerMatch)
	}
	// Without sock_diag the command line is all there is, and it names the
	// old session: the server is not found and the session is kept.
	got = m.gather(t, false, running("renamed"))
	if got[0].Facts.ServerPID != 0 {
		t.Errorf("without sock_diag: matched server %d by a stale command line", got[0].Facts.ServerPID)
	}
	if v := policy.Decide(got[0].Facts, policy.Thresholds{}, now); v.Code != policy.NoServer {
		t.Errorf("verdict without a server: %s", v.Code)
	}
}

func TestExitedAndMissing(t *testing.T) {
	m := newMachine(t)
	testutil.SessionFiles(t, m.info, "gone", testutil.Metadata(0, "x"), "layout {\n}\n", now.Add(-50*time.Hour))
	got := m.gather(t, true, zellij.Listed{Name: "gone", Exited: true}, running("nofiles"), running("keep"))
	g := got[0].Facts
	if !g.HasInfo || g.ActivitySource != "session files" || now.Sub(g.LastActive) < 49*time.Hour {
		t.Errorf("exited: %+v", g)
	}
	if got[1].Facts.HasInfo || got[1].Facts.SocketOK {
		t.Errorf("nothing on disk: %+v", got[1].Facts)
	}
	if !got[2].Facts.Protected || got[2].Facts.ProtectedBy != "protected list" {
		t.Errorf("protected: %+v", got[2].Facts)
	}
}

func TestEveryKnownShellSaysNothingAsATitle(t *testing.T) {
	// The shells a pane can be idle in are the titles that name nothing.
	for _, shell := range inspect.KnownShells {
		in := rename.Input{Titles: []string{shell}, Cwd: "/srv/web-app"}
		if got := rename.Suggest(in, rename.MaxBase); got != "web-app" {
			t.Errorf("%s: got %q", shell, got)
		}
	}
}
