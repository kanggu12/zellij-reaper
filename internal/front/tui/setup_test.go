package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// setupSample starts the setup screen on the sample sessions: with a 1h idle
// wait the first pass reaps scratch (idle 3h) and old-build (exited 100h).
func setupSample(hook string) SetupStart {
	now := time.Now()
	bash := inspect.Pane{PID: 1, Command: "-bash", Prompt: true, LastIO: now.Add(-3 * time.Hour)}
	r := reaper.Report{Outcomes: []reaper.Outcome{
		session("scratch", false, nil, bash),
		session("api-server", false, func(f *policy.Facts) { f.BusyWith = "npm run dev" }, inspect.Pane{PID: 2, Command: "npm run dev", Busy: "npm run dev"}),
		session("notes", false, func(f *policy.Facts) { f.Attached = policy.Yes }, bash),
		session("old-build", true, func(f *policy.Facts) { f.LastActive, f.ServerPID = now.Add(-100*time.Hour), 0 }),
	}}
	return SetupStart{IdleAfter: time.Hour, ExitedAfter: 72 * time.Hour, AutoRename: true,
		Report: r, FirstPass: time.Now().Add(10 * time.Minute), CanRename: true, HookFile: hook}
}

// pressAll sends keys to the setup screen.
func pressAll(m setupModel, keys ...string) setupModel {
	for _, k := range keys {
		next, _ := m.Update(snapshotKey(k))
		m = next.(setupModel)
	}
	return m
}

func names(m setupModel) []string {
	var out []string
	for _, o := range m.listed() {
		out = append(out, o.Session.Facts.Name)
	}
	return out
}

func TestSetupListsWhatTheFirstPassWouldReap(t *testing.T) {
	m := newSetup(setupSample(""), true)
	if got := names(m); !slices.Equal(got, []string{"scratch", "old-build"}) {
		t.Fatalf("listed %v, want scratch and old-build", got)
	}
	// A longer idle wait (4h) leaves scratch, idle 3h, alone.
	m = pressAll(m, "right")
	if got := names(m); !slices.Equal(got, []string{"old-build"}) {
		t.Errorf("with 4h: listed %v, want old-build", got)
	}
	// A week for exited sessions leaves old-build too.
	m = pressAll(m, "down", "right")
	if m.due() != 0 || !strings.Contains(m.render(), i18n.T("setup.first.none")) {
		t.Errorf("with 4h and 1w: %d due, want none said", m.due())
	}
}

func TestSetupKeepsWhatIsMarked(t *testing.T) {
	m := newSetup(setupSample(""), true)
	// Rows: idle, exited, mode, names, then the two sessions.
	m = pressAll(m, "down", "down", "down", "down", "space")
	c := m.Choice()
	if !slices.Equal(c.Protect, []string{"scratch"}) {
		t.Fatalf("protect %v, want scratch", c.Protect)
	}
	if m.due() != 1 {
		t.Errorf("%d due after keeping scratch, want 1", m.due())
	}
	// A kept session stays listed, so it can be let go again.
	m = pressAll(m, "space")
	if c := m.Choice(); len(c.Protect) != 0 {
		t.Errorf("protect %v after unmarking, want none", c.Protect)
	}
}

func TestSetupChoices(t *testing.T) {
	m := newSetup(setupSample("~/.bashrc"), true)
	if !m.Choice().Hook {
		t.Error("the hook is offered on while names are on")
	}
	// mode → only log; names → off, which takes the hook row and the hook away.
	m = pressAll(m, "down", "down", "space", "down", "space")
	c := m.Choice()
	if !c.DryRun || c.AutoRename || c.Hook {
		t.Errorf("got %+v, want dry run, no names, no hook", c)
	}
	if slices.Contains(m.rows(), rowHook) {
		t.Error("the hook row stays with names off")
	}
	// Enter chooses; esc leaves.
	if m = pressAll(m, "enter"); !m.done || m.how != SetupChosen {
		t.Error("enter does not choose")
	}
	if m = pressAll(newSetup(setupSample(""), true), "esc"); !m.done || m.how != SetupLeft {
		t.Error("esc does not leave")
	}
}

func TestSetupHidesNamesWhereRenamesCannotWork(t *testing.T) {
	s := setupSample("~/.bashrc")
	s.CanRename = false
	m := newSetup(s, true)
	if slices.Contains(m.rows(), rowNames) || slices.Contains(m.rows(), rowHook) || m.Choice().AutoRename {
		t.Error("names or the hook offered where renames cannot work")
	}
}

func TestSetupWaitsOutsideTheChoicesStay(t *testing.T) {
	s := setupSample("")
	s.IdleAfter = 90 * time.Minute
	if c := newSetup(s, true).Choice(); c.IdleAfter != 90*time.Minute {
		t.Errorf("idle %v, want the 90m in effect", c.IdleAfter)
	}
}

func TestSetupFitsEverySize(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, sz := range [][2]int{{140, 34}, {100, 30}, {80, 24}, {60, 16}, {30, 8}, {20, 5}} {
			for _, keys := range []string{"", "down down down down down", "down down space"} {
				m := pressAll(newSetup(setupSample("~/.bashrc"), false), strings.Fields(keys)...)
				m.width, m.height = sz[0], sz[1]
				measure(t, string(lang)+" setup "+keys, m.render(), sz[0], sz[1])
			}
		}
	}
	i18n.Set(i18n.English)
}

func TestSetupOpensWithoutAPlan(t *testing.T) {
	s := SetupStart{IdleAfter: 12 * time.Hour, ExitedAfter: 72 * time.Hour, FirstPass: time.Now(), NoPlan: "zellij is not installed"}
	m := pressAll(newSetup(s, true), "right", "enter")
	if m.how != SetupChosen || m.Choice().IdleAfter != 24*time.Hour || !strings.Contains(m.render(), "zellij is not installed") {
		t.Errorf("without a plan: how %v, idle %v", m.how, m.Choice().IdleAfter)
	}
}

func TestSetupListFollowsTheCursorWhenShort(t *testing.T) {
	// 13 rows tall: two session rows do not fit with the settings above.
	m := pressAll(newSetup(setupSample("~/.bashrc"), true), "down", "down", "down", "down", "down", "down")
	m.width, m.height = 100, 13
	out := ansi.Strip(m.render())
	if !strings.Contains(out, "› [ ] old-build") {
		t.Errorf("the selected session is not on screen:\n%s", out)
	}
}
