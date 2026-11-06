package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

// fakeZellij answers zellij's command line for the real backend, without
// starting a process, and records every call.
type fakeZellij struct {
	mu sync.Mutex
	// sessions is what list-sessions prints; listErr makes it fail.
	sessions string
	listErr  error
	// clients is, per session, the number of attached clients or the error
	// list-clients fails with.
	clients map[string]any
	// renameErr makes rename-session fail.
	renameErr error
	// renameUnseen makes rename-session succeed without the new name being
	// listed: a server that has not acted on it yet.
	renameUnseen bool
	// onRename runs while zellij renames: another process acting meanwhile.
	onRename func()
	calls    []string
}

func (f *fakeZellij) run(_ context.Context, args ...string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case args[0] == "list-sessions":
		if f.listErr != nil {
			return "", f.listErr.Error(), f.listErr
		}
		return f.sessions, "", nil
	case args[0] == "delete-session":
		// The session leaves the list, as zellij would.
		var kept []string
		for _, line := range strings.SplitAfter(f.sessions, "\n") {
			if !strings.HasPrefix(line, args[1]+" ") {
				kept = append(kept, line)
			}
		}
		f.sessions = strings.Join(kept, "")
		return "", "", nil
	case len(args) == 4 && args[3] == "list-clients":
		switch v := f.clients[args[1]].(type) {
		case int:
			out := "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n"
			for i := range v {
				out += fmt.Sprintf("%d terminal_0 bash\n", i+1)
			}
			return out, "", nil
		case error:
			return "", v.Error(), v
		}
		return "", "There is no active session!", errors.New("exit status 1")
	case len(args) == 5 && args[3] == "rename-session":
		if f.onRename != nil {
			f.onRename()
		}
		if f.renameErr != nil {
			return "", f.renameErr.Error(), f.renameErr
		}
		// The new name is listed from now on, as zellij would.
		if !f.renameUnseen {
			f.sessions += args[4] + " [Created 1m ago]\n"
		}
		return "", "", nil
	}
	return "", "unexpected call", errors.New("exit status 2")
}

// did reports whether a call starting with prefix was made.
func (f *fakeZellij) did(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// backendFor builds the real backend on a fake zellij, with its files in a
// temporary home and pattern as the protect setting.
func backendFor(t *testing.T, z *fakeZellij, pattern string) (Backend, *app.App) {
	t.Helper()
	home := t.TempDir()
	env := config.MapEnv{"HOME": home}
	cfg := config.Default(env)
	cfg.Protect = pattern
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	pl, err := protect.Load(config.ProtectedFile(env))
	if err != nil {
		t.Fatal(err)
	}
	zj := &zellij.CLI{Bin: "zellij", Timeout: time.Second, Run: z.run}
	a := &app.App{Env: env, Config: cfg, Protected: pl, Zellij: zj}
	a.Reaper = &reaper.Env{Config: cfg, Protected: pl, Zellij: zj, Log: logfile.New(filepath.Join(home, "reaper.log"), 0),
		LockPath: config.LockFile(env), Now: time.Now}
	return NewBackend(a), a
}

func TestDeleteByHandAsksZellijFirst(t *testing.T) {
	i18n.Set(i18n.English)
	listed := "notes [Created 2h ago] \nold [Created 5d ago] (EXITED - attach to resurrect)\n"
	for _, c := range []struct {
		name    string
		target  string
		clients any
		listErr error
		refused string
	}{
		{"a client attached", "notes", 1, nil, "a client is attached now"},
		{"no answer", "notes", errors.New("no answer within 10s"), nil, "cannot tell whether a client is attached"},
		{"the list fails", "notes", 0, errors.New("Os { code: 13 }"), "cannot tell whether a client is attached"},
		{"no longer listed", "gone", 0, nil, "zellij no longer lists it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			z := &fakeZellij{sessions: listed, listErr: c.listErr, clients: map[string]any{"notes": c.clients}}
			b, _ := backendFor(t, z, "")
			err := b.Delete(context.Background(), c.target)
			if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("Delete: %v, want %q", err, c.refused)
			}
			if z.did("delete-session") {
				t.Errorf("deleted although %s: %v", c.name, z.calls)
			}
		})
	}
	// Nobody attached: deleted. An exited session has no server to ask.
	z := &fakeZellij{sessions: listed, clients: map[string]any{"notes": 0}}
	b, _ := backendFor(t, z, "")
	for _, name := range []string{"notes", "old"} {
		if err := b.Delete(context.Background(), name); err != nil {
			t.Errorf("Delete(%s): %v", name, err)
		}
		if !z.did("delete-session " + name) {
			t.Errorf("%s not deleted: %v", name, z.calls)
		}
	}
	if z.did("--session old action list-clients") {
		t.Error("asked an exited session for its clients")
	}
}

func TestRenameByHandKeepsTheProtection(t *testing.T) {
	i18n.Set(i18n.English)
	ctx := context.Background()

	// A name the list cannot hold is refused before zellij renames.
	z := &fakeZellij{}
	b, a := backendFor(t, z, "")
	if err := a.Protected.Set("notes", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rename(ctx, "notes", "#notes"); err == nil || !strings.Contains(err.Error(), "notes is on the protected list") {
		t.Errorf("rename to an unstorable name: %v", err)
	}
	if z.did("--session notes action rename-session") || !a.Protected.Has("notes") {
		t.Errorf("an unstorable rename went ahead: calls %v, list %v", z.calls, a.Protected.Names())
	}

	// A rename zellij refuses leaves the list as it was.
	z.renameErr = errors.New("Session with name journal already exists")
	if _, err := b.Rename(ctx, "notes", "journal"); err == nil {
		t.Error("a failed rename reported success")
	}
	if got := strings.Join(a.Protected.Names(), ","); got != "notes" {
		t.Errorf("list after a failed rename: %s", got)
	}

	// A protection added from another terminal since the dashboard loaded
	// the list moves with the session.
	z.renameErr = nil
	other, _ := protect.Load(a.Protected.Path)
	if err := other.Set("todo", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rename(ctx, "todo", "todo-2"); err != nil {
		t.Fatal(err)
	}
	back, _ := protect.Load(a.Protected.Path)
	if got := strings.Join(back.Names(), ","); got != "notes,todo-2" {
		t.Errorf("list after renames: %s", got)
	}
}

func TestRenameByHandSaysWhenThePatternNoLongerProtects(t *testing.T) {
	i18n.Set(i18n.English)
	ctx := context.Background()
	z := &fakeZellij{}
	b, _ := backendFor(t, z, "^keep-")
	// Out of the pattern: renamed, and the person is told.
	note, err := b.Rename(ctx, "keep-notes", "notes")
	if err != nil || !z.did("--session keep-notes action rename-session notes") {
		t.Fatalf("rename: %v, calls %v", err, z.calls)
	}
	if !strings.Contains(note, "no longer protected") {
		t.Errorf("no warning for a session the pattern stopped protecting: %q", note)
	}
	// Still inside the pattern: nothing to say.
	if note, err := b.Rename(ctx, "keep-a", "keep-b"); err != nil || note != "" {
		t.Errorf("rename within the pattern: %q %v", note, err)
	}
}

func TestThePlanReadsTheProtectedListAgain(t *testing.T) {
	z := &fakeZellij{sessions: "work [Created 1h ago]\n", clients: map[string]any{"work": 0}}
	b, a := backendFor(t, z, "")
	// Another terminal protects work after the dashboard opened.
	other, err := protect.Load(a.Protected.Path)
	if err == nil {
		err = other.Set("work", true)
	}
	if err != nil {
		t.Fatal(err)
	}
	b.Plan(context.Background())
	if !b.Protected("work") {
		t.Error("the dashboard still shows the list it read before")
	}
}

func TestRenameByHandMovesAProtectionAddedMeanwhile(t *testing.T) {
	z := &fakeZellij{sessions: "work [Created 1h ago]\n"}
	b, a := backendFor(t, z, "")
	// While zellij renames it, another terminal protects the old name.
	z.onRename = func() {
		if other, err := protect.Load(a.Protected.Path); err == nil {
			other.Set("work", true)
		}
	}
	if _, err := b.Rename(context.Background(), "work", "job"); err != nil {
		t.Fatal(err)
	}
	if !b.Protected("job") || b.Protected("work") {
		t.Errorf("protected: job %v, work %v", b.Protected("job"), b.Protected("work"))
	}
}

func TestRenameByHandRefusesANameTooLongForTheSocket(t *testing.T) {
	z := &fakeZellij{sessions: "work [Created 1h ago]\n"}
	b, _ := backendFor(t, z, "")
	_, err := b.Rename(context.Background(), "work", strings.Repeat("x", 200))
	if err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("err %v", err)
	}
	if z.did("--session work action rename-session") {
		t.Error("zellij renamed it anyway")
	}
}

func TestRenameByHandRefusesATakenName(t *testing.T) {
	i18n.Set(i18n.English)
	// zellij 0.44 and later would accept this and cut the other session off.
	z := &fakeZellij{sessions: "work [Created 1h ago]\nnotes [Created 2h ago]\n"}
	b, _ := backendFor(t, z, "")
	if _, err := b.Rename(context.Background(), "work", "notes"); !errors.Is(err, zellij.ErrNameTaken) {
		t.Errorf("rename to a taken name: %v", err)
	}
	if z.did("--session work action rename-session") {
		t.Errorf("zellij was asked to rename onto a taken name: %v", z.calls)
	}
}

func TestARenameByHandOntoASocketFileIsRefused(t *testing.T) {
	i18n.Set(i18n.English)
	// zellij does not list notes at this moment, and its socket file is there.
	dir := testutil.SocketDir(t)
	t.Setenv("ZELLIJ_SOCKET_DIR", dir)
	if err := os.MkdirAll(filepath.Join(dir, "contract_version_1"), 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.Socket(t, filepath.Join(dir, "contract_version_1"), "notes")
	z := &fakeZellij{sessions: "work [Created 1h ago]\n"}
	b, a := backendFor(t, z, "")
	a.ZellijVersion = "0.45.1"
	if _, err := b.Rename(context.Background(), "work", "notes"); !errors.Is(err, zellij.ErrNameTaken) {
		t.Fatalf("rename onto a socket file: %v", err)
	}
	if z.did("--session work action rename-session") {
		t.Errorf("zellij was asked to rename onto a socket file: %v", z.calls)
	}
}

func TestAnUnconfirmedRenameKeepsBothNamesProtected(t *testing.T) {
	i18n.Set(i18n.English)
	// zellij takes the rename, and the new name does not show in time: a slow
	// server. The session may have either name by the next pass.
	z := &fakeZellij{sessions: "notes [Created 1h ago]\n", renameUnseen: true}
	b, a := backendFor(t, z, "")
	if err := a.Protected.Set("notes", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rename(context.Background(), "notes", "journal"); !errors.Is(err, zellij.ErrUnconfirmed) {
		t.Fatalf("rename: %v", err)
	}
	if !a.Protected.Has("notes") || !a.Protected.Has("journal") {
		t.Errorf("list after an unconfirmed rename: %v", a.Protected.Names())
	}
}

func TestAnUnconfirmedRenameByHandCountsAsChosen(t *testing.T) {
	i18n.Set(i18n.English)
	// journal is left on the list from a session now gone, and this one may have
	// just been given that name by hand: the next pass must not take it for a pass's.
	z := &fakeZellij{sessions: "work [Created 1h ago]\n", renameUnseen: true}
	b, a := backendFor(t, z, "")
	given := protect.LoadGiven(filepath.Join(t.TempDir(), "named"))
	a.Reaper.Given = given
	if err := given.Set("journal", true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rename(context.Background(), "work", "journal"); !errors.Is(err, zellij.ErrUnconfirmed) {
		t.Fatalf("rename: %v", err)
	}
	if given.Has("journal") {
		t.Errorf("list after it: %v", given.Names())
	}
}

func TestKeepNameKeepsAnyName(t *testing.T) {
	i18n.Set(i18n.English)
	z := &fakeZellij{sessions: "build-watcher [Created 1h ago]\nbrave-peach [Created 1h ago]\n"}
	b, a := backendFor(t, z, "")
	dir := t.TempDir()
	given, kept := protect.LoadGiven(filepath.Join(dir, "named")), protect.LoadKept(filepath.Join(dir, "kept"))
	a.Reaper.Given, a.Reaper.Kept = given, kept
	if err := given.Set("build-watcher", true); err != nil {
		t.Fatal(err)
	}
	// One a pass gave, and one zellij made up, which no list has.
	for _, name := range []string{"build-watcher", "brave-peach"} {
		if err := b.KeepName(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(kept.Names(), ",") != "brave-peach,build-watcher" || given.Has("build-watcher") {
		t.Errorf("kept %v, given %v", kept.Names(), given.Names())
	}
	b2, err := os.ReadFile(a.Reaper.Log.Path)
	if err != nil || !strings.Contains(string(b2), "brave-peach :: name kept from the dashboard") {
		t.Errorf("log: %s, %v", b2, err)
	}
	// The dialog stayed open while a pass renamed the session: nothing to keep.
	if err := b.KeepName(context.Background(), "quiet-mouse"); err == nil || !strings.Contains(err.Error(), "no session is called quiet-mouse") {
		t.Errorf("a name no session has: %v", err)
	}
	if kept.Has("quiet-mouse") {
		t.Errorf("kept a name no session has: %v", kept.Names())
	}
}

func TestKeepNameWaitsForAPass(t *testing.T) {
	// A pass holds the lock: it may have read the list of kept names already, so the
	// name cannot be kept until it is done.
	z := &fakeZellij{sessions: "brave-peach [Created 1h ago]\n"}
	b, a := backendFor(t, z, "")
	a.Reaper.Kept = protect.LoadKept(filepath.Join(t.TempDir(), "kept"))
	held, err := lock.Acquire(a.Reaper.LockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err := b.KeepName(context.Background(), "brave-peach"); !errors.Is(err, lock.ErrBusy) {
		t.Errorf("during a pass: %v", err)
	}
	if a.Reaper.Kept.Has("brave-peach") {
		t.Error("kept a name while a pass held the lock")
	}
}

func TestANameGivenByHandCountsAsChosen(t *testing.T) {
	i18n.Set(i18n.English)
	z := &fakeZellij{sessions: "work [Created 1h ago]\nother [Created 1h ago]\n"}
	b, a := backendFor(t, z, "")
	given := protect.LoadGiven(filepath.Join(t.TempDir(), "named"))
	a.Reaper.Given = given
	for _, name := range []string{"journal", "other"} {
		if err := given.Set(name, true); err != nil {
			t.Fatal(err)
		}
	}
	// Refused: the name is another session's, and its entry stays.
	if _, err := b.Rename(context.Background(), "work", "other"); !errors.Is(err, zellij.ErrNameTaken) {
		t.Fatalf("rename to a taken name: %v", err)
	}
	if !given.Has("other") {
		t.Error("a refused rename took another session's name off the list")
	}
	// Done: the name is someone's choice now.
	if _, err := b.Rename(context.Background(), "work", "journal"); err != nil {
		t.Fatal(err)
	}
	if given.Has("journal") {
		t.Error("a name given by hand stayed on the list of names auto_rename gave")
	}
}

func TestTheSessionADashboardRunsInIsTheOneItsServerHasNow(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	// A server, the shell in one of its panes and the dashboard that shell
	// started; another server; a shell in no session.
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/run/zellij/brave-peach"}})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}})
	fp.Add(testutil.Proc{PID: 102, PPID: 101, Comm: "zellij-reaper", Argv: []string{"zellij-reaper"}})
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/run/zellij/other"}})
	fp.Add(testutil.Proc{PID: 300, PPID: 1, Comm: "bash", Argv: []string{"-bash"}})
	tbl, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	// brave-peach was renamed since it started: the name it is listed under
	// now is the one a zellij command must use.
	rep := reaper.Report{Outcomes: []reaper.Outcome{
		{Session: inspect.Session{Listed: zellij.Listed{Name: "review-the-draft"}, Server: &procfs.Server{PID: 100}}},
		{Session: inspect.Session{Listed: zellij.Listed{Name: "other"}, Server: &procfs.Server{PID: 200}}},
	}}
	if got := sessionAbove(tbl, 102, rep); got != "review-the-draft" {
		t.Errorf("from the dashboard: %q", got)
	}
	if got := sessionAbove(tbl, 300, rep); got != "" {
		t.Errorf("from a shell in no session: %q", got)
	}
}
