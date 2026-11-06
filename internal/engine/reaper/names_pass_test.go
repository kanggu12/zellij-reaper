package reaper_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
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
	for _, want := range []string{"=== names start (dry_run=false) ===", "  -> renamed: busy → htop", "=== names end (1 renamed) ==="} {
		if !strings.Contains(log, want) {
			t.Errorf("no %q in the log:\n%s", want, log)
		}
	}
	// Only the renames: no line for each session, as a full pass writes.
	if strings.Contains(log, "idle-old [RUNNING]") || strings.Contains(log, "reaper start") {
		t.Errorf("log:\n%s", log)
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
		drop    bool
		refused bool
		want    string
	}{
		{false, false, ""},
		{true, false, "htop-2->htop"},
		// zellij refuses a name the session had before: it keeps its tail,
		// with nothing in the log and no other tail tried.
		{true, true, ""},
	} {
		// A pass gave htop-2 to a session that started as quiet-mouse,
		// while another session had htop; that one is gone now.
		w := newWorldWith(t, "htop-2", "quiet-mouse", true)
		w.env.Config.AutoRename = true
		w.env.Config.AutoRenameDropTail = c.drop
		w.zj.refuse["htop"] = c.refused
		if _, err := reaper.Run(context.Background(), w.env, reaper.Options{NamesOnly: true}); err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(w.zj.renamed, ","); got != c.want {
			t.Errorf("drop %v refused %v: renamed %q, want %q", c.drop, c.refused, got, c.want)
		}
		if c.refused && strings.Contains(w.log(t), "FAILED") {
			t.Errorf("log:\n%s", w.log(t))
		}
	}
}

// logBytes is the log as it is, nothing when nothing was written.
func (w *world) logBytes() ([]byte, error) { return os.ReadFile(w.logp) }
