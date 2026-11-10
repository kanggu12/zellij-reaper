package inspect

import (
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestTheServersSocketsAreReadSideBySideAndMergedInOrder(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	// More servers than workers, each with a socket of its own; the first
	// two also hold one socket both, as a forked server might.
	var servers []procfs.Server
	for i := range 20 {
		pid := 100 + i
		socks := []uint32{uint32(1000 + i)}
		if i < 2 {
			socks = append(socks, 7)
		}
		fp.Add(testutil.Proc{PID: pid, PPID: 1, Comm: "zellij", Sockets: socks})
		servers = append(servers, procfs.Server{PID: pid})
	}
	table, err := procfs.ReadSessions(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	owners := socketOwners(table, servers)
	for i := range 20 {
		if got := owners[uint32(1000+i)]; got != 100+i {
			t.Errorf("socket %d: server %d, want %d", 1000+i, got, 100+i)
		}
	}
	// The later server, as when they were read one after the other.
	if owners[7] != 101 {
		t.Errorf("the socket both hold: server %d, want 101", owners[7])
	}
	if len(owners) != 21 {
		t.Errorf("%d sockets", len(owners))
	}
}
