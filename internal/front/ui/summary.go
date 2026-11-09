package ui

import (
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// Summary is the line under a plan's table and the dashboard's status line:
// how many sessions there are and how many the next pass reaps. next is when
// the timer runs that pass (zero when it does not say): the count is taken
// then, and when sessions are due by then that are not due now, the count as
// of now follows, since only those rows show as due.
func Summary(r reaper.Report, next, now time.Time) string {
	n := len(r.Outcomes)
	key := "plan.summary"
	if r.DryRun {
		key = "plan.summary.dry"
	}
	due, nowDue := r.DueAtNext(next, now), r.Due()
	line := i18n.TN(key, n, n, due)
	if due != nowDue {
		line += " " + i18n.T("plan.summary.duenow", nowDue)
	}
	return line
}
