package timers

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// sentScript is the PowerShell script in a command line ps built: its last
// argument, base64 over UTF-16.
func sentScript(t *testing.T, args []string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestAQuoteInAPathStaysInItsString(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\ann\zellij-reaper.exe`: `'C:\Users\ann\zellij-reaper.exe'`,
		`C:\Users\o'neil\z.exe`:          `'C:\Users\o''neil\z.exe'`,
		// The curly quotes end a PowerShell string too: what came after one
		// ran as a command.
		"C:\\Users\\x\u2019; Write-Output INJECTED; \u2019y": "'C:\\Users\\x\u2019\u2019; Write-Output INJECTED; \u2019\u2019y'",
		"\u2018\u201a\u201b": "'\u2018\u2018\u201a\u201a\u201b\u201b'",
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPowerShellReadsAQuotedPathBackWhole(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell itself reads the string")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("no powershell.exe")
	}
	// Read back as numbers, which no console code page changes.
	v := "C:\\Users\\x\u2019; Write-Output INJECTED; \u2019y\\o'neil\u2018\u201a\u201b"
	out, err := Tasks{Run: Exec}.ps("$v = " + psQuote(v) + "\nWrite-Output (([int[]][char[]]$v) -join ',')")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var want []string
	for _, r := range v {
		want = append(want, strconv.Itoa(int(r)))
	}
	if got := strings.TrimSpace(out); got != strings.Join(want, ",") {
		t.Errorf("PowerShell read %q back as\n%s", v, got)
	}
}

func TestTaskTimesAreLocal(t *testing.T) {
	// The status script prints UTC, and every message shows a time as it is:
	// left in UTC, a pass due at 07:35 in Seoul read "11-07 22:35".
	w := Tasks{Run: func(string, ...string) (string, error) {
		return `{"State":"Ready","Last":"","Next":"2026-11-07T22:35:23.0000000Z","Exec":"conhost.exe","Args":"--headless run --quiet","Interval":"PT1H"}`, nil
	}}
	st := w.askStatus(TaskName)
	want := time.Date(2026, 11, 7, 22, 35, 23, 0, time.UTC)
	if !st.Next.Equal(want) || st.Next.Location() != time.Local || !st.Last.IsZero() {
		t.Errorf("next %v in %v, last %v", st.Next, st.Next.Location(), st.Last)
	}
	if !st.Installed || st.Interval != time.Hour || st.ExecStart != "run --quiet" {
		t.Errorf("status %+v", st)
	}
}

func TestATaskScriptEndsInExitZero(t *testing.T) {
	// PowerShell exits 1 when the last command failed, even with its error
	// silenced: an install whose last line removes a names task that is not
	// there failed for that alone.
	var script string
	w := Tasks{Run: func(name string, args ...string) (string, error) {
		script = sentScript(t, args)
		return "", nil
	}}
	if _, err := w.ps("Unregister-ScheduledTask -TaskName 'x' -ErrorAction SilentlyContinue"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(script), "exit 0") {
		t.Errorf("script ends:\n%s", script)
	}
	if !strings.HasPrefix(script, "$ErrorActionPreference = 'Stop'") {
		t.Errorf("script starts:\n%s", script)
	}
}

func TestATaskUninstallCountsItsTasks(t *testing.T) {
	// `uninstall --purge` says whether there were timers by this count: the
	// settings and the state a purge removes are not timers.
	env := config.MapEnv{"HOME": t.TempDir()}
	// The task library: a task registered is there, and removing one that is
	// not there fails unless the line silences the error, as the scripts run
	// with $ErrorActionPreference = 'Stop'.
	have := map[string]bool{}
	w := Tasks{Env: env, Run: func(name string, args ...string) (string, error) {
		for line := range strings.SplitSeq(sentScript(t, args), "\n") {
			for _, task := range []string{TaskName, NamesTaskName} {
				switch {
				case strings.HasPrefix(line, "Register-ScheduledTask -TaskName '"+task+"' "):
					have[task] = true
				case strings.HasPrefix(line, "Unregister-ScheduledTask -TaskName '"+task+"' "):
					if !have[task] && !strings.Contains(line, "-ErrorAction SilentlyContinue") {
						return "Unregister-ScheduledTask : No MSFT_ScheduledTask objects found with property 'TaskName' equal to '" + task + "'.", errors.New("exit status 1")
					}
					delete(have, task)
				}
			}
		}
		return "", nil
	}}
	if _, err := w.Install(InstallOptions{Exe: `C:\Users\u\bin\zellij-reaper.exe`, Config: config.Default(env)}); err != nil {
		t.Fatal(err)
	}
	// Windows gets the one task: no session is renamed there.
	if !have[TaskName] || have[NamesTaskName] {
		t.Fatalf("tasks after install: %v", have)
	}
	r, err := w.Uninstall(true, config.Default(env).LogPath)
	if err != nil {
		t.Fatal(err)
	}
	// One task went, and with it the settings file: two things, one timer.
	if r.Timers != 1 || len(r.Removed) != 2 || len(have) != 0 {
		t.Errorf("timers %d, removed %q, tasks left %v", r.Timers, r.Removed, have)
	}
	// With no task there is no timer to count, purge or not.
	if err := os.MkdirAll(config.ConfigDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ConfigFile(env), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = w.Uninstall(true, config.Default(env).LogPath)
	if err != nil || r.Timers != 0 || len(r.Removed) != 1 {
		t.Errorf("a second uninstall: timers %d, removed %q, err %v", r.Timers, r.Removed, err)
	}
}
