package reaper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
)

// Run is one pass: take the lock, rotate the log, decide, act, log.
func Run(ctx context.Context, e *Env, opts Options) (Report, error) {
	l, err := lock.Acquire(e.LockPath, LockWait)
	if err != nil {
		return Report{}, err
	}
	defer l.Release()
	// Rotate before the first line so the whole pass lands in one file.
	e.Log.Rotate()
	th := e.thresholds(opts.Force)
	dry := e.Config.DryRun || opts.DryRun
	e.Log.Printf("=== reaper start (dry_run=%t idle_after=%s exited_after=%s%s) ===",
		dry, duration.Setting(th.Idle), duration.Setting(th.Exited), map[bool]string{true: " force"}[opts.Force])
	// A protection added while this pass waited for the lock counts. The
	// list is refreshed in place: the dashboard holds the same List and
	// must not keep reading the copy it had before this pass.
	if e.Protected != nil {
		if err := e.Protected.Reload(); err != nil {
			// Without the list nobody can tell which names are protected,
			// so this pass touches nothing.
			e.Log.Printf("could not read the protected list: %v", err)
			e.Log.Printf("=== reaper end ===")
			return Report{Started: e.Now(), DryRun: dry, Force: opts.Force, Thresholds: th}, fmt.Errorf("read the protected list: %w", err)
		}
	}

	r, err := e.survey(ctx, opts)
	if e.stopsHere(r, err) {
		// No session left, so no given name belongs to anyone.
		if r.NoSessions && !dry {
			e.forgetGone(nil)
		}
		return r, err
	}
	taken := takenNames(r)
	for i := range r.Outcomes {
		e.act(ctx, &r.Outcomes[i], dry, taken)
	}
	if !dry {
		e.forgetGone(taken)
	}
	reaped, kept, renamed := r.Counts()
	e.Log.Printf("=== reaper end (%d reaped, %d kept, %d renamed) ===", reaped, kept, renamed)
	return r, nil
}

// stopsHere logs why a pass has nothing to act on, if it has not.
func (e *Env) stopsHere(r Report, err error) bool {
	switch {
	case errors.Is(err, ErrNoZellij):
		e.Log.Printf("zellij not installed, nothing to do")
	case errors.As(err, new(gatherError)):
		e.Log.Printf("%v", err)
	case err != nil:
		e.Log.Printf("zellij list-sessions failed: %v", err)
	case r.NoSessions:
		e.Log.Printf("no sessions")
	default:
		return false
	}
	e.Log.Printf("=== reaper end ===")
	return true
}

// act logs a session's verdict and does what the verdict calls for.
func (e *Env) act(ctx context.Context, o *Outcome, dry bool, taken map[string]bool) {
	s := o.Session
	state := "RUNNING"
	if s.Listed.Exited {
		state = "EXITED"
	}
	e.Log.Printf("%s [%s] panes={%s} :: %s", s.Name(), state, titleSummary(s.Titles()), o.Verdict.LogText())
	if o.Verdict.Action == policy.Reap {
		e.reap(ctx, o, dry, taken)
		return
	}
	// A survivor: give it a name that says what it is doing.
	e.name(ctx, o, dry, taken)
}

// reap deletes a session the policy let go, unless something changed since.
func (e *Env) reap(ctx context.Context, o *Outcome, dry bool, taken map[string]bool) {
	s := o.Session
	if dry {
		e.Log.Printf("  -> DRY_RUN: would run: zellij delete-session %s --force", s.Name())
		return
	}
	// The facts are a few seconds old by now: ask again.
	if ok, why := e.stillDue(ctx, s); !ok {
		o.KeptAtDelete = why
		e.Log.Printf("  -> kept: %s :: %s", s.Name(), why)
		return
	}
	if err := e.Zellij.Delete(ctx, s.Name()); err != nil {
		o.DeleteErr = err
		e.Log.Printf("  -> FAILED: %s :: %v", s.Name(), err)
		return
	}
	o.Deleted = true
	delete(taken, s.Name())
	e.Log.Printf("  -> deleted: %s", s.Name())
}

// name renames a survivor after what it shows, when auto_rename is on.
func (e *Env) name(ctx context.Context, o *Outcome, dry bool, taken map[string]bool) {
	if !e.Config.AutoRename {
		return
	}
	s := o.Session
	desired, target := e.renameTarget(*o, taken)
	if target == "" {
		return
	}
	if dry {
		o.WouldRename = target
		reserve(taken, s.Name(), target)
		e.Log.Printf("  -> DRY_RUN: would rename '%s' → '%s'", s.Name(), target)
		return
	}
	// The facts predate every delete before this, each of which can take
	// seconds: ask again, so a session someone attached to meanwhile keeps
	// the name they know it by.
	if ok, why := e.noClientNow(ctx, s.Name()); !ok {
		o.KeptName = why
		e.Log.Printf("  -> kept name: %s :: %s", s.Name(), why)
		return
	}
	e.rename(ctx, o, desired, target, taken)
}

// stillDue checks, right before a delete, that nothing changed since the
// facts were gathered: a running session still has no client, an exited one
// is still listed as exited. Any doubt keeps the session.
func (e *Env) stillDue(ctx context.Context, s inspect.Session) (bool, string) {
	// A name protected from another terminal since the list was read keeps
	// the session: `protect` does not wait for a pass to finish.
	if e.Protected != nil {
		if err := e.Protected.Reload(); err != nil {
			return false, "could not read the protected list again: " + err.Error()
		}
		if e.Protected.Has(s.Name()) {
			return false, "protected since the check"
		}
	}
	if s.Listed.Exited {
		listed, err := e.Zellij.ListSessions(ctx)
		if err != nil {
			return false, "could not list the sessions again: " + err.Error()
		}
		for _, l := range listed {
			if l.Name == s.Name() {
				if l.Exited {
					return true, ""
				}
				return false, "it is running again"
			}
		}
		return false, "it is no longer listed"
	}
	return e.noClientNow(ctx, s.Name())
}

// noClientNow asks a running session for its clients once more, right before
// it is deleted or renamed: someone may have attached since the facts were
// gathered. An answer that cannot be read counts as a client.
func (e *Env) noClientNow(ctx context.Context, name string) (bool, string) {
	rows, err := e.Zellij.ListClients(ctx, name)
	if err != nil {
		return false, "could not ask for its clients again: " + err.Error()
	}
	if len(rows) > 0 {
		return false, fmt.Sprintf("%d client(s) attached since the check", len(rows))
	}
	return true, ""
}

// titleSummary joins pane titles for the log, each cut to 40 characters.
func titleSummary(titles []string) string {
	parts := make([]string, len(titles))
	for i, t := range titles {
		if r := []rune(t); len(r) > 40 {
			t = string(r[:40])
		}
		parts[i] = t
	}
	return strings.Join(parts, "|")
}
