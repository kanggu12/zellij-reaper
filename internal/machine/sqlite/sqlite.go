// Package sqlite reads the rows of one table from an SQLite database file,
// read-only and without SQLite itself. Since 0.46 zellij keeps the names of its
// sessions in such a file, and the reaper needs two columns of one table from
// it: a driver would add megabytes to the binary for that. The file format is
// SQLite's own, unchanged since 2010 (https://www.sqlite.org/fileformat2.html),
// write-ahead log included.
//
// The file is read without SQLite's locks, while zellij may be writing it. The
// write-ahead log is read before the database, so a checkpoint that lands in
// between leaves pages the log still holds; what a write had not committed yet
// is left out, as SQLite itself would leave it out. A file that does not read
// as a whole is an error, never a guess.
package sqlite

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
)

// MaxFile bounds what is read of the database and of its log, each: zellij's
// index holds a row per session, a few kilobytes, so a file this large is not
// one.
const MaxFile = 64 << 20

// ErrNotSQLite is a file that is not an SQLite database, or one this package
// cannot read (a text encoding other than UTF-8).
var ErrNotSQLite = errors.New("not an SQLite database")

// ErrCorrupt is a database whose pages do not fit together, as a file read
// while it was being rewritten can be.
var ErrCorrupt = errors.New("database does not read as a whole")

// ErrNoTable is a database without the table asked for.
var ErrNoTable = errors.New("no such table")

// Value is one column of a row: nil (NULL), int64, float64, string or []byte.
type Value any

// Row is one row of a table: its rowid and its values in column order. A row
// written before a column was added has fewer values than the table has
// columns; the ones missing read as NULL.
type Row struct {
	RowID  int64
	Values []Value
}

// Table is a table's column names and rows.
type Table struct {
	Columns []string
	Rows    []Row
}

// Col returns the position of a column, -1 when the table has none by that
// name. SQLite compares column names without regard to case.
func (t Table) Col(name string) int {
	for i, c := range t.Columns {
		if strings.EqualFold(c, name) {
			return i
		}
	}
	return -1
}

// Text returns a row's column as text, and false when it is NULL, missing or
// not text.
func (t Table) Text(r Row, col int) (string, bool) {
	if col < 0 || col >= len(r.Values) {
		return "", false
	}
	s, ok := r.Values[col].(string)
	return s, ok
}

// ReadTable reads every row of a table from the database at path, with the
// changes its write-ahead log (path + "-wal") holds that were committed. A
// file that reads in pieces, as one caught in the middle of a write can, is
// read again a few times before ErrCorrupt.
func ReadTable(path, name string) (Table, error) {
	var t Table
	var err error
	for try := 0; try < 3; try++ {
		if try > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		t, err = readTable(path, name)
		// Only a database that did not read as a whole is worth a second look.
		if !errors.Is(err, ErrCorrupt) {
			return t, err
		}
	}
	return t, err
}

// readTable is one attempt at ReadTable.
func readTable(path, name string) (Table, error) {
	d, err := open(path)
	if err != nil {
		return Table{}, err
	}
	return d.table(name)
}

// table reads one table of a database.
func (d *db) table(name string) (Table, error) {
	// The schema table, sqlite_master, is the tree rooted at page 1: one row
	// per table and index, as (type, name, tbl_name, rootpage, sql).
	var root int64
	var sql string
	err := d.rows(1, func(_ int64, payload []byte) error {
		v, err := record(payload)
		if err != nil {
			return err
		}
		if len(v) < 5 || v[0] != "table" {
			return nil
		}
		if n, _ := v[1].(string); !strings.EqualFold(n, name) {
			return nil
		}
		root, _ = v[3].(int64)
		sql, _ = v[4].(string)
		return nil
	})
	if err != nil {
		return Table{}, err
	}
	if root <= 0 || root > math.MaxUint32 {
		return Table{}, fmt.Errorf("%w: %s", ErrNoTable, name)
	}
	cols, alias, err := columns(sql)
	if err != nil {
		return Table{}, err
	}
	t := Table{Columns: cols}
	err = d.rows(uint32(root), func(rowid int64, payload []byte) error {
		v, err := record(payload)
		if err != nil {
			return err
		}
		// An INTEGER PRIMARY KEY column is the rowid itself: the record
		// keeps a NULL in its place.
		if alias >= 0 && alias < len(v) && v[alias] == nil {
			v[alias] = rowid
		}
		t.Rows = append(t.Rows, Row{RowID: rowid, Values: v})
		return nil
	})
	if err != nil {
		return Table{}, err
	}
	return t, nil
}

// db is a database file and the committed pages of its write-ahead log.
type db struct {
	file     []byte
	pageSize int
	// usable is the page size less the bytes each page reserves at its end.
	usable int
	// wal maps a page number to its newest committed copy in the log.
	wal map[uint32][]byte
	// pages is how many pages the database has.
	pages uint32
}

// open reads a database and its log. The log is read first: see the package
// comment.
func open(path string) (*db, error) {
	wal, err := readCapped(path + "-wal")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	file, err := readCapped(path)
	if err != nil {
		return nil, err
	}
	return load(file, wal)
}

// load reads a database from its bytes and its log's.
func load(file, wal []byte) (*db, error) {
	d := &db{file: file}
	// The page size is in the database's header, or in the log's when the
	// database has not been written to yet.
	switch {
	case len(file) >= 100 && isHeader(file):
		d.pageSize = int(binary.BigEndian.Uint16(file[16:18]))
		// 1 stands for 65536, which two bytes cannot hold.
		if d.pageSize == 1 {
			d.pageSize = 65536
		}
	case len(wal) >= 32:
		d.pageSize = int(binary.BigEndian.Uint32(wal[8:12]))
	default:
		return nil, ErrNotSQLite
	}
	// A power of two from 512 to 65536, as SQLite allows.
	if d.pageSize < 512 || d.pageSize > 65536 || d.pageSize&(d.pageSize-1) != 0 {
		return nil, fmt.Errorf("%w: page size %d", ErrNotSQLite, d.pageSize)
	}
	d.pages = uint32(len(file) / d.pageSize)
	d.applyWAL(wal)
	// The header, from the newest copy of page 1.
	first, err := d.page(1)
	if err != nil || !isHeader(first) {
		return nil, ErrNotSQLite
	}
	// Bytes each page keeps at its end for extensions; SQLite requires at
	// least 480 usable bytes.
	d.usable = d.pageSize - int(first[20])
	if d.usable < 480 {
		return nil, fmt.Errorf("%w: %d usable bytes a page", ErrNotSQLite, d.usable)
	}
	// 1 is UTF-8; 0 is a database with nothing in it yet. Text in UTF-16
	// would need decoding no zellij database needs.
	if enc := binary.BigEndian.Uint32(first[56:60]); enc > 1 {
		return nil, fmt.Errorf("%w: text encoding %d", ErrNotSQLite, enc)
	}
	return d, nil
}

// isHeader reports whether b starts with SQLite's magic string.
func isHeader(b []byte) bool {
	return bytes.HasPrefix(b, []byte("SQLite format 3\x00"))
}

// readCapped reads a whole file, refusing one larger than MaxFile.
func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFile {
		return nil, fmt.Errorf("%s: larger than %d bytes", path, MaxFile)
	}
	return b, nil
}

// applyWAL takes in the frames of the write-ahead log that belong to committed
// transactions. A log that does not match the database (another page size, a
// bad header) is ignored, as SQLite ignores it; reading stops at the first
// frame that is not valid, which is where the log's current content ends.
func (d *db) applyWAL(wal []byte) {
	if len(wal) < 32 {
		return
	}
	// The magic number says in which byte order the checksums are computed.
	var order binary.ByteOrder
	switch binary.BigEndian.Uint32(wal[0:4]) {
	case 0x377f0682:
		order = binary.LittleEndian
	case 0x377f0683:
		order = binary.BigEndian
	default:
		return
	}
	if binary.BigEndian.Uint32(wal[4:8]) != 3007000 || int(binary.BigEndian.Uint32(wal[8:12])) != d.pageSize {
		return
	}
	// The header's own checksum covers its first 24 bytes, and every frame's
	// checksum continues from the one before it.
	s0, s1 := checksum(order, wal[0:24], 0, 0)
	if s0 != binary.BigEndian.Uint32(wal[24:28]) || s1 != binary.BigEndian.Uint32(wal[28:32]) {
		return
	}
	salt := wal[16:24]
	committed := map[uint32][]byte{}
	pending := map[uint32][]byte{}
	size := uint32(0)
	frame := 24 + d.pageSize
	for off := 32; off+frame <= len(wal); off += frame {
		h := wal[off : off+24]
		// A frame from before the log was last restarted has the old salt.
		if !bytes.Equal(h[8:16], salt) {
			break
		}
		page := wal[off+24 : off+frame]
		s0, s1 = checksum(order, h[0:8], s0, s1)
		s0, s1 = checksum(order, page, s0, s1)
		if s0 != binary.BigEndian.Uint32(h[16:20]) || s1 != binary.BigEndian.Uint32(h[20:24]) {
			break
		}
		n := binary.BigEndian.Uint32(h[0:4])
		if n == 0 {
			break
		}
		pending[n] = page
		// A commit frame carries the database's size after the commit; the
		// frames up to it are the transaction.
		if commit := binary.BigEndian.Uint32(h[4:8]); commit != 0 {
			for k, v := range pending {
				committed[k] = v
			}
			pending = map[uint32][]byte{}
			size = commit
		}
	}
	if size > 0 {
		d.wal, d.pages = committed, size
	}
}

// checksum continues the log's checksum over b, eight bytes at a time.
func checksum(order binary.ByteOrder, b []byte, s0, s1 uint32) (uint32, uint32) {
	for i := 0; i+8 <= len(b); i += 8 {
		s0 += order.Uint32(b[i:]) + s1
		s1 += order.Uint32(b[i+4:]) + s0
	}
	return s0, s1
}

// page returns page n, counted from 1: its newest committed copy in the log,
// else the database's.
func (d *db) page(n uint32) ([]byte, error) {
	if n == 0 || n > d.pages {
		return nil, fmt.Errorf("%w: page %d of %d", ErrCorrupt, n, d.pages)
	}
	if p, ok := d.wal[n]; ok {
		return p, nil
	}
	off := int64(n-1) * int64(d.pageSize)
	if off+int64(d.pageSize) > int64(len(d.file)) {
		return nil, fmt.Errorf("%w: page %d is past the end of the file", ErrCorrupt, n)
	}
	return d.file[off : off+int64(d.pageSize)], nil
}

// Page types of a table's tree.
const (
	interiorTable = 0x05
	leafTable     = 0x0d
)

// rows calls visit with the rowid and the record of every row in the table
// tree rooted at page root, in rowid order.
func (d *db) rows(root uint32, visit func(rowid int64, payload []byte) error) error {
	// A page reached twice means pages point in a circle, or two cells share
	// overflow pages: never read it again. One set for the tree's pages and
	// the cells' overflow pages alike bounds what a table can make this read
	// to the pages there are: cells that shared a long chain each made a copy
	// of it, and a 10 MB file held 2 GB of rows.
	seen := map[uint32]bool{}
	var walk func(n uint32, depth int) error
	walk = func(n uint32, depth int) error {
		// A tree of a billion rows is about five pages deep.
		if depth > 40 {
			return fmt.Errorf("%w: tree deeper than 40 pages", ErrCorrupt)
		}
		if seen[n] {
			return fmt.Errorf("%w: page %d reached twice", ErrCorrupt, n)
		}
		seen[n] = true
		p, err := d.page(n)
		if err != nil {
			return err
		}
		// Page 1 starts with the file's 100-byte header.
		h := 0
		if n == 1 {
			h = 100
		}
		if len(p) < h+12 {
			return fmt.Errorf("%w: page %d is too short", ErrCorrupt, n)
		}
		cells := int(binary.BigEndian.Uint16(p[h+3:]))
		switch p[h] {
		case leafTable:
			ptrs := h + 8
			if ptrs+2*cells > d.usable {
				return fmt.Errorf("%w: page %d has more cells than room", ErrCorrupt, n)
			}
			for i := 0; i < cells; i++ {
				rowid, payload, err := d.leafCell(p, int(binary.BigEndian.Uint16(p[ptrs+2*i:])), seen)
				if err != nil {
					return err
				}
				if err := visit(rowid, payload); err != nil {
					return err
				}
			}
			return nil
		case interiorTable:
			// Each cell points to the subtree left of its key; the header's
			// right-most pointer to the one after the last key.
			ptrs := h + 12
			if ptrs+2*cells > d.usable {
				return fmt.Errorf("%w: page %d has more cells than room", ErrCorrupt, n)
			}
			for i := 0; i < cells; i++ {
				off := int(binary.BigEndian.Uint16(p[ptrs+2*i:]))
				if off < ptrs || off+4 > d.usable {
					return fmt.Errorf("%w: a cell of page %d is out of place", ErrCorrupt, n)
				}
				if err := walk(binary.BigEndian.Uint32(p[off:]), depth+1); err != nil {
					return err
				}
			}
			return walk(binary.BigEndian.Uint32(p[h+8:]), depth+1)
		}
		return fmt.Errorf("%w: page %d (type %#x) is not part of a table", ErrCorrupt, n, p[h])
	}
	return walk(root, 0)
}

// leafCell reads one cell of a table leaf: the rowid and the whole record,
// following its overflow pages when it does not fit on the page. seen holds
// the pages the table has used already, which no overflow page may be.
func (d *db) leafCell(p []byte, off int, seen map[uint32]bool) (int64, []byte, error) {
	bad := func(what string) (int64, []byte, error) {
		return 0, nil, fmt.Errorf("%w: %s", ErrCorrupt, what)
	}
	if off <= 0 || off >= d.usable {
		return bad("a cell is out of place")
	}
	size, n := varint(p[off:d.usable])
	if n == 0 {
		return bad("a cell's size does not read")
	}
	off += n
	rowid, n := varint(p[off:d.usable])
	if n == 0 {
		return bad("a cell's rowid does not read")
	}
	off += n
	if size > MaxFile {
		return bad("a cell is larger than the file")
	}
	total := int(size)
	local := d.localSize(total)
	if off+local > d.usable {
		return bad("a cell runs past its page")
	}
	// Room for what this page holds: a size that lies is not made room for
	// ahead of the pages that would hold it.
	payload := make([]byte, 0, min(total, d.usable))
	payload = append(payload, p[off:off+local]...)
	if local == total {
		return int64(rowid), payload, nil
	}
	// The rest is in a chain of overflow pages, each with the number of the
	// next one first.
	if off+local+4 > d.usable {
		return bad("a cell's overflow pointer runs past its page")
	}
	next := binary.BigEndian.Uint32(p[off+local:])
	for len(payload) < total {
		if next == 0 || seen[next] {
			return bad("a cell's overflow pages end early, go in a circle, or are another's")
		}
		seen[next] = true
		op, err := d.page(next)
		if err != nil {
			return 0, nil, err
		}
		chunk := op[4:d.usable]
		if want := total - len(payload); want < len(chunk) {
			chunk = chunk[:want]
		}
		payload = append(payload, chunk...)
		next = binary.BigEndian.Uint32(op[0:4])
	}
	return int64(rowid), payload, nil
}

// localSize is how much of a record of p bytes a table leaf keeps on the page
// itself, by SQLite's formula; the rest goes to overflow pages.
func (d *db) localSize(p int) int {
	u := d.usable
	x := u - 35
	if p <= x {
		return p
	}
	m := ((u-12)*32/255 - 23)
	k := m + (p-m)%(u-4)
	if k <= x {
		return k
	}
	return m
}

// varint reads SQLite's variable-length integer: seven bits a byte, high bit
// set on every byte but the last, and all eight bits of a ninth byte. It
// returns the bytes read, 0 when b ends first.
func varint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		if i == 8 {
			return v<<8 | uint64(b[i]), 9
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

// record decodes a record: a header of serial types, then the values.
func record(b []byte) ([]Value, error) {
	bad := func(what string) ([]Value, error) {
		return nil, fmt.Errorf("%w: %s", ErrCorrupt, what)
	}
	hsize, n := varint(b)
	if n == 0 || hsize < uint64(n) || hsize > uint64(len(b)) {
		return bad("a record's header does not read")
	}
	var types []uint64
	for off := n; off < int(hsize); {
		t, m := varint(b[off:int(hsize)])
		if m == 0 {
			return bad("a record's header does not read")
		}
		types = append(types, t)
		off += m
	}
	values := make([]Value, len(types))
	body := int(hsize)
	// Widths of the integer serial types 1 to 6.
	widths := [...]int{0, 1, 2, 3, 4, 6, 8}
	for i, t := range types {
		switch {
		case t == 0:
			values[i] = nil
		case t <= 6:
			w := widths[t]
			if body+w > len(b) {
				return bad("a record ends early")
			}
			// Big-endian two's complement, sign-extended to 64 bits.
			var v int64
			for j := 0; j < w; j++ {
				v = v<<8 | int64(b[body+j])
			}
			shift := uint(64 - 8*w)
			values[i] = v << shift >> shift
			body += w
		case t == 7:
			if body+8 > len(b) {
				return bad("a record ends early")
			}
			values[i] = math.Float64frombits(binary.BigEndian.Uint64(b[body:]))
			body += 8
		case t == 8:
			values[i] = int64(0)
		case t == 9:
			values[i] = int64(1)
		case t >= 12:
			// Even types are blobs, odd ones text, of (t-12)/2 bytes.
			w := (t - 12) / 2
			if w > uint64(len(b)-body) {
				return bad("a record ends early")
			}
			end := body + int(w)
			if t%2 == 0 {
				values[i] = append([]byte(nil), b[body:end]...)
			} else {
				values[i] = string(b[body:end])
			}
			body = end
		default:
			// 10 and 11 are reserved for SQLite's internal use.
			return bad("a record has a reserved type")
		}
	}
	return values, nil
}

// columns reads the column names from a CREATE TABLE statement, and the
// position of a column declared INTEGER PRIMARY KEY, which holds the rowid
// (-1 when there is none). SQLite keeps the statement as it was written, with
// any column ALTER TABLE added written into it.
func columns(sql string) ([]string, int, error) {
	sql = stripComments(sql)
	open := strings.IndexByte(sql, '(')
	end := strings.LastIndexByte(sql, ')')
	if open < 0 || end < open {
		return nil, -1, fmt.Errorf("%w: the table's statement does not read: %q", ErrCorrupt, sql)
	}
	var cols []string
	alias := -1
	for _, def := range splitTop(sql[open+1 : end]) {
		name, rest, quoted := firstToken(def)
		if name == "" {
			continue
		}
		// A table constraint, not a column.
		if !quoted {
			switch strings.ToUpper(name) {
			case "CONSTRAINT", "PRIMARY", "UNIQUE", "CHECK", "FOREIGN":
				continue
			}
		}
		// "id INTEGER PRIMARY KEY" makes id the rowid; INTEGER spelled
		// exactly so, as SQLite requires.
		fields := strings.Fields(strings.ToUpper(rest))
		if len(fields) >= 3 && fields[0] == "INTEGER" && fields[1] == "PRIMARY" && fields[2] == "KEY" && alias < 0 {
			alias = len(cols)
		}
		cols = append(cols, name)
	}
	if len(cols) == 0 {
		return nil, -1, fmt.Errorf("%w: the table has no columns: %q", ErrCorrupt, sql)
	}
	return cols, alias, nil
}

// stripComments removes -- and /* */ comments outside quotes.
func stripComments(s string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			b.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
			b.WriteByte(c)
		case c == '[':
			quote = ']'
			b.WriteByte(c)
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += 2 + end + 1
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// splitTop splits a column list at the commas outside parentheses and quotes.
func splitTop(s string) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']'
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// firstToken returns the first word of a column definition, unquoted, the
// text after it, and whether it was quoted.
func firstToken(def string) (name, rest string, quoted bool) {
	def = strings.TrimSpace(def)
	if def == "" {
		return "", "", false
	}
	closing := map[byte]byte{'"': '"', '`': '`', '[': ']', '\'': '\''}
	if c, ok := closing[def[0]]; ok {
		// A doubled quote inside stands for one.
		var b strings.Builder
		for i := 1; i < len(def); i++ {
			if def[i] == c {
				if c != ']' && i+1 < len(def) && def[i+1] == c {
					b.WriteByte(c)
					i++
					continue
				}
				return b.String(), def[i+1:], true
			}
			b.WriteByte(def[i])
		}
		return b.String(), "", true
	}
	end := strings.IndexAny(def, " \t\r\n(")
	if end < 0 {
		return def, "", false
	}
	return def[:end], def[end:], false
}
