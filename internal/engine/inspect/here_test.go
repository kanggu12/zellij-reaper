package inspect_test

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
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

func TestHereNamesASessionKeptByIDFromTheIndex(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	dir := testutil.SocketDir(t)
	// zellij 0.46 names the socket by an id, which a rename keeps; the index
	// has the name the session has now.
	ino, dev, _ := procfs.FileID(testutil.Socket(t, dir, "0a1b2c3d4e5f"))
	socks := []procfs.UnixSocket{{Inode: 100, State: 10, Name: filepath.Join(dir, "0a1b2c3d4e5f"), VFSIno: ino, VFSDev: dev}}
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(dir, "0a1b2c3d4e5f")}, Sockets: []uint32{100}})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}})
	idx := zellij.NewIndex(map[string]string{"review-the-draft": "0a1b2c3d4e5f"})
	src := inspect.Sources{ProcRoot: fp.Root, UID: os.Getuid(), Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil }, Index: &idx}
	if name, err := inspect.Here(src, 101); err != nil || name != "review-the-draft" {
		t.Errorf("here = %q, %v", name, err)
	}
}

func TestHereReadsTheIndexOfTheZellijItsServerIs(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	cache := t.TempDir()
	// This server's zellij writes contract_version_1, whose index names the
	// session; a newer zellij that ran once left contract_version_2, with an
	// index this one cannot read.
	for v, index := range map[string][]byte{"contract_version_1": fixture(t, "plain.db"), "contract_version_2": []byte("not a database")} {
		info := filepath.Join(cache, v, "session_info")
		if err := os.MkdirAll(info, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(zellij.IndexFile(info), index, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A short folder: macOS caps a socket path at 103 bytes.
	dir := filepath.Join(testutil.SocketDir(t), "contract_version_1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ino, dev, _ := procfs.FileID(testutil.Socket(t, dir, "a1b2c3d4e5f6"))
	socks := []procfs.UnixSocket{{Inode: 100, State: 10, Name: filepath.Join(dir, "a1b2c3d4e5f6"), VFSIno: ino, VFSDev: dev}}
	fp.Add(testutil.Proc{PID: 100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(dir, "a1b2c3d4e5f6")}, Sockets: []uint32{100}})
	fp.Add(testutil.Proc{PID: 101, PPID: 100, Comm: "bash", Argv: []string{"-bash"}})
	// here starts from the newest folder: it does not ask zellij its version.
	src := inspect.Sources{ProcRoot: fp.Root, UID: os.Getuid(), Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil },
		InfoDir: zellij.SessionInfoDir(cache, "")}
	if name, err := inspect.Here(src, 101); err != nil || name != "api-server" {
		t.Errorf("here = %q, %v", name, err)
	}
}

// fixture is a database from the sqlite package's testdata.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "machine", "sqlite", "testdata", name+".gz"))
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
	return b
}
