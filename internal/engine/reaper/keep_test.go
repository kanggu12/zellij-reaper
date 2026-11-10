package reaper_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestANameProtectedDuringAPassKeepsItsName(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	// busy, which the pass would name htop, is protected from another
	// terminal while the pass reads the facts: protect takes no lock.
	w.zj.during = func() {
		other, err := protect.Load(w.env.Protected.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := other.Set("busy", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	// A rename would have left the protection on the old name, and the
	// session under the new one protected by nothing.
	if len(w.zj.renamed) != 0 {
		t.Errorf("renamed %v", w.zj.renamed)
	}
	if !strings.Contains(w.log(t), "  -> kept name: busy :: protected since the check\n") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestKeptNamesOutliveAListThatSaysNothing(t *testing.T) {
	for _, c := range []struct {
		name  string
		spoil func(w *world)
	}{
		// zellij listed no session: a timer looking in another place says
		// that as well as an empty machine does.
		{"an empty list", func(w *world) { w.zj.listed = nil }},
		// zellij listed its sessions by id, which this pass cannot name.
		{"an index that cannot be read", func(w *world) {
			if err := os.WriteFile(zellij.IndexFile(w.env.Sources.InfoDir), []byte("not a database"), 0o600); err != nil {
				t.Fatal(err)
			}
			// As zellij does then: every session under its id.
			for i := range w.zj.listed {
				w.zj.listed[i].Name = fmt.Sprintf("%012x", i+1)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			if err := reaper.KeepName(t.Context(), w.env, "fresh", true, "the command line", time.Second); err != nil {
				t.Fatal(err)
			}
			c.spoil(w)
			if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
				t.Fatal(err)
			}
			if err := w.env.Kept.Reload(); err != nil || !w.env.Kept.Has("fresh") {
				t.Errorf("kept names after the pass: %v (%v)", w.env.Kept.Names(), err)
			}
		})
	}
	// A pass that saw its sessions and their names lets a kept name go with
	// its session: fresh is no longer listed, and its socket file is gone.
	w := newWorld(t)
	if err := reaper.KeepName(t.Context(), w.env, "fresh", true, "the command line", time.Second); err != nil {
		t.Fatal(err)
	}
	w.zj.listed = w.zj.listed[:1]
	if err := os.Remove(filepath.Join(w.sockDir, "fresh")); err != nil {
		t.Fatal(err)
	}
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Kept.Reload(); err != nil || w.env.Kept.Has("fresh") {
		t.Errorf("a kept name whose session is gone: %v (%v)", w.env.Kept.Names(), err)
	}
}

func TestAnExitedSessionIsDeletedWithoutForce(t *testing.T) {
	w := newWorld(t)
	// A dry run says what it would run: --force only for the running one.
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		"  -> DRY_RUN: would run: zellij delete-session idle-old --force\n",
		"  -> DRY_RUN: would run: zellij delete-session gone\n",
	} {
		if !strings.Contains(w.log(t), line) {
			t.Errorf("no %q in the log:\n%s", line, w.log(t))
		}
	}
	// gone exited days ago. Someone may bring it back between the last look
	// and the delete: without --force zellij refuses a session running
	// again, where --force killed it with its client attached.
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.zj.deleted, ",") != "idle-old,gone" || strings.Join(w.zj.forced, ",") != "idle-old" {
		t.Errorf("deleted %v, forced %v", w.zj.deleted, w.zj.forced)
	}
}

func TestAnExitedSessionBroughtBackAtTheDeleteIsKeptNotFailed(t *testing.T) {
	// gone is brought back as zellij is asked to delete it, after the second
	// look: zellij refuses, as it does without --force for a session that
	// runs. The session is kept, as it should be, and the pass did not fail.
	w := newWorld(t)
	w.zj.refuseDelete = map[string]bool{"gone": true}
	w.zj.onDelete = func(name string) {
		if name == "gone" {
			w.zj.listed[3].Exited = false
		}
	}
	r, err := reaper.Run(t.Context(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "gone" && (o.DeleteErr != nil || o.KeptAtDelete != "it is running again") {
			t.Errorf("gone: failed %v, kept %q", o.DeleteErr, o.KeptAtDelete)
		}
	}
	if log := w.log(t); !strings.Contains(log, "  -> kept: gone :: it is running again\n") || strings.Contains(log, "FAILED") {
		t.Errorf("log:\n%s", log)
	}
	// Refused while still listed as exited: that is a failure.
	w = newWorld(t)
	w.zj.refuseDelete = map[string]bool{"gone": true}
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil || !strings.Contains(w.log(t), "  -> FAILED: gone :: ") {
		t.Errorf("%v, log:\n%s", err, w.log(t))
	}
}

func TestAJobStartedBeforeTheDeleteKeepsTheSession(t *testing.T) {
	w := newWorld(t)
	// gone goes first. Right after, a job starts in idle-old's pane, with
	// nobody attached (`zellij --session idle-old run`): the facts said
	// idle, and only clients were asked again before the delete.
	w.zj.listed = append([]zellij.Listed{w.zj.listed[3]}, w.zj.listed[:3]...)
	w.zj.onDelete = func(name string) {
		if name == "gone" {
			w.fp.Add(testutil.Proc{PID: 150, PPID: 100, Comm: "sleep", Argv: []string{"sleep", "99"}})
		}
	}
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.zj.deleted, ",") != "gone" {
		t.Errorf("deleted %v", w.zj.deleted)
	}
	if !strings.Contains(w.log(t), "  -> kept: idle-old :: running in a pane since the check: sleep 99\n") {
		t.Errorf("log:\n%s", w.log(t))
	}
}

func TestAPassPastItsTimeLeavesTheRestAndEndsAsPassesDo(t *testing.T) {
	w := newWorld(t)
	// A budget already spent: the pass comes to no session.
	defer func(d time.Duration) { reaper.PassBudget = d }(reaper.PassBudget)
	reaper.PassBudget = -time.Second
	r, err := reaper.Run(t.Context(), w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.zj.deleted) != 0 {
		t.Errorf("deleted %v", w.zj.deleted)
	}
	for _, o := range r.Outcomes {
		if !o.Left {
			t.Errorf("%s not left for the next pass", o.Session.Name())
		}
	}
	// Said once, and the pass ends with its end line: systemd's stop at a
	// minute left none.
	log := w.log(t)
	if !strings.Contains(log, "] time is up after 0s: 4 sessions left for the next pass\n") || !strings.HasSuffix(log, "=== reaper end (0 reaped, 4 kept, 0 renamed) ===\n") {
		t.Errorf("log:\n%s", log)
	}
	// A dry run changes nothing and is not cut short.
	w = newWorld(t)
	if r, err := reaper.Run(t.Context(), w.env, reaper.Options{DryRun: true}); err != nil || r.Outcomes[0].Left {
		t.Errorf("dry run: %v, left %v", err, r.Outcomes[0].Left)
	}
	// One session is one.
	w = newWorld(t)
	w.zj.listed = w.zj.listed[:1]
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil || !strings.Contains(w.log(t), "] time is up after 0s: 1 session left for the next pass\n") {
		t.Errorf("%v, log:\n%s", err, w.log(t))
	}
}

func TestAPassEndsItsCallsToZellijByItsLimit(t *testing.T) {
	w := newWorld(t)
	defer func(b, l time.Duration) { reaper.PassBudget, reaper.PassLimit = b, l }(reaper.PassBudget, reaper.PassLimit)
	// The budget leaves time to start both deletes; the limit does not.
	reaper.PassBudget, reaper.PassLimit = time.Hour, 500*time.Millisecond
	// idle-old's delete takes longer than the pass has left: zellij was slow.
	w.zj.onDelete = func(name string) {
		if name == "idle-old" {
			time.Sleep(700 * time.Millisecond)
		}
	}
	start := time.Now()
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	// gone, next, is not deleted: its second look at zellij is past the
	// limit, and keeps it. The pass ends as passes do, well before a
	// scheduler would stop it.
	log := w.log(t)
	if strings.Join(w.zj.deleted, ",") != "idle-old" || !strings.Contains(log, "  -> kept: gone :: could not list the sessions again: context deadline exceeded\n") ||
		!strings.HasSuffix(log, "=== reaper end (1 reaped, 3 kept, 0 renamed) ===\n") {
		t.Errorf("deleted %v, log:\n%s", w.zj.deleted, log)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s", took)
	}
}

func TestANamesPassPastItsTimeRenamesNoMore(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	defer func(d time.Duration) { reaper.PassBudget = d }(reaper.PassBudget)
	reaper.PassBudget = -time.Second
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.zj.renamed) != 0 {
		t.Errorf("renamed %v", w.zj.renamed)
	}
	// With the budget there, it renames: the world has a session to name.
	reaper.PassBudget = time.Hour
	if _, err := reaper.Run(t.Context(), w.env, reaper.Options{NamesOnly: true}); err != nil || len(w.zj.renamed) == 0 {
		t.Errorf("renamed %v, %v", w.zj.renamed, err)
	}
}

func TestForgettingANameThatIsNotKeptChangesNothing(t *testing.T) {
	w := newWorld(t)
	if err := reaper.KeepName(t.Context(), w.env, "fresh", true, "the command line", time.Second); err != nil {
		t.Fatal(err)
	}
	before := w.log(t)
	// A typo, or a name never kept: said, with no line in the log saying a
	// name was let go.
	if err := reaper.KeepName(t.Context(), w.env, "frsh", false, "the command line", time.Second); !errors.Is(err, reaper.ErrNotKept) {
		t.Errorf("a name not kept: %v", err)
	}
	if got := w.log(t); got != before {
		t.Errorf("the log grew:\n%s", strings.TrimPrefix(got, before))
	}
	if !w.env.Kept.Has("fresh") {
		t.Error("the kept name went with the typo")
	}
	// The kept one goes, and the log says so.
	if err := reaper.KeepName(t.Context(), w.env, "fresh", false, "the command line", time.Second); err != nil {
		t.Fatal(err)
	}
	if w.env.Kept.Has("fresh") || !strings.Contains(w.log(t), "fresh :: name no longer kept, from the command line\n") {
		t.Errorf("kept %v, log:\n%s", w.env.Kept.Has("fresh"), w.log(t))
	}
}
