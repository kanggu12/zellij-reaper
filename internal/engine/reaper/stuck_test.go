package reaper_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// server is a zellij server process in the given state, serving the socket
// file name under /run/user/1000/zellij.
func server(pid, uid int, state, name string) *procfs.Proc {
	return &procfs.Proc{PID: pid, PPID: 1, UID: uid, Comm: "zellij", State: state,
		Argv: []string{"/usr/bin/zellij", "--server", "/run/user/1000/zellij/contract_version_1/" + name}}
}

func TestStuckNamesTheServersThatAreStopped(t *testing.T) {
	const uid = 1000
	noAnswer := zellij.NoAnswer("zellij list-sessions --no-formatting", 10*time.Second)
	table := &procfs.Table{ByPID: map[int]*procfs.Proc{
		// Stopped by a signal, and by a debugger.
		78: server(78, uid, "T", "beta"),
		90: server(90, uid, "t", "0a1b2c3d4e5f"),
		// Running, another user's stopped one, and a stopped process that is
		// no zellij server: none of them is named.
		95: server(95, uid, "S", "gamma"),
		96: server(96, uid+1, "T", "theirs"),
		97: {PID: 97, PPID: 1, UID: uid, Comm: "vim", State: "T", Argv: []string{"vim", "notes.md"}},
	}}
	// zellij 0.46 names the second one's socket by an id the index knows.
	idx := zellij.NewIndex(map[string]string{"work": "0a1b2c3d4e5f"})
	got := reaper.Stuck(noAnswer, table, uid, idx)
	want := "zellij list-sessions --no-formatting: no answer within 10s; the servers of beta (pid 78) and work (pid 90) are stopped: `kill -CONT 78 90` lets them go on"
	if got == nil || got.Error() != want {
		t.Errorf("two stopped:\n got %v\nwant %s", got, want)
	}
	// What was cut short stays so for whoever asks.
	if !zellij.CutShort(got) || !errors.Is(got, noAnswer) {
		t.Errorf("the explanation lost the error it explains: %v", got)
	}
	// One is said in the singular.
	delete(table.ByPID, 90)
	want = "zellij list-sessions --no-formatting: no answer within 10s; the server of beta (pid 78) is stopped: `kill -CONT 78` lets it go on"
	if got := reaper.Stuck(noAnswer, table, uid, idx); got == nil || got.Error() != want {
		t.Errorf("one stopped:\n got %v\nwant %s", got, want)
	}
	// Nothing to add: no server stopped, an answer that was a refusal, no
	// table to look in.
	delete(table.ByPID, 78)
	if got := reaper.Stuck(noAnswer, table, uid, idx); got != noAnswer {
		t.Errorf("none stopped: %v", got)
	}
	refused := errors.New("zellij list-sessions: Error occurred")
	table.ByPID[78] = server(78, uid, "T", "beta")
	if got := reaper.Stuck(refused, table, uid, idx); got != refused {
		t.Errorf("a refusal: %v", got)
	}
	if got := reaper.Stuck(noAnswer, nil, uid, idx); got != noAnswer {
		t.Errorf("no table: %v", got)
	}
}

func TestAPassNamesTheStoppedServerThatHeldUpTheList(t *testing.T) {
	w := newWorld(t)
	// idle-old's server, pid 100, stopped by a signal: zellij's list waits
	// for it and gives no answer.
	stat := filepath.Join(w.env.Sources.ProcRoot, "100", "stat")
	b, err := os.ReadFile(stat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stat, []byte(strings.Replace(string(b), " (zellij) S ", " (zellij) T ", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	w.zj.listErr = zellij.NoAnswer("zellij list-sessions --no-formatting", 10*time.Second)
	const said = "; the server of idle-old (pid 100) is stopped: `kill -CONT 100` lets it go on"
	if _, err := reaper.Plan(t.Context(), w.env, reaper.Options{}); err == nil || !strings.HasSuffix(err.Error(), said) {
		t.Errorf("plan: %v", err)
	}
	// A pass writes it into its line about the list, and stops there.
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err == nil {
		t.Error("the pass went on without a list")
	}
	if !strings.Contains(w.log(t), "zellij list-sessions failed: zellij list-sessions --no-formatting: no answer within 10s"+said+"\n") {
		t.Errorf("log:\n%s", w.log(t))
	}
	if len(w.zj.deleted) != 0 {
		t.Errorf("deleted %v", w.zj.deleted)
	}
}
