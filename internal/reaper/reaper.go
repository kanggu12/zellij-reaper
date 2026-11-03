// Package reaper runs a pass: list the sessions, inspect them, decide, and
// then delete the ones the policy lets go and rename the survivors, writing
// every decision to the log. Plan does the same without acting.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/config"
	"github.com/kanggu12/zellij-reaper/internal/duration"
	"github.com/kanggu12/zellij-reaper/internal/inspect"
	"github.com/kanggu12/zellij-reaper/internal/lock"
	"github.com/kanggu12/zellij-reaper/internal/logfile"
	"github.com/kanggu12/zellij-reaper/internal/policy"
	"github.com/kanggu12/zellij-reaper/internal/protect"
	"github.com/kanggu12/zellij-reaper/internal/rename"
	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// Zellij is the part of zellij a pass uses. *zellij.CLI implements it; tests
// use a fake that records what it was asked to do.
type Zellij interface {
	Available() bool
	ListSessions(ctx context.Context) ([]zellij.Listed, error)
	ListClients(ctx context.Context, name string) ([]zellij.ClientRow, error)
	Delete(ctx context.Context, name string) error
	Rename(ctx context.Context, name, to string) error
}

// Env is everything a pass needs.
type Env struct {
	Config    *config.Config
	Protected *protect.List
	Zellij    Zellij
	// Sources are where inspect reads facts; Sources.Clients is filled from
	// Zellij when left nil.
	Sources  inspect.Sources
	Log      *logfile.Log
	LockPath string
	Now      func() time.Time
}

// LockWait is how long a pass waits for another one to finish. Everything
// that changes sessions outside a pass (recover) waits the same.
const LockWait = 5 * time.Second

// Options change one pass.
type Options struct {
	// DryRun forces a dry run on top of the setting.
	DryRun bool
	// Force skips both waits; every other rule still applies.
	Force bool
}

// Outcome is what happened to one session.
type Outcome struct {
	Session inspect.Session
	Verdict policy.Verdict
	// Deleted is true when the session was deleted; DeleteErr says why not.
	Deleted   bool
	DeleteErr error
	// KeptAtDelete says why a session the policy let go was kept after all:
	// something changed between the check and the delete.
	KeptAtDelete string
	// RenamedTo is the new name after a rename; WouldRename is the name a
	// dry run would have given; RenameErr is a failed rename.
	RenamedTo   string
	WouldRename string
	RenameErr   error
	// KeptName says why a session auto_rename would have renamed kept its
	// name after all: a client attached between the check and the rename.
	KeptName string
}

// Report is the result of a pass or a plan.
type Report struct {
	// Started is when it began.
	Started time.Time
	// DryRun and Force are the effective mode.
	DryRun, Force bool
	// Thresholds are the waits that applied.
	Thresholds policy.Thresholds
	// NoSessions is true when zellij had nothing to list.
	NoSessions bool
	// Outcomes holds one entry per listed session, in zellij's order.
	Outcomes []Outcome
}

// Counts sums a report: reaped, kept and renamed sessions.
func (r Report) Counts() (reaped, kept, renamed int) {
	for _, o := range r.Outcomes {
		switch {
		case o.Deleted || (r.DryRun && o.Verdict.Action == policy.Reap):
			reaped++
		default:
			kept++
		}
		if o.RenamedTo != "" || o.WouldRename != "" {
			renamed++
		}
	}
	return
}

// Due is how many sessions the policy lets go: what a plan says the next
// pass reaps, or what a dry run would have reaped.
func (r Report) Due() int {
	n := 0
	for _, o := range r.Outcomes {
		if o.Verdict.Action == policy.Reap {
			n++
		}
	}
	return n
}

// gatherError marks a failure to read the facts (as opposed to zellij failing
// to list its sessions), so the log says which.
type gatherError struct{ err error }

func (g gatherError) Error() string { return "could not inspect the sessions: " + g.err.Error() }
func (g gatherError) Unwrap() error { return g.err }

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

// ErrNoZellij is returned when the zellij binary cannot be found.
var ErrNoZellij = errors.New("zellij is not installed or not in PATH")

// thresholds returns the waits for this pass.
func (e *Env) thresholds(force bool) policy.Thresholds {
	if force {
		return policy.Thresholds{}
	}
	return policy.Thresholds{Idle: e.Config.IdleAfter, Exited: e.Config.ExitedAfter}
}

// rules returns the inspect rules from the settings and the protected list.
func (e *Env) rules() inspect.Rules {
	return inspect.Rules{
		Protected: func(name string) (bool, string) {
			if e.Config.ProtectMatches(name) {
				return true, "protect pattern"
			}
			if e.Protected.Has(name) {
				return true, "protected list"
			}
			return false, ""
		},
		ProtectCommands: e.Config.ProtectCommands,
		Shells:          e.Config.Shells,
	}
}

// survey lists, inspects and decides, without acting.
func (e *Env) survey(ctx context.Context, opts Options) (Report, error) {
	r := Report{Started: e.Now(), DryRun: e.Config.DryRun || opts.DryRun, Force: opts.Force}
	r.Thresholds = e.thresholds(opts.Force)
	if !e.Zellij.Available() {
		return r, ErrNoZellij
	}
	listed, err := e.Zellij.ListSessions(ctx)
	if errors.Is(err, zellij.ErrNoSessions) {
		r.NoSessions = true
		return r, nil
	}
	if err != nil {
		return r, err
	}
	src := e.Sources
	if src.Clients == nil {
		src.Clients = e.Zellij
	}
	sessions, err := inspect.Gather(ctx, src, e.rules(), listed)
	if err != nil {
		return r, gatherError{err}
	}
	now := e.Now()
	for _, s := range sessions {
		r.Outcomes = append(r.Outcomes, Outcome{Session: s, Verdict: policy.Decide(s.Facts, r.Thresholds, now)})
	}
	return r, nil
}

// Plan reports what a pass would do now, touching nothing: no lock, no log,
// no deletes, no renames.
func Plan(ctx context.Context, e *Env, opts Options) (Report, error) {
	r, err := e.survey(ctx, opts)
	if err != nil {
		return r, err
	}
	// Show the names a pass would give, as a dry run would log them.
	if e.Config.AutoRename {
		taken := takenNames(r)
		for i := range r.Outcomes {
			_, r.Outcomes[i].WouldRename = e.renameTarget(r.Outcomes[i], taken)
		}
	}
	return r, nil
}

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
	switch {
	case errors.Is(err, ErrNoZellij):
		e.Log.Printf("zellij not installed, nothing to do")
		e.Log.Printf("=== reaper end ===")
		return r, err
	case errors.As(err, new(gatherError)):
		e.Log.Printf("%v", err)
		e.Log.Printf("=== reaper end ===")
		return r, err
	case err != nil:
		e.Log.Printf("zellij list-sessions failed: %v", err)
		e.Log.Printf("=== reaper end ===")
		return r, err
	case r.NoSessions:
		e.Log.Printf("no sessions")
		e.Log.Printf("=== reaper end ===")
		return r, nil
	}

	taken := takenNames(r)
	for i := range r.Outcomes {
		o := &r.Outcomes[i]
		s := o.Session
		state := "RUNNING"
		if s.Listed.Exited {
			state = "EXITED"
		}
		e.Log.Printf("%s [%s] panes={%s} :: %s", s.Name(), state, titleSummary(s.Titles()), o.Verdict.LogText())

		if o.Verdict.Action == policy.Reap {
			if dry {
				e.Log.Printf("  -> DRY_RUN: would run: zellij delete-session %s --force", s.Name())
				continue
			}
			// The facts are a few seconds old by now: ask again.
			if ok, why := e.stillDue(ctx, s); !ok {
				o.KeptAtDelete = why
				e.Log.Printf("  -> kept: %s :: %s", s.Name(), why)
				continue
			}
			if err := e.Zellij.Delete(ctx, s.Name()); err != nil {
				o.DeleteErr = err
				e.Log.Printf("  -> FAILED: %s :: %v", s.Name(), err)
				continue
			}
			o.Deleted = true
			delete(taken, s.Name())
			e.Log.Printf("  -> deleted: %s", s.Name())
			continue
		}
		// A survivor: give it a name that says what it is doing.
		if !e.Config.AutoRename || !mayRename(*o) {
			continue
		}
		desired, target := e.renameTarget(*o, taken)
		if target == "" {
			continue
		}
		if dry {
			o.WouldRename = target
			e.Log.Printf("  -> DRY_RUN: would rename '%s' → '%s'", s.Name(), target)
			continue
		}
		// The facts predate every delete above, each of which can take
		// seconds: ask again, so a session someone attached to meanwhile
		// keeps the name they know it by.
		if ok, why := e.noClientNow(ctx, s.Name()); !ok {
			o.KeptName = why
			e.Log.Printf("  -> kept name: %s :: %s", s.Name(), why)
			continue
		}
		e.rename(ctx, o, desired, target, taken)
	}
	reaped, kept, renamed := r.Counts()
	e.Log.Printf("=== reaper end (%d reaped, %d kept, %d renamed) ===", reaped, kept, renamed)
	return r, nil
}

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
	s := o.Session
	if !mayRename(o) {
		return "", ""
	}
	budget := rename.MaxBase
	if s.Socket != "" {
		budget = rename.BaseBudget(filepath.Dir(s.Socket))
	}
	titles := s.Titles()
	first := ""
	if len(titles) > 0 {
		first = titles[0]
	}
	base := rename.Suggest(first, s.Files.Layout.Cwd, budget)
	if base == "" {
		return "", ""
	}
	when := s.Facts.LastActive
	if when.IsZero() {
		when = e.Now()
	}
	desired = rename.Desired(base, when)
	// Already named this way: nothing to do this pass.
	if desired == s.Name() {
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
