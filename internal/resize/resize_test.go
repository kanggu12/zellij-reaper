package resize_test

import (
	"os"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/procfs"
	"github.com/kanggu12/zellij-reaper/internal/resize"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestTargets(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Add(testutil.Proc{PID: 10, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "attach", "work"}})
	fp.Add(testutil.Proc{PID: 20, PPID: 1, Comm: "zellij", Argv: []string{"/usr/bin/zellij", "--server", "/run/user/1/zellij/contract_version_1/work"}})
	fp.Add(testutil.Proc{PID: 21, PPID: 20, Comm: "bash", Argv: []string{"-bash"}})
	fp.Add(testutil.Proc{PID: 22, PPID: 21, Comm: "vim", Argv: []string{"vim"}})
	fp.Add(testutil.Proc{PID: 30, PPID: 1, Comm: "zellij-reaper", Argv: []string{"zellij-reaper", "resize"}})
	fp.Add(testutil.Proc{PID: 40, PPID: 1, Comm: "zellij", Argv: []string{"zellij"}, UID: os.Getuid() + 1})
	tab, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	roles := func(ts []resize.Target) map[int]string {
		m := map[int]string{}
		for _, x := range ts {
			m[x.PID] = x.Role
		}
		return m
	}
	got := roles(resize.Targets(tab, os.Getuid(), 30, false))
	want := map[int]string{10: "client", 20: "server", 21: "pane", 22: "pane"}
	if len(got) != len(want) {
		t.Fatalf("targets %v", got)
	}
	for pid, role := range want {
		if got[pid] != role {
			t.Errorf("pid %d: %q, want %q", pid, got[pid], role)
		}
	}
	if shallow := roles(resize.Targets(tab, os.Getuid(), 30, true)); len(shallow) != 1 || shallow[10] != "client" {
		t.Errorf("shallow %v", shallow)
	}
}
