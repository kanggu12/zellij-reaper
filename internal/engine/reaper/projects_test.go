package reaper_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

// agentPID is a coding agent the tests start in the busy session, whose
// server is pid 200: a second pane, next to htop.
const agentPID = 210

// projects is a folder of projects in no repository of its own: web-app and
// billing are checkouts, notes is not.
type projects struct{ root, webApp, billing, notes string }

func newProjects(t *testing.T) projects {
	t.Helper()
	root := t.TempDir()
	p := projects{root: root, webApp: filepath.Join(root, "web-app"), billing: filepath.Join(root, "billing"), notes: filepath.Join(root, "notes")}
	for _, d := range []string{"web-app/.git", "web-app/src", "billing/.git", "notes"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// agentWorld is the usual world with its project book, and an agent started
// in dir in the busy session.
func agentWorld(t *testing.T, dir string) *world {
	t.Helper()
	return agentWorldIn(t, newWorld(t), dir)
}

// agentWorldIn is agentWorld made from another world.
func agentWorldIn(t *testing.T, w *world, dir string) *world {
	t.Helper()
	w.env.ProjectsPath = filepath.Join(t.TempDir(), "projects")
	tty := testutil.TTY(t, t.TempDir(), "agent", time.Now())
	w.fp.Add(testutil.Proc{PID: agentPID, PPID: 200, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: dir, Started: 10 * time.Second})
	return w
}

// command starts a command of the agent's in dir, at the given time after boot.
func command(w *world, pid int, dir string, at time.Duration) {
	w.fp.Add(testutil.Proc{PID: pid, PPID: agentPID, Comm: "bash", Argv: []string{"bash", "-c", "make"}, Cwd: dir, Started: at})
}

// pass runs a pass that renames nothing, so the busy session keeps its name
// and its socket, and the book is all that changes.
func pass(t *testing.T, w *world) {
	t.Helper()
	w.env.Config.AutoRename = false
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
}

// suggested is the name a pass would give the busy session now.
func suggested(t *testing.T, w *world) string {
	t.Helper()
	w.env.Config.AutoRename = true
	r, err := reaper.Plan(context.Background(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "busy" {
			return o.Suggested
		}
	}
	t.Fatal("no busy session")
	return ""
}

func TestAnAgentSessionFollowsTheProjectItWorksIn(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	// What it is named without a project: its folder and what it runs.
	base := suggested(t, w)
	// One command in web-app: maybe a look into it, not yet the project.
	command(w, 220, filepath.Join(p.webApp, "src"), 100*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != base {
		t.Errorf("after one command: %q", got)
	}
	// A newer one there too: it works on web-app.
	w.fp.Remove(220)
	command(w, 221, p.webApp, 200*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "web-app" {
		t.Errorf("after two: %q", got)
	}
	// Between commands it still does.
	w.fp.Remove(221)
	pass(t, w)
	if got := suggested(t, w); got != "web-app" {
		t.Errorf("idle: %q", got)
	}
	// It moves on to billing: one command is not enough, two are.
	command(w, 222, p.billing, 300*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "web-app" {
		t.Errorf("after one in billing: %q", got)
	}
	w.fp.Remove(222)
	command(w, 223, p.billing, 400*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "billing" {
		t.Errorf("after two in billing: %q", got)
	}
	// The agent ends: the session is named after what else it shows, and the
	// book forgets it.
	w.fp.Remove(223)
	w.fp.Remove(agentPID)
	pass(t, w)
	if got := suggested(t, w); got != "htop" {
		t.Errorf("after the agent: %q", got)
	}
	if book := readBook(t, w); len(book.Sessions) != 0 {
		t.Errorf("book kept %v", book.Sessions)
	}
}

func TestAnAgentGoingThroughManyProjectsKeepsItsName(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	base := suggested(t, w)
	// One command each, by turns: no project twice in a row.
	for i, dir := range []string{p.webApp, p.billing, p.webApp, p.billing} {
		pid := 230 + i
		command(w, pid, dir, time.Duration(100*(i+1))*time.Second)
		pass(t, w)
		w.fp.Remove(pid)
		if got := suggested(t, w); got != base {
			t.Errorf("after command %d: %q", i+1, got)
		}
	}
}

func TestAProcessThatKeepsRunningCountsOnce(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	base := suggested(t, w)
	// A dev server it started in web-app, seen by three passes: one command.
	command(w, 240, p.webApp, 100*time.Second)
	for range 3 {
		pass(t, w)
	}
	if got := suggested(t, w); got != base {
		t.Errorf("one long command: %q", got)
	}
	// A new command there settles it.
	command(w, 241, p.webApp, 200*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "web-app" {
		t.Errorf("a new one: %q", got)
	}
	// The dev server, older than what was counted, does not take it back
	// from billing.
	command(w, 242, p.billing, 300*time.Second)
	pass(t, w)
	w.fp.Remove(242)
	command(w, 243, p.billing, 400*time.Second)
	pass(t, w)
	w.fp.Remove(243)
	pass(t, w)
	if got := suggested(t, w); got != "billing" {
		t.Errorf("with the dev server still up: %q", got)
	}
}

func TestAnAgentStartedInAProjectIsNamedAfterItAtOnce(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.billing)
	// Before any pass and any command: where it was started.
	if got := suggested(t, w); got != "billing" {
		t.Errorf("plan: %q", got)
	}
	// A folder that is no repository says nothing: the session keeps the
	// name it has from its folder and what it runs.
	w = agentWorld(t, p.notes)
	base := suggested(t, w)
	command(w, 250, p.notes, 100*time.Second)
	pass(t, w)
	command(w, 251, p.notes, 200*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != base || base != "notes-htop" {
		t.Errorf("no repository: %q, before %q", got, base)
	}
}

// readBook reads the project book a pass wrote.
func readBook(t *testing.T, w *world) struct {
	Version  int
	Sessions map[string]map[string]any
} {
	t.Helper()
	var book struct {
		Version  int
		Sessions map[string]map[string]any
	}
	raw, err := os.ReadFile(w.env.ProjectsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &book); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return book
}

func TestTheProjectBookIsAPassToWrite(t *testing.T) {
	p := newProjects(t)
	// plan and the dashboard only read it.
	w := agentWorld(t, p.root)
	command(w, 260, p.webApp, 100*time.Second)
	suggested(t, w)
	if _, err := os.Stat(w.env.ProjectsPath); !os.IsNotExist(err) {
		t.Errorf("plan wrote the book: %v", err)
	}
	// A dry run writes it, as it writes the quiet book: bookkeeping.
	w.env.Config.DryRun = true
	pass(t, w)
	book := readBook(t, w)
	if book.Version != 1 || len(book.Sessions) != 1 {
		t.Errorf("book %+v", book)
	}
	// The session by its server and the name it started under, which a
	// rename does not change.
	for key, e := range book.Sessions {
		if key != "200\t0\tquiet-mouse" || e["last"] != p.webApp {
			t.Errorf("entry %q: %v", key, e)
		}
	}
	// It is this user's own.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(w.env.ProjectsPath); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("mode %v: %v", info.Mode().Perm(), err)
		}
	}
	// The names pass writes it whether or not it renames, in a folder it
	// makes when none is there.
	w = agentWorld(t, p.root)
	w.env.ProjectsPath = filepath.Join(t.TempDir(), "state", "projects")
	w.env.Config.AutoRename = true
	w.zj.refuse["htop"] = true
	command(w, 261, p.webApp, 100*time.Second)
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if book := readBook(t, w); len(book.Sessions) != 1 {
		t.Errorf("names pass book %+v", book)
	}
}

func TestTheProjectBookForgetsWhatNoPassSees(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	// An entry for a server long gone, and one for a server seen an hour ago.
	day, hour := time.Now().Add(-25*time.Hour).Unix(), time.Now().Add(-time.Hour).Unix()
	book := `{"version":1,"sessions":{"999\tgone":{"project":"/srv/x","seen":` + itoa(day) + `},"998\tsoon":{"project":"/srv/y","seen":` + itoa(hour) + `}}}`
	if err := os.WriteFile(w.env.ProjectsPath, []byte(book), 0o600); err != nil {
		t.Fatal(err)
	}
	command(w, 270, p.webApp, 100*time.Second)
	pass(t, w)
	got := readBook(t, w)
	if _, ok := got.Sessions["999\tgone"]; ok {
		t.Errorf("kept a session gone for a day: %v", got.Sessions)
	}
	if _, ok := got.Sessions["998\tsoon"]; !ok {
		t.Errorf("forgot a session seen an hour ago: %v", got.Sessions)
	}
	// A book that cannot be read is an empty one, and a pass writes a good one.
	if err := os.WriteFile(w.env.ProjectsPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pass(t, w)
	if got := readBook(t, w); len(got.Sessions) != 1 {
		t.Errorf("after a broken book: %v", got.Sessions)
	}
}

// writeBook writes a project book with one session's entry.
func writeBook(t *testing.T, w *world, key string, entry map[string]any) {
	t.Helper()
	entry["seen"] = time.Now().Unix()
	raw, err := json.Marshal(map[string]any{"version": 1, "sessions": map[string]any{key: entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.env.ProjectsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// busyKey is the busy session's key in the book: its server's pid, when the
// server started, and the name it started under.
func busyKey(start int64) string {
	return "200\t" + strconv.FormatInt(start, 10) + "\tquiet-mouse"
}

func TestANewServerWithAnOldPidStartsOver(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	// Its server started 7 s after boot.
	w.fp.Remove(200)
	w.fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(w.sockDir, "quiet-mouse")}, Sockets: []uint32{200}, Started: 7 * time.Second})
	base := suggested(t, w)
	start, ok := procfs.StartMark(w.fp.Root, 200)
	if !ok {
		t.Fatal("no start for the server")
	}
	// The book names this server's session after billing.
	writeBook(t, w, busyKey(start), map[string]any{"project": p.billing})
	if got := suggested(t, w); got != "billing" {
		t.Fatalf("this server's entry: %q", got)
	}
	// From before a reboot: a server with the same pid and first name that
	// started at another moment, and counted a command started far later
	// than anything on this boot. It is another session.
	writeBook(t, w, busyKey(start+1), map[string]any{"project": p.billing, "last": p.webApp, "mark": int64(1) << 60})
	if got := suggested(t, w); got != base {
		t.Errorf("took the old server's project: %q", got)
	}
	// Its own commands count from the start.
	command(w, 280, p.webApp, 100*time.Second)
	pass(t, w)
	w.fp.Remove(280)
	command(w, 281, p.webApp, 200*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "web-app" {
		t.Errorf("its own project: %q", got)
	}
}

func TestAnotherAgentInTheSessionStartsAfresh(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	base := suggested(t, w)
	command(w, 290, p.billing, 100*time.Second)
	pass(t, w)
	w.fp.Remove(290)
	command(w, 291, p.billing, 200*time.Second)
	pass(t, w)
	if got := suggested(t, w); got != "billing" {
		t.Fatalf("first agent: %q", got)
	}
	// It ends, and another starts in its place before the next pass: a new
	// conversation, which has not worked in billing.
	w.fp.Remove(291)
	w.fp.Remove(agentPID)
	tty := testutil.TTY(t, t.TempDir(), "agent2", time.Now())
	w.fp.Add(testutil.Proc{PID: agentPID + 1, PPID: 200, Comm: "claude", Argv: []string{"claude"}, Stdin: tty, Cwd: p.root, Started: 300 * time.Second})
	pass(t, w)
	if got := suggested(t, w); got != base {
		t.Errorf("the new agent: %q", got)
	}
}

func TestACommandThatKeepsStartingProcessesCountsOnce(t *testing.T) {
	p := newProjects(t)
	w := agentWorld(t, p.root)
	base := suggested(t, w)
	// A loop it left running in web-app starts another sleep before every
	// pass: one command, however many processes it starts.
	command(w, 300, p.webApp, 100*time.Second)
	for i := range 3 {
		w.fp.Add(testutil.Proc{PID: 310 + i, PPID: 300, Comm: "sleep", Argv: []string{"sleep", "5"}, Cwd: p.webApp, Started: time.Duration(200+100*i) * time.Second})
		pass(t, w)
		w.fp.Remove(310 + i)
	}
	if got := suggested(t, w); got != base {
		t.Errorf("a loop: %q", got)
	}
}

func TestTheBookKeysASessionKeptByIDByItsID(t *testing.T) {
	p := newProjects(t)
	w := agentWorldIn(t, newWorldOf(t, "busy", "quiet-mouse", true, true), p.root)
	command(w, 320, p.webApp, 100*time.Second)
	pass(t, w)
	book := readBook(t, w)
	if _, ok := book.Sessions["200\t0\tid:"+busyID]; !ok || len(book.Sessions) != 1 {
		t.Errorf("book %v", book.Sessions)
	}
}

// itoa writes a number for a JSON literal.
func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
