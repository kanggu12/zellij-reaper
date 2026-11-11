package inspect

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/repo"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

// projectsDir makes a folder of projects, in no repository itself: web-app,
// billing and docs-site are checkouts, notes is not.
func projectsDir(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, d := range []string{"web-app/.git", "web-app/src", "billing/.git", "docs-site/.git", "notes"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return ws
}

func TestAnAgentWorksWhereItsNewestCommandsRun(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	// A server with two panes: a shell an agent was started from, in the
	// folder of projects, and an editor.
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/s"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}, Stdin: tty, Cwd: ws, Started: 2 * time.Second})
	fp.Add(testutil.Proc{PID: 102, PPID: 101, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: ws, Started: 3 * time.Second})
	// The agent's commands: two in web-app, one newer in billing, the newest
	// in a folder that is no repository, and one in docs-site that has ended
	// and waits to be collected.
	fp.Add(testutil.Proc{PID: 110, PPID: 102, Comm: "bash", Argv: []string{"bash", "-c", "go test ./..."}, Cwd: filepath.Join(ws, "web-app/src"), Started: 50 * time.Second})
	fp.Add(testutil.Proc{PID: 111, PPID: 110, Comm: "go", Argv: []string{"go", "test", "./..."}, Cwd: filepath.Join(ws, "web-app/src"), Started: 55 * time.Second})
	fp.Add(testutil.Proc{PID: 112, PPID: 102, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "billing"), Started: 60 * time.Second})
	fp.Add(testutil.Proc{PID: 113, PPID: 102, Comm: "ls", Argv: []string{"ls"}, Cwd: filepath.Join(ws, "notes"), Started: 70 * time.Second})
	fp.Add(testutil.Proc{PID: 114, PPID: 102, Comm: "git", State: "Z", Cwd: filepath.Join(ws, "docs-site"), Started: 80 * time.Second})
	// An editor in docs-site: no agent, so where it is says nothing here.
	fp.Add(testutil.Proc{PID: 120, PPID: 100, Comm: "vim", Argv: []string{"vim"}, Stdin: tty, Cwd: filepath.Join(ws, "docs-site"), Started: 4 * time.Second})
	table, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	mark := func(pid int) int64 {
		m, ok := procfs.StartMark(fp.Root, pid)
		if !ok {
			t.Fatalf("no start for %d", pid)
		}
		return m
	}
	w := agentWork(table, panes(table, 100, shellSet(nil)), []string{"claude", "codex"}, &repo.Finder{}, fp.Root)
	want := []Sighting{{filepath.Join(ws, "billing"), mark(112)}, {filepath.Join(ws, "web-app"), mark(111)}}
	if w.Program != "claude" || w.Started != "" || !slices.Equal(w.Work, want) {
		t.Errorf("agent work %+v, want claude, no start, %+v", w, want)
	}
	// With the agent out of the list, nothing here is an agent's.
	if w := agentWork(table, panes(table, 100, shellSet(nil)), []string{"codex"}, &repo.Finder{}, fp.Root); w.Program != "" || len(w.Work) != 0 {
		t.Errorf("no agent: %+v", w)
	}
}

func TestAnAgentStartedInAProjectWorksThere(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	// The pane runs the agent itself, started in billing, and nothing under it.
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/t"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 201, PPID: 200, Comm: "codex", Argv: []string{"codex"}, Stdin: tty, Cwd: filepath.Join(ws, "billing"), Started: 2 * time.Second})
	table, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	w := agentWork(table, panes(table, 200, shellSet(nil)), []string{"claude", "codex"}, &repo.Finder{}, fp.Root)
	if w.Program != "codex" || w.Started != filepath.Join(ws, "billing") || len(w.Work) != 0 {
		t.Errorf("agent work %+v", w)
	}
}
