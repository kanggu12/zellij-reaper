package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

// refreshEvery is how often the dashboard re-reads the sessions: often enough to
// follow what happens, with little to spare for a survey to be noticed.
const refreshEvery = 5 * time.Second

// mode is what the screen shows.
type mode int

const (
	modeMain mode = iota
	modeLog
	modeHelp
	modeConfirm
	modeRename
	modeFilter
)

// confirmKind is what a confirmation is for.
type confirmKind int

const (
	confirmDelete confirmKind = iota
	confirmForce
	confirmPass
)

// Messages the model receives from its commands.
type (
	planMsg struct {
		report reaper.Report
		err    error
	}
	timerMsg struct{ st timers.TimerStatus }
	tickMsg  time.Time
	// doneMsg reports an action's result as a toast and asks for a refresh.
	doneMsg struct {
		text string
		cat  ui.Category
	}
	logMsg struct {
		lines []string
		err   error
	}
	versionMsg string
)

// model is the dashboard's state.
type model struct {
	b   Backend
	ctx context.Context
	th  ui.Theme

	width, height int

	report  reaper.Report
	err     error
	loaded  bool
	loading bool
	now     time.Time

	// sel is the selected row among the visible ones; selName keeps the
	// selection on the same session across refreshes, and selServer keeps it
	// there when a pass renamed the session in between.
	sel       int
	selName   string
	selServer int
	top       int

	mode   mode
	filter string
	// before is the filter as it was when "/" was pressed, for esc.
	before  string
	input   field
	confirm confirmKind
	target  string
	// suggested is the name the rename field started with: the name the
	// target's panes point to, or its own name when they point nowhere.
	suggested string

	toast      string
	toastCat   ui.Category
	toastUntil time.Time

	logLines []string
	logTop   int
	follow   bool

	timer timers.TimerStatus
	// timerKnown is false until systemd has answered, so the header does
	// not claim a missing timer while it is still asking.
	timerKnown bool
	version    string
}

// newModel builds the dashboard for a backend.
func newModel(ctx context.Context, b Backend) model {
	return model{b: b, ctx: ctx, th: ui.NewTheme(true), now: time.Now(), width: 100, height: 30}
}

// Run opens the dashboard on the terminal.
func Run(a *app.App) error {
	m := newModel(context.Background(), NewBackend(a))
	_, err := tea.NewProgram(m).Run()
	return err
}

// Init starts the first load and the refresh clock, and asks the terminal for
// its background so the colors suit it.
func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.loadPlan(), m.loadTimer(), m.loadVersion(), m.loadLog(), tick())
}

// tick schedules the next refresh.
func tick() tea.Cmd { return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) }) }

// loadPlan reads the sessions in the background.
func (m model) loadPlan() tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		r, err := b.Plan(ctx)
		return planMsg{report: r, err: err}
	}
}

func (m model) loadTimer() tea.Cmd {
	b := m.b
	return func() tea.Msg { return timerMsg{st: b.Timer()} }
}

func (m model) loadVersion() tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg { return versionMsg(b.ZellijVersion(ctx)) }
}

func (m model) loadLog() tea.Cmd {
	b := m.b
	return func() tea.Msg {
		lines, err := b.LogLines(2000)
		return logMsg{lines: lines, err: err}
	}
}

// rows are the table rows after the filter.
func (m model) rows() []ui.Row {
	all := ui.Sorted(ui.RowsFrom(m.report, m.now))
	if m.filter == "" {
		return all
	}
	var out []ui.Row
	f := strings.ToLower(m.filter)
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.Name), f) {
			out = append(out, r)
		}
	}
	return out
}

// selected returns the outcome of the selected row, if any.
func (m model) selected() (reaper.Outcome, bool) {
	rows := m.rows()
	if m.sel < 0 || m.sel >= len(rows) {
		return reaper.Outcome{}, false
	}
	return m.report.Outcomes[rows[m.sel].Index], true
}

// openInput readies the text field: a filled well in the theme's colors, so
// it reads as something to type into on either background.
func (m *model) openInput(prompt string, width int, value string) {
	m.input = field{
		prompt:      prompt,
		width:       width,
		textStyle:   lipgloss.NewStyle().Foreground(m.th.Ink).Background(m.th.Selected),
		promptStyle: lipgloss.NewStyle().Foreground(m.th.Muted),
	}
	m.input.set(value)
}

// inputChanged applies what was typed or pasted: the filter follows the text
// as it changes. A rename waits for enter.
func (m *model) inputChanged() {
	if m.mode == modeFilter {
		// Trimmed as enter trims it, so the rows do not change on enter.
		m.filter = strings.TrimSpace(m.input.value())
		m.sel = 0
		m.reselect()
	}
}

// fieldHoldsTarget reports whether the rename field holds the name the session
// has. Spaces typed around it do not count, and a name that has spaces of its own
// still matches itself.
func (m model) fieldHoldsTarget() bool {
	v := m.input.value()
	return v == m.target || strings.TrimSpace(v) == m.target
}

// say shows a toast for a few seconds.
func (m *model) say(text string, cat ui.Category) {
	m.toast, m.toastCat, m.toastUntil = oneLine(text), cat, time.Now().Add(6*time.Second)
}

// oneLine joins a multi-line message (zellij's errors often are) into one
// line, so a status line never grows the screen.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " · ")
}

// Update handles one message.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil
	case tea.BackgroundColorMsg:
		m.th = ui.NewTheme(msg.IsDark())
		return m, nil
	case planMsg:
		m.loading, m.loaded = false, true
		m.report, m.err, m.now = msg.report, msg.err, time.Now()
		m.reselect()
		return m, nil
	case timerMsg:
		m.timer, m.timerKnown = msg.st, true
		return m, nil
	case versionMsg:
		m.version = string(msg)
		return m, nil
	case tickMsg:
		// The log feeds the recent decisions too, so it is re-read on
		// every tick (it is small: bounded by log.max_bytes).
		cmds := []tea.Cmd{tick(), m.loadTimer(), m.loadLog()}
		if !m.loading {
			m.loading = true
			cmds = append(cmds, m.loadPlan())
		}
		return m, tea.Batch(cmds...)
	case doneMsg:
		if msg.text != "" {
			m.say(msg.text, msg.cat)
		}
		m.loading = true
		return m, tea.Batch(m.loadPlan(), m.loadTimer(), m.loadLog())
	case logMsg:
		m.logLines = msg.lines
		// While following, stay at the newest line.
		if m.mode == modeLog && m.follow {
			m.logTop = max(0, len(m.logLines)-m.logRows()+1)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	// A terminal's paste arrives whole. Anywhere but a text field it means
	// nothing: pasted text must not run as keys.
	case tea.PasteMsg:
		if m.mode == modeRename || m.mode == modeFilter {
			m.input.insert(msg.Content)
			m.inputChanged()
		}
		return m, nil
	}
	return m, nil
}

// reselect keeps the selection on the same session after a refresh.
func (m *model) reselect() {
	rows := m.rows()
	for i, r := range rows {
		if r.Name == m.selName {
			m.sel, m.selServer = i, r.Server
			m.clampScroll()
			return
		}
	}
	// Renamed since the last plan: the same server, if it is the only row
	// with that server, is the same session.
	if i := uniqueServer(rows, m.selServer); i >= 0 {
		m.sel, m.selName = i, rows[i].Name
		m.clampScroll()
		return
	}
	m.sel = min(m.sel, max(0, len(rows)-1))
	if len(rows) > 0 {
		m.selName, m.selServer = rows[m.sel].Name, rows[m.sel].Server
	}
	m.clampScroll()
}

// uniqueServer is the index of the one row whose server is pid, or -1.
func uniqueServer(rows []ui.Row, pid int) int {
	at := -1
	for i, r := range rows {
		if pid > 0 && r.Server == pid {
			if at >= 0 {
				return -1
			}
			at = i
		}
	}
	return at
}

// move changes the selection by delta rows.
func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	m.sel = max(0, min(len(rows)-1, m.sel+delta))
	m.selName, m.selServer = rows[m.sel].Name, rows[m.sel].Server
	m.clampScroll()
}

// Snapshot renders one frame of the dashboard at w x h after a first load,
// for the screenshots in the documentation. dark picks the color theme;
// selected, when not empty, is the session to select; keys, separated by
// spaces ("down", "enter", "tab", "esc", or one character), are pressed after
// that. A key only changes what the screen shows: what it would start (an
// attach, a pass, a delete) is not run.
func Snapshot(a *app.App, w, h int, dark bool, selected, keys string) string {
	m := snapshotModel(NewBackend(a), w, h, dark, selected)
	for k := range strings.FieldsSeq(keys) {
		next, _ := m.Update(snapshotKey(k))
		m = next.(model)
	}
	return m.View().Content
}

// snapshotKey is the key event for a key's name in Snapshot's keys.
func snapshotKey(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "space":
		return tea.KeyPressMsg{Code: ' ', Text: " "}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// snapshotModel is the dashboard after its first load, with a session selected.
func snapshotModel(b Backend, w, h int, dark bool, selected string) model {
	m := newModel(context.Background(), b)
	m.width, m.height, m.th = w, h, ui.NewTheme(dark)
	r, err := b.Plan(m.ctx)
	m.report, m.err, m.loaded, m.now = r, err, true, time.Now()
	m.timer, m.timerKnown, m.version = b.Timer(), true, b.ZellijVersion(m.ctx)
	m.logLines, _ = b.LogLines(2000)
	if selected != "" {
		m.selName = selected
	}
	m.reselect()
	return m
}
