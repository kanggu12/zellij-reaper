package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

// SetupStart is what the setup screen starts from: the settings in effect,
// the sessions as the next pass sees them, and what this machine allows.
type SetupStart struct {
	// IdleAfter, ExitedAfter, DryRun and AutoRename are the settings now.
	IdleAfter, ExitedAfter time.Duration
	DryRun, AutoRename     bool
	// Report is the plan now; its facts are decided again for every choice.
	Report reaper.Report
	// FirstPass is when the first pass after setup runs.
	FirstPass time.Time
	// CanRename is false on Windows, where zellij cannot reach a renamed
	// session, so the names row is left out.
	CanRename bool
	// Installer is true when the install script opened the screen: esc then
	// keeps the defaults and the install goes on.
	Installer bool
	// NoPlan says why no session could be listed, like zellij not being
	// installed yet; the settings can still be chosen.
	NoPlan string
	// HookFile is the shell's startup file a prompt hook can go in, like
	// "~/.bashrc", or "" when there is none to offer (no known shell, or
	// the hook is already there).
	HookFile string
}

// SetupChoice is what the person chose.
type SetupChoice struct {
	IdleAfter, ExitedAfter time.Duration
	DryRun, AutoRename     bool
	// Protect is the sessions to put on the protected list.
	Protect []string
	// Hook is true to add the prompt hook to SetupStart.HookFile.
	Hook bool
}

// The waits offered, shortest first. The setting in effect joins them when it
// is none of these.
var (
	idleChoices   = []time.Duration{time.Hour, 4 * time.Hour, 12 * time.Hour, 24 * time.Hour, 72 * time.Hour, 168 * time.Hour}
	exitedChoices = []time.Duration{24 * time.Hour, 72 * time.Hour, 168 * time.Hour, 336 * time.Hour}
)

// setupRow is one row the cursor stops on.
type setupRow int

const (
	rowIdle setupRow = iota
	rowExited
	rowMode
	rowNames
	rowHook
	// rowSession is a session the first pass would reap; the cursor moves
	// on to each in turn.
	rowSession
)

// setupModel is the setup screen.
type setupModel struct {
	start SetupStart
	th    ui.Theme

	width, height int

	idle, exited []time.Duration
	// idleAt and exitedAt index the chosen wait in idle and exited.
	idleAt, exitedAt int
	dry, names, hook bool
	// keep is the sessions marked to keep, by name.
	keep map[string]bool

	cursor int
	// done is true once the person chose; how says what they chose.
	done bool
	how  SetupOutcome
}

// choicesWith returns the offered waits with cur among them, and its index.
func choicesWith(offered []time.Duration, cur time.Duration) ([]time.Duration, int) {
	out := slices.Clone(offered)
	if !slices.Contains(out, cur) {
		out = append(out, cur)
		slices.Sort(out)
	}
	return out, slices.Index(out, cur)
}

// newSetup builds the screen at its starting values.
func newSetup(s SetupStart, dark bool) setupModel {
	m := setupModel{start: s, th: ui.NewTheme(dark), width: 100, height: 30, keep: map[string]bool{}}
	m.idle, m.idleAt = choicesWith(idleChoices, s.IdleAfter)
	m.exited, m.exitedAt = choicesWith(exitedChoices, s.ExitedAfter)
	m.dry, m.names = s.DryRun, s.AutoRename && s.CanRename
	// The hook is offered switched on: it is what keeps a renamed session's
	// panes working, and the row says where it goes.
	m.hook = s.HookFile != "" && m.names
	return m
}

// rows lists the rows the cursor stops on, in screen order.
func (m setupModel) rows() []setupRow {
	rows := []setupRow{rowIdle, rowExited, rowMode}
	if m.start.CanRename {
		rows = append(rows, rowNames)
		// The hook only matters while sessions get renamed.
		if m.start.HookFile != "" && m.names {
			rows = append(rows, rowHook)
		}
	}
	for range m.listed() {
		rows = append(rows, rowSession)
	}
	return rows
}

// verdict decides one session again under the chosen waits, at the time of
// the first pass, with the sessions marked to keep protected.
func (m setupModel) verdict(o reaper.Outcome) policy.Verdict {
	f := o.Session.Facts
	if m.keep[f.Name] && !f.Protected {
		f.Protected, f.ProtectedBy = true, "protected list"
	}
	th := policy.Thresholds{Idle: m.idle[m.idleAt], Exited: m.exited[m.exitedAt]}
	return policy.Decide(f, th, m.start.FirstPass)
}

// listed is the sessions the first pass would reap under the chosen waits,
// and those marked to keep, so unmarking one leaves it where it was.
func (m setupModel) listed() []reaper.Outcome {
	var out []reaper.Outcome
	for _, o := range m.start.Report.Outcomes {
		if m.keep[o.Session.Facts.Name] || m.verdict(o).Action == policy.Reap {
			out = append(out, o)
		}
	}
	return out
}

// due counts the sessions the first pass would reap.
func (m setupModel) due() int {
	n := 0
	for _, o := range m.listed() {
		if m.verdict(o).Action == policy.Reap {
			n++
		}
	}
	return n
}

// Choice is what the screen holds now.
func (m setupModel) Choice() SetupChoice {
	c := SetupChoice{IdleAfter: m.idle[m.idleAt], ExitedAfter: m.exited[m.exitedAt], DryRun: m.dry,
		AutoRename: m.names, Hook: m.hook && m.names && m.start.HookFile != ""}
	// Only a session still listed can be kept: one a shorter wait no longer
	// reaches stays marked on screen, so it is listed, and goes on the list.
	for _, o := range m.start.Report.Outcomes {
		if m.keep[o.Session.Facts.Name] {
			c.Protect = append(c.Protect, o.Session.Facts.Name)
		}
	}
	return c
}

func (m setupModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.th = ui.NewTheme(msg.IsDark())
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

// key handles one key press.
func (m setupModel) key(s string) (tea.Model, tea.Cmd) {
	rows := m.rows()
	m.cursor = min(m.cursor, len(rows)-1)
	switch s {
	case "ctrl+c":
		m.done, m.how = true, SetupQuit
		return m, tea.Quit
	case "esc", "q":
		m.done, m.how = true, SetupLeft
		return m, tea.Quit
	case "enter":
		m.done, m.how = true, SetupChosen
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j", "tab":
		m.cursor = min(len(rows)-1, m.cursor+1)
	case "left", "h":
		m.change(rows[m.cursor], -1)
	case "right", "l", "space", " ":
		m.change(rows[m.cursor], 1)
	}
	// A change can shorten the list under the cursor.
	m.cursor = min(m.cursor, len(m.rows())-1)
	return m, nil
}

// change moves the value on a row one step; on a session it marks or
// unmarks it to keep.
func (m *setupModel) change(r setupRow, step int) {
	switch r {
	case rowIdle:
		m.idleAt = min(len(m.idle)-1, max(0, m.idleAt+step))
	case rowExited:
		m.exitedAt = min(len(m.exited)-1, max(0, m.exitedAt+step))
	case rowMode:
		m.dry = !m.dry
	case rowNames:
		m.names = !m.names
	case rowHook:
		m.hook = !m.hook
	case rowSession:
		i := m.cursor - (len(m.rows()) - len(m.listed()))
		if l := m.listed(); i >= 0 && i < len(l) {
			name := l[i].Session.Facts.Name
			m.keep[name] = !m.keep[name]
		}
	}
}

// View renders the whole screen.
func (m setupModel) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "zellij-reaper setup"
	return v
}

// render draws the screen as text, exactly the terminal's size.
func (m setupModel) render() string {
	t := m.th
	if m.width < 30 || m.height < 8 {
		return frameTo([]string{ui.Fg(t.Muted).Render(i18n.T("small"))}, m.width, m.height)
	}
	name := lipgloss.NewStyle().Background(t.Mark).Foreground(lipgloss.Color("#1D1C1D")).Bold(true).Padding(0, 1).Render("zellij-reaper")
	lines := []string{" " + name + ui.Fg(t.Ink).Bold(true).Render("  "+i18n.T("setup.title")), ""}
	// The intro wraps rather than losing its last words on a narrow screen.
	for _, l := range wrapWords(i18n.T("setup.intro", ui.Clock(m.start.FirstPass)), m.width-2) {
		lines = append(lines, " "+ui.Fg(t.Muted).Render(l))
	}
	lines = append(lines, "")

	rows := m.rows()
	labelW := 0
	for _, r := range rows {
		if r != rowSession {
			labelW = max(labelW, lipgloss.Width(m.label(r)))
		}
	}
	// pointer marks the row under the cursor.
	pointer := func(i int) string {
		if i == m.cursor {
			return ui.Fg(t.Ink).Bold(true).Render(" ›")
		}
		return "  "
	}
	// Values line up too, at the width of the widest value each row can
	// take, so the notes after them start in one column that never moves.
	valueW := 0
	for _, r := range rows {
		if r != rowSession {
			for _, v := range m.values(r) {
				valueW = max(valueW, lipgloss.Width(v))
			}
		}
	}
	i := 0
	for ; i < len(rows) && rows[i] != rowSession; i++ {
		r := rows[i]
		// Padded by cells, not letters: a Korean letter takes two.
		label := padCells(m.label(r), labelW)
		pad := strings.Repeat(" ", valueW-lipgloss.Width(m.value(r)))
		value := ui.Fg(t.Ink).Bold(true).Render("‹ "+m.value(r)+" ›") + pad
		line := pointer(i) + " " + ui.Fg(t.Ink).Render(label) + "   " + value
		// A note that does not fit whole is left out rather than cut.
		if note := m.note(r); note != "" && lipgloss.Width(line)+2+lipgloss.Width(note) <= m.width {
			line += "  " + ui.Fg(t.Muted).Render(note)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")

	// What the first pass would do with these choices.
	listed := m.listed()
	due := m.due()
	switch {
	case m.start.NoPlan != "":
		lines = append(lines, " "+ui.Fg(t.Muted).Render(i18n.T("setup.noplan", m.start.NoPlan)))
	case m.dry && due == 0:
		lines = append(lines, " "+ui.Fg(t.Cat[ui.CatAttached]).Render(i18n.T("setup.first.none")))
	case m.dry:
		lines = append(lines, " "+ui.Fg(t.Cat[ui.CatReap]).Render(i18n.TN("setup.first.dry", due, due)))
	case due == 0:
		lines = append(lines, " "+ui.Fg(t.Cat[ui.CatAttached]).Render(i18n.T("setup.first.none")))
	default:
		lines = append(lines, " "+ui.Fg(t.Cat[ui.CatReap]).Render(i18n.TN("setup.first", due, due)))
	}
	nameW := 0
	for _, o := range listed {
		nameW = max(nameW, lipgloss.Width(o.Session.Facts.Name))
	}
	// The session rows get what is left above the keys and a blank line.
	// When they do not all fit, the ones shown follow the cursor and one line
	// counts the rest.
	room := max(1, m.height-len(lines)-2)
	start, show := 0, len(listed)
	if len(listed) > room {
		show = max(1, room-1)
		sel := m.cursor - i
		start = min(max(0, sel-show+1), len(listed)-show)
	}
	for j := start; j < start+show && j < len(listed); j++ {
		o := listed[j]
		v := m.verdict(o)
		box := "[ ]"
		if m.keep[o.Session.Facts.Name] {
			box = "[x]"
		}
		cat := ui.Categorize(v)
		line := pointer(i+j) + " " + ui.Fg(t.Ink).Render(box+" "+padCells(o.Session.Facts.Name, nameW)) +
			"  " + t.Dot(cat) + " " + ui.Fg(t.Cat[cat]).Render(setupWhy(v))
		lines = append(lines, line)
	}
	if show < len(listed) {
		lines = append(lines, "   "+ui.Fg(t.Muted).Render(i18n.T("setup.more", len(listed)-show)))
	}

	out := make([]string, m.height)
	copy(out, lines)
	keys := [][2]string{{"↑↓", i18n.T("footer.select")}, {"←→", i18n.T("setup.key.change")}}
	if i < len(rows) && m.cursor >= i {
		keys = append(keys, [2]string{"space", i18n.T("setup.key.keep")})
	}
	enter := i18n.T("setup.key.start")
	if m.dry {
		enter = i18n.T("setup.key.start.dry")
	}
	esc := i18n.T("setup.key.quit")
	if m.start.Installer {
		esc = i18n.T("setup.key.defaults")
	}
	keys = append(keys, [2]string{"⏎", enter}, [2]string{"esc", esc})
	out[m.height-1] = m.keyBar(keys)
	return frameTo(out, m.width, m.height)
}

// setupWhy words a session's verdict: why it goes, or, once marked, that it
// stays.
func setupWhy(v policy.Verdict) string {
	if v.Action == policy.Reap {
		return ui.Why(v)
	}
	return i18n.T("setup.kept")
}

// keyBar joins keycaps that fit the width, dropping from the front: the
// arrows go first, start and quit stay.
func (m setupModel) keyBar(keys [][2]string) string {
	for {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = m.th.KeyCap(k[0], k[1])
		}
		s := " " + strings.Join(parts, "  ")
		if lipgloss.Width(s) <= m.width || len(keys) <= 2 {
			return s
		}
		keys = keys[1:]
	}
}

// label is a settings row's name.
func (m setupModel) label(r setupRow) string {
	switch r {
	case rowIdle:
		return i18n.T("setup.idle")
	case rowExited:
		return i18n.T("setup.exited")
	case rowMode:
		return i18n.T("setup.mode")
	case rowNames:
		return i18n.T("setup.names")
	case rowHook:
		return i18n.T("setup.hook")
	}
	return ""
}

// value is a settings row's value as shown between the arrows.
func (m setupModel) value(r setupRow) string {
	onOff := func(b bool) string {
		if b {
			return i18n.T("setup.on")
		}
		return i18n.T("setup.off")
	}
	switch r {
	case rowIdle:
		return duration.Format(m.idle[m.idleAt])
	case rowExited:
		return duration.Format(m.exited[m.exitedAt])
	case rowMode:
		if m.dry {
			return i18n.T("setup.mode.dry")
		}
		return i18n.T("setup.mode.live")
	case rowNames:
		return onOff(m.names)
	case rowHook:
		if m.hook {
			return i18n.T("setup.hook.add")
		}
		return i18n.T("setup.hook.skip")
	}
	return ""
}

// values lists every value a row can show, for the width of the column.
func (m setupModel) values(r setupRow) []string {
	c := m
	var out []string
	switch r {
	case rowIdle:
		for i := range m.idle {
			c.idleAt = i
			out = append(out, c.value(r))
		}
	case rowExited:
		for i := range m.exited {
			c.exitedAt = i
			out = append(out, c.value(r))
		}
	default:
		for _, b := range []bool{false, true} {
			c.dry, c.names, c.hook = b, b, b
			out = append(out, c.value(r))
		}
	}
	return out
}

// padCells pads s with spaces to w terminal cells.
func padCells(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// wrapWords breaks s into lines of at most w cells, at spaces.
func wrapWords(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	return append(out, line)
}

// note is the muted words after a row's value.
func (m setupModel) note(r setupRow) string {
	switch r {
	case rowIdle:
		return i18n.T("setup.idle.note")
	case rowExited:
		return i18n.T("setup.exited.note")
	case rowMode:
		if m.dry {
			return i18n.T("setup.mode.dry.note")
		}
		return i18n.T("setup.mode.live.note")
	case rowNames:
		if m.names {
			return i18n.T("setup.names.note")
		}
	case rowHook:
		return m.start.HookFile
	}
	return ""
}

// frameTo pads or cuts every line to w and the block to h.
func frameTo(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = ui.Fit(strings.ReplaceAll(lines[i], "\n", " "), w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

// SetupOutcome is how the person left the setup screen.
type SetupOutcome int

const (
	// SetupLeft: esc or q, nothing chosen.
	SetupLeft SetupOutcome = iota
	// SetupChosen: enter, apply the choice.
	SetupChosen
	// SetupQuit: ctrl+c, stop here whatever called the screen.
	SetupQuit
)

// RunSetup shows the setup screen and returns what was chosen and how the
// person left it.
func RunSetup(s SetupStart) (SetupChoice, SetupOutcome, error) {
	final, err := tea.NewProgram(newSetup(s, true)).Run()
	if err != nil {
		return SetupChoice{}, SetupQuit, err
	}
	m := final.(setupModel)
	return m.Choice(), m.how, nil
}

// SetupSnapshot prints one frame of the setup screen after keys, for the
// documentation's screenshots and the tests.
func SetupSnapshot(s SetupStart, w, h int, dark bool, keys string) string {
	m := newSetup(s, dark)
	m.width, m.height = w, h
	for _, k := range strings.Fields(keys) {
		next, _ := m.Update(snapshotKey(k))
		m = next.(setupModel)
	}
	return m.render()
}

// StartFrom gathers what the setup screen starts from on a loaded App.
func StartFrom(ctx context.Context, a *app.App, firstPass time.Time, canRename bool, hookFile string) (SetupStart, error) {
	// Without a plan the settings can still be chosen: the installer opens
	// this screen on machines where zellij comes later.
	r, err := reaper.Plan(ctx, a.Reaper, reaper.Options{})
	noPlan := ""
	if err != nil {
		r, noPlan = reaper.Report{}, err.Error()
	}
	return SetupStart{NoPlan: noPlan, IdleAfter: a.Config.IdleAfter, ExitedAfter: a.Config.ExitedAfter, DryRun: a.Config.DryRun,
		AutoRename: a.Config.AutoRename, Report: r, FirstPass: firstPass, CanRename: canRename, HookFile: hookFile}, nil
}
