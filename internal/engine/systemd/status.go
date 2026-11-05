package systemd

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
)

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
