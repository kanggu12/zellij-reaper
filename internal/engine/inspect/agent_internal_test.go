package inspect

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// markOf is a process's start mark, which the test fails without.
func markOf(t *testing.T, root string, pid int) int64 {
	t.Helper()
	m, ok := procfs.StartMark(root, pid)
	if !ok {
		t.Fatalf("no start for %d", pid)
	}
	return m
}

// readTable reads the fake machine.
func readTable(t *testing.T, fp *testutil.FakeProc) *procfs.Table {
	t.Helper()
	table, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

// only is the agent of a session that has one, which the test fails without.
func only(t *testing.T, ws []AgentWork) AgentWork {
	t.Helper()
	if len(ws) != 1 {
		t.Fatalf("agents %+v, want one", ws)
	}
	return ws[0]
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
	// The agent's commands: one in web-app that runs go, a newer one in
	// billing, the newest in a folder that is no repository, and one in
	// docs-site that has ended and waits to be collected.
	fp.Add(testutil.Proc{PID: 110, PPID: 102, Comm: "bash", Argv: []string{"bash", "-c", "go test ./..."}, Cwd: filepath.Join(ws, "web-app/src"), Started: 50 * time.Second})
	fp.Add(testutil.Proc{PID: 111, PPID: 110, Comm: "go", Argv: []string{"go", "test", "./..."}, Cwd: filepath.Join(ws, "web-app/src"), Started: 55 * time.Second})
	fp.Add(testutil.Proc{PID: 112, PPID: 102, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "billing"), Started: 60 * time.Second})
	fp.Add(testutil.Proc{PID: 113, PPID: 102, Comm: "ls", Argv: []string{"ls"}, Cwd: filepath.Join(ws, "notes"), Started: 70 * time.Second})
	fp.Add(testutil.Proc{PID: 114, PPID: 102, Comm: "git", State: "Z", Cwd: filepath.Join(ws, "docs-site"), Started: 80 * time.Second})
	// An editor in docs-site: no agent, so where it is says nothing here.
	fp.Add(testutil.Proc{PID: 120, PPID: 100, Comm: "vim", Argv: []string{"vim"}, Stdin: tty, Cwd: filepath.Join(ws, "docs-site"), Started: 4 * time.Second})
	table := readTable(t, fp)
	w := only(t, agentsAt(table, panes(table, 100, shellSet(nil)), []string{"claude", "codex"}, &repo.Finder{}, fp.Root))
	// Each command counts from when it started: go under the one in web-app
	// is part of it.
	want := []Sighting{{filepath.Join(ws, "billing"), markOf(t, fp.Root, 112)}, {filepath.Join(ws, "web-app"), markOf(t, fp.Root, 110)}}
	if w.Program != "claude" || w.Started != "" || !slices.Equal(w.Work, want) {
		t.Errorf("agent work %+v, want claude, no start, %+v", w, want)
	}
	// It is told from an agent started later by its pid and start.
	if id := "102:" + strconv.FormatInt(markOf(t, fp.Root, 102), 10); w.ID != id {
		t.Errorf("id %q, want %q", w.ID, id)
	}
	// With the agent out of the list, nothing here is an agent's.
	if ws := agentsAt(table, panes(table, 100, shellSet(nil)), []string{"codex"}, &repo.Finder{}, fp.Root); len(ws) != 0 {
		t.Errorf("no agent: %+v", ws)
	}
}

func TestACommandThatKeepsStartingProcessesIsOneCommand(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	fp.Add(testutil.Proc{PID: 150, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/v"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 151, PPID: 150, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: ws, Started: 2 * time.Second})
	// A loop it left running in web-app, which has just started another
	// sleep, and a language server started in the folder of projects that
	// has just run go in docs-site; then a build in billing, newer than both
	// as commands, older than what they started.
	fp.Add(testutil.Proc{PID: 160, PPID: 151, Comm: "bash", Argv: []string{"bash", "-c", "until done; do sleep 5; done"}, Cwd: filepath.Join(ws, "web-app"), Started: 30 * time.Second})
	fp.Add(testutil.Proc{PID: 161, PPID: 160, Comm: "sleep", Argv: []string{"sleep", "5"}, Cwd: filepath.Join(ws, "web-app"), Started: 290 * time.Second})
	fp.Add(testutil.Proc{PID: 170, PPID: 151, Comm: "gopls", Argv: []string{"gopls"}, Cwd: ws, Started: 40 * time.Second})
	fp.Add(testutil.Proc{PID: 171, PPID: 170, Comm: "go", Argv: []string{"go", "list"}, Cwd: filepath.Join(ws, "docs-site"), Started: 295 * time.Second})
	fp.Add(testutil.Proc{PID: 180, PPID: 151, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "billing"), Started: 100 * time.Second})
	table := readTable(t, fp)
	w := only(t, agentsAt(table, panes(table, 150, shellSet(nil)), []string{"claude"}, &repo.Finder{}, fp.Root))
	want := []Sighting{
		{filepath.Join(ws, "billing"), markOf(t, fp.Root, 180)},
		{filepath.Join(ws, "docs-site"), markOf(t, fp.Root, 170)},
		{filepath.Join(ws, "web-app"), markOf(t, fp.Root, 160)},
	}
	if !slices.Equal(w.Work, want) {
		t.Errorf("work %+v, want %+v", w.Work, want)
	}
}

func TestABigOldCommandLeavesTheNewOnesSeen(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	fp.Add(testutil.Proc{PID: 500, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/x"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 501, PPID: 500, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: ws, Started: 2 * time.Second})
	// A build server it started long ago in web-app, with more workers than
	// a survey reads, and a newer command in billing, with a higher pid.
	fp.Add(testutil.Proc{PID: 510, PPID: 501, Comm: "bash", Argv: []string{"bash", "-c", "make serve"}, Cwd: filepath.Join(ws, "web-app"), Started: 20 * time.Second})
	for i := range maxAgentProcs {
		fp.Add(testutil.Proc{PID: 1000 + i, PPID: 510, Comm: "worker", Argv: []string{"worker"}, Cwd: filepath.Join(ws, "web-app"), Started: 21 * time.Second})
	}
	fp.Add(testutil.Proc{PID: 5000, PPID: 501, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "billing"), Started: 100 * time.Second})
	table := readTable(t, fp)
	w := only(t, agentsAt(table, panes(table, 500, shellSet(nil)), []string{"claude"}, &repo.Finder{}, fp.Root))
	want := []Sighting{{filepath.Join(ws, "billing"), markOf(t, fp.Root, 5000)}, {filepath.Join(ws, "web-app"), markOf(t, fp.Root, 510)}}
	if !slices.Equal(w.Work, want) {
		t.Errorf("work %+v, want %+v", w.Work, want)
	}
}

func TestAnAgentStartedInAProjectWorksThere(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	// The pane runs the agent itself, started in billing, and nothing under it.
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/t"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 201, PPID: 200, Comm: "codex", Argv: []string{"codex"}, Stdin: tty, Cwd: filepath.Join(ws, "billing"), Started: 2 * time.Second})
	table := readTable(t, fp)
	w := only(t, agentsAt(table, panes(table, 200, shellSet(nil)), []string{"claude", "codex"}, &repo.Finder{}, fp.Root))
	if w.Program != "codex" || w.Started != filepath.Join(ws, "billing") || len(w.Work) != 0 {
		t.Errorf("agent work %+v", w)
	}
}

func TestEachPanesAgentIsSeenOnItsOwn(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	// Two panes, an agent in each, at work in two projects: neither one's
	// commands are the other's.
	fp.Add(testutil.Proc{PID: 300, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/u"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 301, PPID: 300, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: ws, Started: 2 * time.Second})
	fp.Add(testutil.Proc{PID: 302, PPID: 300, Comm: "codex", Argv: []string{"codex"}, Stdin: tty, Cwd: ws, Started: 3 * time.Second})
	fp.Add(testutil.Proc{PID: 310, PPID: 301, Comm: "make", Argv: []string{"make"}, Cwd: filepath.Join(ws, "web-app"), Started: 50 * time.Second})
	fp.Add(testutil.Proc{PID: 311, PPID: 302, Comm: "make", Argv: []string{"make"}, Cwd: filepath.Join(ws, "billing"), Started: 60 * time.Second})
	table := readTable(t, fp)
	got := agentsAt(table, panes(table, 300, shellSet(nil)), []string{"claude", "codex"}, &repo.Finder{}, fp.Root)
	if len(got) != 2 ||
		got[0].Program != "claude" || len(got[0].Work) != 1 || got[0].Work[0].Repo != filepath.Join(ws, "web-app") ||
		got[1].Program != "codex" || len(got[1].Work) != 1 || got[1].Work[0].Repo != filepath.Join(ws, "billing") {
		t.Errorf("agents %+v", got)
	}
}

func TestAnAgentStartedAgainUnderItselfWorksThroughItsChild(t *testing.T) {
	ws := projectsDir(t)
	fp := testutil.NewFakeProc(t)
	tty := testutil.TTY(t, t.TempDir(), "pane", time.Now())
	// An agent that starts itself again with more memory, as its only
	// child, whose commands run under the child.
	fp.Add(testutil.Proc{PID: 400, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/w"}, Started: time.Second})
	fp.Add(testutil.Proc{PID: 401, PPID: 400, Comm: "node", Argv: []string{"node", "/usr/local/bin/gemini"}, Stdin: tty, Cwd: ws, Started: 2 * time.Second})
	fp.Add(testutil.Proc{PID: 402, PPID: 401, Comm: "node", Argv: []string{"/usr/bin/node", "--max-old-space-size=8192", "/usr/local/bin/gemini"}, Stdin: tty, Cwd: ws, Started: 3 * time.Second})
	fp.Add(testutil.Proc{PID: 410, PPID: 402, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "billing"), Started: 50 * time.Second})
	fp.Add(testutil.Proc{PID: 411, PPID: 402, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "web-app"), Started: 60 * time.Second})
	table := readTable(t, fp)
	w := only(t, agentsAt(table, panes(table, 400, shellSet(nil)), []string{"claude", "gemini"}, &repo.Finder{}, fp.Root))
	want := []Sighting{{filepath.Join(ws, "web-app"), markOf(t, fp.Root, 411)}, {filepath.Join(ws, "billing"), markOf(t, fp.Root, 410)}}
	if w.Program != "gemini" || !slices.Equal(w.Work, want) {
		t.Errorf("agent work %+v, want gemini, %+v", w, want)
	}
	// The agent as found names it, whatever runs under it.
	if id := "401:" + strconv.FormatInt(markOf(t, fp.Root, 401), 10); w.ID != id {
		t.Errorf("id %q, want %q", w.ID, id)
	}
	// An agent with a child of its own name next to its commands runs them
	// itself: claude serving as a tool's server is one of its children.
	fp.Add(testutil.Proc{PID: 450, PPID: 400, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: ws, Started: 4 * time.Second})
	fp.Add(testutil.Proc{PID: 451, PPID: 450, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: filepath.Join(ws, "docs-site"), Started: 70 * time.Second})
	fp.Add(testutil.Proc{PID: 452, PPID: 450, Comm: "claude", Argv: []string{"claude", "mcp", "serve"}, Cwd: ws, Started: 5 * time.Second})
	table = readTable(t, fp)
	if w := only(t, agentsAt(table, []Pane{{PID: 450}}, []string{"claude"}, &repo.Finder{}, fp.Root)); len(w.Work) != 1 || w.Work[0].Repo != filepath.Join(ws, "docs-site") {
		t.Errorf("an agent with its own name as a child: %+v", w)
	}
}

func TestWhatIsAnAgent(t *testing.T) {
	agents := []string{"claude", "codex", "gemini", "aider"}
	for _, c := range []struct {
		comm string
		argv []string
		want string
	}{
		{"claude", []string{"claude", "--dangerously-skip-permissions"}, "claude"},
		{"node", []string{"node", "/usr/local/bin/claude"}, "claude"},
		{"node", []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, "claude"},
		{"node", []string{"/usr/bin/node", "--max-old-space-size=8192", "/usr/local/bin/gemini"}, "gemini"},
		{"python3.12", []string{"python3.12", "-m", "aider"}, "aider"},
		// A script run by its shebang: the kernel names it after the file.
		{"codex", []string{"/bin/sh", "/usr/local/sbin/codex"}, "codex"},
		// Installed with npm, scoped or not, on Linux or Windows.
		{"node", []string{"node", "/usr/local/lib/node_modules/@openai/codex/bin/codex.js"}, "codex"},
		{"node", []string{"node", "/usr/local/lib/node_modules/@google/gemini-cli/dist/index.js"}, "gemini"},
		{"node", []string{"node", `C:\Users\me\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\cli.js`}, "claude"},
		{"python3", []string{"python3", "-m", "aider.main"}, "aider"},
		// A folder of someone's own with an agent's name in it.
		{"python3", []string{"python3", "/root/claude-workspace/web/server.py"}, ""},
		{"bash", []string{"bash", "/home/me/codex-notes/run.sh"}, ""},
		{"node", []string{"node", "/srv/gemini-playground/node_modules/vite/bin/vite.js"}, ""},
		// Another program's argument is a file it works on.
		{"vim", []string{"vim", "codex"}, ""},
		{"less", []string{"less", "claude.md"}, ""},
		{"man", []string{"man", "claude"}, ""},
		// Code a shell is given to run is no script.
		{"bash", []string{"bash", "-c", "claude"}, ""},
		{"bash", []string{"-bash"}, ""},
	} {
		p := &procfs.Proc{PID: 1, Comm: c.comm, Argv: c.argv}
		if got := agentName(p, agents); got != c.want {
			t.Errorf("%v: %q, want %q", c.argv, got, c.want)
		}
	}
}
