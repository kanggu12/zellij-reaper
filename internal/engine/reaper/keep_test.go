package reaper_test

import (
	"errors"
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
