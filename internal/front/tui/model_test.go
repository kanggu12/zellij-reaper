package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/systemd"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// fake is a Backend that records what the dashboard asks it to do.
type fake struct {
	report    reaper.Report
	planErr   error
	cfg       *config.Config
	deleted   []string
	renamed   []string
	passes    []bool
	protected map[string]bool
	log       []string
	// renameNote is the warning Rename returns.
	renameNote string
	// kept are the names KeepName was asked to keep.
	kept []string
}

func (f *fake) Plan(context.Context) (reaper.Report, error) { return f.report, f.planErr }
func (f *fake) Pass(_ context.Context, force bool) (reaper.Report, error) {
	f.passes = append(f.passes, force)
	return f.report, nil
}
func (f *fake) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}
func (f *fake) Rename(_ context.Context, name, to string) (string, error) {
	f.renamed = append(f.renamed, name+"->"+to)
	return f.renameNote, nil
}
func (f *fake) KeepName(name string) error {
	f.kept = append(f.kept, name)
	return nil
}
func (f *fake) SetProtected(name string, on bool) error { f.protected[name] = on; return nil }
func (f *fake) Protected(name string) bool              { return f.protected[name] }
func (f *fake) PatternProtects(string) bool             { return false }
func (f *fake) Timer() systemd.TimerStatus {
	return systemd.TimerStatus{Installed: true, Enabled: true, Active: true, Next: time.Now().Add(42 * time.Minute), Interval: time.Hour}
}
func (f *fake) ZellijVersion(context.Context) string   { return "0.45.1" }
func (f *fake) Config() *config.Config                 { return f.cfg }
func (f *fake) LogLines(n int) ([]string, error)       { return f.log, nil }
func (f *fake) AttachCommand(string) (*exec.Cmd, bool) { return nil, false }

// session builds one outcome from facts, as a plan would.
func session(name string, exited bool, f func(*policy.Facts), panes ...inspect.Pane) reaper.Outcome {
	now := time.Now()
	facts := policy.Facts{Name: name, Exited: exited, HasInfo: true, LastActive: now.Add(-3 * time.Hour), ActivitySource: "pane terminals",
		SocketOK: true, ServerPID: 4242, Attached: policy.No, PaneCount: max(1, len(panes))}
	if f != nil {
		f(&facts)
	}
	s := inspect.Session{Listed: zellij.Listed{Name: name, Exited: exited}, Facts: facts, Panes: panes, Clients: 0,
		Server: &procfs.Server{PID: 4242, Socket: "/run/user/1000/zellij/contract_version_1/" + name}, ServerMatch: "socket file"}
	if exited {
		s.Server, s.Clients = nil, -1
	}
	if facts.Attached == policy.Yes {
		s.Clients = 1
	}
	return reaper.Outcome{Session: s, Verdict: policy.Decide(facts, policy.Thresholds{Idle: time.Hour, Exited: 72 * time.Hour}, now)}
}

// sample is a dashboard with one session of every kind.
func sample() *fake {
	now := time.Now()
	bash := inspect.Pane{PID: 1, Command: "-bash", Prompt: true, LastIO: now.Add(-3 * time.Hour)}
	cfg := config.Default(config.MapEnv{"HOME": "/home/u"})
	cfg.IdleAfter, cfg.ExitedAfter = time.Hour, 72*time.Hour
	return &fake{
		cfg:       cfg,
		protected: map[string]bool{},
		log: []string{
			"[2026-11-04 09:00:00] === reaper start (dry_run=false idle_after=1h exited_after=3d) ===",
			"[2026-11-04 09:00:00] api-server [RUNNING] panes={npm run dev} :: SKIP running in a pane: npm run dev",
			"[2026-11-04 09:00:00] scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 3h since last activity",
			"[2026-11-04 09:00:00]   -> deleted: scratch",
			"[2026-11-04 09:00:00] === reaper end (1 reaped, 1 kept, 0 renamed) ===",
		},
		report: reaper.Report{Outcomes: []reaper.Outcome{
			session("scratch", false, nil, bash),
			session("api-server", false, func(f *policy.Facts) { f.BusyWith = "npm run dev" }, inspect.Pane{PID: 2, Command: "npm run dev", Busy: "npm run dev"}),
			session("notes", false, func(f *policy.Facts) { f.Attached, f.AttachDetail = policy.Yes, "1 client(s) attached" }, bash),
			session("agent-refactor_1104-0931", false, func(f *policy.Facts) { f.ProtectedCommand = "claude" }, inspect.Pane{PID: 3, Command: "claude", Busy: "claude"}),
			session("fresh", false, func(f *policy.Facts) { f.LastActive = now.Add(-12 * time.Minute) }, bash),
			session("old-build", true, func(f *policy.Facts) { f.LastActive = now.Add(-100 * time.Hour) }),
			session("lost", false, func(f *policy.Facts) { f.ServerPID = 0 }),
		}},
	}
}

// loaded is a model that has received its first plan.
func loaded(t *testing.T, b *fake, w, h int) model {
	t.Helper()
	m := newModel(context.Background(), b)
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, timerMsg{st: b.Timer()})
	m = step(t, m, versionMsg("0.45.1"))
	r, err := b.Plan(context.Background())
	return step(t, m, planMsg{report: r, err: err})
}

// pick moves the selection down to the named session, whatever the order.
func pick(t *testing.T, m model, name string) model {
	t.Helper()
	m = press(t, m, "g")
	for range m.rows() {
		if o, ok := m.selected(); ok && o.Session.Name() == name {
			return m
		}
		m = press(t, m, "down")
	}
	t.Fatalf("no row %q", name)
	return m
}

// step feeds one message.
func step(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(model)
}

// press feeds one key and runs the command it returns (one level, which
// covers every action), feeding the result back.
func press(t *testing.T, m model, key string) model {
	t.Helper()
	var k tea.KeyPressMsg
	switch key {
	case "enter":
		k = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		k = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		k = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		k = tea.KeyPressMsg{Code: tea.KeyUp}
	default:
		r := []rune(key)[0]
		k = tea.KeyPressMsg{Code: r, Text: key}
	}
	next, cmd := m.Update(k)
	m = next.(model)
	if cmd != nil {
		if msg := runCmd(cmd); msg != nil {
			if _, quit := msg.(tea.QuitMsg); !quit {
				m = step(t, m, msg)
			}
		}
	}
	return m
}

// runCmd runs a command unless it would wait (ticks, cursor blinks).
func runCmd(cmd tea.Cmd) tea.Msg {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(200 * time.Millisecond):
		return nil
	}
}

// measure checks that a screen is exactly w x h cells.
func measure(t *testing.T, name string, content string, w, h int) {
	t.Helper()
	lines := strings.Split(content, "\n")
	if len(lines) != h {
		t.Errorf("%s: %d lines on a %d-line screen", name, len(lines), h)
	}
	for i, l := range lines {
		if got := lipgloss.Width(l); got != w {
			t.Errorf("%s: line %d is %d cells wide on a %d-cell screen: %q", name, i, got, w, l)
			return
		}
	}
}

func TestEveryStateFitsEverySize(t *testing.T) {
	sizes := [][2]int{{60, 16}, {80, 24}, {100, 30}, {132, 40}, {200, 50}}
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, sz := range sizes {
			w, h := sz[0], sz[1]
			name := func(s string) string { return fmt.Sprintf("%s %dx%d %s", lang, w, h, s) }
			m := loaded(t, sample(), w, h)
			measure(t, name("main"), m.View().Content, w, h)
			measure(t, name("help"), press(t, m, "?").View().Content, w, h)
			measure(t, name("log"), press(t, m, "l").View().Content, w, h)
			measure(t, name("confirm"), press(t, m, "d").View().Content, w, h)
			measure(t, name("force"), press(t, m, "F").View().Content, w, h)
			measure(t, name("rename"), press(t, m, "r").View().Content, w, h)
			measure(t, name("filter"), press(t, m, "/").View().Content, w, h)
			// The bottom of a long list.
			end := m
			for range 10 {
				end = press(t, end, "down")
			}
			measure(t, name("end"), end.View().Content, w, h)
			empty := &fake{cfg: sample().cfg, protected: map[string]bool{}, report: reaper.Report{NoSessions: true}}
			measure(t, name("empty"), loaded(t, empty, w, h).View().Content, w, h)
			missing := &fake{cfg: sample().cfg, protected: map[string]bool{}, planErr: reaper.ErrNoZellij}
			measure(t, name("no zellij"), loaded(t, missing, w, h).View().Content, w, h)
			broken := &fake{cfg: sample().cfg, protected: map[string]bool{}, planErr: errors.New("zellij list-sessions: Error occurred: Os { code: 13 }")}
			measure(t, name("list error"), loaded(t, broken, w, h).View().Content, w, h)
			first := newModel(context.Background(), sample())
			first = step(t, first, tea.WindowSizeMsg{Width: w, Height: h})
			measure(t, name("loading"), first.View().Content, w, h)
		}
	}
	i18n.Set(i18n.English)
}

func TestActions(t *testing.T) {
	i18n.Set(i18n.English)
	b := sample()
	m := loaded(t, b, 120, 40)
	// The first row is selected; d asks first, y reaps.
	m = press(t, m, "d")
	if m.mode != modeConfirm {
		t.Fatalf("d did not ask: mode %v", m.mode)
	}
	m = press(t, m, "y")
	if strings.Join(b.deleted, ",") != "scratch" {
		t.Errorf("deleted %v", b.deleted)
	}
	if !strings.Contains(m.statusLine(), "Reaped scratch") {
		t.Errorf("status after delete: %q", m.statusLine())
	}
	// n says no.
	m = press(t, press(t, m, "d"), "n")
	if len(b.deleted) != 1 {
		t.Errorf("n deleted: %v", b.deleted)
	}
	// An attached session is never offered for reaping.
	m = pick(t, m, "notes")
	m = press(t, m, "d")
	if m.mode == modeConfirm || len(b.deleted) != 1 {
		t.Error("d offered to reap an attached session")
	}
	// p protects, p again lifts it.
	m = press(t, m, "p")
	if !b.protected["notes"] {
		t.Error("p did not protect")
	}
	m = press(t, m, "p")
	if b.protected["notes"] {
		t.Error("p did not unprotect")
	}
	// r renames through the input.
	m = press(t, m, "r")
	m.input.set("journal")
	m = press(t, m, "enter")
	if strings.Join(b.renamed, ",") != "notes->journal" {
		t.Errorf("renamed %v", b.renamed)
	}
	// f asks first when the pass would reap something; F always asks.
	m = press(t, m, "f")
	if m.mode != modeConfirm || len(b.passes) != 0 {
		t.Fatalf("f ran a pass that reaps without asking (mode %v, passes %v)", m.mode, b.passes)
	}
	m = press(t, m, "y")
	m = press(t, press(t, m, "F"), "y")
	if fmt.Sprint(b.passes) != "[false true]" {
		t.Errorf("passes %v", b.passes)
	}
	// / filters as it is typed; esc in the main view clears it.
	m = press(t, m, "/")
	for _, r := range "old" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")
	if rows := m.rows(); len(rows) != 1 || rows[0].Name != "old-build" {
		t.Errorf("filtered rows %v", rows)
	}
	m = press(t, m, "esc")
	if len(m.rows()) != 7 {
		t.Errorf("esc did not clear the filter: %d rows", len(m.rows()))
	}
	// Esc while typing a filter puts the old one back.
	m = press(t, m, "/")
	m = press(t, m, "z")
	m = press(t, m, "esc")
	if m.filter != "" || len(m.rows()) != 7 {
		t.Errorf("esc kept the typed filter %q", m.filter)
	}
	// Esc in the main view never quits.
	if next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil {
		if _, quit := runCmd(cmd).(tea.QuitMsg); quit {
			t.Error("esc quit the dashboard")
		}
		_ = next
	}
}

func TestMessagesAndTinyScreensFit(t *testing.T) {
	i18n.Set(i18n.English)
	for _, sz := range [][2]int{{80, 1}, {20, 3}, {30, 8}, {10, 10}, {29, 40}, {200, 7}} {
		m := loaded(t, sample(), sz[0], sz[1])
		measure(t, fmt.Sprintf("tiny %dx%d", sz[0], sz[1]), m.View().Content, sz[0], sz[1])
		for _, key := range []string{"d", "F", "r", "/", "?", "l"} {
			measure(t, fmt.Sprintf("tiny %dx%d after %s", sz[0], sz[1], key), press(t, m, key).View().Content, sz[0], sz[1])
		}
	}
	// zellij's errors run over several lines; the screen must not grow.
	m := loaded(t, sample(), 100, 30)
	m.say("Could not attach: Error occurred:\n  Os { code: 13 }\nsecond line", 0)
	measure(t, "multi-line toast", m.View().Content, 100, 30)
	broken := &fake{cfg: sample().cfg, protected: map[string]bool{}, planErr: errors.New("one\ntwo\nthree")}
	measure(t, "multi-line error", loaded(t, broken, 80, 24).View().Content, 80, 24)
}

func TestLogViewLaysOutLines(t *testing.T) {
	th := ui.NewTheme(true)
	plain := func(raw string) string { return ansi.Strip(logLine(th, raw)) }
	cases := map[string]string{
		"[2026-11-04 09:00:00] === reaper start (dry_run=false idle_after=1h exited_after=3d) ===":      "11-04 09:00  ── pass started · idle 1h · exited 3d",
		"[2026-11-04 09:00:00] === reaper start (dry_run=true idle_after=0s exited_after=0s force) ===": "11-04 09:00  ── pass started · idle 0s · exited 0s · dry run · skipping the waits",
		"[2026-11-04 09:00:00] === reaper end (1 reaped, 2 kept, 0 renamed) ===":                        "11-04 09:00  ── pass done: 1 reaped, 2 kept, 0 renamed",
		"[2026-11-04 09:00:00] === reaper end ===":                                                      "11-04 09:00  ── pass done",
		"[2026-11-04 09:00:00] tmp [RUNNING] panes={Pane #1} :: REAP IDLE, 3h since last activity":      "11-04 09:00  tmp  reap: idle 3h",
		"[2026-11-04 09:00:00] web [RUNNING] panes={Pane #1} :: SKIP running in a pane: npm run dev":    "11-04 09:00  web  kept: running npm run dev",
		// Words the dashboard does not know stay as the log wrote them.
		"[2026-05-12 10:00:00] old [RUNNING] panes={a} :: SKIP something older":            "05-12 10:00  old [RUNNING] panes={a} :: SKIP something older",
		"[2026-11-04 09:00:00]   -> deleted: tmp":                                          "11-04 09:00    → deleted tmp",
		"[2026-11-04 09:00:00]   -> kept: tmp :: a client attached since the plan":         "11-04 09:00    → kept tmp: a client attached since the plan",
		"[2026-11-04 09:00:00]   -> FAILED: tmp :: exit status 1":                          "11-04 09:00    → could not delete tmp: exit status 1",
		"[2026-11-04 09:00:00]   -> renamed: tmp → vim-notes_1104-0900":                    "11-04 09:00    → renamed tmp → vim-notes_1104-0900",
		"[2026-11-04 09:00:00]   -> DRY_RUN: would run: zellij delete-session tmp --force": "11-04 09:00    → would delete tmp (dry run)",
		"[2026-11-04 09:00:00]   -> DRY_RUN: would rename 'tmp' → 'vim_1104-0900'":         "11-04 09:00    → would rename tmp → vim_1104-0900 (dry run)",
		// A line the view does not know, such as the shell version's, is
		// shown as written.
		"[2026-05-12 10:00:00] === reaper start (dry_run=1 max_age_hours=72) ===": "05-12 10:00  === reaper start (dry_run=1 max_age_hours=72) ===",
	}
	i18n.Set(i18n.English)
	for raw, want := range cases {
		if got := plain(raw); got != want {
			t.Errorf("%s\n got %q\nwant %q", raw, got, want)
		}
	}
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	if got := plain("[2026-11-04 09:00:00]   -> deleted: tmp"); got != "11-04 09:00    → tmp 정리함" {
		t.Errorf("korean arrow %q", got)
	}
}

func TestScreensWithoutSessions(t *testing.T) {
	i18n.Set(i18n.English)
	// Before systemd answers, the header says nothing about the timer.
	cold := newModel(context.Background(), sample())
	cold.width, cold.height = 100, 24
	if v := cold.View().Content; strings.Contains(v, "timer") {
		t.Error("the header claims a timer state before systemd answered")
	}
	// With nothing to act on, only the keys that work are offered.
	empty := &fake{cfg: sample().cfg, protected: map[string]bool{}, report: reaper.Report{NoSessions: true}}
	foot := ansi.Strip(loaded(t, empty, 100, 24).footer())
	if !strings.Contains(foot, "refresh") || strings.Contains(foot, "reap") || strings.Contains(foot, "attach") {
		t.Errorf("footer on an empty screen: %q", foot)
	}
}

func TestHelpAndDetail(t *testing.T) {
	i18n.Set(i18n.English)
	m := loaded(t, sample(), 120, 32)
	help := ansi.Strip(press(t, m, "?").View().Content)
	for _, want := range []string{"running a command", "a client is attached", "pgup pgdn"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if !strings.Contains(ansi.Strip(press(t, m, "f").View().Content), "It reaps 1 session.") {
		t.Error("the pass dialog does not count in words")
	}
	// Reaping a session where something runs says what stops with it.
	busy := ansi.Strip(press(t, pick(t, m, "api-server"), "d").View().Content)
	if !strings.Contains(busy, "api-server is running npm run dev.") {
		t.Errorf("the dialog for a busy session:\n%s", busy)
	}
	// A detail cut short ends in a mark, not mid-list.
	short := loaded(t, sample(), 100, 18)
	d := short.detail(100, 6)
	if len(d) != 6 || strings.TrimSpace(ansi.Strip(d[5])) != "…" {
		t.Errorf("cut detail ends with %q", ansi.Strip(d[len(d)-1]))
	}
	// Each fact says where it came from, once.
	full := ansi.Strip(strings.Join(m.detail(100, 40), "\n"))
	if strings.Count(full, "from pane terminals") != 1 || strings.Count(full, "found by its socket file") != 1 {
		t.Errorf("sources in the detail:\n%s", full)
	}
}

func TestRowsInOutcomeOrder(t *testing.T) {
	m := loaded(t, sample(), 120, 40)
	var got []string
	for _, r := range m.rows() {
		got = append(got, r.Name)
	}
	// Reaped, then waiting, then the ones nobody could judge, then attached,
	// busy and protected: by name within each.
	want := "scratch fresh lost old-build notes api-server agent-refactor_1104-0931"
	if strings.Join(got, " ") != want {
		t.Errorf("rows %v, want %s", got, want)
	}
}

func TestSelectionSurvivesRefresh(t *testing.T) {
	b := sample()
	m := pick(t, loaded(t, b, 120, 40), "notes")
	// The next plan lists the sessions in another order.
	o := b.report.Outcomes
	b.report.Outcomes = append([]reaper.Outcome{o[6], o[2]}, o[:2]...)
	m = step(t, m, planMsg{report: b.report})
	if got, _ := m.selected(); got.Session.Name() != "notes" {
		t.Errorf("selection moved to %q", got.Session.Name())
	}
}

func TestDetailExplainsTheVerdict(t *testing.T) {
	i18n.Set(i18n.English)
	m := loaded(t, sample(), 140, 40)
	view := m.View().Content
	for _, want := range []string{"Will be reaped at the next pass.", "every pane at a prompt", "found by its socket file", "next pass"} {
		if !strings.Contains(view, want) {
			t.Errorf("main view lacks %q", want)
		}
	}
	m = pick(t, m, "api-server")
	if v := m.View().Content; !strings.Contains(v, "Kept: running npm run dev.") {
		t.Errorf("busy session's detail:\n%s", v)
	}
}

func TestDeleteDecidesOnTheReportAsItIsNow(t *testing.T) {
	i18n.Set(i18n.English)
	for _, c := range []struct {
		name   string
		change func(*reaper.Report)
		want   string
	}{
		{"a client attached", func(r *reaper.Report) {
			r.Outcomes[0] = session("scratch", false, func(f *policy.Facts) { f.Attached, f.AttachDetail = policy.Yes, "1 client(s) attached" })
		}, "scratch has a client attached"},
		{"attach state unknown", func(r *reaper.Report) {
			r.Outcomes[0] = session("scratch", false, func(f *policy.Facts) { f.Attached, f.AttachDetail = policy.Unknown, "list-clients failed" })
		}, "nobody can tell whether a client is attached to scratch"},
		{"no longer listed", func(r *reaper.Report) { r.Outcomes = r.Outcomes[1:] }, "scratch is not in the latest list"},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := sample()
			m := loaded(t, b, 120, 40)
			// The dialog opens for an idle session nobody is attached to.
			m = press(t, m, "d")
			if m.mode != modeConfirm || m.target != "scratch" {
				t.Fatalf("d did not ask for scratch: mode %v target %q", m.mode, m.target)
			}
			// A refresh lands while it is open.
			next := sample().report
			c.change(&next)
			m = step(t, m, planMsg{report: next})
			m = press(t, m, "y")
			if len(b.deleted) != 0 {
				t.Fatalf("deleted %v after the refresh said %s", b.deleted, c.name)
			}
			if !strings.Contains(m.statusLine(), c.want) {
				t.Errorf("status %q, want %q", m.statusLine(), c.want)
			}
		})
	}
	// A session whose attach state is unknown is not offered at all.
	b := sample()
	b.report.Outcomes[0] = session("scratch", false, func(f *policy.Facts) { f.Attached = policy.Unknown })
	m := press(t, pick(t, loaded(t, b, 120, 40), "scratch"), "d")
	if m.mode == modeConfirm {
		t.Error("d offered to reap a session whose attach state is unknown")
	}
}

func TestEnterOnTheNameASessionHasKeepsIt(t *testing.T) {
	i18n.Set(i18n.English)
	b := sample()
	m := press(t, pick(t, loaded(t, b, 120, 40), "notes"), "r")
	m.input.set("notes")
	m = press(t, m, "enter")
	if len(b.renamed) != 0 {
		t.Errorf("renamed a session to the name it has: %v", b.renamed)
	}
	if strings.Join(b.kept, ",") != "notes" || !strings.Contains(m.statusLine(), "keeps this name") {
		t.Errorf("kept %v, status %q", b.kept, m.statusLine())
	}
	// Another name is a rename, and keeps nothing.
	m = press(t, m, "r")
	m.input.set("journal")
	m = press(t, m, "enter")
	if strings.Join(b.renamed, ",") != "notes->journal" || len(b.kept) != 1 {
		t.Errorf("renamed %v, kept %v", b.renamed, b.kept)
	}
}

func TestRenameShowsTheBackendsWarning(t *testing.T) {
	i18n.Set(i18n.English)
	b := sample()
	b.renameNote = "Renamed notes to journal, which the name pattern does not cover: it is no longer protected"
	m := pick(t, loaded(t, b, 120, 40), "notes")
	m = press(t, m, "r")
	m.input.set("journal")
	m = press(t, m, "enter")
	if !strings.Contains(m.statusLine(), "no longer protected") {
		t.Errorf("status after a rename that lost a protection: %q", m.statusLine())
	}
}
