package tui

import (
	"errors"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
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

// header is the top bar: the name, zellij's version, and the settings that
// decide what the next pass does.
func (m model) header() string {
	t := m.th
	// The name as the logo writes it, then this build's version, then
	// zellij's: the two versions a bug report asks for.
	name := " " + t.Wordmark()
	named := " " + t.Brand(version.Short())
	left := named
	if m.version != "" {
		left += ui.Fg(t.Rule).Render(" · ") + ui.Fg(t.Muted).Render("zellij "+m.version)
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
	// Still too wide: zellij's version goes before the mode would, then
	// this build's.
	if lipgloss.Width(left)+lipgloss.Width(r)+1 > m.width {
		left = named
	}
	if lipgloss.Width(left)+lipgloss.Width(r)+1 > m.width {
		left = name
	}
	// Still too wide: the timer goes too, rather than be cut mid-word.
	if lipgloss.Width(left)+lipgloss.Width(r)+1 > m.width {
		r = right[0]
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(r)
	if gap < 1 {
		return name + " " + r
	}
	return left + strings.Repeat(" ", gap) + r
}

// nextPass is when the timer runs the next pass, or zero while systemd has
// not said or the time is unknown or past.
func (m model) nextPass() time.Time {
	if m.timerKnown && m.timer.Next.After(m.now) {
		return m.timer.Next
	}
	return time.Time{}
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
	// launchd does not say when it runs a job next: its interval does.
	case st.Next.IsZero() && st.Interval > 0:
		return ui.Fg(t.Muted).Render(i18n.T("header.every", duration.Format(st.Interval)))
	case st.Next.IsZero():
		return ui.Fg(t.Muted).Render(i18n.T("header.next", "?"))
	}
	return ui.Fg(t.Muted).Render(i18n.T("header.next", ui.Clock(st.Next)))
}

// Patterns for the log lines the recent decisions are built from.
var (
	reapRe    = regexp.MustCompile(`^(.*?) \[(RUNNING|EXITED)\] panes=\{.*\} :: REAP (IDLE|EXITED), (.+) since last activity$`)
	deletedRe = regexp.MustCompile(`^  -> deleted: (.*?)(?: :: .*)?$`)
	renamedRe = regexp.MustCompile(`^  -> renamed: (.*) → (.*)$`)
	failedRe  = regexp.MustCompile(`^  -> FAILED: (.*?) :: `)
	handRe    = regexp.MustCompile(`^(.*) :: (deleted from the dashboard|name kept from the (?:dashboard|command line)|renamed from the dashboard to (.*))$`)
	endRe     = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept`)
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
	var line string
	if m.filter != "" {
		line = i18n.T("status.filtered", len(m.rows()), len(m.report.Outcomes), m.filter)
	} else {
		key := "plan.summary"
		if m.report.DryRun {
			key = "plan.summary.dry"
		}
		// "At the next pass": counted at the time it runs, once the timer
		// has said when.
		due := m.report.DueAtNext(m.nextPass(), m.now)
		line = i18n.TN(key, len(m.report.Outcomes), len(m.report.Outcomes), due)
	}
	// Rows scrolled out of sight, filtered or not.
	above, below := m.hidden()
	if above > 0 {
		line += " · " + i18n.T("status.above", above)
	}
	if below > 0 {
		line += " · " + i18n.T("status.below", below)
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
	dryDelRe  = regexp.MustCompile(`^  -> DRY_RUN: would run: zellij delete-session (.*) --force$`)
	dryRenRe  = regexp.MustCompile(`^  -> DRY_RUN: would rename '(.*)' → '(.*)'$`)
	renFailRe = regexp.MustCompile(`^  -> rename FAILED: (.*) → (.*?) :: (.*)$`)
)
