package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/tui"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
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
		var out strings.Builder
		out.WriteString("CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n")
		for i := range f.clients[args[1]] {
			fmt.Fprintf(&out, "%d terminal_0 bash\n", i+1)
		}
		return out.String(), "", nil
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

// fakeBackend is the dashboard's backend with the plan given: it records the
// sessions reaped, and fails the ones named in fail.
type fakeBackend struct {
	tui.Backend
	report  reaper.Report
	fail    map[string]error
	deleted []string
}

func (f *fakeBackend) Plan(context.Context) (reaper.Report, error) { return f.report, nil }

func (f *fakeBackend) Delete(_ context.Context, name string) error {
	if err := f.fail[name]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, name)
	return nil
}

// set changes a session in the plan: attached says whether a client is
// attached, clients how many (-1 when nobody could ask).
func (f *fakeBackend) set(name string, attached policy.Tri, clients int) {
	for i, o := range f.report.Outcomes {
		if o.Session.Name() == name {
			f.report.Outcomes[i].Session.Facts.Attached, f.report.Outcomes[i].Session.Clients = attached, clients
		}
	}
}

// outcome is one session in a plan, running and unattached or exited.
func outcome(name string, exited bool, code policy.Code) reaper.Outcome {
	s := inspect.Session{Listed: zellij.Listed{Name: name, Exited: exited}, Facts: policy.Facts{Name: name, Exited: exited}}
	if exited {
		s.Facts.Attached, s.Clients = policy.Unknown, -1
	}
	return reaper.Outcome{Session: s, Verdict: policy.Verdict{Code: code}}
}

// answers gives one line per read and calls before(i) as line i is asked
// for: what happens while a question waits.
type answers struct {
	lines  []string
	before func(i int)
	i      int
}

func (a *answers) Read(p []byte) (int, error) {
	if a.i >= len(a.lines) {
		return 0, io.EOF
	}
	if a.before != nil {
		a.before(a.i)
	}
	n := copy(p, a.lines[a.i]+"\n")
	a.i++
	return n, nil
}

func TestDeleteKeepsWhatTheDashboardWouldNot(t *testing.T) {
	// The dashboard refuses from its report before zellij is asked again: a
	// client attached, attach state nobody could read (as when the server
	// was not found), a session not listed. delete once skipped that and
	// went by list-clients alone.
	isolate(t)
	f := &fakeBackend{report: reaper.Report{Outcomes: []reaper.Outcome{
		outcome("notes", false, policy.IdleOld),
		outcome("web", false, policy.Attached),
		outcome("unsure", false, policy.AttachUnknown),
		outcome("lost", false, policy.NoServer),
		outcome("old", true, policy.ExitedOld),
		outcome("busy-now", false, policy.IdleOld),
	}}, fail: map[string]error{"busy-now": errors.New("a client is attached now")}}
	f.set("web", policy.Yes, 1)
	f.set("unsure", policy.Unknown, -1)
	f.set("lost", policy.No, -1)
	cmd, out := output()
	err := reapByHand(cmd, f, []string{"notes", "web", "unsure", "lost", "old", "nobody", "busy-now"}, false)
	if err == nil || err.Error() != "5 of 7 sessions not reaped" {
		t.Errorf("error %v, want 5 of 7 not reaped", err)
	}
	if !slices.Equal(f.deleted, []string{"notes", "old"}) {
		t.Errorf("reaped %v, want notes and old", f.deleted)
	}
	for _, want := range []string{
		"web has a client attached: detach it first.",
		"Not reaped: nobody can tell whether a client is attached to unsure",
		"Not reaped: nobody can tell whether a client is attached to lost",
		"Not reaped: nobody is not in the latest list of sessions",
		// What zellij says right before still counts.
		"Could not reap busy-now: a client is attached now",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDeleteAsksAsTheDashboardDoes(t *testing.T) {
	isolate(t)
	f := &fakeBackend{report: reaper.Report{Outcomes: []reaper.Outcome{
		outcome("old", true, policy.ExitedOld),
		outcome("notes", false, policy.IdleOld),
		outcome("web", false, policy.Attached),
		outcome("late", false, policy.IdleOld),
	}}}
	f.set("web", policy.Yes, 1)
	cmd, out := output()
	// Somebody attaches to late while its question waits.
	cmd.SetIn(&answers{lines: []string{"y", "n", "y"}, before: func(i int) {
		if i == 2 {
			f.set("late", policy.Yes, 1)
		}
	}})
	err := reapByHand(cmd, f, []string{"old", "notes", "web", "late"}, true)
	// A no is the person's choice; web and late have clients.
	if err == nil || err.Error() != "2 of 4 sessions not reaped" {
		t.Errorf("error %v, want 2 of 4 not reaped", err)
	}
	s := out.String()
	for _, want := range []string{
		"Reap old now? Its resurrection data is removed, so it cannot be resurrected. [y/N]",
		"Kept notes",
		"web has a client attached: detach it first.",
		"late has a client attached: detach it first.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
	// web, attached already, is not even asked about.
	if strings.Contains(s, "Reap web now?") || !slices.Equal(f.deleted, []string{"old"}) {
		t.Errorf("reaped %v, output:\n%s", f.deleted, s)
	}
}

func TestDeleteLogsThatItCameFromTheCommandLine(t *testing.T) {
	z := &fakeSessions{listed: "old [Created 5d ago] (EXITED - attach to resurrect)\n"}
	a := handApp(t, z)
	cmd, _ := output()
	if err := reapByHand(cmd, tui.NewCommandLine(a), []string{"old"}, false); err != nil {
		t.Fatal(err)
	}
	if !z.did("delete-session old") {
		t.Errorf("calls: %v", z.calls)
	}
	if log := logged(t, a); !strings.Contains(log, "old :: deleted from the command line") {
		t.Errorf("log:\n%s", log)
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

func TestDeleteWithNoNameAsksForOne(t *testing.T) {
	isolate(t)
	// In the words unprotect and forget-name use, not a count of arguments,
	// and before anything is read: with --yes there is nobody to ask either.
	for _, args := range [][]string{{"delete"}, {"delete", "--yes"}} {
		if out, err := runRoot(t, args...); err == nil || err.Error() != "name a session to reap" {
			t.Errorf("%v: %v\n%s", args, err, out)
		}
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
	// A line break never reaches zellij: the dashboard's field turns it into
	// a space, and a name split in two would list as two sessions.
	if err := renameByHand(cmd, a, b, "release-notes", "a\nb"); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Errorf("rename to a name with a line break: %v", err)
	}
	if z.did("--session release-notes action rename-session") {
		t.Errorf("zellij was asked: %v", z.calls)
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
