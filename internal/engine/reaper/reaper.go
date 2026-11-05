// Package reaper runs a pass: list the sessions, inspect them, decide, and
// then delete the ones the policy lets go and rename the survivors, writing
// every decision to the log. Plan does the same without acting.
package reaper

import (
	"context"
	"errors"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
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
	// Given holds the names auto_rename gave. It is the same kind of list as
	// Protected: names in a file, safe to share between processes.
	Given  *protect.List
	Zellij Zellij
	// Sources are where inspect reads facts; Sources.Clients is filled from
	// Zellij when left nil.
	Sources  inspect.Sources
	Log      *logfile.Log
	LockPath string
	Now      func() time.Time
	// Home and Host are this user's home and this machine's name, which a
	// session's name has no use for repeating.
	Home, Host string
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
	// Suggested is the name this session's panes point to, set by Plan for
	// every running session whatever auto_rename says: what the dashboard
	// offers when someone renames it by hand. "" when it already has it.
	Suggested string
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
	// A pass in another process may have given names since this list was read.
	// A list that cannot be read keeps what it held: at worst a name stays.
	if e.Given != nil {
		_ = e.Given.Reload()
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
	// Show the names a pass would give, in the order it would give them, so
	// two sessions showing the same title come out as a pass would name them.
	taken := takenNames(r)
	for i := range r.Outcomes {
		o := &r.Outcomes[i]
		if o.Session.Listed.Exited {
			continue
		}
		_, o.Suggested = e.nameFor(o.Session, taken)
		if e.Config.AutoRename {
			_, o.WouldRename = e.renameTarget(*o, taken)
			reserve(taken, o.Session.Name(), o.WouldRename)
		}
	}
	return r, nil
}

// reserve moves a session to the name it will have, for the sessions after it.
func reserve(taken map[string]bool, name, target string) {
	if target != "" {
		delete(taken, name)
		taken[target] = true
	}
}
