package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/systemd"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

// refreshEvery is how often the dashboard re-reads the sessions. Each refresh
// asks every running session for its clients, so it is not much more often.
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
	timerMsg struct{ st systemd.TimerStatus }
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
	// selection on the same session across refreshes.
	sel     int
	selName string
	top     int

	mode   mode
	filter string
	// before is the filter as it was when "/" was pressed, for esc.
	before  string
	input   textinput.Model
	confirm confirmKind
	target  string

	toast      string
	toastCat   ui.Category
	toastUntil time.Time

	logLines []string
	logTop   int
	follow   bool

	timer systemd.TimerStatus
	// timerKnown is false until systemd has answered, so the header does
	// not claim a missing timer while it is still asking.
	timerKnown bool
	version    string
}

// newModel builds the dashboard for a backend.
func newModel(ctx context.Context, b Backend) model {
	ti := textinput.New()
	ti.CharLimit = 200
	return model{b: b, ctx: ctx, th: ui.NewTheme(true), input: ti, now: time.Now(), width: 100, height: 30}
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
// it reads as a place to type, its padding filled too.
func (m *model) openInput(prompt string, width int, value string) tea.Cmd {
	st := m.input.Styles()
	st.Focused.Text = lipgloss.NewStyle().Foreground(m.th.Ink).Background(m.th.Selected)
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(m.th.Muted)
	m.input.SetStyles(st)
	m.input.Prompt = prompt
	m.input.SetWidth(width)
	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
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
	}
	// Anything else goes to the text input when it is open.
	if m.mode == modeRename || m.mode == modeFilter {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// reselect keeps the selection on the same session after a refresh.
func (m *model) reselect() {
	rows := m.rows()
	for i, r := range rows {
		if r.Name == m.selName {
			m.sel = i
			m.clampScroll()
			return
		}
	}
	m.sel = min(m.sel, max(0, len(rows)-1))
	if len(rows) > 0 {
		m.selName = rows[m.sel].Name
	}
	m.clampScroll()
}

// move changes the selection by delta rows.
func (m *model) move(delta int) {
	rows := m.rows()
	if len(rows) == 0 {
		return
	}
	m.sel = max(0, min(len(rows)-1, m.sel+delta))
	m.selName = rows[m.sel].Name
	m.clampScroll()
}

// Snapshot renders one frame of the dashboard at w x h after a first load,
// for the screenshots in the documentation. dark picks the color theme;
// selected, when not empty, is the session to select.
func Snapshot(a *app.App, w, h int, dark bool, selected string) string {
	return snapshot(NewBackend(a), w, h, dark, selected)
}

// snapshot is Snapshot for any backend.
func snapshot(b Backend, w, h int, dark bool, selected ...string) string {
	m := newModel(context.Background(), b)
	m.width, m.height, m.th = w, h, ui.NewTheme(dark)
	r, err := b.Plan(m.ctx)
	m.report, m.err, m.loaded, m.now = r, err, true, time.Now()
	m.timer, m.timerKnown, m.version = b.Timer(), true, b.ZellijVersion(m.ctx)
	m.logLines, _ = b.LogLines(2000)
	if len(selected) > 0 && selected[0] != "" {
		m.selName = selected[0]
	}
	m.reselect()
	return m.View().Content
}
