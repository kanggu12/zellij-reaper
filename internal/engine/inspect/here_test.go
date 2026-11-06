package inspect_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

func TestHereIsTheNameTheSessionHasNow(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	dir := testutil.SocketDir(t)
	var socks []procfs.UnixSocket
	// listen gives a server a socket file under name, as the kernel shows it.
	listen := func(pid int, name string) {
		ino, dev, _ := procfs.FileID(testutil.Socket(t, dir, name))
		socks = append(socks, procfs.UnixSocket{Inode: uint32(pid), State: 10, Name: filepath.Join(dir, name), VFSIno: ino, VFSDev: dev})
	}
	// A session started as quiet-magpie and renamed; then a new session took
	// the old name, so its command line now names someone else's socket.
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(dir, "quiet-magpie")}, Sockets: []uint32{100}})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}})
	fp.Add(testutil.Proc{PID: 102, PPID: 101, Comm: "zellij-reaper", Argv: []string{"zellij-reaper", "here"}})
	fp.Add(testutil.Proc{PID: 200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(dir, "quiet-magpie")}, Sockets: []uint32{200}})
	fp.Add(testutil.Proc{PID: 300, PPID: 1, Comm: "bash", Argv: []string{"-bash"}})
	listen(100, "review-the-draft")
	listen(200, "quiet-magpie")
	src := inspect.Sources{ProcRoot: fp.Root, UID: os.Getuid(), Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil }}

	if name, err := inspect.Here(src, 102); err != nil || name != "review-the-draft" {
		t.Errorf("from a pane: %q, %v", name, err)
	}
	if _, err := inspect.Here(src, 300); !errors.Is(err, inspect.ErrNotInSession) {
		t.Errorf("from no session: %v", err)
	}
	// Without the socket table the name cannot be told, and the command
	// line's would be wrong here.
	src.Sockets = nil
	if name, err := inspect.Here(src, 102); !errors.Is(err, inspect.ErrNameUnknown) {
		t.Errorf("without the table: %q, %v", name, err)
	}
}
