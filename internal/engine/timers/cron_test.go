package timers

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	// Zones for the tests below, also where the system has no zone files
	// (Windows).
	_ "time/tzdata"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// fakeCrontab is a crontab in memory.
type fakeCrontab struct{ lines []string }

func (f *fakeCrontab) run(stdin string, args ...string) (string, error) {
	switch args[0] {
	case "-l":
		if f.lines == nil {
			return "no crontab for me\n", errExit
		}
		return strings.Join(f.lines, "\n") + "\n", nil
	case "-r":
		f.lines = nil
	case "-":
		f.lines = strings.Split(strings.TrimRight(stdin, "\n"), "\n")
	}
	return "", nil
}

// rawCrontab is a crontab kept byte for byte, as the crontab command keeps
// one: -l prints the file, - takes its input as the new file.
type rawCrontab struct {
	// text is the file; exists is false for a user who has none.
	text   string
	exists bool
	// none is what -l says of a user without a crontab.
	none string
	// refuse, when not empty, is what every call says instead of working;
	// with readOnly, what the calls that change the crontab say.
	refuse   string
	readOnly bool
}

func (f *rawCrontab) run(stdin string, args ...string) (string, error) {
	if f.refuse != "" && (!f.readOnly || args[0] != "-l") {
		return f.refuse, errExit
	}
	switch args[0] {
	case "-l":
		if !f.exists {
			return f.none, errExit
		}
		return f.text, nil
	case "-r":
		f.text, f.exists = "", false
	case "-":
		f.text, f.exists = stdin, true
	}
	return "", nil
}

var errExit = &exitErr{}

type exitErr struct{}

func (*exitErr) Error() string { return "exit status 1" }

func TestCronSchedules(t *testing.T) {
	for _, c := range []struct {
		in    time.Duration
		sched string
		every time.Duration
	}{
		{time.Hour, "5 * * * *", time.Hour},
		{5 * time.Minute, "*/5 * * * *", 5 * time.Minute},
		{time.Minute, "* * * * *", time.Minute},
		{2 * time.Hour, "5 */2 * * *", 2 * time.Hour},
		{24 * time.Hour, "5 0 * * *", 24 * time.Hour},
		{7 * time.Minute, "*/6 * * * *", 6 * time.Minute},
		{90 * time.Minute, "5 * * * *", time.Hour},
	} {
		sched, every := CronSchedule(c.in)
		if sched != c.sched || every != c.every {
			t.Errorf("%v: %q %v, want %q %v", c.in, sched, every, c.sched, c.every)
		}
		// What it writes reads back as the same interval.
		if got, next := cronEvery(strings.Fields(sched), time.Now()); got != every || next.IsZero() {
			t.Errorf("%q reads back as %v, next %v", sched, got, next)
		}
	}
}

func TestFirstPassFollowsEachScheduler(t *testing.T) {
	// The setup screen promises nothing is deleted before the first pass, so
	// it must name the time the scheduler really starts it. cron has no delay
	// of its own: an install at 22:58 meets the hourly line at 23:05, seven
	// minutes on, where the screen used to say ten.
	at := func(d, h, m, s int) time.Time { return time.Date(2026, 11, d, h, m, s, 0, time.Local) }
	now := at(7, 22, 21, 25)
	for _, c := range []struct {
		name  string
		s     Scheduler
		now   time.Time
		every time.Duration
		want  time.Time
	}{
		{"systemd", Systemd{}, now, time.Hour, now.Add(FirstPassDelay)},
		{"Task Scheduler", Tasks{}, now, time.Hour, now.Add(FirstPassDelay)},
		{"launchd", Launchd{}, now, time.Hour, now.Add(time.Hour)},
		{"cron hourly", Cron{}, now, time.Hour, at(7, 23, 5, 0)},
		{"cron hourly, late in the hour", Cron{}, at(7, 22, 58, 0), time.Hour, at(7, 23, 5, 0)},
		{"cron every minute", Cron{}, now, time.Minute, at(7, 22, 22, 0)},
		{"cron every 5 minutes", Cron{}, now, 5 * time.Minute, at(7, 22, 25, 0)},
		{"cron daily", Cron{}, now, 24 * time.Hour, at(8, 0, 5, 0)},
	} {
		if got := c.s.FirstPass(c.now, c.every); !got.Equal(c.want) {
			t.Errorf("%s: first pass %s, want %s", c.name, got.Format("01-02 15:04:05"), c.want.Format("01-02 15:04:05"))
		}
	}
}

// zone loads a time zone from the data built into the test binary.
func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	z, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func TestCronTimesAreOnTheDaemonsClock(t *testing.T) {
	// The daemon reads its lines in the system's zone, which a person's TZ
	// can differ from. With TZ in India and the daemon on UTC, the hourly
	// line runs at 22:35 India time, not 23:05; with TZ in Seoul, the daily
	// one at 09:05, not at midnight. Both came before the time promised.
	india, seoul := zone(t, "Asia/Kolkata"), zone(t, "Asia/Seoul")
	c := Cron{Zone: func() *time.Location { return time.UTC }}
	for _, tc := range []struct {
		now   time.Time
		every time.Duration
		want  time.Time
	}{
		{time.Date(2026, 11, 7, 22, 21, 25, 0, india), time.Hour, time.Date(2026, 11, 7, 22, 35, 0, 0, india)},
		{time.Date(2026, 11, 8, 1, 51, 25, 0, seoul), 24 * time.Hour, time.Date(2026, 11, 8, 9, 5, 0, 0, seoul)},
	} {
		got := c.FirstPass(tc.now, tc.every)
		if !got.Equal(tc.want) || got.Location() != tc.now.Location() {
			t.Errorf("%s every %v: first pass %s, want %s", tc.now.Format("15:04 MST"), tc.every,
				got.Format("01-02 15:04 MST"), tc.want.Format("01-02 15:04 MST"))
		}
	}

	// The status reads an installed line back on the daemon's clock too,
	// and gives the time in the local zone.
	f := &fakeCrontab{}
	c = Cron{Env: config.MapEnv{"HOME": t.TempDir()}, Run: f.run, Running: func() bool { return true },
		Zone: func() *time.Location { return india }}
	o := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", Config: config.Default(config.MapEnv{"HOME": "/h"})}
	if _, err := c.Install(o); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); st.Next.In(india).Minute() != 5 || st.Next.Location() != time.Local {
		t.Errorf("next pass %s in %v, want five past in India", st.Next.In(india).Format("15:04"), st.Next.Location())
	}
}

func TestCronCountsALineTheClockJumpsOver(t *testing.T) {
	// Summer time in Chile starts at midnight: 00:05 never comes on
	// 2026-09-06, and cron runs the daily line right after the jump, at
	// 01:00, not a day later.
	chile := zone(t, "America/Santiago")
	c := Cron{Zone: func() *time.Location { return chile }}
	now := time.Date(2026, 9, 5, 23, 30, 0, 0, chile)
	if got, want := c.FirstPass(now, 24*time.Hour), time.Date(2026, 9, 6, 1, 0, 0, 0, chile); !got.Equal(want) {
		t.Errorf("first pass %s, want %s", got.Format("01-02 15:04 MST"), want.Format("01-02 15:04 MST"))
	}
	// When the clock goes back an hour, nothing counts as jumped over: in
	// New York on 2026-11-01, 01:59 EDT is followed by 01:00 EST, and the
	// hourly line next runs at 01:05 EST.
	ny := zone(t, "America/New_York")
	c = Cron{Zone: func() *time.Location { return ny }}
	now = time.Date(2026, 11, 1, 5, 50, 0, 0, time.UTC) // 01:50 EDT
	if got, want := c.FirstPass(now, time.Hour), time.Date(2026, 11, 1, 6, 5, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("first pass %s, want %s", got.In(ny).Format("01-02 15:04 MST"), want.In(ny).Format("01-02 15:04 MST"))
	}
}

func TestAutoFirstPassIsTheChosenSchedulers(t *testing.T) {
	// Without a systemd user instance the passes go to cron, and so does the
	// promise of when the first one comes.
	f := &fakeCrontab{}
	a := auto{sys: Systemd{Run: func(string, ...string) (string, error) { return "", errExit }},
		cron: Cron{Run: f.run, Running: func() bool { return true }}, once: &sync.Once{}, pick: new(Scheduler)}
	now := time.Date(2026, 11, 7, 22, 21, 25, 0, time.Local)
	want := time.Date(2026, 11, 7, 23, 5, 0, 0, time.Local)
	if got := a.FirstPass(now, time.Hour); a.Kind() != "cron" || !got.Equal(want) {
		t.Errorf("%s: first pass %s, want 23:05", a.Kind(), got.Format("15:04:05"))
	}
}

func TestUninstallRemovesTheTimersOfBothSchedulers(t *testing.T) {
	// A machine that moved between the two: systemd answers today and the
	// crontab still holds the jobs of an install from before it did, or the
	// other way round. Either way an uninstall leaves no timer behind.
	for _, systemdUp := range []bool{true, false} {
		env := config.MapEnv{"HOME": t.TempDir()}
		cfg := config.Default(env)
		run := &fakeRun{linger: "no"}
		f := &fakeCrontab{lines: []string{"17 4 * * * /bin/true"}}
		sys := Systemd{Env: env, Run: run.run, User: "u"}
		cron := Cron{Env: env, Run: f.run, Running: func() bool { return true }}
		o := InstallOptions{Exe: "/x/zellij-reaper", Config: cfg}
		for _, s := range []Scheduler{sys, cron} {
			if _, err := s.Install(o); err != nil {
				t.Fatal(err)
			}
		}
		run.noUser = !systemdUp
		a := auto{sys: sys, cron: cron, once: &sync.Once{}, pick: new(Scheduler)}
		kind := map[bool]string{true: "systemd", false: "cron"}[systemdUp]
		// Four unit files and the crontab's jobs; the settings a purge takes
		// are not timers.
		r, err := a.Uninstall(true, cfg.LogPath)
		if err != nil || a.Kind() != kind || r.Timers != 5 {
			t.Errorf("%s chosen: %v, %d timers, removed %v", a.Kind(), err, r.Timers, r.Removed)
		}
		if cron.HasJobs() || !slices.Equal(f.lines, []string{"17 4 * * * /bin/true"}) {
			t.Errorf("%s chosen: the crontab now holds %q", kind, f.lines)
		}
		// What both removed is said, the other scheduler's first: the
		// crontab's lines before the unit files where systemd was chosen,
		// after them where cron was.
		cronAt, unitAt := slices.Index(r.Removed, CronJobs), slices.Index(r.Removed, sys.timerPath())
		if cronAt < 0 || unitAt < 0 || (cronAt < unitAt) != systemdUp {
			t.Errorf("%s chosen: removed %q", kind, r.Removed)
		}
		if _, err := os.Stat(sys.timerPath()); err == nil {
			t.Errorf("%s chosen: systemd's timer file is still there", kind)
		}
		// The purge is done once, by the scheduler chosen.
		if _, err := os.Stat(config.ConfigFile(env)); err == nil {
			t.Errorf("%s chosen: the purge kept the settings", kind)
		}
		// Only a systemd that did not answer leaves something to say.
		if want := map[bool]int{true: 0, false: 1}[systemdUp]; len(r.Warnings) != want {
			t.Errorf("%s chosen: warnings %q", kind, r.Warnings)
		}
	}
	// On a machine with neither, the other scheduler's troubles are not this
	// uninstall's: no line about a crontab command that is not there.
	a := auto{sys: Systemd{Env: config.MapEnv{"HOME": t.TempDir()}, Run: (&fakeRun{noUser: true}).run},
		cron: Cron{Run: func(string, ...string) (string, error) { return "", errExit }, Running: func() bool { return false }},
		once: &sync.Once{}, pick: new(Scheduler)}
	if r, err := a.Uninstall(false, ""); err != nil || r.Timers != 0 || len(r.Warnings) != 0 {
		t.Errorf("uninstall with no scheduler: %v, %+v", err, r)
	}
	// Jobs the other scheduler holds and cannot remove are said to stay, and
	// the chosen one's timers still go.
	env := config.MapEnv{"HOME": t.TempDir()}
	run := &fakeRun{linger: "no"}
	sys := Systemd{Env: env, Run: run.run, User: "u"}
	if _, err := sys.Install(InstallOptions{Exe: "/x/zellij-reaper", Config: config.Default(env)}); err != nil {
		t.Fatal(err)
	}
	stuck := "5 * * * * /x run --quiet " + cronMark + "\n"
	f := &rawCrontab{text: stuck, exists: true, refuse: "crontab: /var/spool/cron: Read-only file system\n", readOnly: true}
	a = auto{sys: sys, cron: Cron{Env: env, Run: f.run, Running: func() bool { return true }}, once: &sync.Once{}, pick: new(Scheduler)}
	r, err := a.Uninstall(false, "")
	if err != nil || r.Timers != 4 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "cron's timers stay") || !strings.Contains(r.Warnings[0], "Read-only file system") {
		t.Errorf("uninstall with a crontab that cannot be written: %v, %+v", err, r)
	}
	if f.text != stuck {
		t.Errorf("the crontab now holds %q", f.text)
	}
}

func TestCronUninstallSaysWhenTheCrontabCannotBeRead(t *testing.T) {
	// The jobs of a crontab that cannot be read stay: said, not passed over
	// as if there were none. A purge still removes what it can.
	env := config.MapEnv{"HOME": t.TempDir()}
	cfg := config.Default(env)
	os.MkdirAll(filepath.Dir(config.ConfigFile(env)), 0o755)
	os.WriteFile(config.ConfigFile(env), []byte("# settings\n"), 0o644)
	f := &rawCrontab{text: "5 * * * * /x run --quiet " + cronMark + "\n", exists: true, refuse: "crontab: must be suid to work properly\n"}
	c := Cron{Env: env, Run: f.run, Running: func() bool { return true }}
	r, err := c.Uninstall(true, cfg.LogPath)
	if err != nil || r.Timers != 0 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "must be suid to work properly") {
		t.Errorf("uninstall: %v, %+v", err, r)
	}
	if _, err := os.Stat(config.ConfigFile(env)); err == nil {
		t.Error("the purge kept the settings")
	}
}

func TestCronLinePutsZellijFirstAndOnce(t *testing.T) {
	for dir, want := range map[string]string{
		"/opt/zellij":    "PATH='/opt/zellij:/usr/local/bin:/usr/bin:/bin'",
		"/usr/local/bin": "PATH='/usr/local/bin:/usr/bin:/bin'",
		"/usr/bin":       "PATH='/usr/bin:/usr/local/bin:/bin'",
		"":               "PATH='/usr/local/bin:/usr/bin:/bin'",
	} {
		line := cronLine("5 * * * *", InstallOptions{Exe: "/x/zellij-reaper", ZellijDir: dir}, "run", "--quiet")
		if !strings.Contains(line, " "+want+" ") {
			t.Errorf("zellij in %q: %s", dir, line)
		}
	}
}

func TestCronInstallKeepsTheOwnersLines(t *testing.T) {
	f := &fakeCrontab{lines: []string{"MAILTO=me", "0 3 * * * backup.sh"}}
	c := Cron{Env: config.MapEnv{"HOME": t.TempDir()}, Run: f.run, Running: func() bool { return true }}
	cfg := config.Default(config.MapEnv{"HOME": "/h"})
	o := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", ZellijDir: "/opt/zellij", Config: cfg}
	for range 2 { // a second install replaces the first one's lines
		if _, err := c.Install(o); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.lines) != 4 || !slices.Equal(f.lines[:2], []string{"MAILTO=me", "0 3 * * * backup.sh"}) {
		t.Fatalf("crontab:\n%s", strings.Join(f.lines, "\n"))
	}
	if !strings.HasPrefix(f.lines[2], "5 * * * * env PATH='/opt/zellij:") || !strings.Contains(f.lines[2], "'/h/.local/bin/zellij-reaper' run --quiet") {
		t.Errorf("pass line: %s", f.lines[2])
	}
	st, ns := c.Status(), c.NamesStatus()
	if !st.Installed || !st.Active || st.Interval != time.Hour || st.Next.IsZero() || !ns.Installed || ns.Interval != 5*time.Minute {
		t.Errorf("status %+v, names %+v", st, ns)
	}
	if _, err := c.Uninstall(false, ""); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.lines, []string{"MAILTO=me", "0 3 * * * backup.sh"}) {
		t.Errorf("after uninstall:\n%s", strings.Join(f.lines, "\n"))
	}
	// An install refuses where no daemon would run it.
	c.Running = func() bool { return false }
	if _, err := c.Install(o); err == nil {
		t.Error("installed with no cron daemon")
	}
}

func TestCronGivesTheOwnersCrontabBackByteForByte(t *testing.T) {
	// The crontab Alpine gives root: tabs between the fields and a blank line
	// at the end, which an install and an uninstall used to drop. One that
	// starts with a blank line and one that is a blank line alone, too.
	alpine := "# do daily/weekly/monthly maintenance\n# min\thour\tday\tmonth\tweekday\tcommand\n*/15\t*\t*\t*\t*\trun-parts /etc/periodic/15min\n0\t5\t1\t*\t*\trun-parts /etc/periodic/monthly\n\n"
	for _, text := range []string{alpine, "\n  # indented\n17 4 * * * /bin/true\n", "\n"} {
		f := &rawCrontab{text: text, exists: true}
		home := t.TempDir()
		c := Cron{Env: config.MapEnv{"HOME": home}, Run: f.run, Running: func() bool { return true }}
		o := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", Config: config.Default(config.MapEnv{"HOME": "/h"})}
		if _, err := c.Install(o); err != nil {
			t.Fatal(err)
		}
		// The owner's bytes come first, untouched, and the two jobs after.
		if rest, ok := strings.CutPrefix(f.text, text); !ok || strings.Count(rest, cronMark+"\n") != 2 || strings.Count(rest, "\n") != 2 {
			t.Errorf("after install of %q:\n%q", text, f.text)
		}
		if st := c.Status(); !st.Installed {
			t.Errorf("status after install of %q: %+v", text, st)
		}
		if _, err := c.Uninstall(false, ""); err != nil {
			t.Fatal(err)
		}
		if f.text != text || !f.exists {
			t.Errorf("after uninstall: %q, want %q", f.text, text)
		}
	}
}

func TestCronTakesEachCrontabsWordsForNone(t *testing.T) {
	// What crontab -l says of a user who has no crontab: cron and cronie, then
	// busybox (Alpine), whose words an install took for a failure.
	for _, none := range []string{"no crontab for u\n", "crontab: can't open 'u': No such file or directory\n"} {
		f := &rawCrontab{none: none}
		c := Cron{Env: config.MapEnv{"HOME": t.TempDir()}, Run: f.run, Running: func() bool { return true }}
		o := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", Config: config.Default(config.MapEnv{"HOME": "/h"})}
		// Twice: the uninstall in between removes the crontab again.
		for range 2 {
			if _, err := c.Install(o); err != nil {
				t.Fatalf("%q: %v", none, err)
			}
			if !c.HasJobs() || strings.Count(f.text, "\n") != 2 {
				t.Errorf("%q: crontab after install:\n%s", none, f.text)
			}
			if res, err := c.Uninstall(false, ""); err != nil || len(res.Removed) != 1 || f.exists {
				t.Errorf("%q: uninstall %v, removed %v, crontab still there: %v", none, err, res.Removed, f.exists)
			}
		}
	}
}

func TestCronInstallWritesNothingWhereTheCrontabCannotBeRead(t *testing.T) {
	// busybox's crontab without its setuid bit, and a crontab that is there
	// and cannot be opened: neither is an empty crontab to write over.
	for _, refuse := range []string{"crontab: must be suid to work properly\n", "crontab: can't open 'u': Permission denied\n"} {
		f := &rawCrontab{text: "17 4 * * * /bin/true\n", exists: true, refuse: refuse}
		env := config.MapEnv{"HOME": t.TempDir()}
		c := Cron{Env: env, Run: f.run, Running: func() bool { return true }}
		_, err := c.Install(InstallOptions{Exe: "/h/.local/bin/zellij-reaper", Config: config.Default(env)})
		if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(refuse)) {
			t.Errorf("%q: install: %v", refuse, err)
		}
		// Nothing of the install stays behind, the settings file included.
		if _, err := os.Stat(config.ConfigFile(env)); err == nil {
			t.Errorf("%q: the settings file was written", refuse)
		}
		if f.text != "17 4 * * * /bin/true\n" {
			t.Errorf("%q: crontab now %q", refuse, f.text)
		}
	}
}

func TestCronRefusesALineBreak(t *testing.T) {
	// A line break in a path would end the job's line and start another.
	base := InstallOptions{Exe: "/h/.local/bin/zellij-reaper", Config: config.Default(config.MapEnv{"HOME": "/h"})}
	for what, o := range map[string]InstallOptions{
		"binary":   {Exe: "/h/bin\n* * * * * /bin/false", Config: base.Config},
		"settings": {Exe: base.Exe, ConfigFile: "/h/a\nb.toml", Config: base.Config},
		"zellij":   {Exe: base.Exe, ZellijDir: "/opt/z\r", Config: base.Config},
		"carried":  {Exe: base.Exe, Config: base.Config, Env: map[string]string{"XDG_STATE_HOME": "/h/state\n* * * * * /bin/false"}},
	} {
		f := &rawCrontab{none: "no crontab for u\n"}
		env := config.MapEnv{"HOME": t.TempDir()}
		c := Cron{Env: env, Run: f.run, Running: func() bool { return true }}
		if _, err := c.Install(o); err == nil || !strings.Contains(err.Error(), "line break") {
			t.Errorf("%s: install: %v", what, err)
		}
		if f.exists {
			t.Errorf("%s: a crontab was written:\n%s", what, f.text)
		}
		if _, err := os.Stat(config.ConfigFile(env)); err == nil {
			t.Errorf("%s: the settings file was written", what)
		}
	}
}
