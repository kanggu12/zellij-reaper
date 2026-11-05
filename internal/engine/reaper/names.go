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
	if !mayRename(o) {
		return "", ""
	}
	return e.nameFor(o.Session, taken)
}

// nameFor returns the name a running session's panes point to (desired) and
// the first free variant of it (target). target is "" when the panes say
// nothing, when the session has that name already, or when every variant is
// taken.
func (e *Env) nameFor(s inspect.Session, taken map[string]bool) (desired, target string) {
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
// when zellij refuses a name it still remembers but does not list.
func (e *Env) rename(ctx context.Context, o *Outcome, desired, target string, taken map[string]bool) {
	name := o.Session.Name()
	for attempt := target; ; {
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
