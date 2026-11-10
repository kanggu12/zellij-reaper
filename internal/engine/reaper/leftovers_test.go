package reaper_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// TestMain runs the passes without the wait for a copy zellij may leave
// after a rename: the fake leaves it, when a test asks, before the rename
// returns.
func TestMain(m *testing.M) {
	reaper.LeftoverSettles = 0
	os.Exit(m.Run())
}

// leaveCopy makes the fake rename as zellij does when its rename races the
// job that writes the session's files: the session is listed under the new
// name, and an exited one under the old name, which attach would bring back.
func leaveCopy(z *fakeZellij, old string) {
	z.onRename = func(to string) {
		for i, l := range z.listed {
			if l.Name == old {
				z.listed[i].Name = to
			}
		}
		z.listed = append(z.listed, zellij.Listed{Name: old, Exited: true, Created: "0s ago"})
	}
}

func TestAPassDeletesTheCopyARenameLeavesUnderTheOldName(t *testing.T) {
	for _, names := range []bool{true, false} {
		w := newWorld(t)
		w.env.Config.AutoRename = true
		leaveCopy(w.zj, "busy")
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: names}); err != nil {
			t.Fatal(err)
		}
		// Deleted as exited sessions are, never forced.
		if strings.Join(w.zj.renamed, ",") != "busy->htop" || !strings.Contains(strings.Join(w.zj.deleted, ","), "busy") || strings.Contains(strings.Join(w.zj.forced, ","), "busy") {
			t.Errorf("names pass %t: renamed %v, deleted %v, forced %v", names, w.zj.renamed, w.zj.deleted, w.zj.forced)
		}
		log := w.log(t)
		for _, want := range []string{"  -> renamed: busy → htop", "busy [EXITED] :: copy zellij left when it renamed the session to htop", "  -> deleted: busy"} {
			if !strings.Contains(log, want) {
				t.Errorf("names pass %t: no %q in the log:\n%s", names, want, log)
			}
		}
		// The copy comes after the rename, inside what the names pass writes.
		if names && strings.Index(log, "copy zellij left") > strings.Index(log, "=== names end") {
			t.Errorf("the copy is logged after the pass's end:\n%s", log)
		}
	}
}

func TestAPassWaitsForACopyZellijWritesLate(t *testing.T) {
	// zellij's job writes the copy up to a second after the rename: the pass
	// waits LeftoverSettles before it looks.
	defer func(d time.Duration) { reaper.LeftoverSettles = d }(reaper.LeftoverSettles)
	reaper.LeftoverSettles = 300 * time.Millisecond
	w := newWorld(t)
	w.env.Config.AutoRename = true
	w.zj.lateCopyAfter = 100 * time.Millisecond
	w.zj.lateCopy = func() {
		w.zj.listed = append(w.zj.listed, zellij.Listed{Name: "busy", Exited: true, Created: "0s ago"})
		for i, l := range w.zj.listed {
			if l.Name == "busy" && !l.Exited {
				w.zj.listed[i].Name = "htop"
			}
		}
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(w.zj.deleted, ",") != "busy" {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, w.log(t))
	}
}

func TestAPassLeavesARunningSessionUnderTheOldNameAlone(t *testing.T) {
	// Something started a session under the old name since: it runs, and a
	// pass does not touch it.
	w := newWorld(t)
	w.env.Config.AutoRename = true
	w.zj.onRename = func(to string) {
		w.zj.listed = append(w.zj.listed, zellij.Listed{Name: to})
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.zj.deleted) != 0 || strings.Contains(w.log(t), "copy zellij left") {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, w.log(t))
	}
}

func TestACopyThatCannotBeDeletedOrLookedForIsLogged(t *testing.T) {
	// zellij refuses the delete: the log says so, and the pass has no
	// failed delete for doctor to count.
	w := newWorld(t)
	w.env.Config.AutoRename = true
	leaveCopy(w.zj, "busy")
	w.zj.refuseDelete = map[string]bool{"busy": true}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	log := w.log(t)
	if !strings.Contains(log, "  -> could not delete the copy: busy :: zellij delete-session busy: exit status 1") {
		t.Errorf("log:\n%s", log)
	}
	if p := logfile.LastPass(strings.Split(strings.TrimSpace(log), "\n")); !p.Ended || p.Failed != 0 {
		t.Errorf("the last pass: %+v", p)
	}

	// The list fails after the rename: the copy waits like any exited
	// session, and the log says why.
	w = newWorld(t)
	w.env.Config.AutoRename = true
	w.zj.onRename = func(string) { w.zj.listErr = errors.New("zellij list-sessions: exit status 2") }
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if log := w.log(t); len(w.zj.deleted) != 0 || !strings.Contains(log, "could not look for copies zellij left under old names: zellij list-sessions: exit status 2") {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, log)
	}

	// zellij lists nothing at all: nothing is left, and that is no error.
	w = newWorld(t)
	w.env.Config.AutoRename = true
	w.zj.onRename = func(string) { w.zj.listed = nil }
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if log := w.log(t); len(w.zj.deleted) != 0 || strings.Contains(log, "could not look") {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, log)
	}
}

func TestADryRunOrAPassThatRenamesNothingDoesNotLook(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	w.env.Config.DryRun = true
	leaveCopy(w.zj, "busy")
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	// Nothing renamed, so nothing to look for: no delete, and only the
	// pass's own call to list sessions.
	if len(w.zj.renamed) != 0 || len(w.zj.deleted) != 0 || strings.Contains(w.log(t), "copy zellij left") {
		t.Errorf("renamed %v, deleted %v", w.zj.renamed, w.zj.deleted)
	}
}
