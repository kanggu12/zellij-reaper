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
	if got := FitMiddle("세션이름이아주긴세션", 9); lipgloss.Width(got) != 9 {
		t.Errorf("wide letters: %q is %d cells", got, lipgloss.Width(got))
	}
}
