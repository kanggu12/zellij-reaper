package reaper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/core/rename"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
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
func (e *Env) renameTarget(o Outcome, taken map[string]bool) (desired, target string) {
	if !mayRename(o) || !e.replaceable(o.Session) {
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
//     Someone starting `zellij -s quiet-mouse` looks the same, and nothing can tell.
//   - A session with another name was renamed, by a pass or by someone. A pass writes
//     its names down. Versions from before that list stamped every name, so the stamp
//     tells those, unless stamps are on now: `r` in the dashboard then offers stamped
//     names too, and one taken there was chosen by hand.
//
// Keeping a name used to take the protected list, which keeps the session from being
// reaped as well.
func (e *Env) replaceable(s inspect.Session) bool {
	name := s.Name()
	if s.StartedAs() == name {
		return rename.Generated(name)
	}
	if e.Given.Has(name) {
		return true
	}
	return rename.Stamped(name) && rename.Stamp(e.Config.AutoRenameStamp) != rename.StampTime
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
		// Written down first: a pass that dies right after the rename leaves a
		// name the next pass knows as given. The other order would leave one
		// that looks chosen by hand, and it would never change again.
		e.remember(attempt)
		err := e.Zellij.Rename(ctx, name, attempt)
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

// forgetGone clears given names no session has any more, at the end of a pass: a
// session's old name, a name zellij refused, a session that is gone.
func (e *Env) forgetGone(listed map[string]bool) {
	if e.Given == nil {
		return
	}
	for _, name := range e.Given.Names() {
		if !listed[name] {
			_ = e.Given.Set(name, false)
		}
	}
}
