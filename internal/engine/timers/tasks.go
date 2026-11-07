package timers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Tasks runs the passes on Windows, from two tasks in the user's Task
// Scheduler: one every interval, one every rename_interval. Each starts the
// binary through `conhost.exe --headless`, which gives a console program a
// console that never shows: started directly, every pass would flash a window.
// The tasks are set up through PowerShell's ScheduledTasks module, whose
// answers, unlike schtasks.exe's, do not change with the system's language.
type Tasks struct {
	// Env locates the settings.
	Env config.Env
	// Run executes powershell.exe. Tests replace it.
	Run Runner
}

// The tasks' names in the Task Scheduler library.
const (
	TaskName      = "zellij-reaper"
	NamesTaskName = "zellij-reaper-names"
)

// Kind names the Task Scheduler for messages.
func (w Tasks) Kind() string { return "Task Scheduler" }

// Persists cannot be told: a task set up this way runs while its user is
// logged in, which is when the user's zellij sessions run too.
func (w Tasks) Persists() (on, known bool) { return false, false }

// FirstPass comes FirstPassDelay after the install, where the task's trigger
// first starts it.
func (w Tasks) FirstPass(now time.Time, _ time.Duration) time.Time { return now.Add(FirstPassDelay) }

// ps runs a PowerShell script. The script goes base64-encoded, as UTF-16, so
// no quoting on the way can change it, and its output comes back as UTF-8,
// so a path with non-ASCII letters survives.
func (w Tasks) ps(script string) (string, error) {
	// Progress records would come out as CLIXML on stderr, which the runner
	// mixes into the answer.
	// It ends in exit 0: PowerShell's own exit status is whether the last
	// command succeeded, even one whose error was silenced on purpose. An
	// error that is not silenced stops the script before that line.
	full := "$ErrorActionPreference = 'Stop'\n$ProgressPreference = 'SilentlyContinue'\n[Console]::OutputEncoding = [Text.Encoding]::UTF8\n" + script + "\nexit 0\n"
	u := utf16.Encode([]rune(full))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return w.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(b))
}

// psQuote puts s in a single-quoted PowerShell string.
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// cmdQuote puts an argument in double quotes for a Windows command line.
func cmdQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

// Available reports whether PowerShell's ScheduledTasks module answers.
func (w Tasks) Available() bool {
	_, err := w.ps("Get-Command Register-ScheduledTask | Out-Null")
	return err == nil
}

// TaskScript renders the PowerShell that registers one task: start first after
// first, then every interval, never two at once, stopped after a minute (a
// pass takes well under a second), and run late when the machine was asleep.
func TaskScript(name, description, exe, configFile string, args []string, first, interval time.Duration) string {
	argv := []string{"--headless", cmdQuote(exe)}
	if configFile != "" {
		argv = append(argv, "--config", cmdQuote(configFile))
	}
	argv = append(argv, args...)
	return fmt.Sprintf(`$action = New-ScheduledTaskAction -Execute 'conhost.exe' -Argument %s
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddSeconds(%d) -RepetitionInterval (New-TimeSpan -Seconds %d)
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName %s -Description %s -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
`, psQuote(strings.Join(argv, " ")), int(first.Seconds()), int(interval.Seconds()), psQuote(name), psQuote(description))
}

// Install writes a settings file if there is none, then registers both tasks.
func (w Tasks) Install(o InstallOptions) (Result, error) {
	var r Result
	if !w.Available() {
		return r, errors.New("PowerShell's ScheduledTasks module does not answer")
	}
	if err := ensureSettings(w.Env, o.ConfigFile, &r); err != nil {
		return r, err
	}
	// No names task: zellij on Windows cannot reach a session after a rename,
	// so zellij-reaper renames nothing there. One left from an earlier install
	// goes.
	script := TaskScript(TaskName, "Reap idle and exited zellij sessions", o.Exe, o.ConfigFile,
		[]string{"run", "--quiet"}, FirstPassDelay, o.Config.Interval)
	script += fmt.Sprintf("Unregister-ScheduledTask -TaskName %s -Confirm:$false -ErrorAction SilentlyContinue\n", psQuote(NamesTaskName))
	// The task runs with the PATH Windows keeps for the user and the machine,
	// not the one this shell may have added to. zellij found only by the
	// latter would not be found by the passes.
	if o.ZellijDir != "" {
		script += fmt.Sprintf(`$dirs = ([Environment]::GetEnvironmentVariable('Path', 'User') + ';' + [Environment]::GetEnvironmentVariable('Path', 'Machine')) -split ';' | ForEach-Object { $_.TrimEnd('\') }
if ($dirs -notcontains %s) { 'zellij-not-on-path' }
`, psQuote(strings.TrimRight(o.ZellijDir, `\`)))
	}
	out, err := w.ps(script)
	if err != nil {
		return r, fmt.Errorf("register the tasks: %s", firstLine(out, err))
	}
	forgetStatus()
	r.Wrote = append(r.Wrote, `Task Scheduler\`+TaskName)
	if strings.Contains(out, "zellij-not-on-path") {
		r.Warnings = append(r.Warnings, fmt.Sprintf("the task will not find zellij: %s is not in the PATH Windows keeps for you; add it there, or set zellij = '%s' in the settings", o.ZellijDir, filepath.Join(o.ZellijDir, "zellij.exe")))
	}
	return r, nil
}

// Uninstall removes both tasks; with purge, the settings and the state too.
func (w Tasks) Uninstall(purge bool, logPath string) (Result, error) {
	var r Result
	for _, name := range []string{TaskName, NamesTaskName} {
		if _, err := w.ps(fmt.Sprintf("Unregister-ScheduledTask -TaskName %s -Confirm:$false", psQuote(name))); err == nil {
			r.Removed = append(r.Removed, `Task Scheduler\`+name)
		}
	}
	forgetStatus()
	if purge {
		purgeState(w.Env, logPath, &r)
	}
	return r, nil
}

// Status reports on the hourly task.
func (w Tasks) Status() TimerStatus { return w.statusOf(TaskName) }

// NamesStatus reports on the names task.
func (w Tasks) NamesStatus() TimerStatus { return w.statusOf(NamesTaskName) }

// taskInfo is what the status script prints, as JSON.
type taskInfo struct {
	State, Last, Next, Exec, Args, Interval string
}

// statusOf asks the Task Scheduler about one task. Times come as UTC in ISO
// 8601 and the interval as an ISO 8601 duration ("PT1H"), whatever the
// system's language.
func (w Tasks) statusOf(name string) TimerStatus {
	statusMu.Lock()
	defer statusMu.Unlock()
	if c, ok := statusCache[name]; ok && time.Since(c.at) < statusFor {
		return c.st
	}
	st := w.askStatus(name)
	statusCache[name] = cachedStatus{st, time.Now()}
	return st
}

// statusFor is how long a task's status is reused: each ask starts
// PowerShell, about a third of a second, and the dashboard asks every few
// seconds.
const statusFor = 30 * time.Second

// cachedStatus is a status and when it was asked.
type cachedStatus struct {
	st TimerStatus
	at time.Time
}

var (
	statusMu    sync.Mutex
	statusCache = map[string]cachedStatus{}
)

// forgetStatus drops what was asked before an install or an uninstall.
func forgetStatus() {
	statusMu.Lock()
	statusCache = map[string]cachedStatus{}
	statusMu.Unlock()
}

// askStatus asks PowerShell about one task.
func (w Tasks) askStatus(name string) TimerStatus {
	var st TimerStatus
	out, err := w.ps(fmt.Sprintf(`$t = Get-ScheduledTask -TaskName %s
$i = $t | Get-ScheduledTaskInfo
$a = $t.Actions[0]
$when = { param($d) if ($d -and $d.Year -gt 2000) { $d.ToUniversalTime().ToString('o') } else { '' } }
[pscustomobject]@{ State = [string]$t.State; Last = & $when $i.LastRunTime; Next = & $when $i.NextRunTime;
  Exec = [string]$a.Execute; Args = [string]$a.Arguments; Interval = [string]$t.Triggers[0].Repetition.Interval } | ConvertTo-Json -Compress
`, psQuote(name)))
	if err != nil {
		return st
	}
	// The object is one line of its own; anything else PowerShell printed
	// around it is not part of it.
	var ti taskInfo
	found := false
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &ti) == nil {
			found = true
			break
		}
	}
	if !found {
		return st
	}
	st.Installed = true
	st.Enabled = ti.State != "Disabled"
	st.Active = st.Enabled
	st.Last, st.Next = localTime(ti.Last), localTime(ti.Next)
	st.Interval = isoDuration(ti.Interval)
	st.ExecStart = strings.TrimSpace(strings.TrimPrefix(ti.Args, "--headless "))
	return st
}

// localTime reads a time the status script printed, in UTC, as the local time
// every message shows it in; "" or anything unreadable is the zero time.
func localTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.Local()
}

// isoDuration reads the ISO 8601 durations the Task Scheduler uses, like
// "PT1H", "PT5M" or "P1DT2H". Months and years do not come up for a pass.
func isoDuration(s string) time.Duration {
	if !strings.HasPrefix(s, "P") {
		return 0
	}
	var d time.Duration
	inTime, num := false, ""
	for _, c := range s[1:] {
		switch {
		case c == 'T':
			inTime = true
		case c >= '0' && c <= '9':
			num += string(c)
		default:
			n, err := strconv.Atoi(num)
			if err != nil {
				return 0
			}
			switch {
			case c == 'D' && !inTime:
				d += time.Duration(n) * 24 * time.Hour
			case c == 'H' && inTime:
				d += time.Duration(n) * time.Hour
			case c == 'M' && inTime:
				d += time.Duration(n) * time.Minute
			case c == 'S' && inTime:
				d += time.Duration(n) * time.Second
			default:
				return 0
			}
			num = ""
		}
	}
	return d
}
