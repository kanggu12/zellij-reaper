package ui

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

func TestTheSummaryCountsAtTheNextPassAndSaysWhatIsDueNow(t *testing.T) {
	i18n.Set(i18n.English)
	now := time.Now()
	th := policy.Thresholds{Idle: time.Hour, Exited: 72 * time.Hour}
	at := func(name string, idle time.Duration) reaper.Outcome {
		f := policy.Facts{Name: name, HasInfo: true, LastActive: now.Add(-idle), ActivitySource: "pane terminals",
			SocketOK: true, ServerPID: 7, Attached: policy.No, PaneCount: 1}
		return reaper.Outcome{Session: inspect.Session{Facts: f}, Verdict: policy.Decide(f, th, now)}
	}
	r := reaper.Report{Thresholds: th, Outcomes: []reaper.Outcome{at("old", 3*time.Hour), at("almost", 55*time.Minute), at("fresh", time.Minute)}}
	for _, c := range []struct {
		next time.Time
		want string
	}{
		{now.Add(10 * time.Minute), "3 sessions · 2 to reap at the next pass (1 due now)"},
		// Unknown, or a timer that does not run: as of now, nothing more.
		{time.Time{}, "3 sessions · 1 to reap at the next pass"},
		{now.Add(-time.Minute), "3 sessions · 1 to reap at the next pass"},
	} {
		if got := Summary(r, c.next, now); got != c.want {
			t.Errorf("next %v: %q, want %q", c.next, got, c.want)
		}
	}
}
