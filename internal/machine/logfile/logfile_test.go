package logfile

import (
	"fmt"
	"os"
	"path/filepath"
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
	for i := 0; i < 400; i++ {
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
