package zellij

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// indexAt puts a database from the sqlite package's testdata where zellij
// keeps its session index for infoDir.
func indexAt(t *testing.T, infoDir, fixture string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "sqlite", "testdata", fixture+".gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IndexFile(infoDir), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheIndexMapsNamesToIDs(t *testing.T) {
	info := filepath.Join(t.TempDir(), "contract_version_1", "session_info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		t.Fatal(err)
	}
	// No index: a zellij from before ids, where a session's id is its name.
	x, err := ReadIndex(info)
	if err != nil || x.Len() != 0 || x.ID("notes") != "notes" || x.ByID("notes") {
		t.Fatalf("without an index: %+v, %v", x, err)
	}
	indexAt(t, info, "plain.db")
	if IndexFile(info) != filepath.Join(filepath.Dir(info), "session_index.sqlite") {
		t.Errorf("index file %s", IndexFile(info))
	}
	x, err = ReadIndex(info)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{
		"brave-peach": "3f9a0c1d2b4e",
		"api-server":  "a1b2c3d4e5f6",
		// A row with no id is named on disk by its name.
		"notes": "notes",
		// A session the index does not know, likewise.
		"elsewhere": "elsewhere",
	} {
		if got := x.ID(name); got != id {
			t.Errorf("ID(%q) = %q, want %q", name, got, id)
		}
		if got := x.Name(id); got != name {
			t.Errorf("Name(%q) = %q, want %q", id, got, name)
		}
		if x.ByID(name) != (id != name) {
			t.Errorf("ByID(%q) = %v", name, x.ByID(name))
		}
	}
	if x.Len() != 3 {
		t.Errorf("Len = %d, want 3", x.Len())
	}
	// A file in the index's place that is no database is an error, and the
	// zero index goes with it.
	if err := os.WriteFile(IndexFile(info), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if x, err := ReadIndex(info); err == nil || x.Len() != 0 {
		t.Errorf("a broken index: %+v, %v", x, err)
	}
}

func TestZellijsOwnNamesStartWithADot(t *testing.T) {
	for name, want := range map[string]bool{".zj-panel": true, ".desk": true, "notes": false, "": false} {
		if IsAppName(name) != want {
			t.Errorf("IsAppName(%q) = %v", name, !want)
		}
	}
}
