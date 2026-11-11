package reaper

import (
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
)

// A coding agent's session is named after the repository the agent works in
// (inspect.AgentWork). One survey sees one moment: the agent may be between two
// commands, or running one in a repository it only looks into. So a pass
// settles on a repository only once the newest command it saw ran there twice
// in a row, each time a command newer than the one before: an agent that goes
// through many repositories, one command each, keeps its name, and one that
// works in a project is named after it within two passes that catch it at it.
// A process that keeps running, such as a language server or a dev server,
// counts once, when it is new. Until anything is settled, an agent started in
// a repository works there. A pass writes this down; plan and the dashboard
// only read it.

// projectTTL is how long the book keeps a session no survey has seen: its
// server is gone, or was not found for a while.
const projectTTL = 24 * time.Hour

// projectEntry is what the book holds for one session.
type projectEntry struct {
	// Project is the repository settled on, which the session is named after.
	Project string `json:"project,omitempty"`
	// Last is where the newest command counted ran, and Mark when it started:
	// a later command counts only if it started after it.
	Last string `json:"last,omitempty"`
	Mark int64  `json:"mark,omitempty"`
	// Seen is the last survey that saw the session with its agent, in Unix
	// seconds.
	Seen int64 `json:"seen"`
}

// projectBook holds, per session, what passes settled on.
type projectBook struct {
	path string
	at   map[string]projectEntry
	// changed is true when this survey changed an entry.
	changed bool
}

// projectFile is the book as it is written.
type projectFile struct {
	Version  int                     `json:"version"`
	Sessions map[string]projectEntry `json:"sessions"`
}

// loadProjects reads the book. A missing or unreadable one is empty: at worst a
// session takes two more passes to be named after its project.
func loadProjects(path string) *projectBook {
	b := &projectBook{path: path, at: map[string]projectEntry{}}
	if path == "" {
		return b
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	var f projectFile
	if json.Unmarshal(raw, &f) != nil || f.Version != 1 {
		return b
	}
	for k, e := range f.Sessions {
		b.at[k] = e
	}
	return b
}

// projectKey names a session in the book by what stays through a rename: its
// server's process and the id or name the server started under. "" when its
// server was not found, and then the book neither helps nor forgets it.
func projectKey(s inspect.Session) string {
	if s.Server == nil {
		return ""
	}
	who := s.StartedAs()
	if s.ByID {
		who = "id:" + s.ID
	}
	return strconv.Itoa(s.Server.PID) + "\t" + who
}

// apply settles each agent session's project from what this survey saw, and
// sets it on the session. A session seen with no agent now loses its entry;
// one not seen at all keeps it for projectTTL.
func (b *projectBook) apply(sessions []inspect.Session, now time.Time) {
	for i := range sessions {
		s := &sessions[i]
		key := projectKey(*s)
		if key == "" || s.Listed.Exited {
			continue
		}
		// The agent ended: the session is named by what else it shows.
		if s.Agent.Program == "" {
			if _, ok := b.at[key]; ok {
				delete(b.at, key)
				b.changed = true
			}
			continue
		}
		e := b.at[key]
		before := e
		// The newest command, if it started after the last one counted: the
		// same repository twice in a row settles it.
		if w := s.Agent.Work; len(w) > 0 && w[0].Mark > e.Mark {
			if w[0].Repo == e.Last {
				e.Project = w[0].Repo
			}
			e.Last, e.Mark = w[0].Repo, w[0].Mark
		}
		// Nothing settled yet: an agent works where it was started.
		if e.Project == "" && s.Agent.Started != "" {
			e.Project = s.Agent.Started
		}
		e.Seen = now.Unix()
		// A book whose sessions only aged is not written on every pass, but
		// once an hour, so a session its passes keep seeing does not outlive
		// projectTTL on disk.
		if e.Project != before.Project || e.Last != before.Last || e.Mark != before.Mark || e.Seen-before.Seen > int64(time.Hour/time.Second) {
			b.changed = true
		}
		b.at[key] = e
		s.Project = e.Project
	}
	for k, e := range b.at {
		if now.Sub(time.Unix(e.Seen, 0)) > projectTTL {
			delete(b.at, k)
			b.changed = true
		}
	}
}

// save writes the book when this survey changed it, through a temporary file
// so a reader never sees half of it.
func (b *projectBook) save() error {
	if b == nil || !b.changed || b.path == "" {
		return nil
	}
	raw, err := json.Marshal(projectFile{Version: 1, Sessions: b.at})
	if err != nil {
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
