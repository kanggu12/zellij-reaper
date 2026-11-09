package sqlite

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// unpack ungzips a database from testdata (and its log, when there is one)
// into a temporary directory and returns the database's path there. The
// files come from testdata/make.py, which writes them with SQLite itself.
func unpack(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name+suffix+".gz"))
		if errors.Is(err, os.ErrNotExist) && suffix != "" {
			continue
		}
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
		if err := os.WriteFile(filepath.Join(dir, name+suffix), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, name)
}

// names maps the name column of zellij's sessions table to COALESCE(id,
// name), the way zellij reads it.
func names(t *testing.T, tab Table) map[string]string {
	t.Helper()
	nameCol, idCol := tab.Col("name"), tab.Col("id")
	if nameCol < 0 || idCol < 0 {
		t.Fatalf("columns %v: no name or id", tab.Columns)
	}
	got := map[string]string{}
	for _, r := range tab.Rows {
		n, ok := tab.Text(r, nameCol)
		if !ok {
			t.Fatalf("row %d has no name: %v", r.RowID, r.Values)
		}
		id, ok := tab.Text(r, idCol)
		if !ok {
			id = n
		}
		got[n] = id
	}
	return got
}

func TestReadsZellijsTableWithAndWithoutIDs(t *testing.T) {
	tab, err := ReadTable(unpack(t, "plain.db"), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"name", "folder", "repo", "worktree", "branch", "facts", "created_at", "last_used_at", "id"}
	if !reflect.DeepEqual(tab.Columns, want) {
		t.Errorf("columns = %v, want %v", tab.Columns, want)
	}
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{
		"brave-peach": "3f9a0c1d2b4e", "api-server": "a1b2c3d4e5f6", "notes": "notes",
	}) {
		t.Errorf("names = %v", got)
	}
	// Integers and text come back as Go values.
	for _, r := range tab.Rows {
		if r.Values[tab.Col("created_at")] != int64(1700000000) || r.Values[tab.Col("facts")] != "{}" {
			t.Errorf("row %d = %v", r.RowID, r.Values)
		}
	}
}

func TestAnIntegerPrimaryKeyIsTheRowid(t *testing.T) {
	tab, err := ReadTable(unpack(t, "plain.db"), "numbered")
	if err != nil {
		t.Fatal(err)
	}
	// Rows come in rowid order, the INTEGER PRIMARY KEY filled from the rowid.
	want := []Row{
		{RowID: -3, Values: []Value{int64(-3), nil, -0.25, nil}},
		{RowID: 7, Values: []Value{int64(7), "seven", 7.5, []byte{0x00, 0xff}}},
	}
	if !reflect.DeepEqual(tab.Rows, want) {
		t.Errorf("rows = %#v, want %#v", tab.Rows, want)
	}
}

func TestARowFromBeforeAColumnWasAddedLacksIt(t *testing.T) {
	tab, err := ReadTable(unpack(t, "altered.db"), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"old-one": "old-one", "new-one": "0123456789ab"}) {
		t.Errorf("names = %v", got)
	}
}

func TestTheWriteAheadLogHoldsTheNewestRows(t *testing.T) {
	path := unpack(t, "wal.db")
	tab, err := ReadTable(path, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	// The log adds "second" and renames "first"; the database alone has
	// neither change.
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"first-renamed": "aaaaaaaaaaaa", "second": "bbbbbbbbbbbb"}) {
		t.Errorf("with the log: %v", got)
	}
	if err := os.Remove(path + "-wal"); err != nil {
		t.Fatal(err)
	}
	tab, err = ReadTable(path, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"first": "aaaaaaaaaaaa"}) {
		t.Errorf("without the log: %v", got)
	}
}

// walFrames reads the log of wal.db: its bytes, and the offsets of its frames.
func walFrames(t *testing.T, path string) ([]byte, []int) {
	t.Helper()
	wal, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	frame := 24 + int(binary.BigEndian.Uint32(wal[8:12]))
	var offs []int
	for off := 32; off+frame <= len(wal); off += frame {
		offs = append(offs, off)
	}
	// make.py commits two transactions after the checkpoint: a new row, then
	// a rename, each touching the table and the index of names.
	if len(offs) != 4 {
		t.Fatalf("the log has %d frames, want 4: run make.py again?", len(offs))
	}
	return wal, offs
}

func TestOnlyCommittedFramesOfTheLogCount(t *testing.T) {
	// Each case spoils the last transaction (frames 3 and 4) one way; the
	// first transaction still counts, as it would for SQLite.
	for _, c := range []struct {
		name  string
		spoil func(wal []byte, offs []int) []byte
	}{
		{"cut in the middle of a frame", func(wal []byte, offs []int) []byte { return wal[:offs[3]+100] }},
		{"cut before the commit frame", func(wal []byte, offs []int) []byte { return wal[:offs[3]] }},
		{"a page changed after its checksum", func(wal []byte, offs []int) []byte { wal[offs[2]+24+50] ^= 0xff; return wal }},
		{"a frame from before a restart of the log", func(wal []byte, offs []int) []byte { wal[offs[2]+8] ^= 1; return wal }},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := unpack(t, "wal.db")
			wal, offs := walFrames(t, path)
			if err := os.WriteFile(path+"-wal", c.spoil(wal, offs), 0o600); err != nil {
				t.Fatal(err)
			}
			tab, err := ReadTable(path, "sessions")
			if err != nil {
				t.Fatal(err)
			}
			if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"first": "aaaaaaaaaaaa", "second": "bbbbbbbbbbbb"}) {
				t.Errorf("names = %v", got)
			}
		})
	}
}

func TestALogWithABadHeaderIsIgnored(t *testing.T) {
	path := unpack(t, "wal.db")
	wal, _ := walFrames(t, path)
	// The header's checksum no longer matches: SQLite would ignore the log.
	wal[12] ^= 1
	if err := os.WriteFile(path+"-wal", wal, 0o600); err != nil {
		t.Fatal(err)
	}
	tab, err := ReadTable(path, "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"first": "aaaaaaaaaaaa"}) {
		t.Errorf("names = %v", got)
	}
}

func TestATallTableWithOverflowPages(t *testing.T) {
	tab, err := ReadTable(unpack(t, "big.db"), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Rows) != 301 {
		t.Fatalf("%d rows, want 301", len(tab.Rows))
	}
	got := names(t, tab)
	if got["session-0000"] != "000000000000" || got["session-0299"] != "00000000012b" || got["long-facts"] != "ffffffffffff" {
		t.Errorf("names = %v", got)
	}
	for i, r := range tab.Rows {
		if i > 0 && r.RowID <= tab.Rows[i-1].RowID {
			t.Fatalf("rowids out of order at %d", i)
		}
		facts, _ := tab.Text(r, tab.Col("facts"))
		n, _ := tab.Text(r, tab.Col("name"))
		// The longest value spans several 1024-byte pages.
		if n == "long-facts" && len(facts) != len(`{"note":""}`)+5000 {
			t.Errorf("long-facts has %d bytes of facts", len(facts))
		}
	}
}

func TestPagesOf64KiB(t *testing.T) {
	tab, err := ReadTable(unpack(t, "large-pages.db"), "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, tab); !reflect.DeepEqual(got, map[string]string{"roomy": "cccccccccccc"}) {
		t.Errorf("names = %v", got)
	}
}

func TestWhatItCannotRead(t *testing.T) {
	if _, err := ReadTable(unpack(t, "utf16.db"), "sessions"); !errors.Is(err, ErrNotSQLite) {
		t.Errorf("UTF-16: %v, want ErrNotSQLite", err)
	}
	if _, err := ReadTable(unpack(t, "plain.db"), "nothing"); !errors.Is(err, ErrNoTable) {
		t.Errorf("no table: %v, want ErrNoTable", err)
	}
	if _, err := ReadTable(filepath.Join(t.TempDir(), "missing.db"), "sessions"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: %v, want ErrNotExist", err)
	}
	for _, b := range [][]byte{nil, []byte("SQLite format 3"), bytes.Repeat([]byte{0x41}, 4096)} {
		path := filepath.Join(t.TempDir(), "x.db")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTable(path, "sessions"); !errors.Is(err, ErrNotSQLite) {
			t.Errorf("%q...: %v, want ErrNotSQLite", b[:min(len(b), 16)], err)
		}
	}
}

func TestPagesThatPointInACircleStop(t *testing.T) {
	path := unpack(t, "big.db")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := load(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The table's root is an interior page: point its right-most child back
	// at itself.
	var root uint32
	if err := d.rows(1, func(_ int64, payload []byte) error {
		v, err := record(payload)
		if err == nil && v[1] == "sessions" {
			root = uint32(v[3].(int64))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	off := int(root-1) * d.pageSize
	if raw[off] != interiorTable {
		t.Fatalf("page %d has type %#x, want an interior page", root, raw[off])
	}
	// Two ways round: back to the page itself, and to the first cell's
	// child, which would read that subtree twice.
	cell := int(binary.BigEndian.Uint16(raw[off+12:]))
	first := binary.BigEndian.Uint32(raw[off+cell:])
	for _, to := range []uint32{root, first} {
		bad := bytes.Clone(raw)
		binary.BigEndian.PutUint32(bad[off+8:], to)
		if err := os.WriteFile(path, bad, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTable(path, "sessions"); !errors.Is(err, ErrCorrupt) {
			t.Errorf("right-most pointer to page %d: err = %v, want ErrCorrupt", to, err)
		}
	}
}

func TestColumnsOfStatementsAsPeopleWriteThem(t *testing.T) {
	for _, c := range []struct {
		sql   string
		want  []string
		alias int
	}{
		{"CREATE TABLE t(a, b)", []string{"a", "b"}, -1},
		{`CREATE TABLE "my table" ("a b" TEXT, [c] INT, ` + "`d`" + ` REAL, "e""f" BLOB)`, []string{"a b", "c", "d", `e"f`}, -1},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT DEFAULT 'a,b', w NUMERIC(10, 2), PRIMARY KEY (v), CONSTRAINT c CHECK (w > 0))", []string{"id", "v", "w"}, 0},
		{"CREATE TABLE t ( -- first, a comment\n a TEXT /* a, b */, b INT)", []string{"a", "b"}, -1},
		{"CREATE TABLE t (n INT PRIMARY KEY, m integer primary key)", []string{"n", "m"}, 1},
	} {
		got, alias, err := columns(c.sql)
		if err != nil || !reflect.DeepEqual(got, c.want) || alias != c.alias {
			t.Errorf("columns(%q) = %q, %d, %v; want %q, %d", c.sql, got, alias, err, c.want, c.alias)
		}
	}
}

func TestVarints(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		want uint64
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x81, 0x00}, 128, 2},
		{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ^uint64(0), 9},
		{[]byte{0x81}, 0, 0},
	} {
		if v, n := varint(c.b); v != c.want || n != c.n {
			t.Errorf("varint(%x) = %d, %d; want %d, %d", c.b, v, n, c.want, c.n)
		}
	}
}

// FuzzLoad feeds the reader damaged copies of real databases: it may refuse
// them, but must neither panic nor run on.
func FuzzLoad(f *testing.F) {
	for _, name := range []string{"plain.db", "wal.db", "altered.db"} {
		path := filepath.Join(f.TempDir(), name)
		for _, suffix := range []string{"", "-wal"} {
			raw, err := os.ReadFile(filepath.Join("testdata", name+suffix+".gz"))
			if err != nil {
				continue
			}
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				f.Fatal(err)
			}
			b, err := io.ReadAll(zr)
			if err != nil {
				f.Fatal(err)
			}
			if err := os.WriteFile(path+suffix, b, 0o600); err != nil {
				f.Fatal(err)
			}
		}
		file, _ := os.ReadFile(path)
		wal, _ := os.ReadFile(path + "-wal")
		f.Add(file, wal)
	}
	f.Fuzz(func(t *testing.T, file, wal []byte) {
		d, err := load(file, wal)
		if err != nil {
			return
		}
		_, _ = d.table("sessions")
		_, _ = d.table("numbered")
	})
}
