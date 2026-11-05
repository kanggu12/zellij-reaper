package reaper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/config"
	"github.com/kanggu12/zellij-reaper/internal/inspect"
	"github.com/kanggu12/zellij-reaper/internal/lock"
	"github.com/kanggu12/zellij-reaper/internal/logfile"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/protect"
	"github.com/kanggu12/zellij-reaper/internal/reaper"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// fakeZellij answers like zellij and records what it was asked to change.
type fakeZellij struct {
	listed  []zellij.Listed
	listErr error
	// during runs when the sessions are listed: something another process
	// does while the pass reads the facts.
	during func()
	// late are clients that appear before the second look: someone attaching
	// between the check and the delete. The facts come from the socket table,
	// so list-clients is asked only then.
	late map[string]int
	// lateErr makes that second look fail: zellij no longer answers.
	lateErr map[string]error
	refuse  map[string]bool
	deleted []string
	renamed []string
}

func (f *fakeZellij) Available() bool { return true }
func (f *fakeZellij) ListSessions(context.Context) ([]zellij.Listed, error) {
	if f.during != nil {
		f.during()
		f.during = nil
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.listed) == 0 {
		return nil, zellij.ErrNoSessions
	}
	return f.listed, nil
}
func (f *fakeZellij) ListClients(_ context.Context, name string) ([]zellij.ClientRow, error) {
	if f.lateErr[name] != nil {
		return nil, f.lateErr[name]
	}
	if f.late[name] > 0 {
		return make([]zellij.ClientRow, f.late[name]), nil
	}
	return nil, nil
}
func (f *fakeZellij) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fakeZellij) Rename(_ context.Context, name, to string) error {
	// A name zellij remembers but does not list.
	if f.refuse[to] {
		return zellij.ErrNameTaken
	}
	f.renamed = append(f.renamed, name+"->"+to)
	return nil
}

// world is a fake machine with three running sessions and one exited:
//   - idle-old: a bash pane untouched for 3h (reaped)
//   - busy: a pane running htop (kept)
//   - fresh: a bash pane used a minute ago (kept, idle too short)
//   - gone: exited 5 days ago (reaped)
type world struct {
	env  *reaper.Env
	zj   *fakeZellij
	logp string
	// attach connects a client to a session, as the kernel would show it.
	attach func(name string)
}

func newWorld(t *testing.T) *world {
	t.Helper()
	now := time.Now()
	fp := testutil.NewFakeProc(t)
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	ttys := t.TempDir()
	var socks []procfs.UnixSocket
	servers := map[string]int{}
	add := func(name string, pid int, comm string, argv []string, used time.Time, title string) {
		sock := testutil.Socket(t, sockDir, name)
		servers[name] = pid
		ino, dev, _ := procfs.FileID(sock)
		socks = append(socks, procfs.UnixSocket{Inode: uint32(pid), State: 10, Name: sock, VFSIno: ino, VFSDev: dev})
		fp.Add(testutil.Proc{PID: pid, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", sock}, Sockets: []uint32{uint32(pid)}})
		fp.Add(testutil.Proc{PID: pid + 1, PPID: pid, Comm: comm, Argv: argv, Stdin: testutil.TTY(t, ttys, name, used)})
		testutil.SessionFiles(t, info, name, testutil.Metadata(0, title), "", now)
	}
	add("idle-old", 100, "bash", []string{"-bash"}, now.Add(-3*time.Hour), "vim notes.md")
	add("busy", 200, "htop", []string{"htop"}, now.Add(-5*time.Hour), "htop")
	add("fresh", 300, "bash", []string{"-bash"}, now.Add(-time.Minute), "Pane #1")
	testutil.SessionFiles(t, info, "gone", testutil.Metadata(0, "x"), "layout {\n}\n", now.Add(-120*time.Hour))

	home := t.TempDir()
	cenv := config.MapEnv{"HOME": home}
	cfg := config.Default(cenv)
	cfg.IdleAfter, cfg.ExitedAfter = time.Hour, 72*time.Hour
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	zj := &fakeZellij{
		listed: []zellij.Listed{{Name: "idle-old"}, {Name: "busy"}, {Name: "fresh"}, {Name: "gone", Exited: true}},
		refuse: map[string]bool{},
	}
	pl, _ := protect.Load(config.ProtectedFile(cenv))
	logp := filepath.Join(home, "reaper.log")
	env := &reaper.Env{
		Config:    cfg,
		Protected: pl,
		Zellij:    zj,
		Sources: inspect.Sources{
			ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
			Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil }, Now: time.Now,
		},
		Log:      logfile.New(logp, 1<<20),
		LockPath: filepath.Join(home, "lock"),
		Now:      time.Now,
	}
	attach := func(name string) {
		// One accepted connection in the server and its other end elsewhere.
		pid := servers[name]
		conn, peer := uint32(pid+50), uint32(pid+51)
		fp.AddSocket(pid, conn)
		socks = append(socks,
			procfs.UnixSocket{Inode: conn, State: 1, Name: filepath.Join(sockDir, name), Peer: peer},
			procfs.UnixSocket{Inode: peer, State: 1, Peer: conn})
	}
	return &world{env: env, zj: zj, logp: logp, attach: attach}
}

func (w *world) log(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(w.logp)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunReapsOnlyWhatPassesEveryRule(t *testing.T) {
	w := newWorld(t)
	r, err := reaper.Run(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.zj.deleted, ",") != "idle-old,gone" {
		t.Errorf("deleted %v", w.zj.deleted)
	}
	reaped, kept, renamed := r.Counts()
	if reaped != 2 || kept != 2 || renamed != 0 {
		t.Errorf("counts %d %d %d", reaped, kept, renamed)
	}
	log := w.log(t)
	for _, want := range []string{
		"=== reaper start (dry_run=false idle_after=1h exited_after=3d) ===",
		"idle-old [RUNNING] panes={vim notes.md} :: REAP IDLE, 3h since last activity",
		"  -> deleted: idle-old",
		"busy [RUNNING] panes={htop} :: SKIP running in a pane: htop",
		"fresh [RUNNING] panes={Pane #1} :: SKIP IDLE for 1m, reaped after 1h",
		"gone [EXITED] panes={x} :: REAP EXITED, 5d since last activity",
		"=== reaper end (2 reaped, 2 kept, 0 renamed) ===",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestAttachingDuringThePassKeepsTheSession(t *testing.T) {
	w := newWorld(t)
	// Nobody is attached when the facts are gathered; a client appears
	// before the delete.
	w.zj.late = map[string]int{"idle-old": 1}
	r, err := reaper.Run(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range w.zj.deleted {
		if d == "idle-old" {
			t.Fatal("deleted a session a client attached to during the pass")
		}
	}
	if r.Outcomes[0].KeptAtDelete == "" || !strings.Contains(w.log(t), "  -> kept: idle-old :: 1 client(s) attached since the check") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.zj.deleted)+len(w.zj.renamed) != 0 {
		t.Errorf("a dry run changed something: %v %v", w.zj.deleted, w.zj.renamed)
	}
	log := w.log(t)
	if !strings.Contains(log, "  -> DRY_RUN: would run: zellij delete-session idle-old --force") ||
		!strings.Contains(log, "  -> DRY_RUN: would rename 'busy' → 'htop'") {
		t.Errorf("dry run log:\n%s", log)
	}
}

func TestForceSkipsTheWaitsOnly(t *testing.T) {
	w := newWorld(t)
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	// fresh is reaped now; busy is still busy.
	if strings.Join(w.zj.deleted, ",") != "idle-old,fresh,gone" {
		t.Errorf("deleted %v", w.zj.deleted)
	}
}

func TestAutoRenameStepsPastRefusedNames(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	// htop's desired name is refused once (zellij still remembers it).
	r, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	desired := ""
	for _, o := range r.Outcomes {
		if o.Session.Name() == "busy" {
			desired = o.WouldRename
		}
	}
	if desired != "htop" {
		t.Fatalf("planned name %q", desired)
	}
	w.zj.refuse[desired] = true
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.zj.renamed, ",") != "busy->"+desired+"-2" {
		t.Errorf("renamed %v", w.zj.renamed)
	}
	// "Pane #1" says nothing, and the session has no cwd: fresh keeps its name.
	for _, r := range w.zj.renamed {
		if strings.HasPrefix(r, "fresh->") {
			t.Errorf("renamed an uninformative title: %s", r)
		}
	}
}

func TestProtectedListKeepsAndDoesNotRename(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	if err := w.env.Protected.Set("idle-old", true); err != nil {
		t.Fatal(err)
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	for _, d := range w.zj.deleted {
		if d == "idle-old" {
			t.Error("a protected session was deleted")
		}
	}
	for _, r := range w.zj.renamed {
		if strings.HasPrefix(r, "idle-old->") {
			t.Error("a protected session was renamed")
		}
	}
	if !strings.Contains(w.log(t), "idle-old [RUNNING] panes={vim notes.md} :: SKIP protected by protected list") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestNoSessionsAndFailures(t *testing.T) {
	w := newWorld(t)
	w.zj.listed = nil
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.log(t), "] no sessions\n") {
		t.Errorf("log:\n%s", w.log(t))
	}
	w.zj.listErr = errors.New("zellij list-sessions: Error occurred: Os { code: 13 }")
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err == nil {
		t.Error("a list failure was not returned")
	}
	if !strings.Contains(w.log(t), "zellij list-sessions failed: zellij list-sessions: Error occurred") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestLockKeepsPassesApart(t *testing.T) {
	w := newWorld(t)
	held, err := lock.Acquire(w.env.LockPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	start := time.Now()
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); !errors.Is(err, lock.ErrBusy) {
		t.Errorf("Run with the lock held: %v", err)
	}
	if time.Since(start) < 4*time.Second {
		t.Error("Run did not wait for the other pass")
	}
	if len(w.zj.deleted) != 0 {
		t.Error("a pass ran without the lock")
	}
}

func TestPlanWritesNothing(t *testing.T) {
	w := newWorld(t)
	r, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Outcomes) != 4 || len(w.zj.deleted) != 0 {
		t.Errorf("plan: %d outcomes, deleted %v", len(r.Outcomes), w.zj.deleted)
	}
	if _, err := os.Stat(w.logp); !os.IsNotExist(err) {
		t.Error("Plan wrote the log")
	}
	if _, err := os.Stat(w.env.LockPath); !os.IsNotExist(err) {
		t.Error("Plan took the lock")
	}
}

func TestAPassRefreshesTheListTheDashboardHolds(t *testing.T) {
	w := newWorld(t)
	// The dashboard and the pass share one List.
	shared := w.env.Protected
	// Another process protects idle-old after that List was loaded.
	other, _ := protect.Load(shared.Path)
	if err := other.Set("idle-old", true); err != nil {
		t.Fatal(err)
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, d := range w.zj.deleted {
		if d == "idle-old" {
			t.Error("deleted a session protected while the pass waited")
		}
	}
	if w.env.Protected != shared {
		t.Error("the pass replaced the List instead of refreshing it")
	}
	if !shared.Has("idle-old") {
		t.Errorf("the shared List is stale after the pass: %v", shared.Names())
	}
}

func TestAnUnreadableProtectedListStopsThePass(t *testing.T) {
	w := newWorld(t)
	// A directory where the list should be: it cannot be read.
	if err := os.MkdirAll(w.env.Protected.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{Force: true}); err == nil {
		t.Error("a pass ran without its protected list")
	}
	if len(w.zj.deleted)+len(w.zj.renamed) != 0 {
		t.Errorf("changed sessions without the protected list: %v %v", w.zj.deleted, w.zj.renamed)
	}
	if !strings.Contains(w.log(t), "could not read the protected list: ") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestAutoRenameAsksForClientsAgain(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*fakeZellij)
		why  string
	}{
		{"a client attached", func(z *fakeZellij) { z.late = map[string]int{"busy": 1} }, "1 client(s) attached since the check"},
		{"no answer", func(z *fakeZellij) { z.lateErr = map[string]error{"busy": errors.New("no answer within 10s")} }, "could not ask for its clients again: no answer within 10s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			w.env.Config.AutoRename = true
			// Nobody is attached when the facts are gathered; by the time
			// the deletes before it are done, the answer has changed.
			c.set(w.zj)
			r, err := reaper.Run(context.Background(), w.env, reaper.Options{})
			if err != nil {
				t.Fatal(err)
			}
			for _, rn := range w.zj.renamed {
				if strings.HasPrefix(rn, "busy->") {
					t.Fatalf("renamed a session without asking again: %s", rn)
				}
			}
			if !strings.Contains(w.log(t), "  -> kept name: busy :: "+c.why+"\n") {
				t.Errorf("log:\n%s", w.log(t))
			}
			for _, o := range r.Outcomes {
				if o.Session.Name() == "busy" && o.KeptName != c.why {
					t.Errorf("outcome says %q", o.KeptName)
				}
			}
		})
	}
}

func TestAutoRenameLeavesAttachedSessionsAlone(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	// busy runs a protected program and a client is attached: its verdict is
	// decided before its clients are looked at, and it keeps its name anyway.
	w.env.Config.ProtectCommands = []string{"htop"}
	w.attach("busy")
	plan, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range plan.Outcomes {
		if o.Session.Name() == "busy" && (o.Verdict.Code != policy.ProtectedCommand || o.WouldRename != "") {
			t.Fatalf("plan: %s, next name %q", o.Verdict.Code, o.WouldRename)
		}
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, rn := range w.zj.renamed {
		if strings.HasPrefix(rn, "busy->") {
			t.Fatalf("renamed an attached session: %s", rn)
		}
	}
	// The client was there all along: nothing attached "since the check".
	if strings.Contains(w.log(t), "kept name: busy") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestAProtectionAddedDuringThePassKeepsTheSession(t *testing.T) {
	w := newWorld(t)
	// While the pass reads the facts, `zellij-reaper protect idle-old` runs
	// in another terminal: another List writes the file.
	w.zj.during = func() {
		other, err := protect.Load(w.env.Protected.Path)
		if err == nil {
			err = other.Set("idle-old", true)
		}
		if err != nil {
			t.Error(err)
		}
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, d := range w.zj.deleted {
		if d == "idle-old" {
			t.Fatal("deleted a session protected during the pass")
		}
	}
	if !strings.Contains(w.log(t), "  -> kept: idle-old :: protected since the check\n") {
		t.Errorf("log:\n%s", w.log(t))
	}
}
