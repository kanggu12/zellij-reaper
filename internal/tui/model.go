package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/i18n"
	"github.com/kanggu12/zellij-reaper/internal/lock"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/reaper"
	"github.com/kanggu12/zellij-reaper/internal/systemd"
	"github.com/kanggu12/zellij-reaper/internal/ui"
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

// key handles a key press for the current mode.
func (m model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeHelp:
		if s == "q" {
			return m, tea.Quit
		}
		m.mode = modeMain
		return m, nil
	case modeConfirm:
		return m.keyConfirm(s)
	case modeRename, modeFilter:
		return m.keyInput(k)
	case modeLog:
		return m.keyLog(s)
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "esc":
		// Esc clears a filter and never quits: a second esc after a dialog
		// must not close the dashboard.
		if m.filter != "" {
			m.filter = ""
			m.reselect()
		}
		return m, nil
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.tableRows())
	case "pgdown":
		m.move(m.tableRows())
	case "home", "g":
		m.move(-1 << 20)
	case "end", "G":
		m.move(1 << 20)
	case "?":
		m.mode = modeHelp
	case "R":
		if !m.loading {
			m.loading = true
			return m, m.loadPlan()
		}
	case "l":
		m.mode, m.follow = modeLog, true
		m.logTop = max(0, len(m.logLines)-m.logRows()+1)
		return m, m.loadLog()
	case "/":
		m.mode, m.before = modeFilter, m.filter
		prompt := i18n.T("filter.prompt")
		return m, m.openInput(prompt, max(10, min(40, m.width-lipgloss.Width(prompt)-4)), m.filter)
	case "f":
		// A pass that would reap something asks first, like every other
		// delete from the dashboard.
		if m.report.Due() > 0 {
			m.mode, m.confirm = modeConfirm, confirmPass
			return m, nil
		}
		return m, m.pass(false)
	case "F":
		m.mode, m.confirm = modeConfirm, confirmForce
	case "d", "delete":
		o, ok := m.selected()
		if !ok {
			return m, nil
		}
		// A session somebody may be using is never offered for reaping.
		if why, cat := m.refuseDelete(o.Session.Name()); why != "" {
			m.say(why, cat)
			return m, nil
		}
		m.mode, m.confirm, m.target = modeConfirm, confirmDelete, o.Session.Name()
	case "p":
		return m, m.toggleProtect()
	case "r":
		o, ok := m.selected()
		if !ok {
			return m, nil
		}
		if o.Session.Listed.Exited {
			m.say(i18n.T("rename.exited"), ui.CatWait)
			return m, nil
		}
		m.mode, m.target = modeRename, o.Session.Name()
		// The prompt sits on its own line in the dialog; the field holds
		// only the name, as wide as the dialog.
		suggest := o.Suggested
		if suggest == "" {
			suggest = o.Session.Name()
		}
		return m, m.openInput("", max(10, min(64, m.width-4)-8), suggest)
	case "enter":
		return m.attach()
	}
	return m, nil
}

// keyConfirm answers a confirmation.
func (m model) keyConfirm(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "y", "Y":
		m.mode = modeMain
		switch m.confirm {
		case confirmForce:
			return m, m.pass(true)
		case confirmPass:
			return m, m.pass(false)
		}
		// The dialog may have stayed open across refreshes: decide on the
		// report as it is now, not as it was when the dialog opened.
		if why, cat := m.refuseDelete(m.target); why != "" {
			m.say(why, cat)
			return m, nil
		}
		return m, m.delete(m.target)
	case "n", "N", "esc", "q":
		m.mode = modeMain
	}
	return m, nil
}

// refuseDelete says why a session must not be reaped by hand, with the color
// to say it in, or "" when it may be. It reads the current report: a client
// attached, an attach state nobody could read, or a session the last refresh
// did not list at all keeps it.
func (m model) refuseDelete(name string) (string, ui.Category) {
	for _, o := range m.report.Outcomes {
		if o.Session.Name() != name {
			continue
		}
		// An exited session has no server, so nobody can be attached.
		if o.Session.Listed.Exited {
			return "", 0
		}
		switch {
		case o.Session.Facts.Attached == policy.Yes:
			return i18n.T("confirm.attached", name), ui.CatAttached
		// Clients is -1 when nobody could ask, as for a session whose
		// server was not found.
		case o.Session.Facts.Attached == policy.Unknown || o.Session.Clients < 0:
			return i18n.T("confirm.unknown", name), ui.CatUnknown
		}
		return "", 0
	}
	return i18n.T("confirm.gone", name), ui.CatUnknown
}

// keyInput edits the rename or filter field.
func (m model) keyInput(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		// Esc cancels: the filter goes back to what it was before "/".
		if m.mode == modeFilter {
			m.filter = m.before
			m.reselect()
		}
		m.mode = modeMain
		m.input.Blur()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		m.input.Blur()
		if m.mode == modeFilter {
			m.mode, m.filter = modeMain, value
			m.sel = 0
			m.reselect()
			return m, nil
		}
		m.mode = modeMain
		if value == "" || value == m.target {
			return m, nil
		}
		return m, m.rename(m.target, value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	// The filter applies as it is typed.
	if m.mode == modeFilter {
		m.filter = m.input.Value()
		m.sel = 0
		m.reselect()
	}
	return m, cmd
}

// keyLog scrolls the log view.
func (m model) keyLog(s string) (tea.Model, tea.Cmd) {
	// The view shows logRows()-1 lines under its title.
	page := m.logRows() - 1
	last := max(0, len(m.logLines)-page)
	switch s {
	case "esc", "q", "l":
		m.mode = modeMain
	case "up", "k":
		m.logTop, m.follow = max(0, m.logTop-1), false
	case "down", "j":
		m.logTop = min(last, m.logTop+1)
		m.follow = m.logTop == last
	case "pgup":
		m.logTop, m.follow = max(0, m.logTop-page), false
	case "pgdown", " ":
		m.logTop = min(last, m.logTop+page)
		m.follow = m.logTop == last
	case "home", "g":
		m.logTop, m.follow = 0, false
	case "end", "G", "f":
		m.logTop, m.follow = last, true
	}
	return m, nil
}

// pass runs a pass in the background and reports the counts.
func (m model) pass(force bool) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		r, err := b.Pass(ctx, force)
		if errors.Is(err, lock.ErrBusy) {
			return doneMsg{text: i18n.T("toast.busy"), cat: ui.CatWait}
		}
		if err != nil {
			return doneMsg{text: i18n.T("toast.pass.failed", err), cat: ui.CatUnknown}
		}
		reaped, kept, _ := r.Counts()
		return doneMsg{text: i18n.T("toast.pass", reaped, kept), cat: ui.CatReap}
	}
}

// delete reaps one session by hand.
func (m model) delete(name string) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		if err := b.Delete(ctx, name); err != nil {
			if errors.Is(err, lock.ErrBusy) {
				return doneMsg{text: i18n.T("toast.busy"), cat: ui.CatWait}
			}
			return doneMsg{text: i18n.T("toast.delete.failed", name, err), cat: ui.CatUnknown}
		}
		return doneMsg{text: i18n.T("toast.deleted", name), cat: ui.CatReap}
	}
}

// rename renames one session by hand.
func (m model) rename(name, to string) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		note, err := b.Rename(ctx, name, to)
		if err != nil {
			return doneMsg{text: i18n.T("toast.rename.failed", err), cat: ui.CatUnknown}
		}
		// Renamed, with something the person must know (it lost its
		// protection): in the color of what the next pass may reap.
		if note != "" {
			return doneMsg{text: note, cat: ui.CatReap}
		}
		return doneMsg{text: i18n.T("toast.renamed", name, to), cat: ui.CatBusy}
	}
}

// toggleProtect protects the selected session, or lifts its protection.
func (m model) toggleProtect() tea.Cmd {
	o, ok := m.selected()
	if !ok {
		return nil
	}
	name := o.Session.Name()
	b := m.b
	on := !b.Protected(name)
	return func() tea.Msg {
		if err := b.SetProtected(name, on); err != nil {
			return doneMsg{text: i18n.T("toast.protect.failed", name, err), cat: ui.CatUnknown}
		}
		if on {
			return doneMsg{text: i18n.T("toast.protected", name), cat: ui.CatSafe}
		}
		if b.PatternProtects(name) {
			return doneMsg{text: i18n.T("toast.pattern", name), cat: ui.CatSafe}
		}
		return doneMsg{text: i18n.T("toast.unprotected", name), cat: ui.CatWait}
	}
}

// attach hands the terminal to `zellij attach`, or switches the zellij client
// this dashboard runs in.
func (m model) attach() (tea.Model, tea.Cmd) {
	o, ok := m.selected()
	if !ok {
		return m, nil
	}
	cmd, interactive := m.b.AttachCommand(o.Session.Name())
	if cmd == nil {
		return m, nil
	}
	if !interactive {
		b := cmd
		return m, func() tea.Msg {
			if out, err := b.CombinedOutput(); err != nil {
				return doneMsg{text: i18n.T("toast.attach.failed", strings.TrimSpace(string(out))+" "+err.Error()), cat: ui.CatUnknown}
			}
			return doneMsg{}
		}
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return doneMsg{text: i18n.T("toast.attach.failed", err), cat: ui.CatUnknown}
		}
		return doneMsg{}
	})
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
