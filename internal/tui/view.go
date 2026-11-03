package tui

import (
	"errors"
	"image/color"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/duration"
	"github.com/kanggu12/zellij-reaper/internal/i18n"
	"github.com/kanggu12/zellij-reaper/internal/logfile"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/reaper"
	"github.com/kanggu12/zellij-reaper/internal/ui"
)

// sideBySide is the width from which the details sit beside the table.
const sideBySide = 132

// View renders the whole screen.
func (m model) View() tea.View {
	var body string
	switch {
	// Below this the layout has no room for a table: say so, in one line.
	case m.width < 30 || m.height < 8:
		body = m.frame([]string{ui.Fg(m.th.Muted).Render(i18n.T("small"))})
	case m.mode == modeHelp:
		body = m.helpView()
	case m.mode == modeLog:
		body = m.logView()
	default:
		body = m.mainView()
	}
	v := tea.NewView(body)
	v.AltScreen = true
	v.WindowTitle = "zellij-reaper"
	return v
}

// frame pads or cuts every line to exactly the screen width and the block to
// exactly the screen height, so nothing wraps and nothing is left over.
func (m model) frame(lines []string) string {
	out := make([]string, m.height)
	for i := range out {
		if i < len(lines) {
			// A stray line break inside a line would add a row.
			out[i] = ui.Fit(strings.ReplaceAll(lines[i], "\n", " "), m.width)
		} else {
			out[i] = strings.Repeat(" ", m.width)
		}
	}
	return strings.Join(out, "\n")
}

// mainView is the header, the table, the details, a status line and the keys.
func (m model) mainView() string {
	lines := []string{m.header(), ""}
	body := m.bodyHeight()
	switch {
	case !m.loaded:
		lines = append(lines, m.centered(body, ui.Fg(m.th.Muted).Render(i18n.T("loading")))...)
	case errors.Is(m.err, reaper.ErrNoZellij):
		lines = append(lines, m.centered(body,
			ui.Fg(m.th.Cat[ui.CatUnknown]).Render(i18n.T("error.zellij")),
			ui.Fg(m.th.Muted).Render(ansi.Truncate(i18n.T("error.zellij.hint"), m.width-2, "…")))...)
	case m.err != nil:
		// zellij's own error often starts with the command it ran, which
		// the sentence around it already says.
		msg := strings.TrimPrefix(oneLine(m.err.Error()), "zellij list-sessions: ")
		lines = append(lines, m.centered(body,
			ui.Fg(m.th.Cat[ui.CatUnknown]).Render(ansi.Truncate(i18n.T("error.list", msg), m.width-2, "…")),
			ui.Fg(m.th.Muted).Render(i18n.T("error.retry")))...)
	case m.report.NoSessions:
		lines = append(lines, m.centered(body,
			lipgloss.NewStyle().Bold(true).Foreground(m.th.Ink).Render(i18n.T("empty.title")),
			ui.Fg(m.th.Muted).Render(i18n.T("empty.hint")))...)
	default:
		lines = append(lines, m.sessionsArea(body)...)
	}
	// Pad the body so the status line and the keys sit at the bottom.
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	lines = append(lines[:m.height-2], m.statusLine(), m.footer())
	screen := m.frame(lines)
	// Dialogs float over the screen.
	switch m.mode {
	case modeConfirm:
		return m.overlay(screen, m.confirmBox())
	case modeRename:
		return m.overlay(screen, m.inputBox())
	}
	return screen
}

// bodyHeight is the rows between the header and the status line: the
// header and a blank row above, a blank row, the status line and the keys
// below.
func (m model) bodyHeight() int { return m.height - 5 }

// centered puts a few lines in the middle of a block of height h.
func (m model) centered(h int, lines ...string) []string {
	out := make([]string, 0, h)
	for len(out) < (h-len(lines))/2 {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, lipgloss.PlaceHorizontal(m.width, lipgloss.Center, l))
	}
	return out
}

// header is the top bar: the name, zellij's version, and the settings that
// decide what the next pass does.
func (m model) header() string {
	t := m.th
	name := lipgloss.NewStyle().Background(t.Mark).Foreground(lipgloss.Color("#1D1C1D")).Bold(true).Padding(0, 1).Render("zellij-reaper")
	left := name
	if m.version != "" {
		left += ui.Fg(t.Muted).Render("  zellij " + m.version)
	}
	// A reload in flight shows as a small mark after the name.
	if m.loading {
		left += ui.Fg(t.Muted).Render(" ↻")
	}
	cfg := m.b.Config()
	mode := ui.Fg(t.Cat[ui.CatAttached]).Render("● " + i18n.T("header.live"))
	if cfg.DryRun {
		mode = ui.Fg(t.Cat[ui.CatReap]).Render("● " + i18n.T("header.dry"))
	}
	right := []string{mode,
		ui.Fg(t.Muted).Render(i18n.T("settings.waits", duration.Format(cfg.IdleAfter), duration.Format(cfg.ExitedAfter))),
	}
	// The timer shows once systemd has answered.
	if w := m.timerWords(); w != "" {
		right = append(right, w)
	}
	sep := ui.Fg(t.Rule).Render(" · ")
	// Drop the middle facts first when the screen is narrow; keep the mode
	// and the timer, which matter most.
	for len(right) > 2 && lipgloss.Width(left)+lipgloss.Width(strings.Join(right, sep))+2 > m.width {
		right = append(right[:1], right[2:]...)
	}
	r := strings.Join(right, sep)
	// Still too wide: the zellij version goes before the mode would.
	if lipgloss.Width(left)+lipgloss.Width(r)+1 > m.width {
		left = name
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(r)
	if gap < 1 {
		return name + " " + r
	}
	return left + strings.Repeat(" ", gap) + r
}

// timerWords says when the timer runs next, or that it is not running.
func (m model) timerWords() string {
	t, st := m.th, m.timer
	switch {
	case !m.timerKnown:
		return ""
	case !st.Installed:
		return ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("header.timer.none"))
	case !st.Enabled || !st.Active:
		return ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("header.timer.off"))
	case st.Next.IsZero():
		return ui.Fg(t.Muted).Render(i18n.T("header.next", "?"))
	}
	return ui.Fg(t.Muted).Render(i18n.T("header.next", ui.Clock(st.Next)))
}

// tableRows is how many session rows fit on screen.
func (m model) tableRows() int {
	body := m.bodyHeight()
	if m.width >= sideBySide {
		return max(1, body-1)
	}
	return max(1, m.stackedTableHeight(body)-1)
}

// stackedTableHeight splits a stacked layout: the table takes what its rows
// need, up to 55% of the body, so the details always have room.
func (m model) stackedTableHeight(body int) int {
	return max(3, min(len(m.rows())+1, body*55/100))
}

// clampScroll keeps the selected row inside the visible window.
func (m *model) clampScroll() {
	n := m.tableRows()
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+n {
		m.top = m.sel - n + 1
	}
	m.top = max(0, min(m.top, max(0, len(m.rows())-n)))
}

// sessionsArea is the table and the details, beside or above each other.
func (m model) sessionsArea(body int) []string {
	rows := m.rows()
	if m.filter != "" && len(rows) == 0 {
		return m.centered(body, ui.Fg(m.th.Muted).Render(i18n.T("filter.none", m.filter)))
	}
	// Every line below the header starts one column in, as the header's
	// name, the status line and the keys do.
	inner := m.width - 1
	if m.width >= sideBySide {
		tw := inner * 58 / 100
		dw := inner - tw - 3
		table := m.tableBlock(rows, tw, body)
		// Under a short table, the latest decisions fill the column.
		if room := body - len(table) - 2; room >= 3 {
			if recent := m.recentLines(room); len(recent) > 0 {
				table = append(table, "", ui.Fg(m.th.Muted).Bold(true).Render(i18n.T("detail.recent")))
				for _, r := range recent {
					table = append(table, ansi.Truncate(r, tw, "…"))
				}
			}
		}
		detail := m.detail(dw, body)
		sep := ui.Fg(m.th.Rule).Render(" │ ")
		out := make([]string, body)
		for i := range out {
			l, r := "", ""
			if i < len(table) {
				l = table[i]
			}
			if i < len(detail) {
				r = detail[i]
			}
			out[i] = " " + ui.Fit(l, tw) + sep + ui.Fit(r, dw)
		}
		return out
	}
	th := m.stackedTableHeight(body)
	out := m.tableBlock(rows, inner, th)
	for len(out) < th {
		out = append(out, "")
	}
	out = append(out, m.rule(m.selName, inner))
	out = append(out, m.detail(inner, body-th-1)...)
	for i := range out {
		out[i] = " " + out[i]
	}
	return out
}

// rule is a thin line, width cells wide, with a title on it.
func (m model) rule(title string, width int) string {
	t := m.th
	if title == "" {
		return ui.Fg(t.Rule).Render(strings.Repeat("─", width))
	}
	head := ui.Fg(t.Rule).Render("── ") + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(ansi.Truncate(title, width-8, "…")) + " "
	return head + ui.Fg(t.Rule).Render(strings.Repeat("─", max(0, width-lipgloss.Width(head))))
}

// tableBlock is the table cut to height lines, scrolled to the selection.
func (m model) tableBlock(rows []ui.Row, width, height int) []string {
	lines := m.th.Table(rows, width, m.sel)
	head, body := lines[0], lines[1:]
	n := max(1, height-1)
	top := max(0, min(m.top, len(body)-n))
	end := min(len(body), top+n)
	out := []string{head}
	// How many rows are out of view is said on the status line.
	return append(out, body[top:end]...)
}

// hidden is how many table rows are above and below the visible ones.
func (m model) hidden() (above, below int) {
	n, total := m.tableRows(), len(m.rows())
	top := max(0, min(m.top, total-n))
	return top, max(0, total-top-n)
}

// detail explains the selected session: the verdict in a sentence, every
// check with its evidence, its panes, and where the facts came from.
func (m model) detail(width, height int) []string {
	t := m.th
	if height <= 0 {
		return nil
	}
	o, ok := m.selected()
	if !ok {
		return []string{ui.Fg(t.Muted).Render(i18n.T("detail.none"))}
	}
	s, v := o.Session, o.Verdict
	cat := ui.Categorize(v)
	var lines []string
	// In the side-by-side layout the name heads the panel; stacked, the
	// rule above already shows it.
	if m.width >= sideBySide {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(s.Name()))
	}
	lines = append(lines, ui.Fg(t.Cat[cat]).Bold(true).Render(verdictSentence(v, m.report.DryRun)))
	lines = append(lines, "")
	lines = append(lines, ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.why")))
	for _, c := range v.Checks {
		mark := ui.Fg(t.Cat[ui.CatAttached]).Render("✓")
		label := ui.Fg(t.Ink).Render(ui.CheckName(c.Name))
		if !c.Pass {
			mark = ui.Fg(t.Cat[cat]).Render("✗")
			label = ui.Fg(t.Cat[cat]).Render(ui.CheckName(c.Name))
		}
		line := "  " + mark + " " + label
		d := ui.CheckDetail(c)
		// Where a fact came from goes on its own check, said once.
		switch {
		case c.Name == "last activity" && s.Facts.ActivitySource != "":
			d = join(d, i18n.T("source."+s.Facts.ActivitySource))
		case c.Name == "server" && c.Pass && s.ServerMatch != "":
			d = join(d, i18n.T("match."+s.ServerMatch))
		}
		if d != "" {
			line += ui.Fg(t.Muted).Render("  " + d)
		}
		lines = append(lines, line)
	}
	if len(s.Panes) > 0 {
		lines = append(lines, "", ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.panes")))
		for _, p := range s.Panes {
			// A pane that runs its program directly says "running"; a
			// shell says what it runs.
			var state string
			switch {
			case p.Busy == "":
				state = ui.Fg(t.Muted).Render(i18n.T("detail.pane.prompt"))
			case p.Busy == p.Command:
				state = ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("detail.pane.running"))
			default:
				state = ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("detail.pane.busy", p.Busy))
			}
			used := ""
			if !p.LastIO.IsZero() {
				used = ui.Fg(t.Muted).Render(" · " + i18n.T("detail.pane.used", duration.Format(m.now.Sub(p.LastIO))))
			}
			lines = append(lines, "  "+ui.Fg(t.Ink).Render(p.Command)+ui.Fg(t.Muted).Render(" · ")+state+used)
		}
	}
	if o.WouldRename != "" {
		lines = append(lines, "", ui.Fg(t.Muted).Render(i18n.T("detail.rename", o.WouldRename)))
	}
	// The settings that shaped this verdict, when the panel has room.
	if settings := m.settingsLines(); len(lines)+len(settings)+1 <= height {
		lines = append(lines, "")
		lines = append(lines, settings...)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	// Cut short, a mark right after the last line kept says there is more.
	if len(lines) > height {
		lines = lines[:height-1]
		for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, ui.Fg(t.Muted).Render("  …"))
	}
	return lines
}

// join adds a fact to a check's evidence with a dot between.
func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

// settingsLines summarizes the settings that decide verdicts.
func (m model) settingsLines() []string {
	t := m.th
	c := m.b.Config()
	onOff := func(b bool) string {
		if b {
			return i18n.T("on")
		}
		return i18n.T("off")
	}
	lines := []string{
		ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.settings")),
		"  " + ui.Fg(t.Ink).Render(i18n.T("settings.waits", duration.Format(c.IdleAfter), duration.Format(c.ExitedAfter))),
	}
	if len(c.ProtectCommands) > 0 {
		lines = append(lines, "  "+ui.Fg(t.Ink).Render(i18n.T("settings.programs", strings.Join(c.ProtectCommands, ", "))))
	}
	if c.Protect != "" {
		lines = append(lines, "  "+ui.Fg(t.Ink).Render(i18n.T("settings.pattern", c.Protect)))
	}
	lines = append(lines, "  "+ui.Fg(t.Ink).Render(i18n.T("settings.rename", onOff(c.AutoRename))))
	if c.File != "" {
		lines = append(lines, "  "+ui.Fg(t.Muted).Render(i18n.T("settings.file", tilde(c.File))))
	} else {
		lines = append(lines, "  "+ui.Fg(t.Muted).Render(i18n.T("settings.defaults")))
	}
	return lines
}

// Patterns for the log lines the recent decisions are built from.
var (
	reapRe    = regexp.MustCompile(`^(.*?) \[(RUNNING|EXITED)\] panes=\{.*\} :: REAP (IDLE|EXITED), (.+) since last activity$`)
	deletedRe = regexp.MustCompile(`^  -> deleted: (.*?)(?: :: .*)?$`)
	renamedRe = regexp.MustCompile(`^  -> renamed: (.*) → (.*)$`)
	failedRe  = regexp.MustCompile(`^  -> FAILED: (.*?) :: `)
	handRe    = regexp.MustCompile(`^(.*) :: (deleted from the dashboard|renamed from the dashboard to (.*))$`)
	endRe     = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept`)
)

// recentLines are the last changes, newest last, as short events: what was
// reaped and why, what was renamed, what failed, and passes that did
// something. The log keeps the full wording.
func (m model) recentLines(n int) []string {
	t := m.th
	why := map[string]string{}
	var events []string
	for _, raw := range m.logLines {
		l := logfile.Parse(raw)
		stamp := ""
		if !l.Time.IsZero() {
			stamp = ui.Fg(t.Muted).Render(l.Time.Format("01-02 15:04")) + "  "
		}
		text := l.Text
		switch {
		case reapRe.MatchString(text):
			g := reapRe.FindStringSubmatch(text)
			key := "recent.idle"
			if g[3] == "EXITED" {
				key = "recent.exited"
			}
			why[g[1]] = i18n.T(key, g[4])
		case deletedRe.MatchString(text):
			name := deletedRe.FindStringSubmatch(text)[1]
			line := stamp + ui.Fg(t.Cat[ui.CatReap]).Render(i18n.T("recent.reaped", name))
			// The reason comes from the REAP line just before.
			if w := why[name]; w != "" {
				line += ui.Fg(t.Muted).Render(" · " + w)
			}
			events = append(events, line)
		case renamedRe.MatchString(text):
			g := renamedRe.FindStringSubmatch(text)
			events = append(events, stamp+ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("recent.renamed", g[1], g[2])))
		case failedRe.MatchString(text):
			events = append(events, stamp+ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("recent.failed", failedRe.FindStringSubmatch(text)[1])))
		case handRe.MatchString(text):
			g := handRe.FindStringSubmatch(text)
			by := ui.Fg(t.Muted).Render(" · " + i18n.T("recent.hand"))
			if g[3] != "" {
				events = append(events, stamp+ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("recent.renamed", g[1], g[3]))+by)
			} else {
				events = append(events, stamp+ui.Fg(t.Cat[ui.CatReap]).Render(i18n.T("recent.reaped", g[1]))+by)
			}
		case endRe.MatchString(text):
			g := endRe.FindStringSubmatch(text)
			// A pass that changed nothing is not news.
			if g[1] != "0" {
				events = append(events, stamp+ui.Fg(t.Muted).Render(i18n.T("recent.pass", g[1], g[2])))
			}
		}
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	return events
}

// verdictSentence says in a sentence what the next pass does.
func verdictSentence(v policy.Verdict, dry bool) string {
	switch {
	case v.Action == policy.Reap && dry:
		return i18n.T("detail.verdict.dry")
	case v.Action == policy.Reap:
		return i18n.T("detail.verdict.reap")
	case v.Wait > 0:
		return i18n.T("detail.verdict.in", duration.Format(v.Wait))
	}
	return i18n.T("detail.verdict.keep", ui.Why(v))
}

// statusLine shows the last action's result for a few seconds, otherwise a
// count of the sessions.
func (m model) statusLine() string {
	t := m.th
	if m.mode == modeFilter {
		return " " + m.input.View()
	}
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		return " " + ui.Fg(t.Cat[m.toastCat]).Render(m.toast)
	}
	if m.report.NoSessions || !m.loaded || m.err != nil {
		return ""
	}
	if m.filter != "" {
		line := i18n.T("status.filtered", len(m.rows()), len(m.report.Outcomes), m.filter)
		if _, below := m.hidden(); below > 0 {
			line += " · " + i18n.T("status.below", below)
		}
		return " " + ui.Fg(t.Muted).Render(line)
	}
	reaped := m.report.Due()
	key := "plan.summary"
	if m.report.DryRun {
		key = "plan.summary.dry"
	}
	line := i18n.TN(key, len(m.report.Outcomes), len(m.report.Outcomes), reaped)
	if above, below := m.hidden(); above > 0 || below > 0 {
		if above > 0 {
			line += " · " + i18n.T("status.above", above)
		}
		if below > 0 {
			line += " · " + i18n.T("status.below", below)
		}
	}
	return " " + ui.Fg(t.Muted).Render(line)
}

// footer lists the keys, dropping the least used first when space runs out.
func (m model) footer() string {
	t := m.th
	if m.mode == modeFilter {
		return m.keyBar(t, [][2]string{{"⏎", i18n.T("footer.apply")}, {"esc", i18n.T("footer.cancel")}})
	}
	// With no session on screen, the keys that act on one do nothing.
	if !m.loaded || m.err != nil || m.report.NoSessions {
		return m.keyBar(t, [][2]string{{"R", i18n.T("footer.refresh")}, {"l", i18n.T("footer.log")}, {"?", i18n.T("footer.help")}, {"q", i18n.T("footer.quit")}})
	}
	keys := [][2]string{
		{"↑↓", i18n.T("footer.select")}, {"⏎", i18n.T("footer.attach")}, {"d", i18n.T("footer.reap")},
		{"p", i18n.T("footer.protect")}, {"r", i18n.T("footer.rename")}, {"f", i18n.T("footer.pass")},
		{"l", i18n.T("footer.log")}, {"/", i18n.T("footer.filter")}, {"?", i18n.T("footer.help")}, {"q", i18n.T("footer.quit")},
	}
	return m.keyBar(t, keys)
}

// dropOrder is which keys leave the footer first when it does not fit; the
// help screen lists them all.
var dropOrder = []string{"r", "p", "⏎", "f", "↑↓", "/", "l", "d"}

// keyBar joins keycaps to fit the width, dropping the least needed first.
func (m model) keyBar(t ui.Theme, keys [][2]string) string {
	render := func(keys [][2]string) string {
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = t.KeyCap(k[0], k[1])
		}
		return " " + strings.Join(parts, "  ")
	}
	for _, drop := range dropOrder {
		if lipgloss.Width(render(keys)) <= m.width {
			break
		}
		for i, k := range keys {
			if k[0] == drop {
				keys = append(keys[:i:i], keys[i+1:]...)
				break
			}
		}
	}
	return render(keys)
}

// overlay floats a box in the middle of the screen. The screen behind it
// fades to one faint color, so only the dialog reads.
func (m model) overlay(screen, box string) string {
	rows := strings.Split(screen, "\n")
	for i, r := range rows {
		rows[i] = ui.Fg(m.th.Rule).Render(ansi.Strip(r))
	}
	x := max(0, (m.width-lipgloss.Width(box))/2)
	y := max(0, (m.height-lipgloss.Height(box))/2)
	out := lipgloss.NewCompositor(lipgloss.NewLayer(strings.Join(rows, "\n")), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
	// The compositor drops trailing blanks; frame the result to the screen
	// again so every line keeps the full width.
	return m.frame(strings.Split(out, "\n"))
}

// box draws a dialog: an opaque panel with a rounded border in the color of
// what it is about.
func (m model) box(border lipgloss.Style, lines ...string) string {
	w := min(64, m.width-4)
	body := lipgloss.NewStyle().Width(w - 4).Foreground(m.th.Ink).Render(strings.Join(lines, "\n"))
	return border.Border(lipgloss.RoundedBorder()).Padding(0, 2).Width(w).Render(body)
}

// confirmBox asks before a destructive action.
func (m model) confirmBox() string {
	t := m.th
	text := i18n.T("confirm.force")
	cat := ui.CatReap
	switch m.confirm {
	case confirmPass:
		text = i18n.TN("confirm.pass", m.report.Due(), m.report.Due())
	case confirmDelete:
		text = i18n.T("confirm.reap", m.target)
		for _, o := range m.report.Outcomes {
			if o.Session.Name() != m.target {
				continue
			}
			switch {
			// Protection is said first: it is why the pass would not.
			case o.Verdict.Code == policy.ProtectedName:
				text = i18n.T("confirm.reap.protected", m.target, ui.Why(o.Verdict))
				cat = ui.CatSafe
			// Something still runs in it: say what stops with it.
			case o.Verdict.Code == policy.ProtectedCommand:
				text = i18n.T("confirm.reap.running", m.target, o.Verdict.Detail, o.Verdict.Detail)
				cat = ui.CatSafe
			case o.Verdict.Code == policy.Busy:
				text = i18n.T("confirm.reap.running", m.target, o.Verdict.Detail, o.Verdict.Detail)
				cat = ui.CatBusy
			case o.Session.Listed.Exited:
				text = i18n.T("confirm.reap.exited", m.target)
			}
		}
	}
	keys := t.KeyCap("y", i18n.T("confirm.yes")) + "  " + t.KeyCap("n", i18n.T("confirm.no"))
	return m.box(lipgloss.NewStyle().BorderForeground(t.Cat[cat]), text, "", keys)
}

// inputBox holds the rename field.
func (m model) inputBox() string {
	t := m.th
	keys := t.KeyCap("⏎", i18n.T("footer.rename")) + "  " + t.KeyCap("esc", i18n.T("footer.cancel"))
	prompt := ui.Fg(t.Ink).Render(i18n.T("rename.prompt", m.target))
	return m.box(lipgloss.NewStyle().BorderForeground(t.Cat[ui.CatBusy]), prompt, m.input.View(), "", keys)
}

// helpView lists the keys and what each color means.
func (m model) helpView() string {
	t := m.th
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Ink)
	keys := [][2]string{
		{"↑ ↓", "help.key.updown"}, {"⏎", "help.key.enter"}, {"d", "help.key.d"}, {"p", "help.key.p"},
		{"r", "help.key.r"}, {"f", "help.key.f"}, {"F", "help.key.F"}, {"l", "help.key.l"},
		{"/", "help.key.slash"}, {"esc", "help.key.esc"}, {"g G", "help.key.jump"}, {"pgup pgdn", "help.key.page"},
		{"R", "help.key.R"}, {"q", "help.key.q"},
	}
	// The keys stand in one column, bold; caps stacked row on row would
	// run together into one bar.
	keyW := 0
	for _, k := range keys {
		keyW = max(keyW, lipgloss.Width(k[0]))
	}
	key := lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Width(keyW)
	lines := []string{m.header(), "", " " + title.Render(i18n.T("help.title"))}
	for _, k := range keys {
		lines = append(lines, "   "+key.Render(k[0])+"   "+ui.Fg(t.Ink).Render(i18n.T(k[1])))
	}
	lines = append(lines, "   "+ui.Fg(t.Muted).Render(i18n.T("help.log")))
	lines = append(lines, "", " "+title.Render(i18n.T("help.legend")))
	legend := []struct {
		cat ui.Category
		key string
	}{
		{ui.CatReap, "help.legend.reap"}, {ui.CatWait, "help.legend.wait"}, {ui.CatBusy, "help.legend.busy"},
		{ui.CatAttached, "help.legend.attached"}, {ui.CatSafe, "help.legend.safe"}, {ui.CatUnknown, "help.legend.unknown"},
	}
	for _, l := range legend {
		lines = append(lines, "   "+t.Dot(l.cat)+" "+ui.Fg(t.Ink).Render(i18n.T(l.key)))
	}
	// On a short terminal the list is cut, with a mark where.
	if len(lines) > m.height-1 && m.height > 2 {
		lines = append(lines[:m.height-2], "   "+ui.Fg(t.Muted).Render("…"))
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	// Any key goes back; esc is the one to name.
	lines = append(lines[:m.height-1], m.keyBar(t, [][2]string{{"esc", i18n.T("footer.back")}, {"q", i18n.T("footer.quit")}}))
	return m.frame(lines)
}

// logRows is how many log lines fit on screen.
func (m model) logRows() int { return max(1, m.height-4) }

// logView shows the decision log, newest at the bottom, colored by kind.
func (m model) logView() string {
	t := m.th
	lines := []string{m.header(), "", " " + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("log.title"))}
	if len(m.logLines) == 0 {
		lines = append(lines, " "+ui.Fg(t.Muted).Render(i18n.T("log.empty")))
	}
	n := m.logRows() - 1
	top := max(0, min(m.logTop, len(m.logLines)-n))
	for _, raw := range m.logLines[top:min(len(m.logLines), top+n)] {
		lines = append(lines, " "+logLine(t, raw))
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	follow := i18n.T("footer.follow")
	if m.follow {
		follow += " ●"
	}
	pos := ""
	if len(m.logLines) > 0 {
		pos = ui.Fg(t.Muted).Render("  " + strconv.Itoa(min(len(m.logLines), top+n)) + "/" + strconv.Itoa(len(m.logLines)))
	}
	bar := m.keyBar(t, [][2]string{{"↑↓", i18n.T("footer.scroll")}, {"f", follow}, {"esc", i18n.T("footer.back")}}) + pos
	lines = append(lines[:m.height-1], bar)
	return m.frame(lines)
}

// Patterns for the log view: the pass markers, a decision, and the lines
// under one. The log's own words are an interface and stay as written; the
// view only lays them out.
var (
	startRe   = regexp.MustCompile(`^=== reaper start \(dry_run=(true|false) idle_after=(\S+) exited_after=(\S+?)( force)?\) ===$`)
	endAllRe  = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept, (\d+) renamed\) ===$`)
	decideRe  = regexp.MustCompile(`^(.*?) (\[(?:RUNNING|EXITED)\] panes=\{.*\}) :: (.*)$`)
	keptRe    = regexp.MustCompile(`^  -> kept: (.*?) :: (.*)$`)
	failRe    = regexp.MustCompile(`^  -> FAILED: (.*?) :: (.*)$`)
	dryDelRe  = regexp.MustCompile(`^  -> DRY_RUN: would run: zellij delete-session (.*) --force$`)
	dryRenRe  = regexp.MustCompile(`^  -> DRY_RUN: would rename '(.*)' → '(.*)'$`)
	renFailRe = regexp.MustCompile(`^  -> rename FAILED: (.*) → (.*?) :: (.*)$`)
)

// logLine lays out one log line: a pass's start and end as a muted rule,
// a decision as the name, its facts and the verdict, and what a pass did
// to a session as an arrow, in the current language. A line it does not
// know is shown as written.
func logLine(t ui.Theme, raw string) string {
	l := logfile.Parse(raw)
	stamp := ""
	if !l.Time.IsZero() {
		stamp = ui.Fg(t.Muted).Render(l.Time.Format("01-02 15:04")) + "  "
	}
	text := l.Text
	// arrow is a line under a decision: what the pass did.
	arrow := func(c color.Color, s string) string {
		return stamp + ui.Fg(t.Muted).Render("  → ") + ui.Fg(c).Render(s)
	}
	rule := func(s string) string { return stamp + ui.Fg(t.Muted).Render("── "+s) }
	switch {
	case startRe.MatchString(text):
		g := startRe.FindStringSubmatch(text)
		s := i18n.T("log.start", g[2], g[3])
		if g[1] == "true" {
			s += i18n.T("log.start.dry")
		}
		if g[4] != "" {
			s += i18n.T("log.start.force")
		}
		return rule(s)
	case endAllRe.MatchString(text):
		g := endAllRe.FindStringSubmatch(text)
		return rule(i18n.T("log.end", g[1], g[2], g[3]))
	case text == "=== reaper end ===":
		return rule(i18n.T("log.end.bare"))
	case decideRe.MatchString(text):
		g := decideRe.FindStringSubmatch(text)
		name := ui.Fg(t.Ink).Bold(true).Render(g[1])
		// A decision the dashboard knows reads in the current language,
		// colored like its row; the file keeps the log's own words.
		if v, ok := ui.VerdictFromLog(g[3]); ok {
			key := "log.kept.why"
			if v.Action == policy.Reap {
				key = "log.reap.why"
			}
			return stamp + name + "  " + ui.Fg(t.Cat[ui.Categorize(v)]).Render(i18n.T(key, ui.Why(v)))
		}
		c := t.Ink
		if l.Kind() == "reap" {
			c = t.Cat[ui.CatReap]
		}
		return stamp + name + ui.Fg(t.Muted).Render(" "+g[2]+" :: ") + ui.Fg(c).Render(g[3])
	case deletedRe.MatchString(text):
		return arrow(t.Cat[ui.CatReap], i18n.T("log.deleted", deletedRe.FindStringSubmatch(text)[1]))
	case keptRe.MatchString(text):
		g := keptRe.FindStringSubmatch(text)
		return arrow(t.Ink, i18n.T("log.kept", g[1], g[2]))
	case failRe.MatchString(text):
		g := failRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatUnknown], i18n.T("log.failed", g[1], g[2]))
	case renamedRe.MatchString(text):
		g := renamedRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.renamed", g[1], g[2]))
	case renFailRe.MatchString(text):
		g := renFailRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatUnknown], i18n.T("log.rename.failed", g[1], g[2], g[3]))
	case dryDelRe.MatchString(text):
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.dry.delete", dryDelRe.FindStringSubmatch(text)[1]))
	case dryRenRe.MatchString(text):
		g := dryRenRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.dry.rename", g[1], g[2]))
	}
	c := t.Ink
	switch l.Kind() {
	case "start", "end":
		c = t.Muted
	case "reap", "deleted":
		c = t.Cat[ui.CatReap]
	case "failed":
		c = t.Cat[ui.CatUnknown]
	case "renamed", "dry":
		c = t.Cat[ui.CatBusy]
	}
	return stamp + ui.Fg(c).Render(text)
}

// tilde shortens the home directory to "~" for display.
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
