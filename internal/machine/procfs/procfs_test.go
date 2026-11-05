package procfs_test

import (
	"os"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestReadAndServers(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	uid := os.Getuid()
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"/usr/local/bin/zellij", "--server", "/run/user/1/zellij/contract_version_1/work"}})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}, Stdin: "/dev/pts/3"})
	// A comm with spaces and parentheses must not break the stat parser.
	fp.Add(testutil.Proc{PID: 102, PPID: 101, Comm: "we(ir)d name", Argv: []string{"weird"}})
	// Someone else's server is not ours.
	fp.Add(testutil.Proc{PID: 200, PPID: 1, UID: uid + 1, Comm: "zellij", Argv: []string{"zellij", "--server", "/tmp/zellij-9/contract_version_1/theirs"}})
	// A grep for the server is not a server.
	fp.Add(testutil.Proc{PID: 300, PPID: 1, Comm: "grep", Argv: []string{"grep", "zellij", "--server", "x"}})
	fp.Add(testutil.Proc{PID: 400, PPID: 100, Comm: "bash", State: "Z"})

	tab, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	servers := tab.Servers(uid)
	if len(servers) != 1 || servers[0].PID != 100 || servers[0].Session() != "work" {
		t.Fatalf("Servers = %+v", servers)
	}
	if p := tab.ByPID[102]; p.Comm != "we(ir)d name" || p.PPID != 101 {
		t.Errorf("odd comm: %+v", p)
	}
	if p := tab.ByPID[101]; p.Name() != "bash" || p.Stdin != "/dev/pts/3" {
		t.Errorf("login shell: %+v name %q", p, p.Name())
	}
	if !tab.ByPID[400].IsZombie() {
		t.Error("zombie not recognized")
	}
	kids := tab.Children(100)
	if len(kids) != 2 || kids[0].PID != 101 || kids[1].PID != 400 {
		t.Errorf("Children = %+v", kids)
	}
	desc := tab.Descendants(100, 10)
	if len(desc) != 3 {
		t.Errorf("Descendants = %d", len(desc))
	}
	if len(tab.Descendants(100, 1)) != 1 {
		t.Error("Descendants ignores its limit")
	}
}

func TestNames(t *testing.T) {
	p := &procfs.Proc{Comm: "node", Argv: []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}}
	if got := strings.Join(p.Names(), ","); got != "node,node,cli.js" {
		t.Errorf("Names = %q", got)
	}
	sh := &procfs.Proc{Comm: "claude", Argv: []string{"/bin/sh", "/usr/local/bin/claude", "--resume"}}
	if got := strings.Join(sh.Names(), ","); got != "claude,sh,claude" {
		t.Errorf("script Names = %q", got)
	}
}

func TestSocketInodes(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Add(testutil.Proc{PID: 10, PPID: 1, Comm: "zellij", Argv: []string{"zellij"}, Stdin: "/dev/null", Sockets: []uint32{777, 888}})
	tab, err := procfs.Read(fp.Root)
	if err != nil {
		t.Fatal(err)
	}
	got := tab.SocketInodes(10)
	if len(got) != 2 || !got[777] || !got[888] {
		t.Errorf("SocketInodes = %v", got)
	}
}

func TestUnixSocketsSeesARealListener(t *testing.T) {
	// Ask the kernel for real: a socket made here must come back with its
	// file's inode, which is how renamed sessions are matched to servers.
	dir := testutil.SocketDir(t)
	path := testutil.Socket(t, dir, "probe")
	ino, dev, ok := procfs.FileID(path)
	if !ok {
		t.Fatal("FileID failed")
	}
	socks, err := procfs.UnixSockets()
	if err == procfs.ErrNoSockDiag {
		t.Skip("this kernel has no unix sock_diag")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range socks {
		if s.VFSIno == ino && s.Listening() {
			if s.VFSDev != dev {
				t.Errorf("device %d from sock_diag, %d from stat", s.VFSDev, dev)
			}
			if s.Name != path {
				t.Errorf("bound name %q, want %q", s.Name, path)
			}
			return
		}
	}
	t.Errorf("the listener at %s (inode %d) was not reported among %d sockets", path, ino, len(socks))
}
