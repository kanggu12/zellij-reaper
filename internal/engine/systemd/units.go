package systemd

import (
	"fmt"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
)

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
	return serviceUnit("Reap idle and exited zellij sessions", "run --quiet", exe, zellijDir, configFile)
}

// NamesServiceUnit renders the service of the names timer: a pass that only
// renames.
func NamesServiceUnit(exe, zellijDir, configFile string) string {
	return serviceUnit("Name zellij sessions after what they show", "run --names-only --quiet", exe, zellijDir, configFile)
}

func serviceUnit(description, run, exe, zellijDir, configFile string) string {
	dirs := PathDirs
	if zellijDir != "" {
		dirs = append([]string{escape(zellijDir)}, dirs...)
	}
	args := run
	if configFile != "" {
		args = fmt.Sprintf(`--config "%s" %s`, escapeExec(configFile), run)
	}
	return fmt.Sprintf(`[Unit]
Description=`+description+`
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
	return timerUnit("Run zellij-reaper every %s", interval, FirstPassDelay)
}

// NamesTimerUnit renders the names timer. Its first pass comes a minute after
// it starts: renaming deletes nothing, so there is no reason to wait longer.
func NamesTimerUnit(interval time.Duration) string {
	return timerUnit("Rename zellij sessions every %s", interval, time.Minute)
}

func timerUnit(description string, interval, first time.Duration) string {
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
Description=`+description+`
Documentation=https://github.com/kanggu12/zellij-reaper

[Timer]
# The first pass waits after the timer starts, at install or at login; then
# one pass per interval.
OnActiveSec=%s
OnUnitActiveSec=%s
AccuracySec=%s

[Install]
WantedBy=timers.target
`, duration.Format(interval), spanSetting(first), duration.Setting(interval), duration.Setting(accuracy))
}
