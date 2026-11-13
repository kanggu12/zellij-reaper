package tui

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
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
		// A dry run deleted nothing: it says what it would have reaped.
		if r.DryRun {
			return doneMsg{text: i18n.T("toast.pass.dry", reaped, kept), cat: ui.CatWait}
		}
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
			return doneMsg{text: RenameFailed(err), cat: ui.CatUnknown}
		}
		// Renamed, with something the person must know (it lost its
		// protection): in the color of what the next pass may reap.
		if note != "" {
			return doneMsg{text: note, cat: ui.CatReap, from: name, to: to}
		}
		return doneMsg{text: i18n.T("toast.renamed", name, to), cat: ui.CatBusy, from: name, to: to}
	}
}

// dropLeftover deletes the copy zellij may leave of a session renamed by
// hand under its old name, after the toast, then shows the sessions again.
func (m model) dropLeftover(name, to string) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		b.DropLeftover(ctx, name, to)
		return doneMsg{}
	}
}

// RenameFailed words a failed rename, in the dashboard and for `rename`:
// zellij's refusals and a pass holding the lock in the screen's language,
// anything else as it came.
func RenameFailed(err error) string {
	switch {
	case errors.Is(err, lock.ErrBusy):
		return i18n.T("toast.busy")
	// Before ErrNameTaken and ErrUnconfirmed, which these come with: zellij
	// 0.44 and 0.45 keep the old name of a session asked for one it had,
	// saying only that the name exists (ErrHad), or, with a client attached,
	// telling only that client, which writes it to zellij's log (ErrKept).
	case errors.Is(err, zellij.ErrHad), errors.Is(err, zellij.ErrKept):
		return i18n.T("toast.rename.failed", i18n.T("rename.had"))
	case errors.Is(err, zellij.ErrNameTaken):
		return i18n.T("toast.rename.failed", i18n.T("rename.taken"))
	case errors.Is(err, zellij.ErrUnconfirmed):
		return i18n.T("toast.rename.failed", i18n.T("rename.unconfirmed"))
	}
	return i18n.T("toast.rename.failed", err)
}

// keepName keeps a session's name as it is, when Enter in the rename field leaves
// the name unchanged: the person wants that name, whoever gave it.
func (m model) keepName(name string) tea.Cmd {
	b, ctx := m.b, m.ctx
	return func() tea.Msg {
		if err := b.KeepName(ctx, name); err != nil {
			if errors.Is(err, lock.ErrBusy) {
				return doneMsg{text: i18n.T("toast.busy"), cat: ui.CatWait}
			}
			return doneMsg{text: i18n.T("toast.keep.failed", err), cat: ui.CatUnknown}
		}
		return doneMsg{text: i18n.T("toast.kept", name), cat: ui.CatBusy}
	}
}

// toggleProtect protects the selected session, or lifts its protection.
func (m model) toggleProtect() tea.Cmd {
	o, ok := m.selected()
	if !ok {
		return nil
	}
	name := o.Session.Name()
	b, ctx := m.b, m.ctx
	on := !b.Protected(name)
	return func() tea.Msg {
		// A pass may have renamed it since the last plan, and a protection
		// for a name nobody has keeps nothing.
		if !b.HasSession(ctx, name) {
			return doneMsg{text: i18n.T("keep.gone", name), cat: ui.CatUnknown}
		}
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
	name, server := o.Session.Name(), 0
	if s := o.Session.Server; s != nil {
		server = s.PID
	}
	// Inside zellij the switch finds its sessions first, which takes a look
	// at the machine: off the event loop, so the screen does not stop.
	if m.b.InsideZellij() {
		b, ctx := m.b, m.ctx
		return m, func() tea.Msg {
			if err := b.SwitchTo(ctx, name, server); err != nil {
				return doneMsg{text: i18n.T("toast.attach.failed", err.Error()), cat: ui.CatUnknown}
			}
			return doneMsg{}
		}
	}
	cmd := m.b.AttachCommand(name)
	if cmd == nil {
		return m, nil
	}
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return doneMsg{text: i18n.T("toast.attach.failed", err), cat: ui.CatUnknown}
		}
		return doneMsg{}
	})
}
