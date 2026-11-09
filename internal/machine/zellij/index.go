package zellij

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kanggu12/zellij-reaper/internal/machine/sqlite"
)

// IndexFileName is zellij's session index. Since 0.46 a session's socket and
// its directory under session_info are named by a short id that stays through
// renames, and this database, next to session_info, maps names to those ids.
const IndexFileName = "session_index.sqlite"

// IndexFile is the session index that goes with a session_info directory.
func IndexFile(infoDir string) string {
	return filepath.Join(filepath.Dir(infoDir), IndexFileName)
}

// Index maps session names to the ids zellij names their files by. The zero
// Index knows no session: every id is the session's name, as before 0.46.
type Index struct {
	// ids maps a name to its id, names an id to its name.
	ids, names map[string]string
}

// ReadIndex reads zellij's session index for a session_info directory. No
// index means a zellij from before ids, and gives the zero Index.
func ReadIndex(infoDir string) (Index, error) {
	t, err := sqlite.ReadTable(IndexFile(infoDir), "sessions")
	if errors.Is(err, os.ErrNotExist) {
		return Index{}, nil
	}
	if err != nil {
		return Index{}, fmt.Errorf("read zellij's session index %s: %w", IndexFile(infoDir), err)
	}
	nameCol, idCol := t.Col("name"), t.Col("id")
	if nameCol < 0 {
		return Index{}, fmt.Errorf("zellij's session index %s has no name column", IndexFile(infoDir))
	}
	x := Index{ids: map[string]string{}, names: map[string]string{}}
	for _, r := range t.Rows {
		name, ok := t.Text(r, nameCol)
		if !ok || name == "" {
			continue
		}
		// zellij reads COALESCE(id, name): a row from before ids, or one
		// whose id was never set, is named on disk by its name.
		id, ok := t.Text(r, idCol)
		if !ok || id == "" {
			id = name
		}
		x.ids[name], x.names[id] = id, name
	}
	return x, nil
}

// NewIndex is an Index from name → id pairs, for tests.
func NewIndex(ids map[string]string) Index {
	x := Index{ids: map[string]string{}, names: map[string]string{}}
	for name, id := range ids {
		x.ids[name], x.names[id] = id, name
	}
	return x
}

// ID is what a session's socket and its directory under session_info are
// called: its id, or its name for a session the index does not know.
func (x Index) ID(name string) string {
	if id, ok := x.ids[name]; ok {
		return id
	}
	return name
}

// Name is the session a socket or a directory under session_info belongs to:
// the name the index gives its id, or the id itself, which is the name of a
// session from before ids.
func (x Index) Name(id string) string {
	if name, ok := x.names[id]; ok {
		return name
	}
	return id
}

// ByID reports whether a session's files are named by an id other than its
// name: a session started by zellij 0.46 or later.
func (x Index) ByID(name string) bool {
	return x.ID(name) != name
}

// Len is how many sessions the index knows.
func (x Index) Len() int { return len(x.ids) }

// IsAppName reports whether a socket or directory belongs to zellij itself
// rather than to a session: since 0.46 zellij keeps its own (its desktop
// panel's, for one) under names that start with a dot, and lists none of them.
func IsAppName(name string) bool {
	return len(name) > 0 && name[0] == '.'
}
