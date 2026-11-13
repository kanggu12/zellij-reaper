package reaper_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

func TestANamesPassRenamesAndDeletesNothing(t *testing.T) {
	w := newWorld(t)
	w.env.Config.AutoRename = true
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
		t.Fatal(err)
	}
	if len(w.zj.deleted) != 0 {
		t.Errorf("deleted %v", w.zj.deleted)
	}
	if strings.Join(w.zj.renamed, ",") != "busy->htop" {
		t.Errorf("renamed %v", w.zj.renamed)
	}
	log := w.log(t)
	for _, want := range []string{"=== names start ===", "  -> renamed: busy → htop", "=== names end (1 renamed) ==="} {
		if !strings.Contains(log, want) {
			t.Errorf("no %q in the log:\n%s", want, log)
		}
	}
	// Only the renames: no line for each session, as a full pass writes.
	if strings.Contains(log, "idle-old [RUNNING]") || strings.Contains(log, "reaper start") {
		t.Errorf("log:\n%s", log)
	}
}

// A names pass writes its lines once it has renamed something, each with the
// time it was said. A name zellij refuses takes seconds to learn, so the line
// that opens the pass, stamped when the lines were written, read as later than
// the renames under it. A log that cannot be moved aside is said in the pass
// too, in its place.
func TestANamesPassOpensWithTheTimeItStarted(t *testing.T) {
	for _, stuck := range []bool{false, true} {
		w := newWorld(t)
		w.env.Config.AutoRename = true
		start := time.Date(2026, 11, 13, 11, 23, 53, 0, time.Local)
		if stuck {
			// A log past its size, and a folder where it would move to.
			w.env.Log.MaxBytes = 1
			if err := os.WriteFile(w.env.Log.Path, []byte("[2026-11-13 11:00:00] === reaper end ===\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(w.env.Log.Path+".1", 0o700); err != nil {
				t.Fatal(err)
			}
		}
		said := 0
		// Each line a second after the one before.
		w.env.Log.Now = func() time.Time {
			said++
			return start.Add(time.Duration(said) * time.Second)
		}
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
			t.Fatal(err)
		}
		log := w.log(t)
		lines := strings.Split(strings.TrimSpace(log), "\n")
		if stuck {
			lines = lines[1:]
			if !strings.Contains(log, "could not rotate") {
				t.Errorf("stuck: no failure to rotate in the log:\n%s", log)
			}
		}
		if len(lines) < 3 || !strings.HasSuffix(lines[0], "] === names start ===") {
			t.Fatalf("stuck %v: log:\n%s", stuck, log)
		}
		// [2006-01-02 15:04:05] sorts as the time it says.
		stamps := make([]string, len(lines))
		for i, l := range lines {
			stamps[i], _, _ = strings.Cut(l, "]")
		}
		if !slices.IsSorted(stamps) {
			t.Errorf("stuck %v: a line reads as earlier than one above it:\n%s", stuck, log)
		}
	}
}

func TestANamesPassWithNothingToDoWritesNothing(t *testing.T) {
	for _, rename := range []bool{true, false} {
		w := newWorldWith(t, "htop", "htop", false)
		w.env.Config.AutoRename = rename
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
			t.Fatal(err)
		}
		if len(w.zj.renamed)+len(w.zj.deleted) != 0 {
			t.Errorf("auto_rename %v: renamed %v, deleted %v", rename, w.zj.renamed, w.zj.deleted)
		}
		if b, _ := w.logBytes(); len(b) != 0 {
			t.Errorf("auto_rename %v: wrote\n%s", rename, b)
		}
	}
}

func TestDropTailGivesBackThePlainName(t *testing.T) {
	for _, c := range []struct {
		drop      bool
		refused   bool
		namesOnly bool
		want      string
	}{
		{false, false, false, ""},
		{true, false, false, "htop-2->htop"},
		// The names pass leaves tails to the hourly pass: a tail zellij
		// refuses to drop is then tried once an hour, not every few minutes.
		{true, false, true, ""},
		// zellij refuses a name the session had before: it keeps its tail,
		// with no failure logged and no other tail tried.
		{true, true, false, ""},
	} {
		// A pass gave htop-2 to a session that started as quiet-mouse,
		// while another session had htop; that one is gone now.
		w := newWorldWith(t, "htop-2", "quiet-mouse", true)
		w.env.Config.AutoRename = true
		w.env.Config.AutoRenameDropTail = c.drop
		w.zj.refuse["htop"] = c.refused
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: c.namesOnly}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(w.zj.renamed, ","); got != c.want {
			t.Errorf("drop %v refused %v names-only %v: renamed %q, want %q", c.drop, c.refused, c.namesOnly, got, c.want)
		}
		if b, _ := w.logBytes(); c.refused && strings.Contains(string(b), "FAILED") {
			t.Errorf("log:\n%s", b)
		}
	}
}

func TestANamesPassLogsOnlyNamesThatChanged(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*reaper.Env, *fakeZellij)
	}{
		// zellij takes the rename but never lists the new name in time.
		{"unconfirmed", func(_ *reaper.Env, z *fakeZellij) { z.unseen = true }},
		// A dry run: plan and the hourly pass show what would change.
		{"dry run", func(e *reaper.Env, _ *fakeZellij) { e.Config.DryRun = true }},
	} {
		w := newWorld(t)
		w.env.Config.AutoRename = true
		c.set(w.env, w.zj)
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
			t.Fatal(err)
		}
		if b, _ := w.logBytes(); len(b) != 0 {
			t.Errorf("%s: wrote\n%s", c.name, b)
		}
	}
}

// logBytes is the log as it is, nothing when nothing was written.
func (w *world) logBytes() ([]byte, error) { return os.ReadFile(w.logp) }

func TestNoPassRenamesOnWindows(t *testing.T) {
	// zellij on Windows cannot reach a session after a rename: the same world
	// that renames busy on Linux renames nothing there, says why, and still
	// reaps what it reaps elsewhere.
	defer func(g bool) { zellij.RenamesReachable = g }(zellij.RenamesReachable)
	w := newWorld(t)
	w.env.Config.AutoRename = true
	zellij.RenamesReachable = false
	if _, err := reaper.Run(context.Background(), w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if len(w.zj.renamed) != 0 {
		t.Errorf("renamed %v", w.zj.renamed)
	}
	if !strings.Contains(strings.Join(w.zj.deleted, ","), "idle-old") {
		t.Errorf("deleted %v", w.zj.deleted)
	}
	if log := w.log(t); !strings.Contains(log, "cannot reach a session after a rename") {
		t.Errorf("no reason in the log:\n%s", log)
	}
}
