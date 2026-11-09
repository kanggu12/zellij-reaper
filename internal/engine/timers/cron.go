package timers

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Cron runs the passes from the user's crontab, on Linux machines with no
// systemd user instance but a cron daemon: WSL without systemd, many
// containers and small servers. Its lines carry a marker so install,
// uninstall and the status find them among the person's own, which are never
// touched.
type Cron struct {
	// Env locates the settings.
	Env config.Env
	// Run executes crontab with the given input on stdin ("" for none).
	// Tests replace it.
	Run func(stdin string, args ...string) (string, error)
	// Running reports whether a cron daemon runs. Tests replace it.
	Running func() bool
	// Zone is the time zone the daemon reads its lines in. Tests replace
	// it; without one, the local zone.
	Zone func() *time.Location
}

// cronMark ends every line zellij-reaper writes into the crontab.
const cronMark = "# zellij-reaper"

// NewCron returns the Cron of the real machine.
func NewCron(env config.Env) Cron {
	return Cron{Env: env, Run: crontab, Running: func() bool {
		_, err := exec.LookPath("crontab")
		return err == nil && cronDaemonRuns()
	}, Zone: daemonZone}
}

// daemonZone is the time zone a cron daemon reads its lines in: the system's,
// from /etc/localtime, since a daemon the init system or `service` starts
// has no TZ of its own. A person's TZ can name another zone, which Go follows;
// without the file, the C library takes UTC.
func daemonZone() *time.Location {
	b, err := os.ReadFile("/etc/localtime")
	if err != nil {
		return time.UTC
	}
	z, err := time.LoadLocationFromTZData("cron", b)
	if err != nil {
		return time.UTC
	}
	return z
}

// zone is the daemon's time zone, or the local one when none is set.
func (c Cron) zone() *time.Location {
	if c.Zone == nil {
		return time.Local
	}
	return c.Zone()
}

// crontab runs the crontab command.
func crontab(stdin string, args ...string) (string, error) {
	c := exec.Command("crontab", args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// cronDaemonRuns looks for a cron daemon among the processes: cron (Debian,
// Ubuntu), crond (Fedora, Alpine's busybox), cronie.
func cronDaemonRuns() bool {
	dirs, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, d := range dirs {
		b, err := os.ReadFile(d)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(b)) {
		case "cron", "crond", "cronie", "fcron":
			return true
		}
	}
	return false
}

// Kind names it for messages.
func (c Cron) Kind() string { return "cron" }

// Available reports whether the crontab command is there and a daemon runs
// it: a crontab nobody reads would look installed and never run a pass.
func (c Cron) Available() bool { return c.Running() }

// HasJobs reports whether the crontab holds zellij-reaper's lines.
func (c Cron) HasJobs() bool {
	lines, err := c.read()
	return err == nil && len(others(lines)) != len(lines)
}

// Persists: cron runs whether or not the user is logged in.
func (c Cron) Persists() (on, known bool) { return false, false }

// FirstPass is the next time the pass line's schedule comes round, on the
// daemon's clock: cron has no delay of its own, so the first pass after an
// install may come within a minute, or up to an interval later. It is given
// in now's zone.
func (c Cron) FirstPass(now time.Time, interval time.Duration) time.Time {
	sched, _ := CronSchedule(interval)
	_, next := cronEvery(strings.Fields(sched), now.In(c.zone()))
	if next.IsZero() {
		return next
	}
	return next.In(now.Location())
}

// read returns the crontab's lines; a user with none has an empty one.
func (c Cron) read() ([]string, error) {
	out, err := c.Run("", "-l")
	if err != nil {
		// "no crontab for USER" is an empty crontab, not a failure.
		if strings.Contains(out, "no crontab") {
			return nil, nil
		}
		return nil, fmt.Errorf("crontab -l: %s", firstLine(out, err))
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// write replaces the crontab with lines.
func (c Cron) write(lines []string) error {
	text := strings.Join(lines, "\n") + "\n"
	if len(lines) == 0 {
		// An empty crontab is removed rather than left as one blank line.
		if out, err := c.Run("", "-r"); err != nil && !strings.Contains(out, "no crontab") {
			return fmt.Errorf("crontab -r: %s", firstLine(out, err))
		}
		return nil
	}
	if out, err := c.Run(text, "-"); err != nil {
		return fmt.Errorf("crontab -: %s", firstLine(out, err))
	}
	return nil
}

// others returns the lines that are not zellij-reaper's.
func others(lines []string) []string {
	return slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return strings.HasSuffix(strings.TrimSpace(l), cronMark) })
}

// CronSchedule turns an interval into a cron schedule. cron can only repeat
// at steps that divide an hour or a day, so an interval between them takes
// the nearest one, which it returns.
func CronSchedule(d time.Duration) (string, time.Duration) {
	steps := []time.Duration{}
	for _, m := range []int{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30} {
		steps = append(steps, time.Duration(m)*time.Minute)
	}
	for _, h := range []int{1, 2, 3, 4, 6, 8, 12, 24} {
		steps = append(steps, time.Duration(h)*time.Hour)
	}
	best := steps[0]
	for _, s := range steps {
		if absDur(s-d) < absDur(best-d) {
			best = s
		}
	}
	switch {
	case best == time.Minute:
		return "* * * * *", best
	case best < time.Hour:
		return fmt.Sprintf("*/%d * * * *", int(best.Minutes())), best
	case best == time.Hour:
		// Five past, off the hour most other jobs start on.
		return "5 * * * *", best
	case best == 24*time.Hour:
		return "5 0 * * *", best
	}
	return fmt.Sprintf("5 */%d * * *", int(best.Hours())), best
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// cronLine is one crontab line that runs the binary with args. The
// variables go on the line itself, so they reach this job only and none of
// the person's lines after it.
func cronLine(schedule string, o InstallOptions, args ...string) string {
	dirs := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	if o.ZellijDir != "" {
		// zellij's directory first, so the job runs the zellij install found,
		// and only once when it is one of the others.
		dirs = append([]string{o.ZellijDir}, slices.DeleteFunc(dirs, func(d string) bool { return d == o.ZellijDir })...)
	}
	env := []string{"env", "PATH=" + shQuote(strings.Join(dirs, ":"))}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		env = append(env, "XDG_RUNTIME_DIR="+shQuote(v))
	}
	// Where the reaper and zellij keep their files, as the installing shell
	// has it (see Carried). cron's own environment has none of them.
	for _, k := range Carried {
		if v, ok := o.Env[k]; ok {
			env = append(env, k+"="+shQuote(v))
		}
	}
	cmd := []string{shQuote(o.Exe)}
	if o.ConfigFile != "" {
		cmd = append(cmd, "--config", shQuote(o.ConfigFile))
	}
	cmd = append(cmd, args...)
	return schedule + " " + strings.Join(env, " ") + " " + strings.Join(cmd, " ") + " >/dev/null 2>&1 " + cronMark
}

// shQuote puts s in single quotes for sh. A % would be cut by cron as the end
// of the command, so it is escaped too.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "'", `'\''`), "%", `\%`) + "'"
}

// Install writes a settings file if there is none and puts both jobs in the
// crontab, replacing any it wrote before.
func (c Cron) Install(o InstallOptions) (Result, error) {
	var r Result
	if !c.Available() {
		return r, errors.New("no cron daemon runs")
	}
	if err := ensureSettings(c.Env, o.ConfigFile, &r); err != nil {
		return r, err
	}
	lines, err := c.read()
	if err != nil {
		return r, err
	}
	lines = others(lines)
	sched, every := CronSchedule(o.Config.Interval)
	if every != o.Config.Interval {
		r.Warnings = append(r.Warnings, fmt.Sprintf("cron cannot repeat every %s: a pass runs every %s", fmtDur(o.Config.Interval), fmtDur(every)))
	}
	lines = append(lines, cronLine(sched, o, "run", "--quiet"))
	if o.Config.RenameInterval > 0 {
		ns, _ := CronSchedule(o.Config.RenameInterval)
		lines = append(lines, cronLine(ns, o, "run", "--names-only", "--quiet"))
	}
	if err := c.write(lines); err != nil {
		return r, err
	}
	r.Wrote = append(r.Wrote, "crontab")
	return r, nil
}

// Uninstall removes zellij-reaper's lines from the crontab; with purge, the
// settings and the state too.
func (c Cron) Uninstall(purge bool, logPath string) (Result, error) {
	var r Result
	if lines, err := c.read(); err == nil {
		if rest := others(lines); len(rest) != len(lines) {
			if err := c.write(rest); err != nil {
				return r, err
			}
			r.Removed = append(r.Removed, "crontab")
		}
	}
	if purge {
		purgeState(c.Env, logPath, &r)
	}
	return r, nil
}

// Status reports on the pass job, NamesStatus on the names job.
func (c Cron) Status() TimerStatus      { return c.statusOf(false) }
func (c Cron) NamesStatus() TimerStatus { return c.statusOf(true) }

// statusOf reads one of the two jobs back from the crontab.
func (c Cron) statusOf(names bool) TimerStatus {
	var st TimerStatus
	lines, err := c.read()
	if err != nil {
		return st
	}
	for _, l := range lines {
		if !strings.HasSuffix(strings.TrimSpace(l), cronMark) || strings.Contains(l, "--names-only") != names {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 6 {
			continue
		}
		st.Installed, st.Enabled = true, true
		st.Active = c.Running()
		// Worked out on the daemon's clock, shown on the local one.
		st.Interval, st.Next = cronEvery(f[:5], time.Now().In(c.zone()))
		if !st.Next.IsZero() {
			st.Next = st.Next.Local()
		}
		st.ExecStart = strings.Join(f[5:], " ")
		st.Env, st.EnvKnown = cronCarried(strings.Join(f[5:], " ")), true
		return st
	}
	return st
}

// cronCarried reads the carried variables back from a job's command, which
// cronLine starts with env and the variables, each value in shQuote's quotes.
func cronCarried(command string) map[string]string {
	env := map[string]string{}
	words := cronWords(command)
	if len(words) == 0 || words[0] != "env" {
		return env
	}
	for _, w := range words[1:] {
		k, v, ok := strings.Cut(w, "=")
		if !ok || k == "" || strings.ContainsAny(k, "/'") {
			break
		}
		if isCarried(k) {
			env[k] = v
		}
	}
	return env
}

// cronWords splits a command the way sh would after cron hands it over:
// single quotes keep everything up to the next one, a backslash outside them
// keeps the next character, and cron's own \% stands for a %.
func cronWords(s string) []string {
	var words []string
	var b strings.Builder
	in, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '%':
			b.WriteByte('%')
			i++
			in = true
		case quoted && c == '\'':
			quoted = false
		case quoted:
			b.WriteByte(c)
		case c == '\'':
			quoted, in = true, true
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			i++
			in = true
		case c == ' ' || c == '\t':
			if in {
				words = append(words, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteByte(c)
			in = true
		}
	}
	if in {
		words = append(words, b.String())
	}
	return words
}

// cronEvery reads back a schedule CronSchedule wrote: how often it runs and
// when next after now, on now's clock, which should be the daemon's. Anything
// else gives zero.
func cronEvery(f []string, now time.Time) (time.Duration, time.Time) {
	min, hour := f[0], f[1]
	step := func(s string) int {
		if s == "*" {
			return 1
		}
		n, err := strconv.Atoi(strings.TrimPrefix(s, "*/"))
		if err != nil || !strings.HasPrefix(s, "*/") || n <= 0 {
			return 0
		}
		return n
	}
	// every is how often the line runs, match whether it runs at a time of
	// day.
	var every time.Duration
	var match func(h, m int) bool
	switch {
	case hour == "*" && strings.HasPrefix(min, "*"):
		n := step(min)
		if n == 0 {
			return 0, time.Time{}
		}
		every, match = time.Duration(n)*time.Minute, func(_, m int) bool { return m%n == 0 }
	default:
		at, err := strconv.Atoi(min)
		if err != nil {
			return 0, time.Time{}
		}
		h := 24
		if hour != "0" {
			if h = step(hour); h == 0 {
				return 0, time.Time{}
			}
		}
		every, match = time.Duration(h)*time.Hour, func(hh, mm int) bool { return mm == at && hh%h == 0 }
	}
	// Minute by minute, as the daemon looks, for at most two days. Where the
	// clock jumps forward (summer time), cron runs a line whose time was
	// skipped right after the jump, so that minute counts as its run: what
	// this gives is a time nothing runs before.
	prev := now.Truncate(time.Minute)
	for range 2 * 24 * 60 {
		t := prev.Add(time.Minute)
		if match(t.Hour(), t.Minute()) || jumpedOver(prev, t, match) {
			return every, t
		}
		prev = t
	}
	return 0, time.Time{}
}

// jumpedOver reports whether the clock jumped forward between two minutes in
// a row, past a time of day the line runs at.
func jumpedOver(prev, t time.Time, match func(h, m int) bool) bool {
	from, to := prev.Hour()*60+prev.Minute(), t.Hour()*60+t.Minute()
	if to < from {
		to += 24 * 60
	}
	// One minute on is the usual step; going back, or on by more than three
	// hours, is no summer-time jump.
	if to-from <= 1 || to-from > 3*60 {
		return false
	}
	for w := from + 1; w < to; w++ {
		if match(w/60%24, w%60) {
			return true
		}
	}
	return false
}

// fmtDur words a duration the way the settings do.
func fmtDur(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
