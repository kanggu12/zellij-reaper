package procfs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func uid() int { return os.Getuid() }

func TestYoungestServerFindsAServerStillStarting(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Uptime(100 * time.Second)
	server := func(pid int, started time.Duration) {
		fp.Add(testutil.Proc{PID: pid, PPID: 1, Comm: "zellij", Started: started,
			Argv: []string{"zellij", "--server", "/tmp/zellij-0/contract_version_1/s"}})
	}
	if _, ok := procfs.YoungestServer(fp.Root, uid()); ok {
		t.Error("found a server in an empty table")
	}
	server(10, 50*time.Second)
	// An old server is past the danger, and so is reported as none.
	if _, ok := procfs.YoungestServer(fp.Root, uid()); ok {
		t.Error("an old server counted as young")
	}
	// A client started a moment ago is no server.
	fp.Add(testutil.Proc{PID: 11, PPID: 1, Comm: "zellij", Started: 99900 * time.Millisecond,
		Argv: []string{"zellij", "attach", "--create-background", "s"}})
	if _, ok := procfs.YoungestServer(fp.Root, uid()); ok {
		t.Error("a client counted as a server")
	}
	server(12, 99700*time.Millisecond)
	server(13, 99800*time.Millisecond)
	age, ok := procfs.YoungestServer(fp.Root, uid())
	if !ok || age != 200*time.Millisecond {
		t.Errorf("youngest: %v %v, want 200ms", age, ok)
	}
	// Someone else's young server is not ours to wait for.
	if _, ok := procfs.YoungestServer(fp.Root, uid()+1); ok {
		t.Error("another user's server counted")
	}
}

func TestSettleWaitsOnlyForAStartingServer(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Uptime(100 * time.Second)
	fp.Add(testutil.Proc{PID: 10, PPID: 1, Comm: "zellij", Started: 50 * time.Second,
		Argv: []string{"zellij", "--server", "/tmp/zellij-0/contract_version_1/old"}})
	start := time.Now()
	procfs.Settle(context.Background(), fp.Root, uid())
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("waited %v with no server starting", d)
	}
	// A fake clock does not move, so a young server makes Settle wait its
	// whole bound: what matters is that it waits, and that it ends.
	fp.Add(testutil.Proc{PID: 12, PPID: 1, Comm: "zellij", Started: 99900 * time.Millisecond,
		Argv: []string{"zellij", "--server", "/tmp/zellij-0/contract_version_1/new"}})
	start = time.Now()
	procfs.Settle(context.Background(), fp.Root, uid())
	if d := time.Since(start); d < 800*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v for a server 100ms old", d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	procfs.Settle(ctx, fp.Root, uid())
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("a cancelled wait took %v", d)
	}
}
