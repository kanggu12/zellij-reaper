package ui

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// What passes do, for every timer the platforms report and both modes: a
// timer that is not running fires nothing, so its state is the state whatever
// the mode; one that runs gives the next pass, and the note when it will not
// start again. The times are built with Clock, which writes the date too on
// another day than today.
func TestPassStateWhenAndNote(t *testing.T) {
	i18n.Set(i18n.English)
	now := time.Now().Truncate(time.Minute)
	next := now.Add(41*time.Minute + 30*time.Second)
	at := "next pass " + Clock(next) + ", in 41m"
	running := timers.TimerStatus{Installed: true, Enabled: true, Active: true, Next: next, Interval: time.Hour}
	// A systemd timer disabled without --now: it fires until it stops.
	disabled := timers.TimerStatus{Installed: true, Active: true, Next: next, Interval: time.Hour}
	for _, c := range []struct {
		name              string
		st                timers.TimerStatus
		known, dry        bool
		cat               Category
		state, when, note string
	}{
		{"a running timer", running, true, false, CatAttached, "reaping on", at, ""},
		{"a running timer, dry run", running, true, true, CatReap, "dry run", at, ""},
		{"not asked yet", timers.TimerStatus{}, false, false, CatAttached, "reaping on", "", ""},
		{"not asked yet, dry run", timers.TimerStatus{}, false, true, CatReap, "dry run", "", ""},
		{"no timer", timers.TimerStatus{}, true, false, CatUnknown, "timer not installed", "", ""},
		{"no timer, dry run", timers.TimerStatus{}, true, true, CatUnknown, "timer not installed", "dry run", ""},
		{"stopped", timers.TimerStatus{Installed: true, Enabled: true}, true, false, CatUnknown, "timer off", "", ""},
		{"stopped, dry run", timers.TimerStatus{Installed: true, Enabled: true}, true, true, CatUnknown, "timer off", "dry run", ""},
		{"disabled, still running", disabled, true, false, CatAttached, "reaping on", at, "off after a restart"},
		{"disabled, still running, dry run", disabled, true, true, CatReap, "dry run", at, "off after a restart"},
		{"disabled and stopped", timers.TimerStatus{Installed: true}, true, false, CatUnknown, "timer off", "", ""},
		// launchd gives the interval, not the next time.
		{"launchd", timers.TimerStatus{Installed: true, Enabled: true, Active: true, Interval: time.Hour}, true, false, CatAttached, "reaping on", "a pass every 1h", ""},
		{"no time at all", timers.TimerStatus{Installed: true, Enabled: true, Active: true}, true, false, CatAttached, "reaping on", "next pass ?", ""},
	} {
		cat, state := PassState(c.dry, c.st, c.known)
		if cat != c.cat || state != c.state {
			t.Errorf("%s: state %v %q, want %v %q", c.name, cat, state, c.cat, c.state)
		}
		if got := PassWhen(c.dry, c.st, c.known, now, true); got != c.when {
			t.Errorf("%s: when %q, want %q", c.name, got, c.when)
		}
		if got := PassNote(c.st, c.known); got != c.note {
			t.Errorf("%s: note %q, want %q", c.name, got, c.note)
		}
	}
	// Without the count, and with a time already past: the time alone.
	if got := PassWhen(false, running, true, now, false); got != "next pass "+Clock(next) {
		t.Errorf("without the count: %q", got)
	}
	if got := PassWhen(false, running, true, next.Add(time.Second), true); got != "next pass "+Clock(next) {
		t.Errorf("a time already past: %q", got)
	}
	// In Korean, as the install messages say it.
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	if got, want := PassWhen(false, running, true, now, true), "다음 점검 "+Clock(next)+"(41m 뒤)"; got != want {
		t.Errorf("in Korean: %q, want %q", got, want)
	}
}

// The one-row mark is LogoMarkWidth cells: the header counts on it.
func TestLogoMarkIsOneRowOfItsWidth(t *testing.T) {
	for _, dark := range []bool{true, false} {
		m := NewTheme(dark).LogoMark()
		if w := lipgloss.Width(m); w != LogoMarkWidth {
			t.Errorf("dark %v: %d cells, want %d", dark, w, LogoMarkWidth)
		}
		if got := ansi.Strip(m); got != "▄▀▀▌" {
			t.Errorf("dark %v: %q", dark, got)
		}
	}
}
