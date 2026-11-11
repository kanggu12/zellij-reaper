package procfs

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestAStartLongAfterBootKeepsItsValue(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	// A process started 3.2 years after boot: its start in clock ticks,
	// times a second, is past what an int64 holds.
	long := time.Duration(3.2 * 365 * 24 * float64(time.Hour)).Truncate(10 * time.Millisecond)
	fp.Add(testutil.Proc{PID: 41, PPID: 1, Comm: "make", Started: long + 250*time.Millisecond})
	if m, ok := StartMark(fp.Root, 41); !ok || m != int64(long+250*time.Millisecond) {
		t.Errorf("StartMark = %d, %v; want %d", m, ok, int64(long+250*time.Millisecond))
	}
}

func TestStartMarkOfTakesTheStartTheReaderGave(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Add(testutil.Proc{PID: 42, PPID: 1, Comm: "make", Started: 90 * time.Second})
	table, err := Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	// The /proc reader gives no start: stat is read for it, as StartMark does.
	want, _ := StartMark(fp.Root, 42)
	if m, ok := StartMarkOf(fp.Root, table.ByPID[42]); !ok || m != want {
		t.Errorf("from stat: %d, %v; want %d", m, ok, want)
	}
	// A native reader's start is taken as it is, and nothing is read: the
	// process is in no table on disk.
	at := time.Unix(1_700_000_000, 5)
	if m, ok := StartMarkOf(fp.Root, &Proc{PID: 99999999, started: at}); !ok || m != at.UnixNano() {
		t.Errorf("from the reader: %d, %v", m, ok)
	}
}
