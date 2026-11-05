package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/repair"
	"github.com/kanggu12/zellij-reaper/internal/engine/systemd"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
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
	a.System = systemd.System{Env: env, User: "u", Run: func(name string, args ...string) (string, error) { return "", nil }}
	return a
}

func TestSettingsFromTheEnvironmentAreNamedAsUnseenByTheTimer(t *testing.T) {
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
}

type recoverZellij struct{ deleted []string }

func (z *recoverZellij) ListSessions(context.Context) ([]zellij.Listed, error) { return nil, nil }
func (z *recoverZellij) Delete(_ context.Context, name string) error {
	z.deleted = append(z.deleted, name)
	return nil
}

func newRecoverMachine(t *testing.T) *recoverMachine {
	t.Helper()
	m := &recoverMachine{zj: &recoverZellij{}}
	fp := testutil.NewFakeProc(t)
	sockDir := testutil.SocketDir(t)
	info := filepath.Join(t.TempDir(), "session_info")
	testutil.SessionFiles(t, info, strings.Repeat("x", 120), testutil.Metadata(0, "x"), "", time.Now())
	// One healthy server, one whose socket file is gone.
	healthy, _, _ := procfs.FileID(testutil.Socket(t, sockDir, "healthy"))
	fp.Add(testutil.Proc{PID: 5_000_100, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "healthy")}, Sockets: []uint32{1}})
	fp.Add(testutil.Proc{PID: 5_000_200, PPID: 1, Comm: "zellij", Argv: []string{"zellij", "--server", filepath.Join(sockDir, "gone")}, Sockets: []uint32{2}})
	_, dev, _ := procfs.FileID(filepath.Join(sockDir, "healthy"))
	socks := []procfs.UnixSocket{{Inode: 1, State: 10, VFSIno: healthy, VFSDev: dev}, {Inode: 2, State: 10, VFSIno: 999999, VFSDev: dev}}
	m.in = repair.Inputs{Zellij: m.zj, ProcRoot: fp.Root, InfoDir: info, SocketDirs: []string{sockDir}, UID: os.Getuid(),
		Sockets: func() ([]procfs.UnixSocket, error) { return socks, nil },
		Signal: func(pid int, sig syscall.Signal) error {
			// The process is gone once it got SIGTERM.
			for _, s := range m.signals {
				if s == "TERM" {
					return syscall.ESRCH
				}
			}
			if sig == syscall.SIGTERM {
				m.signals = append(m.signals, "TERM")
			}
			return nil
		}}
	return m
}

func TestRecoverWaitsForThePassLock(t *testing.T) {
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
	if err := recoverSessions(cmd, lockPath, m.in, false, true); !errors.Is(err, lock.ErrBusy) {
		t.Errorf("recover with the lock held: %v\n%s", err, out)
	}
	if time.Since(start) < reaper.LockWait-time.Second {
		t.Errorf("gave up after %v without waiting for the pass", time.Since(start))
	}
	if len(m.zj.deleted)+len(m.signals) != 0 {
		t.Errorf("changed things without the lock: deleted %v, signals %v", m.zj.deleted, m.signals)
	}
	// A dry run changes nothing, so it does not wait.
	cmd, out = output()
	if err := recoverSessions(cmd, lockPath, m.in, true, true); err != nil || !strings.Contains(out.String(), "dry run: nothing changed") {
		t.Errorf("dry run with the lock held: %v\n%s", err, out)
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
}
