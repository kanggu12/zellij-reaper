package repair_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/repair"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

type fakeZellij struct {
	listed  []zellij.Listed
	deleted []string
}

func (f *fakeZellij) ListSessions(context.Context) ([]zellij.Listed, error) { return f.listed, nil }
func (f *fakeZellij) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

func TestFindAndRepair(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	// A healthy session that was renamed: its file has the new name, the
	// server's command line still the first one.
	sock := testutil.Socket(t, sockDir, "renamed")
	ino, dev, _ := procfs.FileID(sock)
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "first-name")}, Sockets: []uint32{1000}})
	// An orphan: it listens, but its file is gone.
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "gone")}, Sockets: []uint32{2000}})
	socks := []procfs.UnixSocket{
		{Inode: 1000, State: 10, VFSIno: ino, VFSDev: dev},
		{Inode: 2000, State: 10, VFSIno: 99999999, VFSDev: dev},
	}
	long := strings.Repeat("x", 120)
	testutil.SessionFiles(t, info, long, testutil.Metadata(0, "x"), "", time.Now())
	testutil.SessionFiles(t, info, "short", testutil.Metadata(0, "x"), "", time.Now())
	zj := &fakeZellij{listed: []zellij.Listed{{Name: "renamed"}}}
	in := repair.Inputs{Zellij: zj, ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
		Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil }}

	sc, err := repair.Find(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Broken) != 1 || sc.Broken[0].Name != long {
		t.Errorf("broken %+v", sc.Broken)
	}
	if !sc.OrphansKnown || len(sc.Orphans) != 1 || sc.Orphans[0].PID != 200 {
		t.Errorf("orphans %+v (known %v): the renamed session's server must not be one", sc.Orphans, sc.OrphansKnown)
	}
	// Without the socket table nothing can be called an orphan.
	in2 := in
	in2.Sockets = func() ([]procfs.UnixSocket, error) { return nil, procfs.ErrNoSockDiag }
	if sc2, _ := repair.Find(context.Background(), in2); sc2.OrphansKnown || len(sc2.Orphans) != 0 {
		t.Errorf("without sock_diag: %+v", sc2)
	}
	// Repair without stopping orphans: the broken session is deleted and
	// its directory cleared; the short one stays.
	steps := repair.Repair(context.Background(), in, sc, false)
	if len(zj.deleted) != 1 || zj.deleted[0] != long {
		t.Errorf("deleted %v", zj.deleted)
	}
	if _, err := os.Stat(filepath.Join(info, long)); !os.IsNotExist(err) {
		t.Errorf("broken directory kept; steps %+v", steps)
	}
	if _, err := os.Stat(filepath.Join(info, "short")); err != nil {
		t.Error("a healthy session's directory was removed")
	}
}

func TestProtectedAndAllOrphaned(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	long := strings.Repeat("y", 120)
	testutil.SessionFiles(t, info, long, testutil.Metadata(0, "x"), "", time.Now())
	// Two servers whose listening sockets match no file: a socket table
	// that looks like that is suspect, so neither is stopped.
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "a")}, Sockets: []uint32{1000}})
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "b")}, Sockets: []uint32{2000}})
	socks := []procfs.UnixSocket{{Inode: 1000, State: 10, VFSIno: 11, VFSDev: 1}, {Inode: 2000, State: 10, VFSIno: 22, VFSDev: 1}}
	zj := &fakeZellij{}
	in := repair.Inputs{Zellij: zj, ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
		Sockets:   func() ([]procfs.UnixSocket, error) { return socks, nil },
		Protected: func(n string) bool { return n == long }}
	sc, err := repair.Find(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Broken) != 0 {
		t.Errorf("a protected name was listed as broken: %+v", sc.Broken)
	}
	if !sc.AllOrphaned {
		t.Errorf("every server looked orphaned, AllOrphaned = false: %+v", sc)
	}
	steps := repair.Repair(context.Background(), in, sc, true)
	for _, st := range steps {
		if strings.HasPrefix(st.Target, "pid ") {
			t.Errorf("stopped %s although every server looked orphaned", st.Target)
		}
	}
}

// orphanMachine is a fake machine with one healthy server and an orphan
// server (its socket file gone) in each state recover must judge. Its pids
// are above the kernel's limit (4194304), so not even a broken fake could
// signal a real process.
type orphanMachine struct {
	in    repair.Inputs
	socks []procfs.UnixSocket
	// signals records every signal sent, as "pid signal".
	signals []string
	gone    map[int]bool
}

const base = 5_000_000

func newOrphanMachine(t *testing.T) *orphanMachine {
	t.Helper()
	m := &orphanMachine{gone: map[int]bool{}}
	fp := testutil.NewFakeProc(t)
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	// server adds a zellij server for a session first named name, holding a
	// listening socket whose file is file (or a removed one when file is
	// ""), and the connected sockets extra.
	server := func(pid int, name, file string, extra ...uint32) procfs.UnixSocket {
		listen := uint32(pid)
		fp.Add(testutil.Proc{PID: pid, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, name)}, Sockets: append([]uint32{listen}, extra...)})
		u := procfs.UnixSocket{Inode: listen, State: 10, Name: filepath.Join(sockDir, name), VFSIno: uint32(pid), VFSDev: 1}
		if file != "" {
			ino, dev, _ := procfs.FileID(testutil.Socket(t, sockDir, file))
			u.VFSIno, u.VFSDev = ino, dev
		}
		m.socks = append(m.socks, u)
		return u
	}
	pane := func(pid, ppid int, comm string, argv ...string) {
		fp.Add(testutil.Proc{PID: pid, PPID: ppid, Comm: comm, Argv: argv})
	}
	// connect adds a connection the server accepted on l, whose other end
	// (peer) the process holder holds; holder 0 is nobody visible.
	connect := func(l procfs.UnixSocket, ino, peer uint32, holder int) {
		m.socks = append(m.socks, procfs.UnixSocket{Inode: ino, State: 1, Name: l.Name, VFSIno: l.VFSIno, VFSDev: l.VFSDev, Peer: peer})
		if holder != 0 {
			fp.Add(testutil.Proc{PID: holder, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "attach"}, Sockets: []uint32{peer}})
		}
	}

	// A healthy session: its file is there.
	server(base+100, "healthy", "healthy")
	pane(base+101, base+100, "bash", "-bash")
	// Orphans nothing keeps. The second holds socket pairs of its own, as
	// every real zellij server does: those are not clients.
	server(base+200, "plain", "")
	pane(base+201, base+200, "bash", "-bash")
	server(base+800, "pairs", "", base+801, base+802)
	m.socks = append(m.socks,
		procfs.UnixSocket{Inode: base + 801, State: 1, Peer: base + 802},
		procfs.UnixSocket{Inode: base + 802, State: 1, Peer: base + 801})
	pane(base+803, base+800, "zsh", "-zsh")
	// A client still attached through the socket it accepted before the
	// file went.
	l := server(base+300, "attached", "", base+301)
	connect(l, base+301, base+302, base+390)
	pane(base+303, base+300, "bash", "-bash")
	// A busy pane, a protected program, a protected first name.
	server(base+400, "busy", "")
	pane(base+401, base+400, "htop", "htop")
	server(base+500, "agent", "")
	pane(base+501, base+500, "bash", "-bash")
	pane(base+502, base+501, "claude", "claude")
	server(base+600, "keep-me", "")
	pane(base+601, base+600, "bash", "-bash")
	// A connection whose other end nobody visible holds: cannot tell.
	l = server(base+700, "ghost", "", base+701)
	connect(l, base+701, base+702, 0)
	pane(base+703, base+700, "bash", "-bash")

	m.in = repair.Inputs{Zellij: &fakeZellij{}, ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
		Sockets:         func() ([]procfs.UnixSocket, error) { return m.socks, nil },
		Protected:       func(n string) bool { return n == "keep-me" },
		ProtectCommands: []string{"claude"},
		Signal: func(pid int, sig syscall.Signal) error {
			// A process that got SIGTERM is gone by the next question.
			if m.gone[pid] {
				return syscall.ESRCH
			}
			if sig != 0 {
				m.signals = append(m.signals, fmt.Sprintf("%d %d", pid, sig))
			}
			if sig == syscall.SIGTERM {
				m.gone[pid] = true
			}
			return nil
		}}
	return m
}

func TestOrphansSomebodyMayUseAreKept(t *testing.T) {
	m := newOrphanMachine(t)
	sc, err := repair.Find(context.Background(), m.in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		base + 200: "",
		base + 800: "",
		base + 300: "client attached (pid 5000390 is connected to its socket)",
		base + 400: "running in a pane: htop",
		base + 500: "protected program running (claude)",
		base + 600: "its first name is protected",
		base + 700: "attach-check uncertain (1 connection(s) to its socket with no visible owner)",
	}
	if len(sc.Orphans) != len(want) || sc.AllOrphaned {
		t.Fatalf("orphans %+v (all orphaned %v)", sc.Orphans, sc.AllOrphaned)
	}
	for _, o := range sc.Orphans {
		if w, ok := want[o.PID]; !ok || o.Kept != w {
			t.Errorf("pid %d (%s): kept %q, want %q", o.PID, o.FirstName, o.Kept, w)
		}
	}
	steps := repair.Repair(context.Background(), m.in, sc, true)
	if got := strings.Join(m.signals, ","); got != fmt.Sprintf("%d 15,%d 15", base+200, base+800) {
		t.Errorf("signals %s; steps %+v", got, steps)
	}
}

func TestASecondLookBeforeTheStop(t *testing.T) {
	m := newOrphanMachine(t)
	sc, err := repair.Find(context.Background(), m.in)
	if err != nil {
		t.Fatal(err)
	}
	// Between the scan and the stop, a client connects to "plain": the
	// server accepts a connection, whose other end a new process holds.
	var plain procfs.UnixSocket
	for _, u := range m.socks {
		if u.Inode == base+200 {
			plain = u
		}
	}
	m.socks = append(m.socks, procfs.UnixSocket{Inode: base + 210, State: 1, Name: plain.Name, VFSIno: plain.VFSIno, VFSDev: plain.VFSDev, Peer: base + 211})
	fd := func(pid int, n int, ino int) {
		dir := filepath.Join(m.in.ProcRoot, fmt.Sprint(pid), "fd")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fmt.Sprintf("socket:[%d]", ino), filepath.Join(dir, fmt.Sprint(n))); err != nil {
			t.Fatal(err)
		}
	}
	fd(base+200, 9, base+210)
	// The client: a process with a stat file, holding the other end.
	fd(base+290, 3, base+211)
	if err := os.WriteFile(filepath.Join(m.in.ProcRoot, fmt.Sprint(base+290), "stat"), []byte(fmt.Sprintf("%d (zellij) S 1 0 0 0 -1 4194560\n", base+290)), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := repair.Repair(context.Background(), m.in, sc, true)
	if got := strings.Join(m.signals, ","); got != fmt.Sprintf("%d 15", base+800) {
		t.Errorf("signals %s; steps %+v", got, steps)
	}
	kept := false
	for _, st := range steps {
		if st.Target == fmt.Sprintf("pid %d", base+200) && st.What == "kept orphan server: client attached (pid 5000290 is connected to its socket)" {
			kept = true
		}
	}
	if !kept {
		t.Errorf("no step says the second look kept it: %+v", steps)
	}

	// Processes that cannot be read again keep every server.
	m = newOrphanMachine(t)
	sc, _ = repair.Find(context.Background(), m.in)
	m.in.ProcRoot = filepath.Join(t.TempDir(), "gone")
	steps = repair.Repair(context.Background(), m.in, sc, true)
	if len(m.signals) != 0 {
		t.Errorf("signaled %v without a second look; steps %+v", m.signals, steps)
	}
	// So does a socket table that cannot be read again.
	m = newOrphanMachine(t)
	sc, _ = repair.Find(context.Background(), m.in)
	m.in.Sockets = func() ([]procfs.UnixSocket, error) { return nil, procfs.ErrNoSockDiag }
	if repair.Repair(context.Background(), m.in, sc, true); len(m.signals) != 0 {
		t.Errorf("signaled %v without the socket table", m.signals)
	}
}

func TestAProtectedSessionWithoutASocketKeepsEveryOrphan(t *testing.T) {
	m := newOrphanMachine(t)
	// A protected session renamed past the socket path's limit: its server
	// is one of the orphans, and nothing says which.
	long := strings.Repeat("l", 120)
	testutil.SessionFiles(t, m.in.InfoDir, long, testutil.Metadata(0, "x"), "layout {\n}\n", time.Now())
	m.in.Protected = func(n string) bool { return n == "keep-me" || n == long }
	sc, err := repair.Find(context.Background(), m.in)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Orphans) == 0 {
		t.Fatal("no orphans found")
	}
	for _, o := range sc.Orphans {
		if o.Kept == "" {
			t.Errorf("orphan %s (pid %d) would be stopped", o.FirstName, o.PID)
		}
	}
}
