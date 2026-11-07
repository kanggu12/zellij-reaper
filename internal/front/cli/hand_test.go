package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// fakeSessions answers zellij's command line for the commands that act by
// hand, without starting a process, and records every call.
type fakeSessions struct {
	// listed is what list-sessions prints.
	listed string
	// clients is the number of clients attached to each running session.
	clients map[string]int
	calls   []string
}

func (f *fakeSessions) run(_ context.Context, args ...string) (string, string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case args[0] == "--version":
		return "zellij 0.45.1\n", "", nil
	case args[0] == "list-sessions":
		if f.listed == "" {
			return "", "No active zellij sessions found.", errors.New("exit status 1")
		}
		return f.listed, "", nil
	case args[0] == "delete-session":
		// The session leaves the list, as zellij would.
		var kept []string
		for _, line := range strings.SplitAfter(f.listed, "\n") {
			if !strings.HasPrefix(line, args[1]+" ") {
				kept = append(kept, line)
			}
		}
		f.listed = strings.Join(kept, "")
		return "", "", nil
	case len(args) == 4 && args[3] == "list-clients":
		out := "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n"
		for i := range f.clients[args[1]] {
			out += fmt.Sprintf("%d terminal_0 bash\n", i+1)
		}
		return out, "", nil
	case len(args) == 5 && args[3] == "rename-session":
		// The new name is listed from now on, as zellij would.
		f.listed += args[4] + " [Created 1m ago]\n"
		return "", "", nil
	}
	return "", "unexpected call", errors.New("exit status 2")
}

// did reports whether a call starting with prefix was made.
func (f *fakeSessions) did(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// handApp is fakeApp on fake sessions, with the name lists a pass keeps, and
// no real zellij socket in sight.
func handApp(t *testing.T, z *fakeSessions) *app.App {
	t.Helper()
	home := isolate(t)
	t.Setenv("ZELLIJ_SOCKET_DIR", t.TempDir())
	a := fakeApp(t, config.MapEnv{"HOME": home})
	a.Zellij.Run = z.run
	for _, l := range []struct {
		path string
		to   **protect.List
	}{{config.KeptFile(a.Env), &a.Reaper.Kept}, {config.GivenFile(a.Env), &a.Reaper.Given}} {
		list, err := protect.Load(l.path)
		if err != nil {
			t.Fatal(err)
		}
		*l.to = list
	}
	return a
}

// logged is the decision log so far.
func logged(t *testing.T, a *app.App) string {
	t.Helper()
	b, _ := os.ReadFile(a.Config.LogPath)
	return string(b)
}

func TestDeleteReapsWhatNobodyUsesAndSaysWhatItKept(t *testing.T) {
	z := &fakeSessions{
		listed:  "notes [Created 2h ago] \nbusy [Created 1h ago] \nold [Created 5d ago] (EXITED - attach to resurrect)\n",
		clients: map[string]int{"busy": 1},
	}
	a := handApp(t, z)
	cmd, out := output()
	err := reapByHand(cmd, tui.NewCommandLine(a), []string{"notes", "busy", "old"}, false)
	if err == nil || err.Error() != "1 of 3 sessions not reaped" {
		t.Errorf("error %v, want 1 of 3 not reaped", err)
	}
	// zellij is asked right before, as from the dashboard: the session with
	// a client stays, the exited one has no server to ask.
	for _, name := range []string{"notes", "old"} {
		if !z.did("delete-session " + name) {
			t.Errorf("%s not reaped: %v", name, z.calls)
		}
	}
	if z.did("delete-session busy") {
		t.Error("reaped a session with a client attached")
	}
	if !strings.Contains(out.String(), "Could not reap busy: a client is attached now") {
		t.Errorf("output:\n%s", out)
	}
	if log := logged(t, a); !strings.Contains(log, "notes :: deleted from the command line") {
		t.Errorf("log:\n%s", log)
	}
}

func TestDeleteAsksAsTheDashboardDoes(t *testing.T) {
	z := &fakeSessions{listed: "notes [Created 2h ago] \nold [Created 5d ago] (EXITED - attach to resurrect)\n"}
	a := handApp(t, z)
	cmd, out := output()
	cmd.SetIn(strings.NewReader("y\nn\n"))
	err := reapByHand(cmd, tui.NewCommandLine(a), []string{"old", "notes", "nobody"}, true)
	// A no is the person's choice; only the session nobody lists fails.
	if err == nil || err.Error() != "1 of 3 sessions not reaped" {
		t.Errorf("error %v, want 1 of 3 not reaped", err)
	}
	s := out.String()
	for _, want := range []string{
		"Reap old now? Its resurrection data is removed, so it cannot be resurrected. [y/N]",
		"Kept notes",
		"Not reaped: nobody is not in the latest list of sessions",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	if !z.did("delete-session old") || z.did("delete-session notes") {
		t.Errorf("calls: %v", z.calls)
	}
}

func TestDeleteNeedsYesWithoutATerminal(t *testing.T) {
	isolate(t)
	root := newRoot()
	root.SetArgs([]string{"delete", "notes"})
	root.SetOut(&strings.Builder{})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("delete with nobody to answer: %v", err)
	}
}

func TestRenameFromTheCommandLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zellij on Windows cannot reach a session after a rename")
	}
	z := &fakeSessions{listed: "notes [Created 2h ago] \nold [Created 5d ago] (EXITED - attach to resurrect)\n"}
	a := handApp(t, z)
	b := tui.NewCommandLine(a)
	cmd, out := output()
	if err := renameByHand(cmd, a, b, "notes", "release-notes"); err != nil {
		t.Fatal(err)
	}
	if !z.did("--session notes action rename-session release-notes") || !strings.Contains(out.String(), "Renamed notes to release-notes") {
		t.Errorf("calls %v, output:\n%s", z.calls, out)
	}
	if log := logged(t, a); !strings.Contains(log, "notes :: renamed from the command line to release-notes") {
		t.Errorf("log:\n%s", log)
	}
	// As in the dashboard: an exited session and one nobody lists are refused.
	for name, want := range map[string]string{"old": "An exited session cannot be renamed", "nobody": "no session is called nobody"} {
		if err := renameByHand(cmd, a, b, name, "x"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("rename %s: %v, want %q", name, err, want)
		}
	}
	// The name it has already: kept as it is.
	cmd, out = output()
	if err := renameByHand(cmd, a, b, "release-notes", "release-notes"); err != nil {
		t.Fatal(err)
	}
	if !a.Reaper.Kept.Has("release-notes") || !strings.Contains(out.String(), "release-notes keeps this name") {
		t.Errorf("kept %v, output:\n%s", a.Reaper.Kept.Names(), out)
	}
}
