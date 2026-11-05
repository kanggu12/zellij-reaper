package tui

import (
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
)

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
