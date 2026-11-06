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
	// zellij rewrites its session files every minute: they cannot say how
	// long it has been idle.
	w := newWorld(t)
	if err := os.Remove(filepath.Join(w.env.Sources.ProcRoot, "101", "fd", "0")); err != nil {
		t.Fatal(err)
	}
	w.env.QuietPath = filepath.Join(t.TempDir(), "state", "quiet")
	start := time.Now()
	w.env.Now = func() time.Time { return start }
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
