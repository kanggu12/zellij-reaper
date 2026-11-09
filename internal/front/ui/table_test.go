package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

func TestKeptAtTheDeleteReadsInTheReadersLanguage(t *testing.T) {
	defer i18n.Set(i18n.English)
	// A session the policy let go, kept because a client attached during
	// the pass.
	r := reaper.Report{Outcomes: []reaper.Outcome{{
		Session:      inspect.Session{Listed: zellij.Listed{Name: "notes"}},
		Verdict:      policy.Verdict{Action: policy.Reap, Code: policy.IdleOld},
		KeptAtDelete: "1 client(s) attached since the check",
	}}}
	for lang, want := range map[i18n.Lang]string{
		i18n.English: "kept: 1 client(s) attached since the check",
		i18n.Korean:  "유지: 1 client(s) attached since the check",
	} {
		i18n.Set(lang)
		rows := RowsAfterPass(r, time.Now())
		if rows[0].Next != want || rows[0].Cat != CatUnknown {
			t.Errorf("%s: %q (%v), want %q", lang, rows[0].Next, rows[0].Cat, want)
		}
	}
}

func TestFitMiddleKeepsBothEndsOfAName(t *testing.T) {
	// Cut from the end, these two read the same: "release-notes-dra…".
	a, b := FitMiddle("release-notes-draft", 18), FitMiddle("release-notes-draft-2", 18)
	if a == b || lipgloss.Width(a) != 18 || lipgloss.Width(b) != 18 {
		t.Errorf("%q and %q", a, b)
	}
	if got := FitMiddle("short", 18); got != "short"+strings.Repeat(" ", 13) {
		t.Errorf("a short name changed: %q", got)
	}
	// Wide letters: the end is kept, never cut a second time.
	for _, c := range []struct {
		name string
		w    int
	}{{"세션이름이아주긴세션", 9}, {"세션이름이아주긴세션", 10}, {"a세션이름이아주긴세션b", 6}} {
		got := FitMiddle(c.name, c.w)
		last := []rune(c.name)[len([]rune(c.name))-1]
		if lipgloss.Width(got) != c.w || strings.Count(got, "…") != 1 || !strings.HasSuffix(strings.TrimRight(got, " "), string(last)) {
			t.Errorf("FitMiddle(%q, %d) = %q", c.name, c.w, got)
		}
	}
}

func TestWithoutColorsTheSelectedRowIsReversed(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	th := NewTheme(true)
	rows := []Row{{Name: "a", Next: "reap"}, {Name: "b", Next: "reap"}}
	lines := th.Table(rows, 60, 1)
	if !strings.Contains(lines[2], "\x1b[7m") && !strings.Contains(lines[2], ";7m") && !strings.Contains(lines[2], ";7;") {
		t.Errorf("the selected row is not reversed: %q", lines[2])
	}
}
