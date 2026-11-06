package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

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
		m.openInput(prompt, max(10, min(40, m.width-lipgloss.Width(prompt)-4)), m.filter)
		return m, nil
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
		if !zellij.RenameReachable() {
			m.say(i18n.T("rename.windows"), ui.CatWait)
			return m, nil
		}
		m.mode, m.target = modeRename, o.Session.Name()
		// The prompt sits on its own line in the dialog; the field holds
		// only the name, as wide as the dialog.
		suggest := o.Suggested
		if suggest == "" {
			suggest = o.Session.Name()
		}
		m.suggested = suggest
		m.openInput("", max(10, min(64, m.width-4)-8), suggest)
		return m, nil
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
		return m, nil
	case "tab":
		// Tab swaps the rename field between the suggested name and the name the
		// session has, so keeping a name takes no typing.
		if m.mode == modeRename && m.suggested != m.target {
			if m.fieldHoldsTarget() {
				m.input.set(m.suggested)
			} else {
				m.input.set(m.target)
			}
		}
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.value())
		if m.mode == modeFilter {
			m.mode, m.filter = modeMain, value
			m.sel = 0
			m.reselect()
			return m, nil
		}
		m.mode = modeMain
		if value == "" {
			return m, nil
		}
		// Enter on the name it has: that is the name the person wants.
		if m.fieldHoldsTarget() {
			return m, m.keepName(m.target)
		}
		return m, m.rename(m.target, value)
	}
	if m.input.key(k) {
		m.inputChanged()
	}
	return m, nil
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
