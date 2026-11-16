package ui

import (
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// PassState is whether passes delete anything on their own now, as the
// dashboard's header and the line above plan's and run's tables say it: the
// category whose color it takes, and its words. A timer that is not running
// fires nothing, whatever the mode, so then the timer's state is the state.
// Running is the runtime state (Active): a systemd timer disabled without
// --now still fires until it stops or systemd starts again, which PassNote
// says. Before the timer has been asked (known false), the mode alone.
func PassState(dry bool, st timers.TimerStatus, known bool) (Category, string) {
	switch {
	case known && !st.Installed:
		return CatUnknown, i18n.T("header.timer.none")
	case known && !st.Active:
		return CatUnknown, i18n.T("header.timer.off")
	case dry:
		return CatReap, i18n.T("header.dry")
	}
	return CatAttached, i18n.T("header.live")
}

// PassWhen says when the timer runs next, and with countdown how long until
// then, counted from now. With the timer not running it says what a pass run
// by hand would do instead, if that is only to log; before the timer has been
// asked, nothing.
func PassWhen(dry bool, st timers.TimerStatus, known bool, now time.Time, countdown bool) string {
	switch {
	case !known:
		return ""
	case !st.Installed || !st.Active:
		if dry {
			return i18n.T("header.dry")
		}
		return ""
	// launchd does not say when it runs a job next: its interval does.
	case st.Next.IsZero() && st.Interval > 0:
		return i18n.T("header.every", duration.Format(st.Interval))
	case st.Next.IsZero():
		return i18n.T("header.next", "?")
	}
	// A time already past has no count left: the timer is about to say the
	// next one.
	if wait := st.Next.Sub(now); countdown && wait > 0 {
		return i18n.T("header.next.in", Clock(st.Next), duration.Format(wait))
	}
	return i18n.T("header.next", Clock(st.Next))
}

// PassNote is what a running timer that will not start again says: a systemd
// timer disabled without --now fires until it stops or systemd starts again,
// and then no more. Empty otherwise; only systemd has a timer that runs
// without being enabled.
func PassNote(st timers.TimerStatus, known bool) string {
	if known && st.Installed && st.Active && !st.Enabled {
		return i18n.T("header.timer.boot")
	}
	return ""
}
