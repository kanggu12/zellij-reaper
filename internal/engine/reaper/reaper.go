// Package reaper runs a pass: list the sessions, inspect them, decide, and
// then delete the ones the policy lets go and rename the survivors, writing
// every decision to the log. Plan does the same without acting.
package reaper

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
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
	// DeleteExited deletes a session that exited, and nothing more: one
	// brought back since the last look is running again, and zellij refuses.
	DeleteExited(ctx context.Context, name string) error
	Rename(ctx context.Context, name, to string) error
}

// Env is everything a pass needs.
type Env struct {
	Config    *config.Config
	Protected *protect.List
	// Given holds the names auto_rename gave. It is the same kind of list as
	// Protected: names in a file, safe to share between processes.
	Given *protect.List
	// Kept holds the names someone kept with r and Enter in the dashboard.
	Kept   *protect.List
	Zellij Zellij
	// Sources are where inspect reads facts; Sources.Clients is filled from
	// Zellij when left nil.
	Sources  inspect.Sources
	Log      *logfile.Log
	LockPath string
	// QuietPath is the book of when a pass first found a session quiet, for
	// sessions whose terminals cannot be read (see quiet.go).
	QuietPath string
	Now       func() time.Time
	// Home and Host are this user's home and this machine's name, which a
	// session's name has no use for repeating.
	Home, Host string
}

// LockWait is how long a pass waits for another one to finish. Everything
// that changes sessions outside a pass (recover) waits the same.
const LockWait = 5 * time.Second

// PassLockWait is how long a full pass waits: a names pass may hold the lock
// when the hourly timer fires, and an hour's reaping must not be skipped for it.
var PassLockWait = 30 * time.Second

// NamesLockWait is how long a names pass waits. It comes back in a few
// minutes, so it gives way to anything else that holds the lock.
var NamesLockWait = time.Second

// Options change one pass.
type Options struct {
	// DryRun forces a dry run on top of the setting.
	DryRun bool
	// Force skips both waits; every other rule still applies.
	Force bool
	// NamesOnly renames and deletes nothing: the names timer's pass, which
	// lets a name follow its session sooner than the hourly pass.
	NamesOnly bool
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
	// quiet is the book this survey read and updated; a pass saves it.
	quiet *quietBook
	// DryRun and Force are the effective mode.
	DryRun, Force bool
	// Thresholds are the waits that applied.
	Thresholds policy.Thresholds
	// NoSessions is true when zellij had nothing to list.
	NoSessions bool
	// Outcomes holds one entry per listed session, in zellij's order.
	Outcomes []Outcome
	// NoRenames says why this pass renames nothing, when something stops it:
	// the list of kept names could not be read.
	NoRenames error
	// IndexErr says why zellij's session index could not be read: sessions
	// zellij 0.46 and later name by id are then not found, and are kept.
	IndexErr error
	// index is the session index the survey judged by.
	index zellij.Index
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

// ReapsAt reports whether a pass at a given time would reap a session, with
// its facts as they are now: one a few minutes short of its wait now is due at
// a pass later on.
func (r Report) ReapsAt(o Outcome, at time.Time) bool {
	f := o.Session.Facts
	// The quiet book had not recorded it: the pass at that time is the first
	// to, and its wait starts there.
	if o.Session.QuietFromNow {
		f.LastActive = at
	}
	return policy.Decide(f, r.Thresholds, at).Action == policy.Reap
}

// DueAt is how many sessions a pass at a given time would reap: what a plan,
// the dashboard and install say the next pass reaps, when the timer says
// when that pass runs.
func (r Report) DueAt(at time.Time) int {
	n := 0
	for _, o := range r.Outcomes {
		if r.ReapsAt(o, at) {
			n++
		}
	}
	return n
}

// DueAtNext is DueAt at the timer's next pass, or Due when that time is
// unknown (zero) or already past.
func (r Report) DueAtNext(next, now time.Time) int {
	if next.After(now) {
		return r.DueAt(next)
	}
	return r.Due()
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
	// zellij on Windows cannot reach a renamed session.
	if !zellij.RenameReachable() {
		r.NoRenames = zellij.ErrRenameUnreachable
	}
	// Without the kept names nobody can tell which names must stay, so nothing
	// is renamed; reaping does not depend on them.
	if e.Kept != nil && r.NoRenames == nil {
		if err := e.Kept.Reload(); err != nil {
			r.NoRenames = fmt.Errorf("could not read the list of kept names: %w", err)
		}
	}
	src := e.Sources
	if src.Clients == nil {
		src.Clients = e.Zellij
	}
	// The index changes as sessions start and are renamed: read it for each
	// survey, unless a test set one.
	if src.Index == nil {
		r.IndexErr = src.LoadIndex()
	}
	r.index = *src.Index
	// On Linux /proc is read while zellij lists the sessions: listing starts a
	// zellij process and waits on every server, about half of a pass on its
	// own. On macOS and Windows a renamed session is matched to its server by
	// the socket file and the servers in the table, so the table comes after
	// the list: a session started while the list was being made then has its
	// server in it, and is not matched to another one.
	type procRead struct {
		table *procfs.Table
		err   error
	}
	procs := make(chan procRead, 1)
	read := func() {
		t, err := inspect.ReadProcesses(src)
		procs <- procRead{t, err}
	}
	if runtime.GOOS == "linux" {
		go read()
	}
	listed, err := e.Zellij.ListSessions(ctx)
	if runtime.GOOS != "linux" {
		read()
	}
	table := <-procs
	if errors.Is(err, zellij.ErrNoSessions) {
		r.NoSessions = true
		return r, nil
	}
	// A list that gave no answer says which server held it up, where the
	// process table shows one stopped.
	if err != nil {
		return r, Stuck(err, table.table, src.UID, r.index)
	}
	if table.err != nil {
		return r, gatherError{table.err}
	}
	sessions, err := inspect.GatherFrom(ctx, src, e.rules(), listed, table.table)
	if err != nil {
		return r, gatherError{err}
	}
	now := e.Now()
	r.quiet = loadQuiet(e.QuietPath)
	r.quiet.apply(sessions, now)
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
		_, o.Suggested = e.nameFor(o.Session, taken, false)
		if e.Config.AutoRename && r.NoRenames == nil {
			_, o.WouldRename = e.renameTarget(*o, taken, false)
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
