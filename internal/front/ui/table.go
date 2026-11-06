package ui

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// Row is one session as the table shows it.
type Row struct {
	Name    string
	Exited  bool
	Idle    string
	Clients string
	Next    string
	Cat     Category
	// Wait is how long until the next pass reaps it, for the ones waiting.
	Wait time.Duration
	// Index is the session's position in the report the row came from.
	Index int
	// Server is the session's server process, 0 for none: a rename leaves it.
	Server int
	// Rename is the name the next pass gives the session, if any.
	Rename string
}

// renameMark is what the verdict column adds for a session the next pass renames.
func renameMark(name string) string { return "  → " + name }

// rank orders the groups: what goes first, then what is waiting, then what is
// kept, with the sessions nobody could judge before the ones kept for a reason.
var rank = map[Category]int{CatReap: 0, CatWait: 1, CatUnknown: 2, CatAttached: 3, CatBusy: 4, CatSafe: 5}

// Sorted orders rows by what the next pass does to them: reaped first, then
// the waiting ones (soonest first), then the kept ones; by name within a group.
func Sorted(rows []Row) []Row {
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b Row) int {
		if c := cmp.Compare(rank[a.Cat], rank[b.Cat]); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Wait, b.Wait); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// RowsFrom turns a report into table rows, in the report's order.
func RowsFrom(r reaper.Report, now time.Time) []Row {
	rows := make([]Row, 0, len(r.Outcomes))
	for i, o := range r.Outcomes {
		row := Row{Name: o.Session.Name(), Exited: o.Session.Listed.Exited, Index: i}
		if s := o.Session.Server; s != nil {
			row.Server = s.PID
		}
		if t := o.Session.Facts.LastActive; !t.IsZero() {
			row.Idle = duration.Format(now.Sub(t))
		} else {
			row.Idle = "?"
		}
		switch {
		case row.Exited:
			row.Clients = "–"
		case o.Session.Clients < 0:
			row.Clients = "?"
		default:
			row.Clients = strconv.Itoa(o.Session.Clients)
		}
		row.Next = NextPass(o.Verdict, r.DryRun)
		row.Cat = Categorize(o.Verdict)
		row.Wait = o.Verdict.Wait
		row.Rename = o.WouldRename
		rows = append(rows, row)
	}
	return rows
}

// RowsAfterPass turns a pass's report into rows whose last column says what
// the pass did: reaped, failed, would reap (a dry run), or why it kept one.
func RowsAfterPass(r reaper.Report, now time.Time) []Row {
	rows := RowsFrom(r, now)
	for i, o := range r.Outcomes {
		switch {
		case o.Deleted:
			rows[i].Next = i18n.T("result.reaped")
		case o.DeleteErr != nil:
			rows[i].Next, rows[i].Cat = i18n.T("result.failed"), CatUnknown
		case o.KeptAtDelete != "":
			// The reason is the log's, in English; the word in front says
			// in the reader's language what happened.
			rows[i].Next, rows[i].Cat = i18n.T("result.kept", o.KeptAtDelete), CatUnknown
		case r.DryRun && o.Verdict.Action == policy.Reap:
			rows[i].Next = i18n.T("result.dry")
		}
	}
	return rows
}

// Fit cuts s to w cells (adding "…" when cut) and pads it to exactly w, so
// columns line up whatever the script: a Hangul syllable takes two cells.
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

// columns are the widths of the fixed columns and the gap between columns.
type columns struct{ name, state, idle, clients, next int }

const gap = 2

// layout splits width between the columns: the fixed ones take what their
// longest word needs, the name and the verdict share the rest.
func layout(rows []Row, width int) columns {
	c := columns{
		state:   max(lipgloss.Width(i18n.T("col.state")), lipgloss.Width(i18n.T("state.running")), lipgloss.Width(i18n.T("state.exited"))),
		idle:    lipgloss.Width(i18n.T("col.idle")),
		clients: lipgloss.Width(i18n.T("col.clients")),
	}
	nameW, nextW := lipgloss.Width(i18n.T("col.session")), lipgloss.Width(i18n.T("col.verdict"))
	for _, r := range rows {
		c.idle = max(c.idle, lipgloss.Width(r.Idle))
		c.clients = max(c.clients, lipgloss.Width(r.Clients))
		nameW = max(nameW, lipgloss.Width(r.Name))
		next := r.Next
		if r.Rename != "" {
			next += renameMark(r.Rename)
		}
		nextW = max(nextW, lipgloss.Width(next))
	}
	// Two cells for the dot and its space, then the gaps between columns.
	rest := width - 2 - c.state - c.idle - c.clients - 4*gap
	switch {
	case nameW+nextW <= rest:
		c.name, c.next = nameW, rest-nameW
	default:
		// Not enough room: the name gets up to 60% and never less than 12.
		c.name = max(12, min(nameW, rest*6/10))
		c.next = max(8, rest-c.name)
	}
	return c
}

// nextCell is the verdict in its category's color, then the name the next pass
// gives the session, muted, cut to what the column leaves; the verdict comes
// first when there is not room for both.
func (t Theme) nextCell(bg lipgloss.Style, r Row, width int) string {
	verdict := bg.Foreground(t.Cat[r.Cat])
	room := width - lipgloss.Width(r.Next)
	// "  → " and at least a few letters of the name, or no mark at all.
	if r.Rename == "" || room < 8 {
		return verdict.Render(Fit(r.Next, width))
	}
	return verdict.Render(r.Next) + bg.Foreground(t.Muted).Render(Fit(renameMark(r.Rename), room))
}

// Table renders the header and the rows to exactly width cells per line. The
// row at selected (-1 for none) gets the selection background across the
// whole line.
func (t Theme) Table(rows []Row, width, selected int) []string {
	return t.TableWith(rows, width, selected, i18n.T("col.verdict"))
}

// TableWith is Table with its own heading for the last column.
func (t Theme) TableWith(rows []Row, width, selected int, last string) []string {
	c := layout(rows, width)
	pad := strings.Repeat(" ", gap)
	head := Fg(t.Muted).Bold(true)
	lines := []string{
		"  " + head.Render(Fit(i18n.T("col.session"), c.name)) + pad +
			head.Render(Fit(i18n.T("col.state"), c.state)) + pad +
			head.Render(Fit(i18n.T("col.idle"), c.idle)) + pad +
			head.Render(Fit(i18n.T("col.clients"), c.clients)) + pad +
			head.Render(Fit(last, c.next)),
	}
	for i, r := range rows {
		bg := lipgloss.NewStyle()
		if i == selected {
			bg = bg.Background(t.Selected)
		}
		ink := bg.Foreground(t.Ink)
		muted := bg.Foreground(t.Muted)
		state := i18n.T("state.running")
		if r.Exited {
			state = i18n.T("state.exited")
		}
		name := ink
		if i == selected {
			name = name.Bold(true)
		}
		line := bg.Foreground(t.Cat[r.Cat]).Render("●") + bg.Render(" ") +
			name.Render(Fit(r.Name, c.name)) + bg.Render(pad) +
			muted.Render(Fit(state, c.state)) + bg.Render(pad) +
			ink.Render(Fit(r.Idle, c.idle)) + bg.Render(pad) +
			ink.Render(Fit(r.Clients, c.clients)) + bg.Render(pad) +
			t.nextCell(bg, r, c.next)
		// Fill to the full width so the selection reads as one bar.
		if w := lipgloss.Width(line); w < width {
			line += bg.Render(strings.Repeat(" ", width-w))
		}
		lines = append(lines, line)
	}
	return lines
}
