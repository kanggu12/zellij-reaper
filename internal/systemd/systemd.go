// Package systemd installs zellij-reaper as a systemd user timer, removes it,
// reports its state, and moves an install of the earlier shell version over.
package systemd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/config"
	"github.com/kanggu12/zellij-reaper/internal/duration"
	"github.com/kanggu12/zellij-reaper/internal/protect"
)

// Unit names. They are the same as the shell version's, so installing over
// it replaces its units in place.
const (
	ServiceName = "zellij-reaper.service"
	TimerName   = "zellij-reaper.timer"
)

// Runner runs a command and returns its combined output. Tests replace it.
type Runner func(name string, args ...string) (string, error)

// Exec runs the real command.
func Exec(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// System is the part of the machine the installer touches.
type System struct {
	// Env locates the unit, settings and state directories.
	Env config.Env
	// Run executes systemctl and loginctl.
	Run Runner
	// User is the login name for loginctl.
	User string
	// Uptime reads the monotonic clock systemd schedules the timer on; nil
	// means the real one.
	Uptime func() (time.Duration, bool)
}

// FirstPassDelay is how long after the timer starts (at install, or when the
// user's systemd starts) the first pass runs: time to look at the plan first.
const FirstPassDelay = 10 * time.Minute

// systemctl runs `systemctl --user ...`.
func (s System) systemctl(args ...string) (string, error) {
	return s.Run("systemctl", append([]string{"--user"}, args...)...)
}

// Available reports whether a systemd user instance answers.
func (s System) Available() bool {
	_, err := s.systemctl("show-environment")
	return err == nil
}

// servicePath and timerPath are the unit files.
func (s System) servicePath() string { return filepath.Join(config.UnitDir(s.Env), ServiceName) }
func (s System) timerPath() string   { return filepath.Join(config.UnitDir(s.Env), TimerName) }

// PathDirs are put in the service's PATH so it finds zellij wherever the usual
// installers put it (cargo, nix, Homebrew on Linux, distro packages). systemd
// expands %h to the home directory.
var PathDirs = []string{
	"%h/.local/bin", "%h/.cargo/bin", "%h/.nix-profile/bin", "/nix/var/nix/profiles/default/bin",
	"/home/linuxbrew/.linuxbrew/bin", "/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
}

// escape makes a path safe inside a unit file: "%" is a specifier there.
func escape(p string) string { return strings.ReplaceAll(p, "%", "%%") }

// escapeExec makes a path safe inside ExecStart=, which also expands "$NAME"
// from the environment; "$$" is a plain "$".
func escapeExec(p string) string { return strings.ReplaceAll(escape(p), "$", "$$") }

// quotable refuses a path that cannot sit inside ExecStart='s double quotes:
// a quote would end them, a backslash starts an escape, and a line break
// would end the line and start a directive of its own.
func quotable(what, p string) error {
	if strings.ContainsAny(p, "\"\\\n\r") {
		return fmt.Errorf("%s %q has a quote, a backslash or a line break, which a systemd unit cannot hold: use a path without them", what, p)
	}
	return nil
}

// ServiceUnit renders the service. exe is the absolute path of this binary;
// zellijDir, when not empty, goes first in PATH; configFile, when not empty,
// is passed with --config so the timer reads the same settings as install.
// Install refuses paths ServiceUnit cannot quote (see quotable).
func ServiceUnit(exe, zellijDir, configFile string) string {
	dirs := PathDirs
	if zellijDir != "" {
		dirs = append([]string{escape(zellijDir)}, dirs...)
	}
	args := "run --quiet"
	if configFile != "" {
		args = fmt.Sprintf(`--config "%s" run --quiet`, escapeExec(configFile))
	}
	return fmt.Sprintf(`[Unit]
Description=Reap idle and exited zellij sessions
Documentation=https://github.com/kanggu12/zellij-reaper

[Service]
Type=oneshot
ExecStart="%s" %s
Environment="PATH=%s"
Nice=10
# A pass takes well under a second. One still running after a minute is
# stuck (a zellij server that stopped answering), and is stopped.
TimeoutStartSec=60
`, escapeExec(exe), args, strings.Join(dirs, ":"))
}

// TimerUnit renders the timer for an interval.
func TimerUnit(interval time.Duration) string {
	// Waking up to five minutes late is fine and lets systemd batch wakeups;
	// short intervals get a proportionally short window.
	accuracy := 5 * time.Minute
	if interval/12 < accuracy {
		accuracy = interval / 12
	}
	if accuracy < time.Second {
		accuracy = time.Second
	}
	return fmt.Sprintf(`[Unit]
Description=Run zellij-reaper every %s
Documentation=https://github.com/kanggu12/zellij-reaper

[Timer]
# The first pass waits after the timer starts, at install or at login; then
# one pass per interval.
OnActiveSec=%s
OnUnitActiveSec=%s
AccuracySec=%s

[Install]
WantedBy=timers.target
`, duration.Format(interval), spanSetting(FirstPassDelay), duration.Setting(interval), duration.Setting(accuracy))
}

// Result says what an install or an uninstall did.
type Result struct {
	// Wrote and Removed list the files written and deleted.
	Wrote, Removed []string
	// Moved lists "from -> to" for files moved (legacy logs).
	Moved []string
	// Migrated is the settings file written from a legacy install, if any.
	Migrated string
	// Created is the settings file written fresh, if any.
	Created string
	// Linger is "already", "enabled" or the error that stopped it.
	Linger string
	// Warnings are things install did not do and the person should know.
	Warnings []string
}

// InstallOptions are the inputs to Install.
type InstallOptions struct {
	// Exe is the absolute path of the zellij-reaper binary.
	Exe string
	// ZellijDir is the directory holding zellij, if known.
	ZellijDir string
	// Config is the effective settings (for the interval and paths).
	Config *config.Config
	// ConfigFile is a settings file chosen with --config or
	// ZELLIJ_REAPER_CONFIG; empty for the default location.
	ConfigFile string
}

// Install writes the units, moves a legacy install over, writes a settings
// file if there is none, and enables the timer and lingering.
func (s System) Install(o InstallOptions) (Result, error) {
	var r Result
	// Refused before anything is written: a unit that cannot hold these
	// paths would run something else, or nothing.
	if err := quotable("the zellij-reaper binary", o.Exe); err != nil {
		return r, err
	}
	if err := quotable("the settings file", o.ConfigFile); err != nil {
		return r, err
	}
	// zellij's directory goes inside the quoted PATH assignment.
	if err := quotable("zellij's directory", o.ZellijDir); err != nil {
		return r, err
	}
	if !s.Available() {
		return r, errors.New("no systemd user instance answers (on WSL, set systemd=true in /etc/wsl.conf)")
	}
	// The shell version's settings live in its service file, which is about
	// to be overwritten: read them first.
	legacy := DetectLegacy(s.Env)
	// The log the timer will write: the one the settings in effect name,
	// unless the settings are about to be moved over from the shell version.
	logPath := o.Config.LogPath
	// The settings come first: if they cannot be written, the shell
	// version's service (and its settings) must stay as they are.
	cfgPath := config.ConfigFile(s.Env)
	if o.ConfigFile != "" {
		cfgPath = o.ConfigFile
	}
	if _, err := os.Stat(cfgPath); err == nil && legacy != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("kept the settings in %s; the shell version's own settings were not moved over", cfgPath))
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		var text string
		if legacy != nil {
			migrated := legacy.Config(s.Env)
			text = config.Render(migrated, "Moved from the settings of the earlier shell version's service file.")
			r.Migrated = cfgPath
			// The moved settings keep a log path the shell version was given
			// on purpose; its log has to end up there, not at the default.
			logPath = migrated.LogPath
		} else {
			text = config.Render(config.Default(s.Env), "zellij-reaper settings. Run `zellij-reaper config show` to see the values in effect.")
			r.Created = cfgPath
		}
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
			return r, err
		}
		if err := writeFile(cfgPath, text); err != nil {
			return r, err
		}
	}
	if err := os.MkdirAll(config.UnitDir(s.Env), 0o755); err != nil {
		return r, err
	}
	if err := writeFile(s.servicePath(), ServiceUnit(o.Exe, o.ZellijDir, o.ConfigFile)); err != nil {
		return r, err
	}
	if err := writeFile(s.timerPath(), TimerUnit(o.Config.Interval)); err != nil {
		return r, err
	}
	r.Wrote = append(r.Wrote, s.servicePath(), s.timerPath())
	if legacy != nil {
		moved, removed := legacy.Retire(s.Env, logPath)
		r.Moved, r.Removed = append(r.Moved, moved...), append(r.Removed, removed...)
	}
	if out, err := s.systemctl("daemon-reload"); err != nil {
		return r, fmt.Errorf("systemctl --user daemon-reload: %s", firstLine(out, err))
	}
	if out, err := s.systemctl("enable", "--now", TimerName); err != nil {
		return r, fmt.Errorf("systemctl --user enable --now %s: %s", TimerName, firstLine(out, err))
	}
	// Without lingering, a user's timers stop when their last login ends.
	r.Linger = s.enableLinger()
	return r, nil
}

// enableLinger turns on lingering for the user, if it is not on yet.
func (s System) enableLinger() string {
	if out, err := s.Run("loginctl", "show-user", s.User, "--property=Linger"); err == nil && strings.Contains(out, "Linger=yes") {
		return "already"
	}
	if out, err := s.Run("loginctl", "enable-linger", s.User); err != nil {
		return firstLine(out, err)
	}
	return "enabled"
}

// Uninstall stops and removes the timer and the service. With purge it also
// removes the settings, the log and the protected list.
func (s System) Uninstall(purge bool, logPath string) (Result, error) {
	var r Result
	if s.Available() {
		// "not loaded" is fine: there may be nothing to disable.
		s.systemctl("disable", "--now", TimerName)
	}
	for _, p := range []string{s.timerPath(), s.servicePath()} {
		if err := os.Remove(p); err == nil {
			r.Removed = append(r.Removed, p)
		}
	}
	if s.Available() {
		s.systemctl("daemon-reload")
	}
	if purge {
		// The protected list's own lock file goes too, or it alone would
		// keep the state directory from being removed.
		for _, p := range []string{config.ConfigFile(s.Env), logPath, logPath + ".1", config.ProtectedFile(s.Env),
			protect.LockPath(config.ProtectedFile(s.Env)), config.LockFile(s.Env)} {
			if err := os.Remove(p); err == nil {
				r.Removed = append(r.Removed, p)
			}
		}
		// Remove the directories too, but only if nothing else is in them.
		os.Remove(config.ConfigDir(s.Env))
		os.Remove(config.StateDir(s.Env))
	}
	return r, nil
}

// TimerStatus is the timer as systemd reports it.
type TimerStatus struct {
	// Installed is true when the unit file is known to systemd.
	Installed bool
	// Enabled and Active are its unit-file and runtime states.
	Enabled, Active bool
	// Last is when it last fired; Next when it fires next (zero if unknown).
	Last, Next time.Time
	// Interval is OnUnitActiveSec read back from the unit file.
	Interval time.Duration
	// ExecStart is the command the service runs, from the unit file.
	ExecStart string
}

// Status asks systemd about the timer. Next comes from the monotonic time
// systemd will fire at, read against the same clock; failing that, from the
// last run and the interval, because systemd leaves NextElapseUSecRealtime empty for
// monotonic timers like this one.
func (s System) Status() TimerStatus {
	var st TimerStatus
	out, err := s.systemctl("show", TimerName, "--property=LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime,NextElapseUSecMonotonic")
	if err != nil {
		return st
	}
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	st.Installed = props["LoadState"] == "loaded"
	st.Enabled = props["UnitFileState"] == "enabled"
	st.Active = props["ActiveState"] == "active"
	st.Last = parseTimestamp(props["LastTriggerUSec"])
	st.Interval, st.ExecStart = s.unitFacts()
	// Only an active timer fires at all.
	if !st.Active {
		return st
	}
	if next := parseTimestamp(props["NextElapseUSecRealtime"]); !next.IsZero() {
		st.Next = next
	} else if at, ok := s.fromMonotonic(props["NextElapseUSecMonotonic"]); ok {
		// Before its first run (and after a reboot), the timer fires
		// FirstPassDelay after it started: only the monotonic time says when.
		st.Next = at
	} else if !st.Last.IsZero() && st.Interval > 0 {
		st.Next = st.Last.Add(st.Interval)
	}
	return st
}

// fromMonotonic turns systemd's monotonic "2d 13h 55min 46.6s" (time since
// boot) into a wall-clock time, using the same clock.
func (s System) fromMonotonic(v string) (time.Time, bool) {
	at, ok := parseTimespan(v)
	if !ok || at <= 0 {
		return time.Time{}, false
	}
	uptime := s.Uptime
	if uptime == nil {
		uptime = monotonicNow
	}
	now, ok := uptime()
	if !ok {
		return time.Time{}, false
	}
	return time.Now().Add(at - now).Truncate(time.Second), true
}

// parseTimespan reads systemd's printed time spans: "2d 13h 55min 46.608644s",
// "10min", "500ms". Anything else (empty, "infinity") is not a time.
func parseTimespan(v string) (time.Duration, bool) {
	units := []struct {
		suffix string
		d      time.Duration
	}{
		// Longest suffixes first: "min" and "ms" before "m" and "s".
		{"month", 30*24*time.Hour + 10*time.Hour + 30*time.Minute}, {"min", time.Minute}, {"ms", time.Millisecond},
		{"us", time.Microsecond}, {"w", 7 * 24 * time.Hour}, {"d", 24 * time.Hour}, {"h", time.Hour}, {"s", time.Second},
	}
	var total time.Duration
	fields := strings.Fields(v)
	if len(fields) == 0 {
		return 0, false
	}
	for _, f := range fields {
		matched := false
		for _, u := range units {
			num, found := strings.CutSuffix(f, u.suffix)
			if !found {
				continue
			}
			n, err := strconv.ParseFloat(num, 64)
			if err != nil || n < 0 {
				return 0, false
			}
			total += time.Duration(n * float64(u.d))
			matched = true
			break
		}
		if !matched {
			return 0, false
		}
	}
	return total, true
}

// spanSetting writes a duration the way systemd reads it in a unit file.
func spanSetting(d time.Duration) string {
	if d%time.Minute == 0 && d < time.Hour {
		return strconv.Itoa(int(d/time.Minute)) + "min"
	}
	return duration.Setting(d)
}

// unitFacts reads the interval and the command back from the unit files.
func (s System) unitFacts() (time.Duration, string) {
	var interval time.Duration
	var execStart string
	if b, err := os.ReadFile(s.timerPath()); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "OnUnitActiveSec="); ok {
				interval = parseSpan(v)
			}
		}
	}
	if b, err := os.ReadFile(s.servicePath()); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
				execStart = v
			}
		}
	}
	return interval, execStart
}

// parseSpan reads a systemd time span as written in unit files ("1h", "30min").
func parseSpan(v string) time.Duration {
	v = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(v), "min", "m"), " ", "")
	d, _ := duration.Parse(v)
	return d
}

// parseTimestamp reads systemd's "Wed 2026-11-04 03:11:47 KST". The zone
// abbreviation is ambiguous on its own, so the time is read as local time,
// which is how systemd printed it.
func parseTimestamp(v string) time.Time {
	f := strings.Fields(v)
	if len(f) < 3 {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", f[1]+" "+f[2], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeFile writes atomically, so a crash never leaves half a unit file.
func writeFile(path, body string) error {
	// A temporary file of its own next to the target, then one rename.
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return err
	}
	os.Chmod(f.Name(), 0o644)
	return os.Rename(f.Name(), path)
}

// firstLine keeps a command's complaint short.
func firstLine(out string, err error) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return err.Error()
	}
	line, _, _ := strings.Cut(out, "\n")
	return line
}
