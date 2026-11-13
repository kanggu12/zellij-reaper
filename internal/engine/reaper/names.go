package reaper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/core/rename"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// mayRename says whether auto_rename may rename a survivor: its server was
// found, and either nobody is attached and its verdict allows a rename, or
// attachedToo (auto_rename_attached) lets a session someone works in go too. A
// session kept for a protected program is decided before its clients are looked
// at, so the verdict alone does not say whether someone is using it.
func mayRename(o Outcome, attachedToo bool) bool {
	s := o.Session
	if s.Listed.Exited || s.Server == nil {
		return false
	}
	switch s.Facts.Attached {
	case policy.No:
		return o.Verdict.MayRename()
	case policy.Yes:
		// Kept for its client, or for a reason that allows a rename anyway. A
		// server that did not answer list-clients would not answer a rename
		// either: it may be starting or stopping.
		return attachedToo && !s.ServerSilent && (o.Verdict.Code == policy.Attached || o.Verdict.MayRename())
	}
	// Nobody could tell whether a client is attached: the server may be
	// starting or stopping, so the session keeps its name.
	return false
}

// takenNames is the set of names a session has: the ones zellij lists, and the
// socket files in the sessions' socket directories. zellij once left a session it
// had just renamed out of its list for a moment, and its socket file stayed put, so
// a rename steers clear of it and its entries on the name lists stay.
func takenNames(r Report) map[string]bool {
	taken := map[string]bool{}
	dirs := map[string]bool{}
	for _, o := range r.Outcomes {
		taken[o.Session.Name()] = true
		if o.Session.Socket != "" {
			dirs[filepath.Dir(o.Session.Socket)] = true
		}
	}
	for dir := range dirs {
		for _, file := range zellij.Sockets(dir) {
			// zellij's own sockets are no session's; since 0.46 the others
			// are named by id, which the index turns into a name.
			if !zellij.IsAppName(file) {
				taken[r.index.Name(file)] = true
			}
		}
	}
	return taken
}

// renameTarget returns the name auto_rename wants for a survivor (desired)
// and the first free variant of it (target); target is "" for no rename.
func (e *Env) renameTarget(o Outcome, taken map[string]bool, dropTail bool) (desired, target string) {
	if !mayRename(o, e.Config.AutoRenameAttached) || !e.replaceable(o.Session) {
		return "", ""
	}
	return e.nameFor(o.Session, taken, dropTail)
}

// replaceable reports whether auto_rename may replace a session's name. A name
// someone chose stays, and the server's command line, which keeps the name a session
// started under through every rename, is what tells them apart:
//
//   - A session that still has that name was never renamed, so no pass gave it. Only
//     two words from zellij's lists can go, since that is what zellij makes up.
//     zellij keeps nothing that tells such a name from one someone typed (`zellij -s
//     quiet-mouse`): its command line, environment and files are the same. So a
//     person says so, with r and Enter in the dashboard, and the name is kept. A
//     session zellij brought back from exited is the exception: it starts under the
//     name it had, and a name a pass gave it goes as before.
//   - A session with another name was renamed, by a pass or by someone. A pass writes
//     its names down, and only those go. Versions from before that list stamped every
//     name, so the stamp tells those, unless stamps are on now: `r` in the dashboard
//     then offers stamped names too, and one taken there was chosen by hand.
//
// The decision log is no evidence: its lines name sessions, not which session, and
// zellij gives a name out again once a session is gone. 0.3.2 read it, and replaced
// names people chose.
func (e *Env) replaceable(s inspect.Session) bool {
	name := s.Name()
	if e.Kept.Has(name) {
		return false
	}
	if s.StartedAs() == name {
		// Unless zellij brought it back: a session it resurrects starts again
		// under the name it had, and a name a pass gave it is the pass's
		// still. A session made anew under such a name is someone's choice,
		// and forgetChosen takes the name off the list.
		if s.Resurrected && e.Given.Has(name) {
			return true
		}
		return rename.Generated(name)
	}
	if e.Given.Has(name) {
		return true
	}
	// zellij 0.46 and later name a session by id. No version that stamped
	// names ever saw such a session, so a stamp on one was put there by hand.
	if s.ByID {
		return false
	}
	return rename.Stamped(name) && rename.Stamp(e.Config.AutoRenameStamp) != rename.StampTime
}

// nameFor returns the name a running session's panes point to (desired) and
// the first free variant of it (target). target is "" when the panes say
// nothing, when the session has that name already, or when every variant is
// taken.
func (e *Env) nameFor(s inspect.Session, taken map[string]bool, dropTail bool) (desired, target string) {
	// The client count is near the end of zellij's metadata file: without it
	// the file is being rewritten, and titles that are not there yet must not
	// read as a session with nothing to show.
	if !s.Files.Metadata.HasClientCount {
		return "", ""
	}
	budget := rename.MaxBase
	// A socket named after the session bounds the name's length; one named
	// by id does not.
	if s.Socket != "" && !s.ByID {
		budget = rename.BaseBudgetFor(zellij.NameRoom(filepath.Dir(s.Socket)), rename.Stamp(e.Config.AutoRenameStamp))
	}
	base := rename.Suggest(rename.Input{
		Titles:  s.Files.Metadata.TitlesByFocus(),
		Busy:    s.Facts.BusyWith,
		Cwd:     s.WorkDir(),
		Home:    e.Home,
		Host:    e.Host,
		Project: s.Project,
	}, budget)
	if base == "" {
		return "", ""
	}
	when := s.Facts.LastActive
	if when.IsZero() {
		when = e.Now()
	}
	desired = rename.Desired(base, rename.Stamp(e.Config.AutoRenameStamp), when)
	// The names another session has, and those zellij refuses this one
	// because it had them before.
	taken = unusable(s, taken)
	// Named this way already, perhaps with a tail from a collision: nothing
	// to do. The tail stays even once the other session is gone, because a
	// name changes only when what the session shows changes, or the project
	// its agent works in.
	if rename.Same(s.Name(), desired) {
		// auto_rename_drop_tail: the plain name is free again, so the tail
		// can go. Only names a pass may replace get here.
		if dropTail && e.Config.AutoRenameDropTail && s.Name() != desired && !taken[desired] {
			return desired, desired
		}
		return desired, ""
	}
	free, ok := rename.Free(desired, taken)
	if !ok {
		return desired, ""
	}
	return desired, free
}

// maxKept bounds how many names one rename asks for that zellij keeps the
// old name instead of: each costs a wait, and a server that keeps its name
// again and again may not be acting on renames at all. The book of former
// names keeps what each refusal taught, so the next pass goes on from there.
const maxKept = 3

// rename renames a session to target, stepping through desired-2, -3, ...
// when zellij refuses a name it still remembers but does not list. The end of the
// pass clears from the list of given names what did not stick: the session's old
// name, and any name zellij refused. book learns the names zellij refused the
// session because it had them before.
func (e *Env) rename(ctx context.Context, book *formerBook, o *Outcome, desired, target string, taken map[string]bool) {
	name := o.Session.Name()
	// The names this session had: zellij refuses them, so no variant asks.
	former := formerSet(o.Session)
	if former == nil {
		former = map[string]bool{}
	}
	// kept counts the names zellij kept the old name instead of (zellij.ErrKept).
	kept := 0
	for attempt := target; ; {
		err := e.renameTo(ctx, o.Session, attempt)
		if err == nil {
			// The wall clock, as the files of a copy zellij may leave are
			// stamped (see DropLeftovers).
			o.RenamedTo, o.RenamedAt = attempt, time.Now()
			delete(taken, name)
			taken[attempt] = true
			e.Log.Printf("  -> renamed: %s → %s", name, attempt)
			return
		}
		refused := errors.Is(err, zellij.ErrNameTaken)
		// zellij kept the old name: what 0.44 and 0.45 do, telling only the
		// client attached, with a name the session had before and the book
		// did not know of (one the session had before the book, or one a lost
		// book held). The book learns it, so no pass asks for it again. A slow
		// server may yet act on it, so the name stays out of reach of the
		// sessions after this one, as an unconfirmed rename's does.
		if errors.Is(err, zellij.ErrKept) {
			book.learn(o.Session, attempt, e.Now())
			former[attempt] = true
			taken[attempt] = true
			kept++
			refused = kept <= maxKept
		}
		// The same refusal with no client attached: zellij says the name
		// exists, which it did not list. Learned too, and costs no wait.
		if errors.Is(err, zellij.ErrHad) {
			book.learn(o.Session, attempt, e.Now())
			former[attempt] = true
		}
		// Dropping a tail zellij refuses, because the session had the plain
		// name before: it keeps the name it has, and nothing is logged.
		if refused && rename.Same(name, desired) {
			return
		}
		// Anything but a taken name is a real failure: stop and say so.
		if !refused {
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
		// Skip variants another listed session already has, and those this
		// session had before.
		for ok && (taken[next] || former[next]) {
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

// forgetChosen takes a session made anew under a given name off the list of
// given names: the session a pass gave it to ended, or got another name by
// hand, and someone started this one under that name, which is theirs. Left on
// the list, the name would read as a pass's once zellij brought this session
// back (replaceable). Until a pass sees the session, it has no way to know.
func (e *Env) forgetChosen(outcomes []Outcome) {
	if e.Given == nil {
		return
	}
	for _, o := range outcomes {
		s := o.Session
		// Only a running session whose saved panes say it was made anew: one
		// that says nothing yet keeps the list as it is.
		if s.Listed.Exited || !s.MadeAnew || s.StartedAs() != s.Name() || !e.Given.Has(s.Name()) {
			continue
		}
		_ = e.Given.Set(s.Name(), false)
	}
}

// forgetGone clears given names no session has any more, at the end of a pass:
// a session's old name, a name zellij refused, a session that is gone. Kept
// names, which people chose, go too only with keptToo: after a pass that saw
// sessions and could tell all their names. A list that came back empty, or
// one zellij gave by ids it could not name, says nothing about which names are
// gone, and a person's choice lost to it is a session renamed after all.
func (e *Env) forgetGone(listed map[string]bool, keptToo bool) {
	lists := []*protect.List{e.Given}
	if keptToo {
		lists = append(lists, e.Kept)
	}
	for _, l := range lists {
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
