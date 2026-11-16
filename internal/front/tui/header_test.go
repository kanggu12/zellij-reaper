package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/version"
)

// headerState is one state the header can be in, set on a loaded model.
type headerState struct {
	name string
	set  func(m *model)
}

// headerStates are the states of the timer and the mode the header tells
// apart.
var headerStates = []headerState{
	{"a timer with its next pass", func(m *model) {}},
	{"dry run", func(m *model) { dryRun(m) }},
	{"the timer off", func(m *model) { m.timer = timers.TimerStatus{Installed: true, Enabled: true} }},
	{"the timer off, dry run", func(m *model) { m.timer = timers.TimerStatus{Installed: true, Enabled: true}; dryRun(m) }},
	{"no timer", func(m *model) { m.timer = timers.TimerStatus{} }},
	// A systemd timer disabled without --now: it runs until it stops.
	{"a running timer not enabled", func(m *model) { m.timer.Enabled = false }},
	{"launchd's interval", func(m *model) {
		m.timer = timers.TimerStatus{Installed: true, Enabled: true, Active: true, Interval: time.Hour}
	}},
	{"before systemd answers", func(m *model) { m.timerKnown = false }},
	{"a reload in flight", func(m *model) { m.loading = true }},
}

// dryRun sets the model's settings to dry run, on a copy.
func dryRun(m *model) {
	c := *m.b.Config()
	c.DryRun = true
	m.b.(*fake).cfg = &c
}

// What runs on its own: never "reaping on" beside a stopped timer, as the
// header said, and a stopped timer never hides one that still runs.
func TestTheHeaderSaysWhatRunsOnItsOwn(t *testing.T) {
	i18n.Set(i18n.English)
	for _, c := range []struct {
		state       string
		want, never []string
	}{
		{"a timer with its next pass", []string{"● reaping on", "next pass "}, []string{"timer", "dry run", "restart"}},
		{"dry run", []string{"● dry run", "next pass "}, []string{"reaping on", "timer"}},
		{"the timer off", []string{"● timer off"}, []string{"reaping on", "dry run", "next pass"}},
		{"the timer off, dry run", []string{"● timer off · dry run"}, []string{"reaping on", "next pass"}},
		{"no timer", []string{"● timer not installed"}, []string{"reaping on", "next pass"}},
		{"a running timer not enabled", []string{"● reaping on · next pass ", " · off after a restart"}, []string{"timer off"}},
		{"launchd's interval", []string{"● reaping on · a pass every 1h"}, []string{"timer"}},
		{"before systemd answers", []string{"● reaping on"}, []string{"timer", "next pass"}},
	} {
		m := headerModel(t, c.state, 200)
		h := ansi.Strip(m.header())
		for _, w := range c.want {
			if !strings.Contains(h, w) {
				t.Errorf("%s: no %q in %q", c.state, w, h)
			}
		}
		for _, n := range c.never {
			if strings.Contains(h, n) {
				t.Errorf("%s: %q in %q", c.state, n, h)
			}
		}
	}
}

// Each tick moves the clock the header counts down from.
func TestATickMovesTheCountdownsClock(t *testing.T) {
	m := loaded(t, sample(), 120, 30)
	at := time.Now().Add(5 * time.Minute)
	if m = step(t, m, tickMsg(at)); !m.clock.Equal(at) {
		t.Errorf("after a tick at %s the clock is %s", at, m.clock)
	}
}

// A timer that runs without being enabled fires at its next pass, and the
// status line counts what that pass reaps, as the header gives its time: a
// session idle 30m with a 1h wait is not due now but is by then, 42 minutes
// away, as in TestTheDashboardCountsWhatTheNextPassReaps.
func TestATimerThatRunsDisabledCountsAtItsNextPass(t *testing.T) {
	i18n.Set(i18n.English)
	b := sample()
	now := time.Now()
	b.report.Outcomes = append(b.report.Outcomes, session("soon", false, func(f *policy.Facts) { f.LastActive = now.Add(-30 * time.Minute) }))
	m := loaded(t, b, 200, 30)
	m.timer.Enabled = false
	want := b.report.Due() + 1
	if s := ansi.Strip(m.statusLine()); !strings.Contains(s, fmt.Sprintf("%d to reap at the next pass · %d due now", want, want-1)) {
		t.Errorf("the status line under a running timer not enabled: %q, want %d at the next pass", s, want)
	}
	if h := ansi.Strip(m.header()); !strings.Contains(h, "next pass ") {
		t.Errorf("the header under a running timer not enabled: %q", h)
	}
}

// The next pass is a time and how long until then, counted from the later of
// the last plan and the last tick; a time already past keeps only the time.
func TestTheHeaderCountsDownToTheNextPass(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		m := headerModel(t, "a timer with its next pass", 200)
		next := m.timer.Next
		want := i18n.T("header.next.in", ui.Clock(next), duration.Format(next.Sub(m.now)))
		if h := ansi.Strip(m.header()); !strings.Contains(h, want) {
			t.Errorf("%s: no %q in %q", lang, want, h)
		}
		// A plan slow to come: the tick's time counts.
		m.clock = m.now.Add(10 * time.Minute)
		want = i18n.T("header.next.in", ui.Clock(next), duration.Format(next.Sub(m.clock)))
		if h := ansi.Strip(m.header()); !strings.Contains(h, want) {
			t.Errorf("%s, a tick after the plan: no %q in %q", lang, want, h)
		}
		// Past the time, the header says the time alone: with and without
		// the count, the words are the same.
		m.now = next.Add(time.Minute)
		past := i18n.T("header.next", ui.Clock(next))
		if h := ansi.Strip(m.header()); !strings.Contains(h, past) || ansi.Strip(m.whenWords(true)) != ansi.Strip(m.whenWords(false)) {
			t.Errorf("%s: a past time %q in %q", lang, past, h)
		}
	}
	i18n.Set(i18n.English)
}

// A narrow header drops its pieces in headerDrops' order whatever the state:
// a piece is on screen only if every piece after it in that order, which the
// state has, is on screen too. It drops no piece before it must: on a screen
// exactly as wide as the header without the first k pieces, those k are all
// that went. The state is always there, a reload changes no piece, and the
// line is never wider than the screen, with a cell of margin at its right end
// and a blank before the state.
func TestTheHeaderDropsItsPiecesInOneOrder(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, s := range headerStates {
			m := headerModel(t, s.name, 300)
			has := func(m model) map[string]bool { return headerPieces(m, ansi.Strip(m.header())) }
			all := has(m)
			state := ansi.Strip(m.stateWords())
			for w := 30; w <= 220; w++ {
				m.width = w
				raw := m.header()
				h := ansi.Strip(raw)
				if got := lipgloss.Width(raw); got > w {
					t.Errorf("%s, %s, %d columns: the header is %d wide: %q", lang, s.name, w, got, h)
				}
				if !strings.Contains(h, state) {
					t.Errorf("%s, %s, %d columns: the state is gone: %q", lang, s.name, w, h)
				}
				if !strings.HasSuffix(h, " ") || !strings.Contains(h, " "+state) {
					t.Errorf("%s, %s, %d columns: no margin at the end, or no blank before the state: %q", lang, s.name, w, h)
				}
				on := has(m)
				for i, piece := range headerDrops {
					for _, kept := range headerDrops[i+1:] {
						if on[piece] && all[kept] && !on[kept] {
							t.Errorf("%s, %s, %d columns: %s is there and %s is not: %q", lang, s.name, w, piece, kept, h)
						}
					}
				}
				// A reload in flight shows the same pieces.
				other := m
				other.loading = !m.loading
				if got := has(other); !maps.Equal(got, on) {
					t.Errorf("%s, %s, %d columns: a reload changes the pieces: %v, then %v", lang, s.name, w, on, got)
				}
			}
			// No piece goes early.
			for k := range headerDrops {
				_, need := m.headerWith(headerDrops[:k])
				if need < 30 || need > 300 {
					continue
				}
				// The line as it is padded on a screen that wide.
				m.width = need
				line, _ := m.headerWith(headerDrops[:k])
				if got := m.header(); got != line {
					t.Errorf("%s, %s, %d columns: more went than %v: %q", lang, s.name, need, headerDrops[:k], ansi.Strip(got))
				}
			}
		}
	}
	i18n.Set(i18n.English)
}

// headerModel is a loaded model in one of headerStates, on a screen w wide.
func headerModel(t *testing.T, state string, w int) model {
	t.Helper()
	m := loaded(t, sample(), w, 30)
	i := slices.IndexFunc(headerStates, func(s headerState) bool { return s.name == state })
	if i < 0 {
		t.Fatalf("no header state %q", state)
	}
	headerStates[i].set(&m)
	return m
}

// headerPieces says which of headerDrops' pieces a header shows, from its
// words: each piece as the header writes it.
func headerPieces(m model, h string) map[string]bool {
	cfg := m.b.Config()
	on := map[string]bool{
		"name":    strings.Contains(h, "zellij-reaper"),
		"mark":    strings.Contains(h, "▄▀▀▌"),
		"version": strings.Contains(h, "zellij-reaper  "+version.Short()),
		"zellij":  strings.Contains(h, "zellij "+m.version),
		"waits":   strings.Contains(h, i18n.T("settings.waits", duration.Format(cfg.IdleAfter), duration.Format(cfg.ExitedAfter))),
	}
	if n := ui.PassNote(m.timer, m.timerKnown); n != "" {
		on["note"] = strings.Contains(h, n)
	}
	full, short := ansi.Strip(m.whenWords(true)), ansi.Strip(m.whenWords(false))
	on["countdown"] = full != short && strings.Contains(h, full)
	on["when"] = short != "" && strings.Contains(h, short)
	return on
}
