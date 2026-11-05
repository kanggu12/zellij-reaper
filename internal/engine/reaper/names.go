package reaper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/core/rename"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// mayRename says whether auto_rename may rename a survivor: its verdict allows
// it, and its server was found and said nobody was attached. A session kept
// for a protected program is decided before its clients are looked at, so the
// verdict alone does not say whether someone is using it.
func mayRename(o Outcome) bool {
	s := o.Session
	return !s.Listed.Exited && o.Verdict.MayRename() && s.Server != nil && s.Facts.Attached == policy.No
}

// takenNames is the set of names zellij lists, which a rename must avoid.
func takenNames(r Report) map[string]bool {
	taken := map[string]bool{}
	for _, o := range r.Outcomes {
		taken[o.Session.Name()] = true
	}
	return taken
}

// renameTarget returns the name auto_rename wants for a survivor (desired)
// and the first free variant of it (target); target is "" for no rename.
func (e *Env) renameTarget(o Outcome, taken map[string]bool, h *history) (desired, target string) {
	if !mayRename(o) || !e.replaceable(o.Session, h) {
		return "", ""
	}
	return e.nameFor(o.Session, taken)
}

// replaceable reports whether auto_rename may replace a session's name. A name
// someone chose stays, and the server's command line, which keeps the name a session
// started under through every rename, is what tells them apart:
//
//   - A session that still has that name was never renamed, so no pass gave it. Only
//     two words from zellij's lists can go, since that is what zellij makes up.
//     zellij keeps nothing that tells such a name from one someone typed (`zellij -s
//     quiet-mouse`): its command line, environment and files are the same. So a
//     person says so, with r and Enter in the dashboard, and the name is kept.
//   - A session with another name was renamed, by a pass or by someone. A pass writes
//     its names down. When the list does not have a name, because a version from
//     before it gave the name or the list was lost, the decision log's renames still
//     tell. Versions from before the log stamped every name, so the stamp tells those,
//     unless stamps are on now: `r` in the dashboard then offers stamped names too, and
//     one taken there was chosen by hand.
//
// Keeping a name used to take the protected list, which keeps the session from being
// reaped as well.
func (e *Env) replaceable(s inspect.Session, h *history) bool {
	name := s.Name()
	if e.Kept.Has(name) {
		return false
	}
	if s.StartedAs() == name {
		return rename.Generated(name)
	}
	if e.Given.Has(name) {
		return true
	}
	if rename.Stamped(name) && rename.Stamp(e.Config.AutoRenameStamp) != rename.StampTime {
		return true
	}
	return rename.GivenByPass(h.get(), name, s.StartedAs())
}

// history is the renames in the decision log, read the first time a name needs them:
// only a session renamed since it started, whose name no list accounts for, does.
type history struct {
	path    string
	once    sync.Once
	changes []rename.Change
}

// get reads the renames once. A log that cannot be read says no pass gave a name, so
// the name stays.
func (h *history) get() []rename.Change {
	if h == nil {
		return nil
	}
	h.once.Do(func() { h.changes, _ = logfile.Changes(h.path) })
	return h.changes
}

// nameFor returns the name a running session's panes point to (desired) and
// the first free variant of it (target). target is "" when the panes say
// nothing, when the session has that name already, or when every variant is
// taken.
func (e *Env) nameFor(s inspect.Session, taken map[string]bool) (desired, target string) {
	// The client count is near the end of zellij's metadata file: without it
	// the file is being rewritten, and titles that are not there yet must not
	// read as a session with nothing to show.
	if !s.Files.Metadata.HasClientCount {
		return "", ""
	}
	budget := rename.MaxBase
	if s.Socket != "" {
		budget = rename.BaseBudget(zellij.NameRoom(filepath.Dir(s.Socket)))
	}
	base := rename.Suggest(rename.Input{
		Titles: s.Files.Metadata.TitlesByFocus(),
		Busy:   s.Facts.BusyWith,
		Cwd:    s.WorkDir(),
		Home:   e.Home,
		Host:   e.Host,
	}, budget)
	if base == "" {
		return "", ""
	}
	when := s.Facts.LastActive
	if when.IsZero() {
		when = e.Now()
	}
	desired = rename.Desired(base, rename.Stamp(e.Config.AutoRenameStamp), when)
	// Named this way already, perhaps with a tail from a collision: nothing
	// to do. The tail stays even once the other session is gone, because a
	// name changes only when what the session shows changes.
	if rename.Same(s.Name(), desired) {
		return desired, ""
	}
	free, ok := rename.Free(desired, taken)
	if !ok {
		return desired, ""
	}
	return desired, free
}

// rename renames a session to target, stepping through desired-2, -3, ...
// when zellij refuses a name it still remembers but does not list. The end of the
// pass clears from the list of given names what did not stick: the session's old
// name, and any name zellij refused.
func (e *Env) rename(ctx context.Context, o *Outcome, desired, target string, taken map[string]bool) {
	name := o.Session.Name()
	for attempt := target; ; {
		err := e.renameTo(ctx, o.Session, attempt)
		if err == nil {
			o.RenamedTo = attempt
			delete(taken, name)
			taken[attempt] = true
			e.Log.Printf("  -> renamed: %s → %s", name, attempt)
			return
		}
		// Anything but a taken name is a real failure: stop and say so.
		if !errors.Is(err, zellij.ErrNameTaken) {
			// A rename that could not be confirmed may have happened, so the session
			// may have either name until a pass sees which. Both stay on the list,
			// and out of reach of the sessions after this one.
			if errors.Is(err, zellij.ErrUnconfirmed) {
				taken[attempt] = true
			}
			o.RenameErr = err
			e.Log.Printf("  -> rename FAILED: %s → %s :: %v", name, attempt, err)
			return
		}
		next, ok := rename.Next(desired, attempt)
		// Skip variants another listed session already has.
		for ok && taken[next] {
			next, ok = rename.Next(desired, next)
		}
		if !ok {
			o.RenameErr = fmt.Errorf("too many names taken")
			e.Log.Printf("  -> rename gave up: %s → %s (too many collisions)", name, desired)
			return
		}
		attempt = next
	}
}

// renameTo renames a session to one name. A file at that name in the session's
// socket directory is another session's, listed or not: zellij would move this
// session's socket over it and cut the other one off, so it counts as taken.
func (e *Env) renameTo(ctx context.Context, s inspect.Session, to string) error {
	if s.Socket != "" && zellij.SocketTaken(filepath.Dir(s.Socket), to) {
		return zellij.ErrNameTaken
	}
	// Written down first: a pass that dies right after the rename leaves a name
	// the next pass knows as given. The other order would leave one that looks
	// chosen by hand, and it would never change again.
	e.remember(to)
	return e.Zellij.Rename(ctx, s.Name(), to)
}

// remember records a name auto_rename is about to give. A list that cannot be
// written costs little: the session keeps that name until someone changes it.
func (e *Env) remember(name string) {
	if e.Given == nil {
		return
	}
	if err := e.Given.Set(name, true); err != nil {
		e.Log.Printf("  -> could not record the name %s: %v", name, err)
	}
}

// forgetGone clears given and kept names no session has any more, at the end of a
// pass: a session's old name, a name zellij refused, a session that is gone.
func (e *Env) forgetGone(listed map[string]bool) {
	for _, l := range []*protect.List{e.Given, e.Kept} {
		if l == nil {
			continue
		}
		for _, name := range l.Names() {
			if !listed[name] {
				_ = l.Set(name, false)
			}
		}
	}
}
