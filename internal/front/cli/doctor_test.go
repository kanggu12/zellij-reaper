package cli

// What doctor says about the last pass and the log. The log and the pass lock
// are under a temporary home, and the scheduler is a fake: nothing here
// reaches this machine's timers, log or lock.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func TestPassWordsSayHowTheLastPassWent(t *testing.T) {
	i18n.Set(i18n.English)
	now := time.Now()
	// started is a pass that started ago before now, with its clock time.
	started := func(ago time.Duration) (time.Time, string) {
		at := now.Add(-ago)
		return at, ui.Clock(at)
	}
	recent, recentAt := started(12 * time.Minute)
	moment, momentAt := started(10 * time.Second)
	long, longAt := started(5 * time.Hour)
	const lockPath = "~/.local/state/zellij-reaper/lock"
	for _, c := range []struct {
		name  string
		p     logfile.Pass
		every time.Duration
		held  bool
		mark  string
		text  string
	}{
		{"none in the log", logfile.Pass{}, time.Hour, false, "", ""},
		{"one that acted", logfile.Pass{Found: true, Started: recent, Ended: true, Acted: true, Reaped: 1, Kept: 4, Renamed: 2}, time.Hour, false,
			"ok", "last pass " + recentAt + " (12m ago): 1 reaped, 4 kept, 2 renamed"},
		// zellij refused a delete: the session is still there.
		{"one with a failed delete", logfile.Pass{Found: true, Started: recent, Ended: true, Acted: true, Reaped: 1, Kept: 4, Failed: 1}, time.Hour, false,
			"warn", "last pass " + recentAt + " (12m ago): 1 reaped, 4 kept, 0 renamed; 1 delete failed (zellij-reaper logs)"},
		{"one with no sessions", logfile.Pass{Found: true, Started: recent, Ended: true, Stopped: "no sessions"}, time.Hour, false,
			"ok", "last pass " + recentAt + " (12m ago): no sessions"},
		// A pass that stops each time keeps every session, and says so only
		// in the log.
		{"one that stopped", logfile.Pass{Found: true, Started: recent, Ended: true, Stopped: "zellij not installed, nothing to do"}, time.Hour, false,
			"bad", "the last pass, " + recentAt + " (12m ago), stopped: zellij not installed, nothing to do"},
		{"one ended from outside", logfile.Pass{Found: true, Started: recent}, time.Hour, false,
			"warn", "the last pass, " + recentAt + " (12m ago), did not finish: something ended it before it wrote its last line"},
		{"one running now", logfile.Pass{Found: true, Started: moment}, time.Hour, true,
			"ok", "a pass is running now, since " + momentAt},
		{"one that does not end", logfile.Pass{Found: true, Started: recent}, time.Hour, true,
			"bad", "a pass started " + recentAt + " (12m ago) and has not ended: it holds the pass lock (" + lockPath + "), so every later pass gives up"},
		{"the lock held by something else", logfile.Pass{Found: true, Started: recent, Ended: true, Acted: true}, time.Hour, true,
			"bad", "no pass is running, but something holds the pass lock (" + lockPath + "): while it does, every pass gives up"},
		{"the lock held with no pass in the log", logfile.Pass{}, time.Hour, true,
			"bad", "no pass is running, but something holds the pass lock (" + lockPath + "): while it does, every pass gives up"},
		// Passes stopped coming: more than two intervals and the first
		// pass's wait have gone by.
		{"passes that stopped coming", logfile.Pass{Found: true, Started: long, Ended: true, Acted: true}, time.Hour, false,
			"warn", "the last pass was " + longAt + " (" + duration.Format(5*time.Hour) + " ago), though the timer runs every 1h"},
		{"not late yet", logfile.Pass{Found: true, Started: long, Ended: true, Acted: true}, 3 * time.Hour, false,
			"ok", "last pass " + longAt + " (" + duration.Format(5*time.Hour) + " ago): 0 reaped, 0 kept, 0 renamed"},
		// With no timer, no pass is due: an old one is only old.
		{"no timer", logfile.Pass{Found: true, Started: long, Ended: true, Acted: true}, 0, false,
			"ok", "last pass " + longAt + " (" + duration.Format(5*time.Hour) + " ago): 0 reaped, 0 kept, 0 renamed"},
	} {
		mark, text := passWords(c.p, c.every, c.held, lockPath, now)
		if mark != c.mark || text != c.text {
			t.Errorf("%s:\n got %q %q\nwant %q %q", c.name, mark, text, c.mark, c.text)
		}
	}
}

// writeLog puts lines in the log of a's settings.
func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorSaysWhatCameOfTheLastPass(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	stamp := time.Now().Add(-3 * time.Minute).Format("2006-01-02 15:04:05")
	writeLog(t, a.Config.LogPath,
		"["+stamp+"] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===",
		"["+stamp+"] zellij list-sessions failed: zellij list-sessions --no-formatting: no answer within 10s",
		"["+stamp+"] === reaper end ===")
	// The lock file a pass leaves, free: doctor takes it for its look.
	if err := os.WriteFile(a.Reaper.LockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	before, _ := doctor(context.Background(), a, cmd)
	if !strings.Contains(out.String(), "stopped: zellij list-sessions failed: zellij list-sessions --no-formatting: no answer within 10s\n") {
		t.Errorf("doctor after a pass that stopped:\n%s", out)
	}
	// The pass lock held by something that is not a pass. Taking it at once
	// also says that doctor let go of it after its look: a lock it kept would
	// make this wait out its time.
	l, err := lock.Acquire(a.Reaper.LockPath, 0)
	if err != nil {
		t.Fatalf("the lock after doctor: %v", err)
	}
	// A lock let go of while doctor waits for it is no failure: a names
	// pass holds it for a moment. The test waits for the letting go before
	// it goes on, so nothing touches the lock from two goroutines at once.
	released := make(chan struct{})
	time.AfterFunc(300*time.Millisecond, func() {
		l.Release()
		close(released)
	})
	cmd, out = output()
	problems, _ := doctor(context.Background(), a, cmd)
	<-released
	if problems != before || strings.Contains(out.String(), "holds the pass lock") {
		t.Errorf("doctor with the lock held for a moment: %d problems (before %d):\n%s", problems, before, out)
	}
	// One held all along is. The wait is cut short here, to keep the test
	// quick.
	defer func(d time.Duration) { lockLook = d }(lockLook)
	lockLook = 200 * time.Millisecond
	if l, err = lock.Acquire(a.Reaper.LockPath, 0); err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	cmd, out = output()
	held, _ := doctor(context.Background(), a, cmd)
	// The lock's line takes the place of the stopped pass's: one problem, as
	// before.
	if !strings.Contains(out.String(), "no pass is running, but something holds the pass lock (") || held != before {
		t.Errorf("doctor with the lock held: %d problems (before %d):\n%s", held, before, out)
	}
}

func TestDoctorSaysWhenTheLogCannotBeWritten(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	// A directory where the log would be: no pass can write it, whoever
	// runs it, and the passes go on with status 0.
	if err := os.MkdirAll(a.Config.LogPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	problems, _ := doctor(context.Background(), a, cmd)
	if !strings.Contains(out.String(), "the log cannot be written (") || problems == 0 {
		t.Errorf("doctor with a directory for a log: %d problems:\n%s", problems, out)
	}
}

func TestLogsReadsTheLogWhenTheProtectedListCannotBeRead(t *testing.T) {
	isolate(t)
	// The places the command itself finds, from the environment isolate set:
	// on Windows a map with HOME alone names other folders than the process's
	// does, and the test wrote where logs did not read.
	env := config.OSEnv{}
	logPath := config.Default(env).LogPath
	writeLog(t, logPath,
		"[2026-11-04 09:00:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===")
	// A directory where the protected list would be stops every pass: the
	// log is where one looks for why.
	if err := os.MkdirAll(config.ProtectedFile(env), 0o755); err != nil {
		t.Fatal(err)
	}
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"logs", "-n", "5"})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "=== reaper start") {
		t.Errorf("logs: %v\n%s", err, out.String())
	}
}
