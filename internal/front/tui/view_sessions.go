package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

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
