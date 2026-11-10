package reaper_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
)

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
