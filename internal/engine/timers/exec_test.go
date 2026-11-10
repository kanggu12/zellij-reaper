package timers

// The real commands' runners, on stand-ins: a script named crontab, or any
// other, first on the PATH of the test. No real crontab, systemctl or
// PowerShell is run here.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// standIn puts an executable shell script called name first on the PATH.
func standIn(t *testing.T, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCrontabReadsOnlyWhatTheCrontabHolds(t *testing.T) {
	// A crontab command that warns on its error stream and succeeds: the
	// warning is no line of the crontab, and the next install would have
	// written it in.
	standIn(t, "crontab", "echo 'crontab: a warning' >&2\nprintf '# mine\\n0 3 * * * /usr/bin/true\\n\\n'\n")
	out, err := crontab("", "-l")
	if err != nil || out != "# mine\n0 3 * * * /usr/bin/true\n\n" {
		t.Errorf("crontab -l: %q, %v", out, err)
	}
	// A failure says why, with what it printed on both streams.
	standIn(t, "crontab", "echo 'no crontab for me' >&2\nexit 1\n")
	if out, err := crontab("", "-l"); err == nil || out != "no crontab for me\n" {
		t.Errorf("a failure: %q, %v", out, err)
	}
}

func TestACommandThatNeverAnswersIsLetGo(t *testing.T) {
	defer func(d time.Duration) { commandLimit = d }(commandLimit)
	commandLimit = 200 * time.Millisecond
	// A child keeps the output open after the command is killed, as one a
	// scheduler starts might.
	standIn(t, "crontab", "sleep 5 &\nexec sleep 30\n")
	standIn(t, "systemctl", "sleep 5 &\nexec sleep 30\n")
	start := time.Now()
	if _, err := crontab("", "-l"); err == nil || !strings.Contains(err.Error(), "gave no answer within 200ms") {
		t.Errorf("crontab: %v", err)
	}
	if _, err := Exec("systemctl", "--user", "show"); err == nil || err.Error() != "systemctl gave no answer within 200ms" {
		t.Errorf("systemctl: %v", err)
	}
	// Each at the limit and the second after it, not the child's 5 s.
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("took %s", took)
	}
}
