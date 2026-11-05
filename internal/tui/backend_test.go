package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/config"
	"github.com/kanggu12/zellij-reaper/internal/i18n"
	"github.com/kanggu12/zellij-reaper/internal/logfile"
	"github.com/kanggu12/zellij-reaper/internal/protect"
	"github.com/kanggu12/zellij-reaper/internal/reaper"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
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
		f.sessions += args[4] + " [Created 1m ago]\n"
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
