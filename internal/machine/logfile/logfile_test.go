package logfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newLog(t *testing.T, max int64) *Log {
	t.Helper()
	l := New(filepath.Join(t.TempDir(), "reaper.log"), max)
	l.Now = func() time.Time { return time.Date(2026, 11, 4, 9, 31, 0, 0, time.Local) }
	return l
}

func fill(t *testing.T, path string, n int) string {
	t.Helper()
	body := strings.Repeat("[old] filler line\n", n/18+1)[:n]
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheLogIsTheUsersOwnAndRunsNoControls(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	dir := filepath.Join(t.TempDir(), "state")
	l := New(filepath.Join(dir, "reaper.log"), 0)
	// A pane title that would write the clipboard, and a line break.
	if err := l.Printf("%s [RUNNING] panes={%s}", "notes", "x\x1b]52;c;aGk=\x07y\nz"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.HasSuffix(got, "] notes [RUNNING] panes={x\\x1b]52;c;aGk=\\x07y z}\n") {
		t.Errorf("log line %q", got)
	}
	// Pane titles and command lines are in it: for this user only.
	for p, want := range map[string]os.FileMode{dir: 0o700, l.Path: 0o600} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, %v", p, info.Mode().Perm(), err)
		}
	}
	// One an earlier release made 0644 becomes the user's own, with the
	// previous log; a mode the user chose stays.
	for mode, want := range map[os.FileMode]os.FileMode{0o644: 0o600, 0o640: 0o640} {
		for _, p := range []string{l.Path, l.Path + ".1"} {
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Printf("another line"); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{l.Path, l.Path + ".1"} {
			if info, _ := os.Stat(p); info.Mode().Perm() != want {
				t.Errorf("%s of %v became %v", filepath.Base(p), mode, info.Mode().Perm())
			}
		}
	}
}

func TestRotateOverLimit(t *testing.T) {
	l := newLog(t, 1000)
	old := fill(t, l.Path, 1001)
	l.Rotate()
	l.Printf("=== reaper start ===")
	moved, _ := os.ReadFile(l.Path + ".1")
	fresh, _ := os.ReadFile(l.Path)
	if string(moved) != old {
		t.Error("the old log was not moved whole to .1")
	}
	if string(fresh) != "[2026-11-04 09:31:00] === reaper start ===\n" {
		t.Errorf("new log: %q", fresh)
	}
}

func TestNoRotateAtLimitOrOff(t *testing.T) {
	l := newLog(t, 1000)
	fill(t, l.Path, 1000)
	l.Rotate()
	if _, err := os.Stat(l.Path + ".1"); err == nil {
		t.Error("rotated at exactly the limit")
	}
	off := newLog(t, 0)
	fill(t, off.Path, 5000)
	off.Rotate()
	if _, err := os.Stat(off.Path + ".1"); err == nil {
		t.Error("rotated with rotation off")
	}
}

func TestRotateRefusesDirectory(t *testing.T) {
	l := newLog(t, 100)
	fill(t, l.Path, 300)
	if err := os.Mkdir(l.Path+".1", 0o755); err != nil {
		t.Fatal(err)
	}
	l.Rotate()
	entries, _ := os.ReadDir(l.Path + ".1")
	if len(entries) != 0 {
		t.Error("the log was moved into the directory at .1")
	}
	b, _ := os.ReadFile(l.Path)
	if !strings.Contains(string(b), "could not rotate") {
		t.Error("the failed rotation was not logged")
	}
}

func TestBoundOverManyPasses(t *testing.T) {
	// Each pass writes a few lines; the two files together stay within
	// twice the limit plus one pass.
	l := newLog(t, 2000)
	worst := int64(0)
	for i := range 400 {
		l.Rotate()
		l.Printf("=== reaper start ===")
		l.Printf("session-%d [RUNNING] panes={Pane #1} :: SKIP IDLE for 2m, reaped after 1h", i)
		l.Printf("=== reaper end (0 reaped, 1 kept, 0 renamed) ===")
		total := int64(0)
		for _, p := range []string{l.Path, l.Path + ".1"} {
			if info, err := os.Stat(p); err == nil {
				total += info.Size()
			}
		}
		if total > worst {
			worst = total
		}
	}
	if worst > 2*2000+2*200 {
		t.Errorf("log and .1 reached %d bytes", worst)
	}
}

func TestTailAcrossRotation(t *testing.T) {
	l := newLog(t, 0)
	os.WriteFile(l.Path+".1", []byte("a\nb\n"), 0o644)
	os.WriteFile(l.Path, []byte("c\nd\n"), 0o644)
	got, err := Tail(l.Path, 3)
	if err != nil || strings.Join(got, ",") != "b,c,d" {
		t.Errorf("Tail = %q, %v", got, err)
	}
	if _, err := Tail(filepath.Join(t.TempDir(), "none.log"), 3); !os.IsNotExist(err) {
		t.Errorf("missing log: %v", err)
	}
	// A count below zero is no lines, not a slice past its end.
	for _, n := range []int{0, -1, -100} {
		if got, err := Tail(l.Path, n); err != nil || len(got) != 0 {
			t.Errorf("Tail(%d) = %q, %v", n, got, err)
		}
	}
}

func TestParseAndKind(t *testing.T) {
	cases := map[string]string{
		"[2026-11-04 09:31:00] === reaper start (dry_run=false) ===":      "start",
		"[2026-11-04 09:31:00] work [RUNNING] panes={a} :: REAP IDLE, 2h": "reap",
		"[2026-11-04 09:31:00] work [RUNNING] panes={a} :: SKIP busy":     "skip",
		"[2026-11-04 09:31:00]   -> deleted: work":                        "deleted",
		"[2026-11-04 09:31:00]   -> renamed: a → b":                       "renamed",
		"[2026-11-04 09:31:00]   -> DRY_RUN: would rename 'a' → 'b'":      "dry",
		"[2026-11-04 09:31:00]   -> FAILED: work :: boom":                 "failed",
		"[2026-11-04 09:31:00] === reaper end ===":                        "end",
		"no timestamp at all":                                             "",
		// Kept at the last moment: its own kind, also when the reason
		// says something could not be read.
		"[2026-11-04 09:31:00]   -> kept: work :: 1 client(s) attached since the check":                "kept",
		"[2026-11-04 09:31:00]   -> kept: work :: could not ask for its clients again: no answer":      "kept",
		"[2026-11-04 09:31:00]   -> kept name: work :: could not ask for its clients again: no answer": "kept",
		"[2026-11-04 09:31:00]   -> rename FAILED: a → b :: Session with name b already exists":        "failed",
		// recover's steps: a server kept on the second look is kept, whatever
		// its reason says; a step that failed is a failure; one that went
		// through has no kind of its own.
		"[2026-11-04 09:31:00] pid 4242 (first name: work) :: recover: kept orphan server: could not read the socket table again": "kept",
		"[2026-11-04 09:31:00] pid 4242 (first name: work) :: recover: could not stop orphan server: operation not permitted":     "failed",
		"[2026-11-04 09:31:00] work :: recover: zellij could not delete it: exit status 1":                                        "failed",
		"[2026-11-04 09:31:00] work :: recover: could not remove /x/work: unlinkat /x/work/a: permission denied":                  "failed",
		"[2026-11-04 09:31:00] pid 4242 (first name: work) :: recover: stopped orphan server":                                     "",
		"[2026-11-04 09:31:00] work :: recover: deleted with zellij":                                                              "",
		"[2026-11-04 09:31:00] could not read the protected list: read /x: is a directory":                                        "failed",
	}
	for raw, kind := range cases {
		l := Parse(raw)
		if l.Kind() != kind {
			t.Errorf("%q: kind %q, want %q", raw, l.Kind(), kind)
		}
		if strings.HasPrefix(raw, "[") && (l.Time.IsZero() || strings.HasPrefix(l.Text, "[")) {
			t.Errorf("%q: timestamp not split: %+v", raw, l)
		}
	}
}

func TestLastPassSaysHowItEnded(t *testing.T) {
	at := func(s string) time.Time {
		t.Helper()
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	earlier := []string{
		"[2026-11-04 08:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
		"[2026-11-04 08:31:00] === reaper end (2 reaped, 1 kept, 0 renamed) ===",
	}
	for _, c := range []struct {
		name  string
		lines []string
		want  Pass
	}{
		{"no pass at all", []string{"[2026-11-04 09:31:00] notes :: deleted from the dashboard"}, Pass{}},
		{"one that acted", append(earlier,
			"[2026-11-04 09:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
			"[2026-11-04 09:31:00] scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 13h since last activity",
			"[2026-11-04 09:31:01]   -> deleted: scratch",
			"[2026-11-04 09:31:01] === reaper end (1 reaped, 4 kept, 2 renamed) ===",
		), Pass{Found: true, Started: at("2026-11-04 09:31:00"), Ended: true, Acted: true, Reaped: 1, Kept: 4, Renamed: 2}},
		// One that stopped before it acted says why on the line before its end.
		{"one that stopped", append(earlier,
			"[2026-11-04 09:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
			"[2026-11-04 09:31:10] zellij list-sessions failed: zellij list-sessions --no-formatting: no answer within 10s",
			"[2026-11-04 09:31:10] === reaper end ===",
		), Pass{Found: true, Started: at("2026-11-04 09:31:00"), Ended: true, Stopped: "zellij list-sessions failed: zellij list-sessions --no-formatting: no answer within 10s"}},
		// A pass ended from outside wrote no end line.
		{"one with no end", append(earlier,
			"[2026-11-04 09:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
			"[2026-11-04 09:31:00] scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 13h since last activity",
		), Pass{Found: true, Started: at("2026-11-04 09:31:00")}},
		// A names pass and an action by hand after the last pass change
		// nothing about it.
		{"a names pass after it", append(earlier,
			"[2026-11-04 08:35:00] === names start ===",
			"[2026-11-04 08:35:00]   -> renamed: brave-peach → notes",
			"[2026-11-04 08:35:00] === names end (1 renamed) ===",
			"[2026-11-04 08:36:00] notes :: name kept from the command line",
		), Pass{Found: true, Started: at("2026-11-04 08:31:00"), Ended: true, Acted: true, Reaped: 2, Kept: 1}},
		// The deletes zellij refused are counted: they are among the kept.
		{"one with a failed delete", append(earlier,
			"[2026-11-04 09:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
			"[2026-11-04 09:31:00] scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 13h since last activity",
			"[2026-11-04 09:31:01]   -> FAILED: scratch :: zellij delete-session scratch --force: exit status 1",
			"[2026-11-04 09:31:01] gone [EXITED] panes={} :: REAP EXITED, 4d since it exited",
			"[2026-11-04 09:31:01]   -> deleted: gone",
			"[2026-11-04 09:31:01] busy [RUNNING] panes={htop} :: KEEP BUSY, htop",
			"[2026-11-04 09:31:01]   -> rename FAILED: busy → htop :: the name is taken",
			"[2026-11-04 09:31:01] === reaper end (1 reaped, 2 kept, 0 renamed) ===",
		), Pass{Found: true, Started: at("2026-11-04 09:31:00"), Ended: true, Acted: true, Reaped: 1, Kept: 2, Failed: 1}},
		// A log from before renames ends a pass with two counts.
		{"two counts", []string{
			"[2026-11-04 09:31:00] === reaper start (dry_run=false max_age=72h) ===",
			"[2026-11-04 09:31:00] === reaper end (3 reaped, 1 kept) ===",
		}, Pass{Found: true, Started: at("2026-11-04 09:31:00"), Ended: true, Acted: true, Reaped: 3, Kept: 1}},
	} {
		if got := LastPass(c.lines); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestWritableSaysWhyALineCouldNotBeAdded(t *testing.T) {
	dir := t.TempDir()
	// A log that is there, and one not made yet in a directory, or in
	// directories that are not made yet either: a pass makes those.
	there := filepath.Join(dir, "reaper.log")
	if err := os.WriteFile(there, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{there, filepath.Join(dir, "new.log"), filepath.Join(dir, "a", "b", "reaper.log")} {
		if err := Writable(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	// Nothing was made or changed on the way.
	if b, _ := os.ReadFile(there); string(b) != "x\n" {
		t.Errorf("the log is now %q", b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries in the directory", len(entries))
	}
	// A directory where the log would be, and a file where its directory
	// would be: no pass can write either, whoever runs it.
	if err := os.Mkdir(filepath.Join(dir, "logdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "logdir"), filepath.Join(there, "reaper.log")} {
		if err := Writable(p); err == nil {
			t.Errorf("%s: no error", p)
		}
	}
}

func TestWritableSaysWhenTheFileSystemIsFull(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "reaper.log")
	if err := os.WriteFile(log, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The real reading works here, and finds room.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if n, err := spaceLeft(dir); err != nil || n == 0 {
			t.Errorf("space left in %s: %d, %v", dir, n, err)
		}
	}
	defer func(f func(string) (uint64, error)) { freeBytes = f }(freeBytes)
	// 4 KiB left: the log still opens, and no pass's lines fit. Measured in
	// a container, a pass on a full file system wrote nothing and ended with
	// status 0, and doctor said the log was fine.
	freeBytes = func(string) (uint64, error) { return 4096, nil }
	for _, p := range []string{log, filepath.Join(dir, "new", "reaper.log")} {
		if err := Writable(p); err == nil || err.Error() != "only 4096 bytes left on the file system "+dir+" is on" {
			t.Errorf("%s on a full file system: %v", p, err)
		}
	}
	// Room enough, or room that cannot be read: nothing to say.
	freeBytes = func(string) (uint64, error) { return minRoom, nil }
	if err := Writable(log); err != nil {
		t.Errorf("with room: %v", err)
	}
	freeBytes = func(string) (uint64, error) { return 0, errors.New("statfs: not supported") }
	if err := Writable(log); err != nil {
		t.Errorf("with the room unknown: %v", err)
	}
}

func TestWritableSaysADirectoryMayNotBeWritten(t *testing.T) {
	// Root writes anywhere, and Windows does not go by a directory's mode:
	// the case comes up for neither.
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("a directory's mode keeps nothing from root, or on Windows")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	// Writable again for the test's own cleanup.
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	// The log is not made yet, and the directory it goes in may not be
	// written: a pass could not make it.
	if err := Writable(filepath.Join(dir, "reaper.log")); err == nil || !strings.Contains(err.Error(), dir) {
		t.Errorf("a read-only directory: %v", err)
	}
}

func TestTailReadsOnlyTheEndOfALargeLog(t *testing.T) {
	l := newLog(t, 0)
	// 5 MiB of numbered lines: more than Tail reads.
	f, _ := os.Create(l.Path)
	n := 0
	for size := 0; size < 5<<20; n++ {
		line := fmt.Sprintf("[2026-11-04 09:31:00] line %07d\n", n)
		f.WriteString(line)
		size += len(line)
	}
	f.Close()
	got, err := Tail(l.Path, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		fmt.Sprintf("[2026-11-04 09:31:00] line %07d", n-3),
		fmt.Sprintf("[2026-11-04 09:31:00] line %07d", n-2),
		fmt.Sprintf("[2026-11-04 09:31:00] line %07d", n-1),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("Tail = %q, want %q", got, want)
	}
}
