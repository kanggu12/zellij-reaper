package reaper_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
)

func TestAnIdleSessionWithoutATerminalGoesAfterItsWait(t *testing.T) {
	// idle-old's pane has no terminal to read, as every pane on Windows, and
	// its session files were written just now, as zellij does when a client
	// leaves: they cannot say how long it has been idle.
	w := newWorld(t)
	if err := os.Remove(filepath.Join(w.env.Sources.ProcRoot, "101", "fd", "0")); err != nil {
		t.Fatal(err)
	}
	w.env.QuietPath = filepath.Join(t.TempDir(), "state", "quiet")
	start := time.Now()
	w.env.Now = func() time.Time { return start }
	// The last client left an hour before the first pass.
	if err := touchAll(filepath.Join(w.env.Sources.InfoDir, "idle-old"), start.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := reaper.Run(ctx, w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(w.zj.deleted, ","), "idle-old") {
		t.Fatal("reaped at the first pass that found it quiet")
	}
	book, err := os.ReadFile(w.env.QuietPath)
	if err != nil || !strings.HasPrefix(string(book), "idle-old\t100\t") {
		t.Fatalf("book %q, %v", book, err)
	}
	// Plan reads the book and leaves it alone.
	r, err := reaper.Plan(ctx, w.env, reaper.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range r.Outcomes {
		if o.Session.Name() == "idle-old" && (o.Session.Facts.ActivitySource != "quiet" || !o.Session.Facts.LastActive.Equal(start.Truncate(time.Second))) {
			t.Errorf("plan: %s from %s", o.Session.Facts.LastActive, o.Session.Facts.ActivitySource)
		}
	}
	// Past idle_after from that first quiet pass, it goes.
	w.env.Now = func() time.Time { return start.Add(w.env.Config.IdleAfter + time.Hour) }
	if _, err := reaper.Run(ctx, w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(w.zj.deleted, ","), "idle-old") {
		t.Errorf("not reaped past its wait: deleted %v\n%s", w.zj.deleted, w.log(t))
	}
	// Gone, so out of the book.
	if book, _ := os.ReadFile(w.env.QuietPath); strings.Contains(string(book), "idle-old") {
		t.Errorf("book still has it: %q", book)
	}
}

func TestAClientBetweenTwoPassesStartsTheWaitOver(t *testing.T) {
	// As TestAnIdleSessionWithoutATerminalGoesAfterItsWait, but someone
	// attached to idle-old and left between the passes: zellij rewrote its
	// files when they left, which no pass saw.
	w := newWorld(t)
	if err := os.Remove(filepath.Join(w.env.Sources.ProcRoot, "101", "fd", "0")); err != nil {
		t.Fatal(err)
	}
	w.env.QuietPath = filepath.Join(t.TempDir(), "quiet")
	start := time.Now()
	w.env.Now = func() time.Time { return start }
	ctx := context.Background()
	if _, err := reaper.Run(ctx, w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	later := start.Add(w.env.Config.IdleAfter + time.Hour)
	left := later.Add(-10 * time.Minute)
	if err := touchAll(filepath.Join(w.env.Sources.InfoDir, "idle-old"), left); err != nil {
		t.Fatal(err)
	}
	w.env.Now = func() time.Time { return later }
	if _, err := reaper.Run(ctx, w.env, reaper.Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(w.zj.deleted, ","), "idle-old") {
		t.Errorf("reaped ten minutes after a client left:\n%s", w.log(t))
	}
}

// touchAll sets the modification time of every file in dir.
func touchAll(dir string, at time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), at, at); err != nil {
			return err
		}
	}
	return nil
}
