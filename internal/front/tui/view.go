package tui

import (
	"errors"
	"image/color"
	"regexp"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/visible"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/version"
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
	// Session names, pane titles and command lines come from other
	// programs: only the screen's own colors go through as escape sequences.
	v := tea.NewView(visible.Terminal(body))
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
			// A stray line break or tab inside a line would add a row, or
			// cells the width does not count; a name's control characters
			// are written out before the line is measured.
			out[i] = ui.Fit(visible.Terminal(oneRow.Replace(lines[i])), m.width)
		} else {
			out[i] = strings.Repeat(" ", m.width)
		}
	}
	return strings.Join(out, "\n")
}

// oneRow keeps a line on its row.
var oneRow = strings.NewReplacer("\n", " ", "\t", " ")

// mainView is the header, the table, the details, a status line and the keys.
func (m model) mainView() string {
	lines := []string{m.header(), ""}
	body := m.bodyHeight()
	switch {
	case !m.loaded:
		lines = append(lines, m.centered(body, ui.Fg(m.th.Muted).Render(i18n.T("loading")))...)
	case errors.Is(m.err, reaper.ErrNoZellij):
		lines = append(lines, m.centered(body, append(
			[]string{ui.Fg(m.th.Cat[ui.CatUnknown]).Render(i18n.T("error.zellij"))},
			m.paragraph(i18n.T("error.zellij.hint"), m.th.Muted, body-1)...)...)...)
	case m.err != nil:
		// zellij's own error often starts with the command it ran, which
		// the sentence around it already says.
		msg := strings.TrimPrefix(oneLine(m.err.Error()), "zellij list-sessions: ")
		lines = append(lines, m.centered(body, append(
			m.paragraph(i18n.T("error.list", msg), m.th.Cat[ui.CatUnknown], body-1),
			ui.Fg(m.th.Muted).Render(i18n.T("error.retry")))...)...)
	case m.report.NoSessions:
		msg := []string{
			lipgloss.NewStyle().Bold(true).Foreground(m.th.Ink).Render(i18n.T("empty.title")),
			ui.Fg(m.th.Muted).Render(i18n.T("empty.hint")),
		}
		// Nothing to list leaves the room for the logo above the words.
		if logo := m.logoLines(); body >= len(logo)+len(msg)+2 && m.width >= blockWidth(logo)+2 {
			msg = append(append(evenBlock(logo), ""), msg...)
		}
		lines = append(lines, m.centered(body, msg...)...)
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

// paragraph sets s in lines that fit the screen, as many as most allows, as one
// block that centered puts in the middle. Wrapped, not cut: the end of a
// message can say what to do, such as the command that lets a stopped server
// go on.
func (m model) paragraph(s string, ink color.Color, most int) []string {
	words := wrapWords(s, min(m.width-4, 96))
	if most >= 1 && len(words) > most {
		words = append(words[:most-1], ansi.Truncate(strings.Join(words[most-1:], " "), m.width-4, "…"))
	}
	w := blockWidth(words)
	out := make([]string, len(words))
	for i, l := range words {
		out[i] = ui.Fg(ink).Render(padCells(l, w))
	}
	return out
}

// logoLines is the logo with the name and version beside it, and under
// them what the tool does.
func (m model) logoLines() []string {
	t := m.th
	return t.LogoBlock(t.Brand(version.Short()), ui.Fg(t.Muted).Render(i18n.T("logo.tagline")))
}

// blockWidth is the widest of some lines, in cells.
func blockWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	return w
}

// evenBlock pads lines to one width, so that centering moves them as one
// block instead of each line on its own.
func evenBlock(lines []string) []string {
	w := blockWidth(lines)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l + strings.Repeat(" ", w-lipgloss.Width(l))
	}
	return out
}

// header is the top bar. On the left the mark, the name and the two versions
// a bug report asks for; on the right whether passes delete anything, when
// the next one runs, and the waits. A screen too narrow for all of it drops
// pieces in one order, the same in every state (headerDrops), the name last;
// the state stays.
func (m model) header() string {
	var gone []string
	for _, piece := range headerDrops {
		if line, w := m.headerWith(gone); w <= m.width {
			return line
		}
		gone = append(gone, piece)
	}
	line, _ := m.headerWith(gone)
	return line
}

// headerDrops is the order the header's pieces leave a narrow screen, the
// least needed first: the waits, which the details say too; zellij's version
// and this build's, which a bug report asks for and `version` prints; how long
// until the next pass, its time staying; the mark; that a running timer will
// not start again; the next pass; and on the narrowest screens the name, which
// the window's title says too. What passes do stays.
var headerDrops = []string{"waits", "zellij", "version", "countdown", "mark", "note", "when", "name"}

// headerWith is the header without the pieces in gone, and the cells it
// needs: the line itself is padded to the screen's width, or not when it
// does not fit.
func (m model) headerWith(gone []string) (string, int) {
	t := m.th
	without := func(piece string) bool { return slices.Contains(gone, piece) }
	sep := ui.Fg(t.Rule).Render(" · ")
	// One cell of margin on each side, as the table has.
	left := " "
	if !without("mark") {
		left += t.LogoMark() + " "
	}
	if !without("name") {
		left += t.Wordmark()
	}
	if !without("version") {
		left += "  " + ui.Fg(t.Muted).Render(version.Short())
	}
	if m.version != "" && !without("zellij") {
		left += sep + ui.Fg(t.Muted).Render("zellij "+m.version)
	}
	// A reload in flight shows as a small mark after the name. Its two cells
	// are kept when there is none, so a reload every few seconds does not
	// drop a piece and bring it back on a screen just wide enough for it.
	if m.loading {
		left += ui.Fg(t.Muted).Render(" ↻")
	} else {
		left += "  "
	}
	right := []string{m.stateWords()}
	if w := m.whenWords(!without("countdown")); w != "" && !without("when") {
		right = append(right, w)
	}
	if n := ui.PassNote(m.timer, m.timerKnown); n != "" && !without("note") {
		right = append(right, ui.Fg(t.Cat[ui.CatUnknown]).Render(n))
	}
	if !without("waits") {
		cfg := m.b.Config()
		right = append(right, ui.Fg(t.Muted).Render(i18n.T("settings.waits", duration.Format(cfg.IdleAfter), duration.Format(cfg.ExitedAfter))))
	}
	r := strings.Join(right, sep) + " "
	need := lipgloss.Width(left) + 1 + lipgloss.Width(r)
	if need > m.width {
		return left + " " + r, need
	}
	return left + strings.Repeat(" ", m.width-lipgloss.Width(left)-lipgloss.Width(r)) + r, need
}

// stateWords is whether passes delete anything (ui.PassState), bold, in the
// color of what it means.
func (m model) stateWords() string {
	cat, words := ui.PassState(m.b.Config().DryRun, m.timer, m.timerKnown)
	return lipgloss.NewStyle().Bold(true).Foreground(m.th.Cat[cat]).Render("● " + words)
}

// nextPass is when the timer runs the next pass, or zero while systemd has
// not said or the time is unknown or past.
func (m model) nextPass() time.Time {
	if next := m.timer.NextPass(); m.timerKnown && next.After(m.now) {
		return next
	}
	return time.Time{}
}

// whenWords says when the timer runs next (ui.PassWhen), quietly, counted
// from the last plan's time or the last tick's, whichever is later: a plan
// that takes its time would hold the count still.
func (m model) whenWords(countdown bool) string {
	now := m.now
	if m.clock.After(now) {
		now = m.clock
	}
	w := ui.PassWhen(m.b.Config().DryRun, m.timer, m.timerKnown, now, countdown)
	if w == "" {
		return ""
	}
	return ui.Fg(m.th.Muted).Render(w)
}

// Patterns for the log lines the recent decisions are built from.
var (
	reapRe    = regexp.MustCompile(`^(.*?) \[(RUNNING|EXITED)\] panes=\{.*\} :: REAP (IDLE|EXITED), (.+) since last activity$`)
	deletedRe = regexp.MustCompile(`^  -> deleted: (.*?)(?: :: .*)?$`)
	renamedRe = regexp.MustCompile(`^  -> renamed: (.*) → (.*)$`)
	failedRe  = regexp.MustCompile(`^  -> FAILED: (.*?) :: `)
	// What was done by hand reads the same whether the dashboard did it or
	// `delete`, `rename` and `keep-name` did.
	handRe = regexp.MustCompile(`^(.*) :: (deleted from the (?:dashboard|command line)|name kept from the (?:dashboard|command line)|renamed from the (?:dashboard|command line) to (.*))$`)
	endRe  = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept`)
)

// statusLine shows the last action's result for a few seconds, otherwise a
// count of the sessions.
func (m model) statusLine() string {
	t := m.th
	if m.mode == modeFilter {
		return " " + m.input.view()
	}
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		return " " + ui.Fg(t.Cat[m.toastCat]).Render(m.toast)
	}
	if m.report.NoSessions || !m.loaded || m.err != nil {
		return ""
	}
	var line, tail string
	if m.filter != "" {
		line = i18n.T("status.filtered", len(m.rows()), len(m.report.Outcomes), m.filter)
	} else {
		// "At the next pass": counted at the time it runs, once the timer
		// has said when.
		line, tail = ui.SummaryParts(m.report, m.nextPass(), m.now)
	}
	// Rows scrolled out of sight, filtered or not.
	var scroll string
	above, below := m.hidden()
	if above > 0 {
		scroll += " · " + i18n.T("status.above", above)
	}
	if below > 0 {
		scroll += " · " + i18n.T("status.below", below)
	}
	// The count as of now goes first when the line would not fit: the
	// yellow rows say it too, and the scroll counts say what is off screen.
	if tail != "" && lipgloss.Width(" "+line+" · "+tail+scroll) <= m.width {
		line += " · " + tail
	}
	return " " + ui.Fg(t.Muted).Render(line+scroll)
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
// help screen lists them all. The arrows go first, since everyone tries them,
// then running a pass by hand, which the timer does anyway; the keys that act
// on the selected session stay longest.
var dropOrder = []string{"↑↓", "f", "/", "l", "⏎", "r", "p", "d"}

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

// Patterns for the log view: the pass markers, a decision, and the lines
// under one. The log's own words are an interface and stay as written; the
// view only lays them out.
var (
	startRe   = regexp.MustCompile(`^=== reaper start \(dry_run=(true|false) idle_after=(\S+) exited_after=(\S+?)( force)?\) ===$`)
	endAllRe  = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept, (\d+) renamed\) ===$`)
	decideRe  = regexp.MustCompile(`^(.*?) (\[(?:RUNNING|EXITED)\] panes=\{.*\}) :: (.*)$`)
	keptRe    = regexp.MustCompile(`^  -> kept: (.*?) :: (.*)$`)
	failRe    = regexp.MustCompile(`^  -> FAILED: (.*?) :: (.*)$`)
	dryDelRe  = regexp.MustCompile(`^  -> DRY_RUN: would run: zellij delete-session (.*?)(?: --force)?$`)
	dryRenRe  = regexp.MustCompile(`^  -> DRY_RUN: would rename '(.*)' → '(.*)'$`)
	renFailRe = regexp.MustCompile(`^  -> rename FAILED: (.*) → (.*?) :: (.*)$`)
)
