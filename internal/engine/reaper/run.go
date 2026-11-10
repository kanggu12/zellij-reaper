package reaper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
)

// PassBudget is how long a pass goes on starting deletes and renames, from
// when it began to wait for the lock; PassLimit is when every call it makes to
// zellij ends. systemd and the Task Scheduler stop a pass after a minute, and
// one stopped from outside writes no end line and keeps none of its book of
// quiet sessions; under cron and launchd nothing stops it at all. A delete
// asks zellij up to four times, each call with a limit of its own: the last
// one started by PassBudget is cut off by PassLimit at the latest, and the
// pass still has time to write its end before the minute is up.
var (
	PassBudget = 40 * time.Second
	PassLimit  = 50 * time.Second
)

// Run is one pass: take the lock, rotate the log, decide, act, log.
func Run(ctx context.Context, e *Env, opts Options) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, PassLimit)
	defer cancel()
	began := time.Now()
	if opts.NamesOnly {
		return namesPass(ctx, e, opts, began)
	}
	l, err := lock.Acquire(e.LockPath, PassLockWait)
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
		// No session left, so no given name belongs to anyone. The kept ones
		// stay: an empty list from a timer that looks in the wrong place,
		// say, is no sign that people stopped wanting them.
		if r.NoSessions && !dry {
			e.forgetGone(nil, false)
		}
		return r, err
	}
	if r.NoRenames != nil && e.Config.AutoRename {
		e.Log.Printf("%v, so no session is renamed", r.NoRenames)
	}
	// No session's name can be told then, so every session is kept: the log
	// says why, once, above the sessions.
	if r.IndexErr != nil {
		e.Log.Printf("%v, so every session is kept", r.IndexErr)
	}
	taken := takenNames(r)
	for i := range r.Outcomes {
		// Each delete can take seconds when zellij is slow to answer. Past
		// the budget the rest waits, said once, and the pass ends as passes
		// do. A dry run changes nothing and takes no such time.
		if !dry && time.Since(began) > PassBudget {
			left := r.Outcomes[i:]
			sessions := fmt.Sprintf("%d sessions", len(left))
			if len(left) == 1 {
				sessions = "1 session"
			}
			e.Log.Printf("time is up after %s: %s left for the next pass", duration.Format(PassBudget), sessions)
			for j := range left {
				left[j].Left = true
			}
			break
		}
		e.act(ctx, &r, &r.Outcomes[i], dry, true, taken)
	}
	// Without the index the list holds ids, not names: every name would
	// read as gone, kept ones too.
	if !dry && r.IndexErr == nil {
		e.forgetGone(taken, true)
	}
	// The book of quiet sessions is bookkeeping, like the log: a dry run
	// keeps it too, or a session would never come due in one. A session this
	// pass deleted leaves it.
	for _, o := range r.Outcomes {
		if o.Deleted {
			r.quiet.forget(o.Session)
		}
	}
	if err := r.quiet.save(); err != nil {
		e.Log.Printf("could not write %s: %v", e.QuietPath, err)
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
func (e *Env) act(ctx context.Context, r *Report, o *Outcome, dry, full bool, taken map[string]bool) {
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
	e.name(ctx, r, o, dry, full, taken)
}

// reap deletes a session the policy let go, unless something changed since.
func (e *Env) reap(ctx context.Context, o *Outcome, dry bool, taken map[string]bool) {
	s := o.Session
	// An exited session is deleted without --force: one someone brought back
	// after the last look is running again, and zellij refuses to delete it
	// where --force would have killed it with its client attached.
	del, force := e.Zellij.DeleteExited, ""
	if !s.Listed.Exited {
		del, force = e.Zellij.Delete, " --force"
	}
	if dry {
		e.Log.Printf("  -> DRY_RUN: would run: zellij delete-session %s%s", s.Name(), force)
		return
	}
	// The facts are a few seconds old by now: ask again.
	if ok, why := e.stillDue(ctx, s); !ok {
		o.KeptAtDelete = why
		e.Log.Printf("  -> kept: %s :: %s", s.Name(), why)
		return
	}
	if err := del(ctx, s.Name()); err != nil {
		// An exited session someone brought back since the second look is
		// refused by zellij, which deletes a running one only with --force:
		// kept, as it should be, and no failure.
		if s.Listed.Exited && RunningAgain(ctx, e.Zellij, s.Name()) {
			o.KeptAtDelete = "it is running again"
			e.Log.Printf("  -> kept: %s :: %s", s.Name(), o.KeptAtDelete)
			return
		}
		o.DeleteErr = err
		e.Log.Printf("  -> FAILED: %s :: %v", s.Name(), err)
		return
	}
	o.Deleted = true
	delete(taken, s.Name())
	e.Log.Printf("  -> deleted: %s", s.Name())
}

// name renames a survivor after what it shows, when auto_rename is on. full is
// a full pass, the only one that drops collision tails (auto_rename_drop_tail):
// a tail zellij refuses to drop is then tried once an hour, not every few minutes.
func (e *Env) name(ctx context.Context, r *Report, o *Outcome, dry, full bool, taken map[string]bool) {
	if !e.Config.AutoRename || r.NoRenames != nil {
		return
	}
	s := o.Session
	desired, target := e.renameTarget(*o, taken, full)
	if target == "" {
		return
	}
	if dry {
		o.WouldRename = target
		reserve(taken, s.Name(), target)
		e.Log.Printf("  -> DRY_RUN: would rename '%s' → '%s'", s.Name(), target)
		return
	}
	// A name protected from another terminal since the list was read keeps
	// its name, as it keeps its session in stillDue: `protect` does not wait
	// for a pass, and a rename would leave the protection on the old name,
	// with the session under the new one protected by nothing.
	if e.Protected != nil {
		why := ""
		if err := e.Protected.Reload(); err != nil {
			why = "could not read the protected list again: " + err.Error()
		} else if e.Protected.Has(s.Name()) {
			why = "protected since the check"
		}
		if why != "" {
			o.KeptName = why
			e.Log.Printf("  -> kept name: %s :: %s", s.Name(), why)
			return
		}
	}
	// The facts predate every delete before this, each of which can take
	// seconds: ask again, so a session someone attached to meanwhile keeps
	// the name they know it by. With auto_rename_attached a client is no
	// reason to keep a name, so there is nothing to ask.
	if !e.Config.AutoRenameAttached {
		if ok, why := e.noClientNow(ctx, s.Name()); !ok {
			o.KeptName = why
			e.Log.Printf("  -> kept name: %s :: %s", s.Name(), why)
			return
		}
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
	// A job started in a pane since the facts were gathered, by `zellij
	// run` or keys sent to it with nobody attached, keeps the session too:
	// --force would end it with the session.
	if s.Server != nil {
		table, err := inspect.ReadProcesses(e.Sources)
		if err != nil {
			return false, "could not read the processes again: " + err.Error()
		}
		for _, p := range inspect.Panes(table, s.Server.PID, e.Config.Shells) {
			if p.Busy != "" {
				return false, "running in a pane since the check: " + p.Busy
			}
		}
	}
	return e.noClientNow(ctx, s.Name())
}

// RunningAgain reports whether zellij lists name as running: an exited session
// brought back since it was looked at. A list that cannot be read says no.
func RunningAgain(ctx context.Context, z Zellij, name string) bool {
	listed, err := z.ListSessions(ctx)
	if err != nil {
		return false
	}
	for _, l := range listed {
		if l.Name == name {
			return !l.Exited
		}
	}
	return false
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

// namesPass renames what a full pass would rename and nothing else, every few
// minutes. It deletes nothing, so it needs neither the waits nor a second look
// before a delete. It writes to the log only when a name changed, so attempts
// that keep failing do not fill the log every few minutes; the hourly pass logs
// those. A dry run does nothing: plan and the hourly dry pass show the names.
func namesPass(ctx context.Context, e *Env, opts Options, began time.Time) (Report, error) {
	r := Report{Started: e.Now()}
	if !e.Config.AutoRename || e.Config.DryRun || opts.DryRun {
		return r, nil
	}
	l, err := lock.Acquire(e.LockPath, NamesLockWait)
	if err != nil {
		return r, err
	}
	defer l.Release()
	// A name protected since the last pass is never renamed.
	if e.Protected != nil {
		if err := e.Protected.Reload(); err != nil {
			return r, fmt.Errorf("read the protected list: %w", err)
		}
	}
	r, err = e.survey(ctx, opts)
	if err != nil || r.NoSessions || r.NoRenames != nil {
		return r, err
	}
	taken := takenNames(r)
	e.Log.Hold()
	for i := range r.Outcomes {
		o := &r.Outcomes[i]
		// A session the next full pass deletes keeps its name until then.
		if o.Verdict.Action == policy.Reap {
			continue
		}
		// The names timer has the same minute as the full pass's.
		if time.Since(began) > PassBudget {
			e.Log.Printf("time is up after %s: the rest are named at the next pass", duration.Format(PassBudget))
			break
		}
		e.name(ctx, &r, o, false, false, taken)
	}
	held := e.Log.Take()
	_, _, renamed := r.Counts()
	if renamed == 0 {
		return r, nil
	}
	if r.IndexErr == nil {
		e.forgetGone(taken, true)
	}
	e.Log.Rotate()
	e.Log.Printf("=== names start ===")
	e.Log.Write(held)
	e.Log.Printf("=== names end (%d renamed) ===", renamed)
	return r, nil
}
