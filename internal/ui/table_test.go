package ui

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/i18n"
	"github.com/kanggu12/zellij-reaper/internal/inspect"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/reaper"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
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
