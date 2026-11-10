package reaper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
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
// The world keeps the session's folder under the old name, as such a copy has.
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
		for _, want := range []string{"  -> renamed: busy → htop", "busy [EXITED] :: copy zellij left when it renamed the session to htop", "  -> deleted the copy: busy"} {
			if !strings.Contains(log, want) {
				t.Errorf("names pass %t: no %q in the log:\n%s", names, want, log)
			}
		}
		// Not "  -> deleted: busy", which reads as a session reaped.
		if strings.Contains(log, "  -> deleted: busy") {
			t.Errorf("names pass %t: the copy logged as a session deleted:\n%s", names, log)
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

func TestACopyStaysWhenProtectedUsedOrUnread(t *testing.T) {
	for name, c := range map[string]struct {
		// meanwhile runs as zellij renames, after the copy is listed.
		meanwhile func(w *world)
		why       string
	}{
		// `protect` from another terminal since the pass read the list.
		"protected since": {func(w *world) {
			other, _ := protect.Load(w.env.Protected.Path)
			if err := other.Set("busy", true); err != nil {
				t.Fatal(err)
			}
		}, "protected by protected list"},
		// Someone brought the copy back, used it and quit it before the
		// pass looked: its files are newer than zellij's moment.
		"used since": {func(w *world) {
			later := time.Now().Add(10 * time.Second)
			testutil.SessionFiles(t, w.env.Sources.InfoDir, "busy", testutil.Metadata(0, "htop"), "", later)
		}, "written since the rename, so someone may have used it"},
		// Files that cannot be read tell nothing.
		"unread": {func(w *world) {
			if err := os.RemoveAll(filepath.Join(w.env.Sources.InfoDir, "busy")); err != nil {
				t.Fatal(err)
			}
		}, "its files could not be read"},
	} {
		w := newWorld(t)
		w.env.Config.AutoRename = true
		leaveCopy(w.zj, "busy")
		renamed := w.zj.onRename
		w.zj.onRename = func(to string) { renamed(to); c.meanwhile(w) }
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
			t.Fatal(err)
		}
		if log := w.log(t); len(w.zj.deleted) != 0 || !strings.Contains(log, "  -> kept: busy :: "+c.why) {
			t.Errorf("%s: deleted %v:\n%s", name, w.zj.deleted, log)
		}
	}
}

func TestTheFileTimeRuleAtItsBoundary(t *testing.T) {
	// zellij writes the copy within LeftoverSettles of the rename: it goes.
	// Written later, someone used it: it stays.
	defer func(d time.Duration) { reaper.LeftoverSettles = d }(reaper.LeftoverSettles)
	reaper.LeftoverSettles = 400 * time.Millisecond
	for after, gone := range map[time.Duration]bool{100 * time.Millisecond: true, 700 * time.Millisecond: false} {
		w := newWorld(t)
		w.env.Config.AutoRename = true
		w.zj.lateCopyAfter = 300 * time.Millisecond
		w.zj.lateCopy = func() {
			testutil.SessionFiles(t, w.env.Sources.InfoDir, "busy", testutil.Metadata(0, "htop"), "layout {\n}\n", w.zj.renamedAt.Add(after))
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
		log := w.log(t)
		if gone != (strings.Join(w.zj.deleted, ",") == "busy") || (!gone && !strings.Contains(log, "written since the rename")) {
			t.Errorf("written %s after the rename: deleted %v:\n%s", after, w.zj.deleted, log)
		}
	}
}

func TestAPassLeavesARunningSessionUnderTheOldNameAlone(t *testing.T) {
	// Renamed, then something started a new session under the old name
	// before the pass looked: it runs, and a pass does not touch it.
	w := newWorld(t)
	w.env.Config.AutoRename = true
	w.zj.onRename = func(to string) {
		for i, l := range w.zj.listed {
			if l.Name == "busy" {
				w.zj.listed[i].Name = to
			}
		}
		w.zj.listed = append(w.zj.listed, zellij.Listed{Name: "busy", Created: "0s ago"})
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

	// Refused because someone brought the copy back as the pass asked:
	// kept, as an exited session brought back is.
	w = newWorld(t)
	w.env.Config.AutoRename = true
	leaveCopy(w.zj, "busy")
	w.zj.refuseDelete = map[string]bool{"busy": true}
	w.zj.onDelete = func(name string) {
		for i, l := range w.zj.listed {
			if l.Name == name {
				w.zj.listed[i].Exited = false
			}
		}
	}
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if log := w.log(t); !strings.Contains(log, "  -> kept: busy :: it is running again") {
		t.Errorf("log:\n%s", log)
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

func TestAPassPastItsBudgetDoesNotLookForCopies(t *testing.T) {
	// A pass starts no delete past its budget, a copy's included.
	defer func(d time.Duration) { reaper.PassBudget = d }(reaper.PassBudget)
	reaper.PassBudget = 200 * time.Millisecond
	w := newWorld(t)
	w.env.Config.AutoRename = true
	leaveCopy(w.zj, "busy")
	renamed := w.zj.onRename
	w.zj.onRename = func(to string) { renamed(to); time.Sleep(250 * time.Millisecond) }
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if log := w.log(t); len(w.zj.deleted) != 0 || !strings.Contains(log, ": no look for copies zellij may have left under old names") {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, log)
	}
}

func TestDropLeftoversStartsNoDeletePastItsDeadline(t *testing.T) {
	// Two copies, and the first delete takes the time that was left.
	w := newWorld(t)
	before := time.Now().Add(-time.Hour)
	for _, name := range []string{"copy-a", "copy-b"} {
		testutil.SessionFiles(t, w.env.Sources.InfoDir, name, testutil.Metadata(0, "x"), "layout {\n}\n", before)
		w.zj.listed = append(w.zj.listed, zellij.Listed{Name: name, Exited: true})
	}
	w.zj.onDelete = func(string) { time.Sleep(60 * time.Millisecond) }
	now := time.Now()
	renamed := map[string]reaper.Renamed{"copy-a": {To: "a", At: now}, "copy-b": {To: "b", At: now}}
	w.env.DropLeftovers(context.Background(), renamed, now.Add(30*time.Millisecond))
	if strings.Join(w.zj.deleted, ",") != "copy-a" || !strings.Contains(w.log(t), "  -> kept: copy-b :: time is up after") {
		t.Errorf("deleted %v:\n%s", w.zj.deleted, w.log(t))
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
