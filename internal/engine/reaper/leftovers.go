package reaper

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// LeftoverSettles is how long after a rename zellij can still write the
// session's files under the name it had. zellij moves the files to the new
// name, then tells the job that writes them every second; a write that falls
// in between makes the old name's folder again (zellij's own source says so:
// "make this atomic", in screen.rs). zellij then lists a session by the old
// name as exited, a copy that attach would bring back as a second session.
// In the integration runs it happened about once in some hundreds of renames.
// A copy written later than this is kept, and waits like any exited session.
var LeftoverSettles = 1500 * time.Millisecond

// Renamed is a rename made by a pass or by hand: the new name, and when
// zellij listed it.
type Renamed struct {
	To string
	At time.Time
}

// SettleLeftovers waits LeftoverSettles, until a copy zellij may leave after a
// rename would be there. It is false when ctx ends first.
func SettleLeftovers(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(LeftoverSettles):
		return true
	}
}

// DropLeftovers deletes the copies zellij may leave of renamed sessions under
// the names they had, given by old name. The caller has waited SettleLeftovers
// and holds the pass lock. Until the rename the name was the renamed session's
// own, which ran, so no exited session had it: one zellij lists as exited now
// is that copy, if its files were written no later than LeftoverSettles after
// the rename. Written later, someone brought it back and used it since, and it
// stays. It goes with `zellij delete-session`, never --force, unless its name
// is protected now, as a protected name keeps its session, or the deadline has
// passed, when there is one; a copy that runs again is refused by zellij, and
// stays. Each copy is logged, and so is a list that could not be read.
func (e *Env) DropLeftovers(ctx context.Context, renamed map[string]Renamed, deadline time.Time) {
	if len(renamed) == 0 {
		return
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
		e.Log.Printf("%s [EXITED] :: copy zellij left when it renamed the session to %s", old, renamed[old].To)
		if why := e.keepsCopy(old, renamed[old], deadline); why != "" {
			e.Log.Printf("  -> kept: %s :: %s", old, why)
			continue
		}
		if err := e.Zellij.DeleteExited(ctx, old); err != nil {
			if RunningAgain(ctx, e.Zellij, old) {
				e.Log.Printf("  -> kept: %s :: it is running again", old)
				continue
			}
			// Not "  -> FAILED": a copy left is no session the pass failed to
			// delete, and waits like any exited session.
			e.Log.Printf("  -> could not delete the copy: %s :: %v", old, err)
			continue
		}
		// Not "  -> deleted:", which the dashboard lists as a session reaped.
		e.Log.Printf("  -> deleted the copy: %s", old)
	}
}

// keepsCopy says why a copy under an old name stays, or "" when it goes: a
// name the protect pattern in the settings read matches, or the protected
// list holds when read again now, as before any delete; files that cannot be
// read, or that were written after zellij's moment (LeftoverSettles); or a
// deadline past. The files are read by name: zellij 0.43 to 0.45 name their
// folders so, and 0.46, which names them by id, moves none on a rename and
// leaves no copy. Their times are the file system's, set as zellij writes
// them, against this machine's clock: one set back, or a network file
// system's that is off by more than LeftoverSettles, can make a copy used
// since look untouched, or the other way round.
func (e *Env) keepsCopy(name string, rn Renamed, deadline time.Time) string {
	if e.Config.ProtectMatches(name) {
		return "protected by protect pattern"
	}
	if e.Protected != nil {
		if err := e.Protected.Reload(); err != nil {
			return "could not read the protected list again: " + err.Error()
		}
		if e.Protected.Has(name) {
			return "protected by protected list"
		}
	}
	// zellij names its folders after its sessions, one path element each;
	// a name that would leave the folder is no copy of one.
	dir := filepath.Join(e.Sources.InfoDir, name)
	if e.Sources.InfoDir == "" || filepath.Dir(dir) != filepath.Clean(e.Sources.InfoDir) {
		return "its files could not be found"
	}
	files, err := zellij.ReadFiles(e.Sources.InfoDir, name)
	if err != nil || !files.Exists {
		return "its files could not be read"
	}
	if files.Newest.After(rn.At.Add(LeftoverSettles)) {
		return "written since the rename, so someone may have used it"
	}
	if !deadline.IsZero() && time.Now().After(deadline) {
		return fmt.Sprintf("time is up after %s", duration.Format(PassBudget))
	}
	return ""
}

// dropLeftovers is DropLeftovers for a pass, which holds the lock already. A
// pass starts no delete past PassBudget: when the wait would take it past
// that, it does not look.
func (e *Env) dropLeftovers(ctx context.Context, r Report, began time.Time) {
	renamed := r.renames()
	if len(renamed) == 0 {
		return
	}
	deadline := began.Add(PassBudget)
	if time.Now().Add(LeftoverSettles).After(deadline) {
		e.Log.Printf("time is up after %s: no look for copies zellij may have left under old names", duration.Format(PassBudget))
		return
	}
	if SettleLeftovers(ctx) {
		e.DropLeftovers(ctx, renamed, deadline)
	}
}

// renames is what a pass renamed, by old name.
func (r Report) renames() map[string]Renamed {
	out := map[string]Renamed{}
	for _, o := range r.Outcomes {
		if o.RenamedTo != "" {
			out[o.Session.Name()] = Renamed{To: o.RenamedTo, At: o.RenamedAt}
		}
	}
	return out
}
