package reaper

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// LeftoverSettles is how long after a rename zellij can still write the
// session's files under the name it had. zellij moves the files to the new
// name, then tells the job that writes them every second; a write that falls
// in between makes the old name's folder again (zellij's own source says so:
// "make this atomic", in screen.rs). zellij then lists a session by the old
// name as exited, a copy that attach would bring back as a second session.
// In the integration runs it happened about once in some hundreds of renames.
var LeftoverSettles = 1500 * time.Millisecond

// DropLeftovers deletes the copies zellij may leave of renamed sessions under
// the names they had, given as old name → new name, and logs each one. It waits
// LeftoverSettles first, then deletes every old name zellij lists as exited, the
// way an exited session is deleted: never forced, so zellij refuses one that
// runs. Until the rename the name was the renamed session's own, which ran, so
// no exited session had it: an exited one by it now is that copy.
func (e *Env) DropLeftovers(ctx context.Context, renamed map[string]string) {
	if len(renamed) == 0 {
		return
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(LeftoverSettles):
	}
	listed, err := e.Zellij.ListSessions(ctx)
	if err != nil {
		// Nothing listed is nothing left. A copy left for want of a list is
		// an exited session, which waits like any other.
		if !errors.Is(err, zellij.ErrNoSessions) {
			e.Log.Printf("could not look for copies zellij left under old names: %v", err)
		}
		return
	}
	for _, old := range slices.Sorted(maps.Keys(renamed)) {
		if !slices.ContainsFunc(listed, func(l zellij.Listed) bool { return l.Name == old && l.Exited }) {
			continue
		}
		e.Log.Printf("%s [EXITED] :: copy zellij left when it renamed the session to %s", old, renamed[old])
		// Not "  -> FAILED": a copy left is no session the pass failed to
		// delete, and waits like any exited session.
		if err := e.Zellij.DeleteExited(ctx, old); err != nil {
			e.Log.Printf("  -> could not delete the copy: %s :: %v", old, err)
			continue
		}
		e.Log.Printf("  -> deleted: %s", old)
	}
}

// renames is what a pass renamed, as old name → new name.
func (r Report) renames() map[string]string {
	out := map[string]string{}
	for _, o := range r.Outcomes {
		if o.RenamedTo != "" {
			out[o.Session.Name()] = o.RenamedTo
		}
	}
	return out
}
