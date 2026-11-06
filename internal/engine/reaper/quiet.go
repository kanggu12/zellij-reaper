package reaper

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
)

// Idle time comes from a session's terminals. Where none can be read, which is
// every session on Windows (a console has no terminal file), zellij's session
// files alone cannot stand in for them: they change with what zellij keeps
// track of (clients attaching and leaving, tabs, titles), not with what a pane
// prints. A detached session whose job ran for hours and just ended would look
// idle since it was detached, and go at once, where on Linux its terminal says
// the job just printed. Such a session is idle from the later of the first
// pass that found it quiet (no client attached, every pane at a prompt), so a
// job a pass saw running starts the wait over, and the last change of
// zellij's files, so a client that came and left between two passes does too.
// Passes run once an interval, so a session can go up to one interval late. A
// pass writes these times down; plan and the dashboard only read them.

// sourceQuiet is the activity source of a time taken from the book.
const sourceQuiet = "quiet"

// quietBook holds, per session, when a pass first found it quiet. A session
// is its name and its server's process ID, so a new server under an old name
// starts over.
type quietBook struct {
	path string
	at   map[string]time.Time
	// changed is true when this survey added or dropped an entry.
	changed bool
}

// loadQuiet reads the book. A missing or unreadable one is empty: at worst a
// session's idle time starts over, which only keeps it longer.
func loadQuiet(path string) *quietBook {
	q := &quietBook{path: path, at: map[string]time.Time{}}
	if path == "" {
		return q
	}
	f, err := os.Open(path)
	if err != nil {
		return q
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// name <TAB> server pid <TAB> unix seconds
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 3 {
			continue
		}
		secs, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		q.at[parts[0]+"\t"+parts[1]] = time.Unix(secs, 0)
	}
	return q
}

// quietKey names a session in the book.
func quietKey(s inspect.Session) string {
	return s.Name() + "\t" + strconv.Itoa(s.Facts.ServerPID)
}

// apply sets the last activity of every running session whose terminals could
// not be read, and notes in the book what this survey saw: a session found
// quiet for the first time from now, a session no longer quiet or gone
// dropped. The last activity is the later of the first quiet pass and the last
// change of zellij's files: zellij rewrites them when a client attaches and
// when it leaves (measured on 0.44.3 and 0.45.1), so a session used between two
// passes, which no pass saw attached, starts its wait over when it was left.
func (q *quietBook) apply(sessions []inspect.Session, now time.Time) {
	seen := map[string]bool{}
	for i := range sessions {
		s := &sessions[i]
		if s.Listed.Exited || s.Facts.ActivitySource != "session files" || s.Facts.ServerPID == 0 {
			continue
		}
		k := quietKey(*s)
		seen[k] = true
		quiet := s.Facts.Attached == policy.No && s.Facts.BusyWith == ""
		since, known := q.at[k]
		switch {
		case !quiet:
			// In use now: when it goes quiet again, it starts over.
			if known {
				delete(q.at, k)
				q.changed = true
			}
			continue
		case !known:
			since = now.Truncate(time.Second)
			q.at[k] = since
			q.changed = true
		}
		// The files' time stays when it is the later one: a client left
		// after the first quiet pass.
		if since.After(s.Facts.LastActive) {
			s.Facts.LastActive, s.Facts.ActivitySource = since, sourceQuiet
		}
	}
	for k := range q.at {
		if !seen[k] {
			delete(q.at, k)
			q.changed = true
		}
	}
}

// forget drops a session, which a pass deleted.
func (q *quietBook) forget(s inspect.Session) {
	if q == nil {
		return
	}
	if _, ok := q.at[quietKey(s)]; ok {
		delete(q.at, quietKey(s))
		q.changed = true
	}
}

// save writes the book when this survey changed it.
func (q *quietBook) save() error {
	if q == nil || q.path == "" || !q.changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(q.at))
	for k := range q.at {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%d\n", k, q.at[k].Unix())
	}
	tmp := q.path + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, q.path)
}
