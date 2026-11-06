// Package testutil builds stand-ins for what the reaper reads, so tests can
// describe a whole machine (processes, terminals, sockets, zellij's files)
// without touching the real one.
package testutil

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Proc describes one fake process.
type Proc struct {
	PID, PPID int
	// UID defaults to the test's own uid.
	UID  int
	Comm string
	Argv []string
	// Stdin is where fd 0 points; empty leaves no fd 0.
	Stdin string
	// State defaults to "S" (sleeping); "Z" makes a zombie.
	State string
	// Sockets are socket inodes the process holds, as fd 3, 4, ...
	Sockets []uint32
	// Cwd is the directory the process works in; empty leaves no link.
	Cwd string
	// CTTY is the device of its controlling terminal; 0 means none, like a
	// helper a server starts.
	CTTY int
	// Started is when it started, counted from boot (see FakeProc.Uptime).
	Started time.Duration
}

// FakeProc is a directory laid out like /proc.
type FakeProc struct {
	Root string
	t    *testing.T
}

// NewFakeProc creates an empty fake /proc.
func NewFakeProc(t *testing.T) *FakeProc {
	t.Helper()
	SkipOnWindows(t)
	root := filepath.Join(t.TempDir(), "proc")
	// Like the real /proc, the directory exists even with no process in it.
	must(t, os.MkdirAll(root, 0o755))
	return &FakeProc{Root: root, t: t}
}

// Add writes a process into the tree.
func (f *FakeProc) Add(p Proc) {
	f.t.Helper()
	if p.State == "" {
		p.State = "S"
	}
	if p.UID == 0 {
		p.UID = os.Getuid()
	}
	dir := filepath.Join(f.Root, strconv.Itoa(p.PID))
	must(f.t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
	// Of the fields after ppid, only tty_nr and starttime (in 1/100 s) are
	// read; zeros keep the shape real.
	must(f.t, os.WriteFile(filepath.Join(dir, "stat"),
		[]byte(fmt.Sprintf("%d (%s) %s %d 0 0 %d -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 %d\n",
			p.PID, p.Comm, p.State, p.PPID, p.CTTY, p.Started/(10*time.Millisecond))), 0o644))
	must(f.t, os.WriteFile(filepath.Join(dir, "status"),
		[]byte(fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", p.Comm, p.UID, p.UID, p.UID, p.UID)), 0o644))
	argv := ""
	if len(p.Argv) > 0 {
		argv = strings.Join(p.Argv, "\x00") + "\x00"
	}
	must(f.t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o644))
	if p.Stdin != "" {
		must(f.t, os.Symlink(p.Stdin, filepath.Join(dir, "fd", "0")))
	}
	for i, ino := range p.Sockets {
		must(f.t, os.Symlink(fmt.Sprintf("socket:[%d]", ino), filepath.Join(dir, "fd", strconv.Itoa(3+i))))
	}
	if p.Cwd != "" {
		must(f.t, os.Symlink(p.Cwd, filepath.Join(dir, "cwd")))
	}
}

// Uptime sets how long the fake machine has been up.
func (f *FakeProc) Uptime(d time.Duration) {
	f.t.Helper()
	must(f.t, os.WriteFile(filepath.Join(f.Root, "uptime"), []byte(fmt.Sprintf("%.2f 0.00\n", d.Seconds())), 0o644))
}

// AddSocket gives a process one more socket, which is what a server gets when
// a client connects.
func (f *FakeProc) AddSocket(pid int, ino uint32) {
	f.t.Helper()
	dir := filepath.Join(f.Root, strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	must(f.t, err)
	must(f.t, os.Symlink(fmt.Sprintf("socket:[%d]", ino), filepath.Join(dir, strconv.Itoa(10+len(entries)))))
}

// TTY creates a file that stands in for a pane's terminal, last written at
// the given time.
func TTY(t *testing.T, dir, name string, at time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	must(t, os.WriteFile(p, nil, 0o644))
	must(t, os.Chtimes(p, at, at))
	return p
}

// SocketDir returns a short temporary directory for socket files: Linux
// limits a socket path to 107 bytes, which t.TempDir() can exceed.
func SocketDir(t *testing.T) string {
	t.Helper()
	SkipOnWindows(t)
	d, err := os.MkdirTemp("", "zr")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// Socket creates a real listening socket file at dir/name, as a zellij
// server does, and closes it when the test ends.
func Socket(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	l, err := net.Listen("unix", p)
	must(t, err)
	// Keep the file when the listener closes, as zellij leaves it behind.
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	t.Cleanup(func() { l.Close() })
	return p
}

// SessionFiles writes a session's directory under session_info, with its
// files last modified at the given time.
func SessionFiles(t *testing.T, infoDir, name, metadata, layout string, at time.Time) {
	t.Helper()
	dir := filepath.Join(infoDir, name)
	must(t, os.MkdirAll(dir, 0o755))
	if metadata != "" {
		p := filepath.Join(dir, "session-metadata.kdl")
		must(t, os.WriteFile(p, []byte(metadata), 0o644))
		must(t, os.Chtimes(p, at, at))
	}
	if layout != "" {
		p := filepath.Join(dir, "session-layout.kdl")
		must(t, os.WriteFile(p, []byte(layout), 0o644))
		must(t, os.Chtimes(p, at, at))
	}
}

// Metadata returns a minimal session-metadata.kdl with the given terminal
// pane titles and client count.
func Metadata(clients int, titles ...string) string {
	var b strings.Builder
	b.WriteString("name \"x\"\npanes {\n")
	for _, t := range titles {
		fmt.Fprintf(&b, "    pane {\n        is_plugin false\n        title %q\n        exited false\n    }\n", t)
	}
	b.WriteString("    pane {\n        is_plugin true\n        title \"tab-bar\"\n    }\n}\n")
	fmt.Fprintf(&b, "connected_clients %d\n", clients)
	return b.String()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// SkipOnWindows skips a test that sets up a Linux machine: a /proc and Unix
// socket files. Windows has neither; the platforms workflow checks it on real
// zellij sessions instead.
func SkipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a fake Linux machine: /proc and Unix sockets")
	}
}
