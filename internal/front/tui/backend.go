// Package tui is the dashboard: every session with what the next pass will do
// to it and why, the decision log, and the actions people take by hand.
package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/systemd"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// Backend is everything the dashboard reads and does. The real one works on
// the machine; tests use a fake that records the actions.
type Backend interface {
	// Plan is what the next pass would do now.
	Plan(ctx context.Context) (reaper.Report, error)
	// Pass runs a pass now, skipping the waits when force is set.
	Pass(ctx context.Context, force bool) (reaper.Report, error)
	// Delete reaps one session by hand, unless zellij says right before
	// that somebody uses it.
	Delete(ctx context.Context, name string) error
	// Rename renames one session by hand. The text, when not empty, is a
	// warning to show instead of the usual "renamed".
	Rename(ctx context.Context, name, to string) (string, error)
	// KeepName makes a name auto_rename gave someone's choice, so passes leave it
	// alone, and reports whether it was such a name.
	KeepName(name string) (bool, error)
	// SetProtected adds a name to the protected list or removes it.
	SetProtected(name string, on bool) error
	// Protected reports whether a name is on the protected list.
	Protected(name string) bool
	// PatternProtects reports whether the settings' pattern protects a name.
	PatternProtects(name string) bool
	// Timer is the systemd timer's state.
	Timer() systemd.TimerStatus
	// ZellijVersion is the zellij version, or "".
	ZellijVersion(ctx context.Context) string
	// Config is the settings in effect.
	Config() *config.Config
	// LogLines returns the last n lines of the decision log.
	LogLines(n int) ([]string, error)
	// AttachCommand returns the command that attaches to (or, inside
	// zellij, switches to) a session; interactive is true when it needs the
	// terminal.
	AttachCommand(name string) (cmd *exec.Cmd, interactive bool)
}

// real is the Backend of a loaded App.
type real struct{ a *app.App }

// NewBackend returns the Backend for a.
func NewBackend(a *app.App) Backend { return real{a: a} }

func (r real) Plan(ctx context.Context) (reaper.Report, error) {
	// Other terminals and the timer change the protected list: read it as
	// the file holds it now, so the screen and the p toggle start from that.
	if err := r.a.Protected.Reload(); err != nil {
		return reaper.Report{}, errors.New(i18n.T("rename.nolist", err))
	}
	return reaper.Plan(ctx, r.a.Reaper, reaper.Options{})
}

func (r real) Pass(ctx context.Context, force bool) (reaper.Report, error) {
	return reaper.Run(ctx, r.a.Reaper, reaper.Options{Force: force})
}

// handAction runs an action by hand under the pass lock, so it never races a
// timer pass, and records it in the decision log.
func (r real) handAction(do func() error, logf string, args ...any) error {
	l, err := lock.Acquire(r.a.Reaper.LockPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer l.Release()
	if err := do(); err != nil {
		return err
	}
	r.a.Reaper.Log.Printf(logf, args...)
	return nil
}

func (r real) Delete(ctx context.Context, name string) error {
	return r.handAction(func() error {
		// The dialog may have stayed open across refreshes: what the screen
		// showed is not enough, so ask zellij now.
		if err := r.unused(ctx, name); err != nil {
			return err
		}
		return r.a.Zellij.Delete(ctx, name)
	}, "%s :: deleted from the dashboard", name)
}

// unused checks, right before a delete by hand, that nobody uses the session:
// an exited one is still listed as exited, a running one has no client. Any
// answer that cannot be read refuses the delete.
func (r real) unused(ctx context.Context, name string) error {
	listed, err := r.a.Zellij.ListSessions(ctx)
	switch {
	case errors.Is(err, zellij.ErrNoSessions):
		return errors.New(i18n.T("delete.gone"))
	case err != nil:
		return errors.New(i18n.T("delete.unknown", err))
	}
	for _, l := range listed {
		if l.Name != name {
			continue
		}
		// An exited session has no server, so nobody can be attached.
		if l.Exited {
			return nil
		}
		rows, err := r.a.Zellij.ListClients(ctx, name)
		if err != nil {
			return errors.New(i18n.T("delete.unknown", err))
		}
		if len(rows) > 0 {
			return errors.New(i18n.T("delete.attached"))
		}
		return nil
	}
	return errors.New(i18n.T("delete.gone"))
}

func (r real) Rename(ctx context.Context, name, to string) (string, error) {
	var note string
	err := r.handAction(func() error {
		var err error
		note, err = r.renameByHand(ctx, name, to)
		// A name given by hand is someone's choice: from now on auto_rename leaves
		// it alone, even when it was once on the list of names it gave. That goes for
		// a rename that could not be confirmed too, in case it happened. A name zellij
		// refused may belong to another session, whose entry must stay. Under the pass
		// lock, so a pass waiting for it reads the list with this change in it.
		if err == nil || errors.Is(err, zellij.ErrUnconfirmed) {
			if g := r.a.Reaper.Given; g != nil {
				_ = g.Set(to, false)
			}
		}
		return err
	}, "%s :: renamed from the dashboard to %s", name, to)
	return note, err
}

func (r real) KeepName(name string) (bool, error) {
	g := r.a.Reaper.Given
	if g == nil || !g.Has(name) {
		return false, nil
	}
	return true, g.Set(name, false)
}

// renameByHand does a rename asked for in the dashboard, and returns what the person
// must hear about it, if anything.
func (r real) renameByHand(ctx context.Context, name, to string) (string, error) {
	var note string
	pl := r.a.Protected
	// A name past the socket path's limit leaves a session no zellij
	// command can reach: refuse it before zellij renames anything.
	if limit := r.nameLimit(); limit > 0 && len(to) > limit {
		return "", errors.New(i18n.T("rename.toolong", limit))
	}
	// The list as the file holds it now: a protection added from
	// another terminal moves with the session too.
	if err := pl.Reload(); err != nil {
		return "", errors.New(i18n.T("rename.nolist", err))
	}
	if pl.Has(name) {
		return "", r.renameListed(ctx, name, to)
	}
	if err := r.a.Zellij.Rename(ctx, name, to); err != nil {
		return "", err
	}
	// A protection the old name got from another terminal while zellij
	// renamed it moves to the new name (nothing to move otherwise).
	if err := pl.Rename(name, to); err != nil {
		return "", err
	}
	// Only the pattern protected the old name, and it does not cover
	// the new one: the session is renamed as asked, and loses its
	// protection, which the person must hear about.
	if r.a.Config.ProtectMatches(name) && !r.a.Config.ProtectMatches(to) && !pl.Has(to) {
		note = i18n.T("toast.renamed.unprotected", name, to)
	}
	return note, nil
}

// nameLimit is the longest session name, in bytes, whose socket path fits in
// zellij's socket directory here; 0 when the directory is not known.
func (r real) nameLimit() int {
	dir := zellij.SocketDir(os.LookupEnv, os.Getuid(), r.a.ZellijVersion)
	if dir == "" {
		return 0
	}
	return zellij.NameRoom(dir)
}

// renameListed renames a session on the protected list without a moment in
// which none of its names is protected.
func (r real) renameListed(ctx context.Context, name, to string) error {
	pl := r.a.Protected
	// A name the list cannot hold would leave the session unprotected after
	// the rename: refuse before zellij renames anything.
	if err := protect.Storable(to); err != nil {
		return errors.New(i18n.T("rename.unstorable", name, err))
	}
	// Protect the new name first. If that cannot be saved, nothing has
	// been renamed yet.
	added := false
	if !pl.Has(to) {
		if err := pl.Set(to, true); err != nil {
			return err
		}
		added = true
	}
	if err := r.a.Zellij.Rename(ctx, name, to); err != nil {
		// Not renamed: take back the name added for it. Should that fail,
		// the extra name only ever keeps a session, never reaps one. When
		// zellij took the request and the result could not be seen, the
		// session may have either name, so both stay protected.
		if added && !errors.Is(err, zellij.ErrUnconfirmed) {
			pl.Set(to, false)
		}
		return err
	}
	// The old name no longer names this session. Should this fail, the old
	// name stays on the list as well, which again only keeps more.
	pl.Set(name, false)
	return nil
}

func (r real) SetProtected(name string, on bool) error { return r.a.Protected.Set(name, on) }
func (r real) Protected(name string) bool              { return r.a.Protected.Has(name) }
func (r real) PatternProtects(name string) bool        { return r.a.Config.ProtectMatches(name) }
func (r real) Timer() systemd.TimerStatus              { return r.a.System.Status() }
func (r real) Config() *config.Config                  { return r.a.Config }

func (r real) ZellijVersion(ctx context.Context) string {
	v, _ := r.a.Zellij.Version(ctx)
	return v
}

func (r real) LogLines(n int) ([]string, error) {
	lines, err := logfile.Tail(r.a.Config.LogPath, n)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return lines, err
}

func (r real) AttachCommand(name string) (*exec.Cmd, bool) {
	// Inside zellij, attaching would nest a session in a pane; switching
	// moves this client over instead.
	if os.Getenv("ZELLIJ") != "" {
		return r.a.Zellij.SwitchTo(name), false
	}
	return r.a.Zellij.Attach(name), true
}
