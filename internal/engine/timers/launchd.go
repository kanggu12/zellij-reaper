package timers

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Launchd runs the passes on macOS, from two launch agents in
// ~/Library/LaunchAgents: one every interval, one every rename_interval.
// launchd starts a job with StartInterval once the interval has gone by, so
// the first pass comes an interval after install, not ten minutes.
type Launchd struct {
	// Env locates the home directory and the settings.
	Env config.Env
	// Run executes launchctl. Tests replace it.
	Run Runner
	// UID is the user's id, for the gui/<uid> domain; 0 means os.Getuid().
	UID int
}

// The agents' labels, which name their files too.
const (
	LaunchdLabel      = "io.github.kanggu12.zellij-reaper"
	LaunchdNamesLabel = "io.github.kanggu12.zellij-reaper-names"
)

// launchdPath lists the directories a pass looks for zellij in, after zellij's
// own: Homebrew on Apple silicon and on Intel, then the system's, with sbin for
// lsof.
var launchdPath = []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// Kind names launchd for messages.
func (l Launchd) Kind() string { return "launchd" }

// domain is the user's GUI domain, where launch agents run.
func (l Launchd) domain() string {
	uid := l.UID
	if uid == 0 {
		uid = os.Getuid()
	}
	return "gui/" + strconv.Itoa(uid)
}

// agentDir is ~/Library/LaunchAgents.
func (l Launchd) agentDir() string { return filepath.Join(l.Env.Home(), "Library", "LaunchAgents") }

func (l Launchd) agentPath(label string) string { return filepath.Join(l.agentDir(), label+".plist") }

// Available reports whether launchctl answers for the user's domain.
func (l Launchd) Available() bool {
	_, err := l.Run("launchctl", "print", l.domain())
	return err == nil
}

// Persists cannot be told: a launch agent runs while its user is logged in.
func (l Launchd) Persists() (on, known bool) { return false, false }

// FirstPass comes one interval after the install: launchd starts a job that
// has an interval once the interval has passed.
func (l Launchd) FirstPass(now time.Time, interval time.Duration) time.Time { return now.Add(interval) }

// Plist renders a launch agent that runs args every interval.
func Plist(label, exe, zellijDir, configFile string, args []string, interval time.Duration, env map[string]string) string {
	argv := []string{exe}
	if configFile != "" {
		argv = append(argv, "--config", configFile)
	}
	argv = append(argv, args...)
	var b strings.Builder
	for _, a := range argv {
		fmt.Fprintf(&b, "\n\t\t<string>%s</string>", html.EscapeString(a))
	}
	dirs := launchdPath
	if zellijDir != "" {
		dirs = append([]string{zellijDir}, dirs...)
	}
	// Where the reaper and zellij keep their files, as the installing shell
	// has it (see Carried).
	var carried strings.Builder
	for _, k := range Carried {
		if v, ok := env[k]; ok {
			fmt.Fprintf(&carried, "\n\t\t<key>%s</key>\n\t\t<string>%s</string>", k, html.EscapeString(v))
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>%s
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>%s
	</dict>
	<key>StartInterval</key>
	<integer>%d</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>Nice</key>
	<integer>10</integer>
</dict>
</plist>
`, label, b.String(), html.EscapeString(strings.Join(dirs, ":")), carried.String(), int(interval.Seconds()))
}

// Install writes a settings file if there is none, then both agents, and
// loads them.
func (l Launchd) Install(o InstallOptions) (Result, error) {
	var r Result
	if !l.Available() {
		return r, errors.New("launchctl does not answer for this user")
	}
	if err := ensureSettings(l.Env, o.ConfigFile, &r); err != nil {
		return r, err
	}
	if err := os.MkdirAll(l.agentDir(), 0o755); err != nil {
		return r, err
	}
	if err := l.load(LaunchdLabel, Plist(LaunchdLabel, o.Exe, o.ZellijDir, o.ConfigFile, []string{"run", "--quiet"}, o.Config.Interval, o.Env), &r); err != nil {
		return r, err
	}
	if o.Config.RenameInterval <= 0 {
		l.unload(LaunchdNamesLabel, &r)
		return r, nil
	}
	err := l.load(LaunchdNamesLabel, Plist(LaunchdNamesLabel, o.Exe, o.ZellijDir, o.ConfigFile, []string{"run", "--names-only", "--quiet"}, o.Config.RenameInterval, o.Env), &r)
	return r, err
}

// load writes an agent and (re)loads it, so a changed interval applies.
func (l Launchd) load(label, plist string, r *Result) error {
	path := l.agentPath(label)
	if err := writeFile(path, plist); err != nil {
		return err
	}
	r.Wrote = append(r.Wrote, path)
	// Not loaded yet is fine.
	l.Run("launchctl", "bootout", l.domain()+"/"+label)
	if out, err := l.Run("launchctl", "bootstrap", l.domain(), path); err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %s", path, firstLine(out, err))
	}
	return nil
}

// unload stops an agent and removes its file.
func (l Launchd) unload(label string, r *Result) {
	l.Run("launchctl", "bootout", l.domain()+"/"+label)
	if err := os.Remove(l.agentPath(label)); err == nil {
		r.Removed = append(r.Removed, l.agentPath(label))
	}
}

// Uninstall stops and removes both agents; with purge, the settings and the
// state too.
func (l Launchd) Uninstall(purge bool, logPath string) (Result, error) {
	var r Result
	l.unload(LaunchdLabel, &r)
	l.unload(LaunchdNamesLabel, &r)
	if purge {
		purgeState(l.Env, logPath, &r)
	}
	return r, nil
}

// Status reports on the hourly agent.
func (l Launchd) Status() TimerStatus { return l.statusOf(LaunchdLabel) }

// NamesStatus reports on the names agent.
func (l Launchd) NamesStatus() TimerStatus { return l.statusOf(LaunchdNamesLabel) }

var (
	plistIntervalRe = regexp.MustCompile(`<key>StartInterval</key>\s*<integer>(\d+)</integer>`)
	plistArgsRe     = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`)
	plistStringRe   = regexp.MustCompile(`<string>(.*?)</string>`)
	plistEnvRe      = regexp.MustCompile(`(?s)<key>EnvironmentVariables</key>\s*<dict>(.*?)</dict>`)
	plistPairRe     = regexp.MustCompile(`<key>(.*?)</key>\s*<string>(.*?)</string>`)
)

// statusOf reads the agent's file for its interval and command, and launchctl
// for whether it is loaded. launchd does not say when a StartInterval job runs
// next, so Next stays zero.
func (l Launchd) statusOf(label string) TimerStatus {
	var st TimerStatus
	b, err := os.ReadFile(l.agentPath(label))
	if err != nil {
		return st
	}
	st.Installed = true
	if m := plistIntervalRe.FindSubmatch(b); m != nil {
		n, _ := strconv.Atoi(string(m[1]))
		st.Interval = time.Duration(n) * time.Second
	}
	if m := plistArgsRe.FindSubmatch(b); m != nil {
		var args []string
		for _, s := range plistStringRe.FindAllSubmatch(m[1], -1) {
			args = append(args, html.UnescapeString(string(s[1])))
		}
		st.ExecStart = strings.Join(args, " ")
	}
	st.Env, st.EnvKnown = plistCarried(b), true
	if _, err := l.Run("launchctl", "print", l.domain()+"/"+label); err == nil {
		st.Enabled, st.Active = true, true
	}
	return st
}

// plistCarried reads the carried variables back from an agent's file.
func plistCarried(b []byte) map[string]string {
	env := map[string]string{}
	if m := plistEnvRe.FindSubmatch(b); m != nil {
		for _, kv := range plistPairRe.FindAllSubmatch(m[1], -1) {
			if k := string(kv[1]); isCarried(k) {
				env[k] = html.UnescapeString(string(kv[2]))
			}
		}
	}
	return env
}
