package reaper

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
)

// zellij 0.44 and 0.45 refuse a session any name it had before, for as long as
// its server runs. The server remembers those names and nothing on disk does:
// another session may take such a name, and the refusal lasts until the server
// ends. With a client attached, zellij sends the refusal to that client, which
// only writes it to zellij's log, and the command that asked exits 0 with the
// old name kept (zellij.ErrKept, seen after a wait); without one, the command
// that asked hears of it (zellij.ErrNameTaken). zellij 0.43 gives the name
// back, and so does zellij's main branch, its next release.
//
// The book of former names holds the names passes saw each running session
// under, and those zellij refused it. A session coming back to one of them,
// because its title or its project came back, gets a tail at once (-2), with
// no rename asked for in vain and no wait for it. A pass writes the book; plan
// and the dashboard only read it.

// maxFormer bounds the names the book keeps for one session; the oldest go
// first.
const maxFormer = 32

// formerTTL is how long the book keeps a session no survey has seen: its
// server is gone, and the names went with it.
const formerTTL = 24 * time.Hour

// formerEntry is what the book holds for one session.
type formerEntry struct {
	// Names are the names the session was seen under or refused, oldest first.
	Names []string `json:"names"`
	// Seen is the last survey that saw the session, in Unix seconds.
	Seen int64 `json:"seen"`
}

// formerBook holds the former names of each running session, keyed as the
// project book is (projectKey): by what stays through a rename.
type formerBook struct {
	path string
	at   map[string]formerEntry
	// changed is true when this survey changed an entry.
	changed bool
}

// formerFile is the book as it is written.
type formerFile struct {
	Version  int                    `json:"version"`
	Sessions map[string]formerEntry `json:"sessions"`
}

// loadFormer reads the book. A missing or unreadable one is empty: at worst a
// pass asks for a name zellij refuses, and learns it then.
func loadFormer(path string) *formerBook {
	b := &formerBook{path: path, at: map[string]formerEntry{}}
	if path == "" {
		return b
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	var f formerFile
	if json.Unmarshal(raw, &f) != nil || f.Version != 1 {
		return b
	}
	for k, e := range f.Sessions {
		b.at[k] = e
	}
	return b
}

// apply writes down the name each running session has now, when record is on
// (a zellij that refuses former names), and sets on each session the names it
// had (inspect.Session.Former). A session no survey has seen for formerTTL
// leaves the book.
func (b *formerBook) apply(sessions []inspect.Session, now time.Time, record bool) {
	for i := range sessions {
		s := &sessions[i]
		key := projectKey(*s)
		if key == "" || s.Listed.Exited {
			continue
		}
		e, ok := b.at[key]
		if record && !slices.Contains(e.Names, s.Name()) {
			e.Names = appendFormer(e.Names, s.Name())
			b.changed = true
		}
		// Nothing to keep for a session that had no other name.
		if !ok && len(e.Names) == 0 {
			continue
		}
		// An entry whose session only aged is written once an hour, so a
		// session its passes keep seeing does not outlive formerTTL on disk.
		if now.Unix()-e.Seen > int64(time.Hour/time.Second) {
			b.changed = true
		}
		e.Seen = now.Unix()
		b.at[key] = e
		s.Former = slices.Clone(e.Names)
	}
	for k, e := range b.at {
		if now.Sub(time.Unix(e.Seen, 0)) > formerTTL {
			delete(b.at, k)
			b.changed = true
		}
	}
}

// learn writes down a name zellij refused a session because it had it before.
func (b *formerBook) learn(s inspect.Session, name string, now time.Time) {
	if b == nil {
		return
	}
	key := projectKey(s)
	if key == "" {
		return
	}
	e := b.at[key]
	if !slices.Contains(e.Names, name) {
		e.Names = appendFormer(e.Names, name)
	}
	e.Seen = now.Unix()
	b.at[key] = e
	b.changed = true
}

// appendFormer adds a name, dropping the oldest past maxFormer.
func appendFormer(names []string, name string) []string {
	names = append(names, name)
	if len(names) > maxFormer {
		names = slices.Clone(names[len(names)-maxFormer:])
	}
	return names
}

// formerSet is the names a session had, as a set; nil for none.
func formerSet(s inspect.Session) map[string]bool {
	if len(s.Former) == 0 {
		return nil
	}
	set := make(map[string]bool, len(s.Former))
	for _, n := range s.Former {
		set[n] = true
	}
	return set
}

// unusable is the names a session cannot be given: those another session has
// (taken), and those zellij refuses it because it had them before. taken is
// shared by the whole pass, so a session with former names gets a copy.
func unusable(s inspect.Session, taken map[string]bool) map[string]bool {
	if len(s.Former) == 0 {
		return taken
	}
	out := make(map[string]bool, len(taken)+len(s.Former))
	for k, v := range taken {
		out[k] = v
	}
	for _, n := range s.Former {
		out[n] = true
	}
	return out
}

// save writes the book when this survey changed it, through a temporary file
// so a reader never sees half of it.
func (b *formerBook) save() error {
	if b == nil || !b.changed || b.path == "" {
		return nil
	}
	raw, err := json.Marshal(formerFile{Version: 1, Sessions: b.at})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	tmp := b.path + ".new"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, b.path); err != nil {
		return err
	}
	b.changed = false
	return nil
}
