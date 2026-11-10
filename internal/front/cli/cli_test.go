package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/repair"
	"github.com/kanggu12/zellij-reaper/internal/engine/resize"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

// isolate points everything a command could read from the environment at a
// temporary home, so no test reads or writes this machine's settings, log or
// protected list, and none can start the real zellij.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "ZELLIJ_REAPER_CONFIG"} {
		t.Setenv(k, "")
	}
	for k := range config.EnvNames {
		t.Setenv(config.EnvNames[k], "")
	}
	t.Setenv("ZELLIJ_REAPER_ZELLIJ", filepath.Join(home, "no-zellij"))
	i18n.Set(i18n.English)
	return home
}

// output is a command whose output goes to a buffer.
func output() (*cobra.Command, *bytes.Buffer) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetContext(context.Background())
	return cmd, &buf
}

// fakeApp is an App on fakes: zellij answers with no sessions, systemd with
// an empty user instance, and env holds the settings' environment.
func fakeApp(t *testing.T, env config.MapEnv) *app.App {
	t.Helper()
	cfg, err := config.Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := protect.Load(config.ProtectedFile(env))
	if err != nil {
		t.Fatal(err)
	}
	zj := &zellij.CLI{Bin: "zellij", Timeout: time.Second, Run: func(_ context.Context, args ...string) (string, string, error) {
		if args[0] == "--version" {
			return "zellij 0.45.1\n", "", nil
		}
		return "", "No active zellij sessions found.", errors.New("exit status 1")
	}}
	a := &app.App{Env: env, Config: cfg, Protected: pl, Zellij: zj, InfoDir: filepath.Join(env.Home(), "session_info")}
	a.Reaper = &reaper.Env{Config: cfg, Protected: pl, Zellij: zj, Log: logfile.New(cfg.LogPath, cfg.LogMaxBytes), LockPath: config.LockFile(env), Now: time.Now}
	a.System = timers.Systemd{Env: env, User: "u", Run: func(name string, args ...string) (string, error) { return "", nil }}
	return a
}

func TestSettingsFromTheEnvironmentAreNamedAsUnseenByTheTimer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the timer here is systemd's, which Windows does not have")
	}
	home := isolate(t)
	env := config.MapEnv{"HOME": home, "ZELLIJ_REAPER_DRY_RUN": "1", "ZELLIJ_REAPER_IDLE_AFTER": "2h"}
	want := []string{
		"ZELLIJ_REAPER_DRY_RUN sets dry_run here, but the timer does not see it",
		"ZELLIJ_REAPER_IDLE_AFTER sets idle_after here, but the timer does not see it",
	}

	// install says so after it set the timer up.
	a := fakeApp(t, env)
	cmd, out := output()
	if err := install(cmd, a, func() (*app.App, error) { return a, nil }); err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if !strings.Contains(out.String(), w) {
			t.Errorf("install output lacks %q:\n%s", w, out)
		}
	}

	// doctor counts each as a warning.
	cmd, out = output()
	_, warnings := doctor(context.Background(), fakeApp(t, env), cmd)
	for _, w := range want {
		if !strings.Contains(out.String(), w) {
			t.Errorf("doctor output lacks %q:\n%s", w, out)
		}
	}
	// Without the variables there is nothing to say, and one warning less
	// for each.
	cmd, out = output()
	_, fewer := doctor(context.Background(), fakeApp(t, config.MapEnv{"HOME": home}), cmd)
	if strings.Contains(out.String(), "the timer does not see it") || fewer != warnings-2 {
		t.Errorf("doctor without the variables: %d warnings (with them %d):\n%s", fewer, warnings, out)
	}
}

func TestInstallMakesAnOlderReleasesFilesTheUsersOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no Unix modes")
	}
	home := isolate(t)
	env := config.MapEnv{"HOME": home}
	a := fakeApp(t, env)
	// The state folder and a list, as 0.12 made them.
	state := config.StateDir(env)
	list := filepath.Join(state, "protected")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for p, mode := range map[string]os.FileMode{state: 0o755, list: 0o644} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	cmd, out := output()
	if err := install(cmd, a, func() (*app.App, error) { return a, nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 files and folders of zellij-reaper made yours only (0600, 0700)") {
		t.Errorf("install said:\n%s", out)
	}
	for p, want := range map[string]os.FileMode{state: 0o700, list: 0o600} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v (%v)", p, info.Mode().Perm(), err)
		}
	}
}

func TestDoctorSaysWhenThisShellKeepsItsFilesElsewhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the timer here is systemd's, which Windows does not have")
	}
	home := isolate(t)
	defer func(g string) { zellij.GOOS = g }(zellij.GOOS)
	zellij.GOOS = "linux"
	// loaded is an App whose systemd has the timer loaded and running, as
	// it is once install wrote the units.
	loaded := func(env config.MapEnv) *app.App {
		a := fakeApp(t, env)
		a.System = timers.Systemd{Env: env, User: "u", Run: func(_ string, args ...string) (string, error) {
			if slices.Contains(args, "show") {
				return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\n", nil
			}
			return "", nil
		}}
		return a
	}
	// The installing shell keeps the reaper's state in a place of its own.
	// Its TMPDIR decides nothing next to a runtime directory.
	shell := config.MapEnv{"HOME": home, "XDG_RUNTIME_DIR": "/run/user/1000", "XDG_STATE_HOME": filepath.Join(home, "state-a"), "TMPDIR": "/tmp/build-shell"}
	a := loaded(shell)
	cmd, out := output()
	if err := install(cmd, a, func() (*app.App, error) { return a, nil }); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	unit, err := os.ReadFile(filepath.Join(config.UnitDir(shell), timers.ServiceName))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{`Environment="XDG_STATE_HOME=` + filepath.Join(home, "state-a") + `"`, "UnsetEnvironment=XDG_CACHE_HOME", "UnsetEnvironment=TMPDIR"} {
		if !strings.Contains(string(unit), line+"\n") {
			t.Errorf("the service lacks %s:\n%s", line, unit)
		}
	}
	// From the same shell there is nothing to say.
	cmd, out = output()
	_, warnings := doctor(context.Background(), loaded(shell), cmd)
	if strings.Contains(out.String(), "differs") {
		t.Errorf("doctor from the installing shell:\n%s", out)
	}
	// Another shell keeps its state and zellij's cache elsewhere: the
	// dashboard there would protect sessions in a list the timer never reads.
	other := config.MapEnv{"HOME": home, "XDG_RUNTIME_DIR": "/run/user/1000", "XDG_STATE_HOME": filepath.Join(home, "state-b"), "XDG_CACHE_HOME": filepath.Join(home, "cache-b")}
	cmd, out = output()
	_, more := doctor(context.Background(), loaded(other), cmd)
	for _, w := range []string{
		"XDG_STATE_HOME differs: the timer has ~/state-a, this shell ~/state-b; run `zellij-reaper install` again from this shell",
		"XDG_CACHE_HOME differs: the timer has none, this shell ~/cache-b; run `zellij-reaper install` again from this shell",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("doctor output lacks %q:\n%s", w, out)
		}
	}
	if more != warnings+2 {
		t.Errorf("%d warnings from the other shell, %d from the installing one:\n%s", more, warnings, out)
	}
	// A shell whose units live elsewhere cannot read this timer's: it says
	// nothing about what it cannot see.
	moved := config.MapEnv{"HOME": home, "XDG_RUNTIME_DIR": "/run/user/1000", "XDG_CONFIG_HOME": filepath.Join(home, "config-c")}
	cmd, out = output()
	doctor(context.Background(), loaded(moved), cmd)
	if strings.Contains(out.String(), "the timer has") {
		t.Errorf("doctor without the unit files:\n%s", out)
	}
}

func TestUninstallSaysWhetherThereWereTimers(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	if _, err := a.System.Install(timers.InstallOptions{Exe: "/x/zellij-reaper", Config: a.Config}); err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	if err := uninstall(cmd, a, false, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := out.String(); !strings.Contains(got, "Removed the timers.") || strings.Contains(got, "Found no timers") {
		t.Errorf("uninstall with the timers installed:\n%s", got)
	}
	// A second uninstall has no timers to remove, and says so. With --purge
	// the settings still go, and the line about keeping them is left out.
	cmd, out = output()
	if err := uninstall(cmd, a, true, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got := out.String()
	if !strings.Contains(got, "Found no timers to remove.") || strings.Contains(got, "Removed the timers.") {
		t.Errorf("uninstall with no timers left:\n%s", got)
	}
	if !strings.Contains(got, filepath.Base(config.ConfigFile(a.Env))) || strings.Contains(got, "Kept the settings") {
		t.Errorf("the purge of an uninstall with no timers left:\n%s", got)
	}
}

func TestDoctorCountsOneSessionAsOne(t *testing.T) {
	z := &fakeSessions{listed: "notes [Created 1m ago]\n"}
	a := handApp(t, z)
	cmd, out := output()
	doctor(context.Background(), a, cmd)
	if got := out.String(); !strings.Contains(got, "1 session, data in") {
		t.Errorf("doctor with one session:\n%s", got)
	}
	z.listed += "build [Created 2m ago]\n"
	cmd, out = output()
	doctor(context.Background(), a, cmd)
	if got := out.String(); !strings.Contains(got, "2 sessions, data in") {
		t.Errorf("doctor with two sessions:\n%s", got)
	}
}

func TestLogsRefusesANegativeCount(t *testing.T) {
	home := isolate(t)
	// A log to read, so a count that slipped through would reach Tail.
	logPath := filepath.Join(home, ".local", "state", "zellij-reaper", "reaper.log")
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	os.WriteFile(logPath, []byte("[2026-11-04 09:00:00] === reaper end ===\n"), 0o644)
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"logs", "-n", "-1"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--lines") {
		t.Errorf("logs -n -1: %v\n%s", err, out.String())
	}
}

// recoverMachine is a fake machine for recover: a session whose name is too
// long for its socket path, and an orphan server nothing keeps. Its pids are
// above the kernel's limit, so not even a broken fake could signal a real
// process.
type recoverMachine struct {
	in      repair.Inputs
	zj      *recoverZellij
	signals []string
	// logged is every line recover handed to the decision log.
	logged []string
	// fp and socks are its processes and the kernel's sockets, for a test to
	// add a server to or to take a socket file from.
	fp    *testutil.FakeProc
	socks []procfs.UnixSocket
}

type recoverZellij struct {
	deleted []string
	// fail is what every delete returns, when set.
	fail error
}

func (z *recoverZellij) ListSessions(context.Context) ([]zellij.Listed, error) { return nil, nil }
func (z *recoverZellij) Delete(_ context.Context, name string) error {
	if z.fail != nil {
		return z.fail
	}
	z.deleted = append(z.deleted, name)
	return nil
}

func newRecoverMachine(t *testing.T) *recoverMachine {
	t.Helper()
	fp := testutil.NewFakeProc(t)
	m := &recoverMachine{zj: &recoverZellij{}, fp: fp}
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	testutil.SessionFiles(t, info, strings.Repeat("x", 120), testutil.Metadata(0, "x"), "", time.Now())
	// One healthy server, one whose socket file is gone.
	healthy, _, _ := procfs.FileID(testutil.Socket(t, sockDir, "healthy"))
	fp.Add(testutil.Proc{PID: 5_000_100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "healthy")}, Sockets: []uint32{1}})
	fp.Add(testutil.Proc{PID: 5_000_200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "gone")}, Sockets: []uint32{2}})
	_, dev, _ := procfs.FileID(filepath.Join(sockDir, "healthy"))
	m.socks = []procfs.UnixSocket{{Inode: 1, State: 10, VFSIno: healthy, VFSDev: dev}, {Inode: 2, State: 10, VFSIno: 999999, VFSDev: dev}}
	m.in = repair.Inputs{Zellij: m.zj, ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
		// Read at each scan, so a test can change them after this.
		Sockets: func() ([]procfs.UnixSocket, error) { return m.socks, nil },
		Log:     func(line string) { m.logged = append(m.logged, line) },
		Signal: func(pid int, sig syscall.Signal) error {
			// The process is gone once it got SIGTERM.
			if slices.Contains(m.signals, "TERM") {
				return syscall.ESRCH
			}
			if sig == syscall.SIGTERM {
				m.signals = append(m.signals, "TERM")
			}
			return nil
		}}
	return m
}

func TestRecoverWaitsForThePassLock(t *testing.T) {
	testutil.SkipOnWindows(t)
	isolate(t)
	lockPath := filepath.Join(t.TempDir(), "lock")
	held, err := lock.Acquire(lockPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A pass holds the lock: a real recover waits, then gives up having
	// changed nothing.
	m := newRecoverMachine(t)
	cmd, out := output()
	start := time.Now()
	// It says so as the dashboard does, in the reader's language.
	if err := recoverSessions(cmd, lockPath, m.in, false, true); !errors.Is(err, lock.ErrBusy) || err.Error() != "Another pass is running; try again in a moment" {
		t.Errorf("recover with the lock held: %v\n%s", err, out)
	}
	if time.Since(start) < reaper.LockWait-time.Second {
		t.Errorf("gave up after %v without waiting for the pass", time.Since(start))
	}
	if len(m.zj.deleted)+len(m.signals)+len(m.logged) != 0 {
		t.Errorf("changed things without the lock: deleted %v, signals %v, logged %q", m.zj.deleted, m.signals, m.logged)
	}
	// A dry run changes nothing, so it does not wait. The decision log is
	// for what was done: a dry run leaves it as it is.
	cmd, out = output()
	if err := recoverSessions(cmd, lockPath, m.in, true, true); err != nil || !strings.Contains(out.String(), "dry run: nothing changed") {
		t.Errorf("dry run with the lock held: %v\n%s", err, out)
	}
	if len(m.logged) != 0 {
		t.Errorf("a dry run wrote to the log: %q", m.logged)
	}
	// Once the pass is done, recover repairs.
	held.Release()
	cmd, out = output()
	if err := recoverSessions(cmd, lockPath, m.in, false, true); err != nil {
		t.Fatal(err)
	}
	if len(m.zj.deleted) != 1 || len(m.signals) != 1 {
		t.Errorf("after the pass: deleted %v, signals %v\n%s", m.zj.deleted, m.signals, out)
	}
	// Each step it printed is in the log too, in the same order: the server
	// with the session it is, then the name and its files.
	long := strings.Repeat("x", 120)
	want := []string{
		"pid 5000200 (first name: gone) :: recover: stopped orphan server",
		long + " :: recover: deleted with zellij",
		long + " :: recover: removed " + filepath.Join(m.in.InfoDir, long),
	}
	if !slices.Equal(m.logged, want) {
		t.Errorf("the log holds\n%s\nwant\n%s", strings.Join(m.logged, "\n"), strings.Join(want, "\n"))
	}
}

func TestRecoverSaysWhichSessionAnOrphanIs(t *testing.T) {
	testutil.SkipOnWindows(t)
	isolate(t)
	lockPath := filepath.Join(t.TempDir(), "lock")
	m := newRecoverMachine(t)
	// said is what a dry run prints about the machine as it is now.
	said := func() string {
		t.Helper()
		cmd, out := output()
		if err := recoverSessions(cmd, lockPath, m.in, true, true); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// Before ids, the command line has the name the session started under.
	if out := said(); !strings.Contains(out, "pid 5000200 serves no socket file (first name: gone)\n") {
		t.Errorf("an orphan from before ids:\n%s", out)
	}
	// Kept by id, the command line has an id nobody knows the session by:
	// the index has the name it has now.
	idx := zellij.NewIndex(map[string]string{"notes": "gone"})
	m.in.Index = &idx
	if out := said(); !strings.Contains(out, "pid 5000200 serves no socket file (name: notes)\n") {
		t.Errorf("an orphan kept by id:\n%s", out)
	}
	// One that is kept is named the same way.
	m.in.Protected = func(n string) bool { return n == "notes" }
	if out := said(); !strings.Contains(out, "pid 5000200 serves no socket file (name: notes), kept: its name is protected\n") {
		t.Errorf("a kept orphan kept by id:\n%s", out)
	}
}

func TestRecoverChangesNothingWithoutTheIndex(t *testing.T) {
	testutil.SkipOnWindows(t)
	isolate(t)
	m := newRecoverMachine(t)
	// zellij's index is there and cannot be read: a long name and a server
	// without its socket file are no longer told from live ones.
	if err := os.WriteFile(zellij.IndexFile(m.in.InfoDir), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	// Nothing it was asked for could be done: status 1, with nothing more
	// said than the line on why.
	if err := recoverSessions(cmd, filepath.Join(t.TempDir(), "lock"), m.in, false, true); err != errSilentExit {
		t.Errorf("recover without the index: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "  ! zellij's session index cannot be read (") || !strings.HasSuffix(lines[1], "): no session can be told from another, so nothing is changed") {
		t.Errorf("recover without the index:\n%s", out)
	}
	if len(m.zj.deleted)+len(m.signals)+len(m.logged) != 0 {
		t.Errorf("deleted %v, signals %v, logged %v", m.zj.deleted, m.signals, m.logged)
	}
}

func TestRecoverEndsWithStatus1WhenAStepFailed(t *testing.T) {
	testutil.SkipOnWindows(t)
	isolate(t)
	m := newRecoverMachine(t)
	m.zj.fail = errors.New("zellij said no")
	cmd, out := output()
	// The step says why on its line; the status tells a script.
	if err := recoverSessions(cmd, filepath.Join(t.TempDir(), "lock"), m.in, false, false); err != errSilentExit {
		t.Errorf("a step failed: %v", err)
	}
	if !strings.Contains(out.String(), "  ✗ "+strings.Repeat("x", 120)+": ") || !strings.Contains(out.String(), "zellij said no") {
		t.Errorf("recover:\n%s", out)
	}
}

func TestAPassWithAFailedDeleteEndsWithStatus1(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	named := func(name string) inspect.Session { return inspect.Session{Listed: zellij.Listed{Name: name}} }
	r := reaper.Report{Outcomes: []reaper.Outcome{
		{Session: named("gone"), Deleted: true},
		{Session: named("stuck"), DeleteErr: errors.New("zellij said no")},
		{Session: named("busy"), KeptAtDelete: "a client attached since the check"},
		{Session: named("work"), RenameErr: errors.New("the name is taken")},
	}}
	// The timer's pass prints nothing, and its status still says so.
	for _, quiet := range []bool{true, false} {
		cmd, out := output()
		err := showPass(cmd.Context(), out, a, r, false, quiet)
		if err == nil || err.Error() != "1 delete failed: `zellij-reaper logs` says why" || report(err, io.Discard) != 1 {
			t.Errorf("quiet %t: %v", quiet, err)
		}
		if quiet != (out.Len() == 0) || !quiet && !strings.Contains(out.String(), "  stuck: failed: zellij said no\n") {
			t.Errorf("quiet %t:\n%s", quiet, out)
		}
	}
	// A session kept at the delete and a rename zellij refused are no
	// failure: the timer's status stays 0.
	r.Outcomes[1].DeleteErr = nil
	cmd, out := output()
	if err := showPass(cmd.Context(), out, a, r, false, true); err != nil {
		t.Errorf("no failed delete: %v", err)
	}
}

func TestResizeEndsWithStatus1WhenASignalWasNotSent(t *testing.T) {
	isolate(t)
	th := ui.NewTheme(true)
	targets := []resize.Target{{PID: 4242, Role: "client", Command: "zellij attach notes"}}
	var out strings.Builder
	if err := showResized(&out, th, targets); err != nil {
		t.Errorf("every signal sent: %v\n%s", err, out.String())
	}
	targets = append(targets, resize.Target{PID: 4243, Role: "pane", Command: "htop", Err: syscall.EPERM})
	out.Reset()
	// The line says why; the status tells a script.
	if err := showResized(&out, th, targets); err != errSilentExit || !strings.Contains(ansi.Strip(out.String()), "✗ 4243  pane  htop: operation not permitted") {
		t.Errorf("a signal not sent: %v\n%s", err, out.String())
	}
}

func TestRecoverNamesTheCommandForTheServersItLeaves(t *testing.T) {
	testutil.SkipOnWindows(t)
	isolate(t)
	lockPath := filepath.Join(t.TempDir(), "lock")
	const (
		orphan = "pid 5000200 serves no socket file (first name: gone)\n"
		one    = "1 server serves no socket file and nothing keeps it: `zellij-reaper recover --aggressive` stops it\n"
		two    = "2 servers serve no socket file and nothing keeps them: `zellij-reaper recover --aggressive` stops them\n"
	)
	// said is what recover prints about m.
	said := func(m *recoverMachine, dry, aggressive bool) string {
		t.Helper()
		cmd, out := output()
		if err := recoverSessions(cmd, lockPath, m.in, dry, aggressive); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	// served is a machine with nothing to repair: no name is too long, and one
	// server has lost its socket file.
	served := func() *recoverMachine {
		m := newRecoverMachine(t)
		m.in.InfoDir = t.TempDir()
		return m
	}

	// A name to repair and a server left: the command closes a dry run.
	m := newRecoverMachine(t)
	if out := said(m, true, false); !strings.HasSuffix(out, "dry run: nothing changed\n"+one) {
		t.Errorf("a dry run that leaves a server:\n%s", out)
	}
	// A real run repairs the name, leaves the server and says the same last.
	out := said(m, false, false)
	if !strings.HasSuffix(out, one) || strings.Contains(out, "nothing to repair") || len(m.zj.deleted) != 1 || len(m.signals) != 0 {
		t.Errorf("a run that leaves a server: deleted %v, signals %v\n%s", m.zj.deleted, m.signals, out)
	}
	// With --aggressive the server is the run's to stop: no command to name.
	if out := said(newRecoverMachine(t), true, true); strings.Contains(out, "--aggressive") {
		t.Errorf("a dry run with --aggressive:\n%s", out)
	}

	// Nothing to repair, and a server left: the list of them ended in
	// "nothing to repair", which told nobody there was a command for it.
	m = served()
	if out := said(m, false, false); !strings.HasSuffix(out, orphan+one) || strings.Contains(out, "nothing to repair") {
		t.Errorf("nothing to repair and a server left:\n%s", out)
	}
	// Two of them are counted as two.
	m.fp.Add(testutil.Proc{PID: 5_000_300, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(m.in.SocketDirs[0], "lost")}, Sockets: []uint32{3}})
	m.socks = append(m.socks, procfs.UnixSocket{Inode: 3, State: 10, VFSIno: 999998, VFSDev: m.socks[0].VFSDev})
	if out := said(m, true, false); !strings.HasSuffix(out, two) || strings.Contains(out, "nothing to repair") {
		t.Errorf("two servers left:\n%s", out)
	}

	// A server something keeps is not one --aggressive would stop.
	m = served()
	m.in.Protected = func(n string) bool { return n == "gone" }
	if out := said(m, true, false); !strings.HasSuffix(out, "kept: its first name is protected\n  ✓ nothing to repair\n") || strings.Contains(out, "--aggressive") {
		t.Errorf("a server that is kept:\n%s", out)
	}
	// Nor is any where every server looks orphaned: none would be stopped.
	m = served()
	m.socks[0].VFSIno = 999997
	if out := said(m, true, false); !strings.HasSuffix(out, "none will be stopped\n  ✓ nothing to repair\n") || strings.Contains(out, "--aggressive") {
		t.Errorf("every server orphaned:\n%s", out)
	}
	// And a machine with nothing wrong says so, as it did.
	m = served()
	m.socks[1].VFSIno = m.socks[0].VFSIno
	if out := said(m, false, false); !strings.HasSuffix(out, "  ✓ nothing to repair\n") || strings.Contains(out, "--aggressive") {
		t.Errorf("nothing wrong:\n%s", out)
	}
}

func TestConfigShowLinesUpTheValues(t *testing.T) {
	isolate(t)
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "show"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")[1:]
	at := strings.Index(lines[0], " = ")
	for _, l := range lines {
		if i := strings.Index(l, " = "); i != at {
			t.Errorf("%q has its = at %d, the first line at %d", l, i, at)
		}
	}
}

// runRoot runs the command line with args in an isolated home and returns
// what it printed and its error.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestLogsWithNoLinesAskedPrintsNothing(t *testing.T) {
	home := isolate(t)
	logPath := filepath.Join(home, ".local", "state", "zellij-reaper", "reaper.log")
	os.MkdirAll(filepath.Dir(logPath), 0o755)
	os.WriteFile(logPath, []byte("[2026-11-04 09:00:00] === reaper end ===\n"), 0o644)
	// -n 0 said "The log is empty", with a line in it.
	if out, err := runRoot(t, "logs", "-n", "0"); err != nil || out != "" {
		t.Errorf("logs -n 0: %v, %q", err, out)
	}
}

func TestListsAndSettingsSayWhatTheyDid(t *testing.T) {
	isolate(t)
	// These printed their message keys, which no catalog had.
	out, err := runRoot(t, "unprotect", "nosuch")
	if err != nil || out != i18n.T("protect.notlisted", "nosuch")+"\n" {
		t.Errorf("unprotect nosuch: %v, %q", err, out)
	}
	out, err = runRoot(t, "config", "init")
	if err != nil || !strings.HasPrefix(out, "Wrote ") || !strings.Contains(out, "config.toml") {
		t.Errorf("config init: %v, %q", err, out)
	}
}
