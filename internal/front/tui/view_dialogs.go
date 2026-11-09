package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/version"
)

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
	// The border takes a cell on each side and the padding two: the text
	// gets the rest, or lipgloss wraps it a second time and strands a word.
	body := lipgloss.NewStyle().Width(w - 6).Foreground(m.th.Ink).Render(strings.Join(lines, "\n"))
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
		text, cat = ReapQuestion(m.report, m.target)
	}
	keys := t.KeyCap("y", i18n.T("confirm.yes")) + "  " + t.KeyCap("n", i18n.T("confirm.no"))
	return m.box(lipgloss.NewStyle().BorderForeground(t.Cat[cat]), text, "", keys)
}

// ReapQuestion is what is asked before a session is reaped by hand, in the
// dashboard or by `delete`, with the color to ask it in: what protects the
// session or still runs in it is said first, since a pass would keep it for
// that.
func ReapQuestion(r reaper.Report, name string) (string, ui.Category) {
	text, cat := i18n.T("confirm.reap", name), ui.CatReap
	for _, o := range r.Outcomes {
		if o.Session.Name() != name {
			continue
		}
		switch {
		// Protection is said first: it is why the pass would not.
		case o.Verdict.Code == policy.ProtectedName:
			// The question says protected already: in brackets, only by what.
			key := "confirm.reap.protected"
			// An exited session has no panes to close: what goes is
			// what zellij keeps to bring it back.
			if o.Session.Listed.Exited {
				key = "confirm.reap.protected.exited"
			}
			text, cat = i18n.T(key, name, ui.ProtectedBy(o.Verdict)), ui.CatSafe
		// Something still runs in it: say what stops with it.
		case o.Verdict.Code == policy.ProtectedCommand:
			text, cat = i18n.T("confirm.reap.running", name, o.Verdict.Detail, o.Verdict.Detail), ui.CatSafe
		case o.Verdict.Code == policy.Busy:
			text, cat = i18n.T("confirm.reap.running", name, o.Verdict.Detail, o.Verdict.Detail), ui.CatBusy
		case o.Session.Listed.Exited:
			text = i18n.T("confirm.reap.exited", name)
		}
	}
	return text, cat
}

// inputBox holds the rename field.
func (m model) inputBox() string {
	t := m.th
	// The keys say what Enter does with the field as it is now, and what Tab
	// puts in it instead. An empty field gives Enter nothing to do.
	var enter, swap string
	switch {
	case strings.TrimSpace(m.input.value()) == "":
	case m.fieldHoldsTarget():
		enter = t.KeyCap("⏎", i18n.T("footer.keep"))
	default:
		enter = t.KeyCap("⏎", i18n.T("footer.rename"))
	}
	if m.suggested != m.target {
		if m.fieldHoldsTarget() {
			swap = t.KeyCap("⇥", i18n.T("footer.suggested"))
		} else {
			swap = t.KeyCap("⇥", i18n.T("footer.current"))
		}
	}
	esc := t.KeyCap("esc", i18n.T("footer.cancel"))
	// One line, so no key is parted from its words: on a narrow screen the Tab
	// key goes first, then esc, which the help screen still lists.
	inner := min(64, m.width-4) - 6
	var keys string
	for _, set := range [][]string{{enter, swap, esc}, {enter, esc}, {enter}} {
		var parts []string
		for _, p := range set {
			if p != "" {
				parts = append(parts, p)
			}
		}
		keys = strings.Join(parts, "  ")
		if lipgloss.Width(keys) <= inner {
			break
		}
	}
	prompt := ui.Fg(t.Ink).Render(i18n.T("rename.prompt", m.target))
	return m.box(lipgloss.NewStyle().BorderForeground(t.Cat[ui.CatBusy]), prompt, m.input.view(), "", keys)
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
	items := make([]string, len(legend))
	for i, l := range legend {
		items[i] = t.Dot(l.cat) + " " + ui.Fg(t.Ink).Render(i18n.T(l.key))
	}
	// Short of rows, the colors stand in two columns rather than lose
	// their last lines; the keys above are too many to do the same.
	if colW := blockWidth(items) + 3; len(lines)+len(items) > m.height-1 && 3+2*colW <= m.width {
		half := (len(items) + 1) / 2
		for i := range half {
			l := "   " + items[i]
			if i+half < len(items) {
				l += strings.Repeat(" ", colW-lipgloss.Width(items[i])) + items[i+half]
			}
			lines = append(lines, l)
		}
	} else {
		for _, it := range items {
			lines = append(lines, "   "+it)
		}
	}
	// A wide screen has room beside the keys for the logo, the version and
	// where the project lives.
	card := t.LogoBlock(t.Brand(version.Short()), ui.Fg(t.Muted).Render(i18n.T("logo.tagline")),
		ui.Fg(t.Muted).Render("github.com/kanggu12/zellij-reaper"))
	// Only the rows the card sits beside need to clear it.
	if gutter := 4; len(lines) >= 2+len(card) && m.width >= blockWidth(lines[2:2+len(card)])+gutter+blockWidth(card) {
		leftW := blockWidth(lines[2 : 2+len(card)])
		for i, c := range card {
			l := lines[2+i]
			lines[2+i] = l + strings.Repeat(" ", leftW+gutter-lipgloss.Width(l)) + c
		}
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
