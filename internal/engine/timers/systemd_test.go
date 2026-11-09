package timers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// fakeRun answers systemctl and loginctl like a machine with a user instance
// and records every call.
type fakeRun struct {
	calls  []string
	linger string
	show   string
	noUser bool
}

func (f *fakeRun) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case strings.Contains(call, "show-environment") && f.noUser:
		return "Failed to connect to bus", os.ErrNotExist
	case strings.Contains(call, "show-user"):
		return "Linger=" + f.linger, nil
	case strings.Contains(call, "show "+TimerName):
		return f.show, nil
	}
	return "", nil
}

func newSystem(t *testing.T) (Systemd, *fakeRun, config.MapEnv) {
	t.Helper()
	env := config.MapEnv{"HOME": t.TempDir()}
	f := &fakeRun{linger: "no"}
	return Systemd{Env: env, Run: f.run, User: "u"}, f, env
}

func TestUnits(t *testing.T) {
	svc := ServiceUnit("/home/u/bin/zellij 100%/zellij-reaper", "/opt/zellij/bin", "", nil)
	if custom := ServiceUnit("/usr/bin/zellij-reaper", "", "/etc/zr.toml", nil); !strings.Contains(custom, `ExecStart="/usr/bin/zellij-reaper" --config "/etc/zr.toml" run --quiet`) {
		t.Errorf("a chosen settings file is not passed to the timer:\n%s", custom)
	}
	for _, want := range []string{
		`ExecStart="/home/u/bin/zellij 100%%/zellij-reaper" run --quiet`,
		`Environment="PATH=/opt/zellij/bin:%h/.local/bin:%h/.cargo/bin:`,
		"TimeoutStartSec=60", "Type=oneshot",
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("service lacks %q:\n%s", want, svc)
		}
	}
	timer := TimerUnit(time.Hour)
	for _, want := range []string{"OnActiveSec=10min", "OnUnitActiveSec=1h", "AccuracySec=5m", "WantedBy=timers.target"} {
		if !strings.Contains(timer, want) {
			t.Errorf("timer lacks %q:\n%s", want, timer)
		}
	}
	if !strings.Contains(TimerUnit(5*time.Minute), "AccuracySec=25s") {
		t.Errorf("short interval accuracy:\n%s", TimerUnit(5*time.Minute))
	}
}

func TestInstallFresh(t *testing.T) {
	s, f, env := newSystem(t)
	cfg := config.Default(env)
	r, err := s.Install(InstallOptions{Exe: "/usr/bin/zellij-reaper", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	// The pass timer and, with rename_interval on by default, the names timer.
	if len(r.Wrote) != 4 || r.Created == "" || r.Migrated != "" || r.Linger != "enabled" {
		t.Errorf("result %+v", r)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"systemctl --user daemon-reload", "systemctl --user enable --now zellij-reaper.timer", "systemctl --user enable --now zellij-reaper-names.timer", "loginctl enable-linger u"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in\n%s", want, joined)
		}
	}
	// The written settings file loads cleanly and holds the defaults.
	c, err := config.Load("", env)
	if err != nil || c.File == "" || c.IdleAfter != cfg.IdleAfter {
		t.Errorf("settings after install: %+v %v", c, err)
	}
	// Installing again keeps the settings file as it is.
	os.WriteFile(config.ConfigFile(env), []byte("idle_after = \"5h\"\n"), 0o644)
	if _, err := s.Install(InstallOptions{Exe: "/usr/bin/zellij-reaper", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(config.ConfigFile(env)); string(b) != "idle_after = \"5h\"\n" {
		t.Errorf("a second install rewrote the settings: %q", b)
	}
}

func TestUnitPathsAreEscapedOrRefused(t *testing.T) {
	// ExecStart= expands "$NAME"; "$$" is a plain "$".
	svc := ServiceUnit("/home/u/$HOME/bin/zellij-reaper", "", "/etc/$x.toml", nil)
	if want := `ExecStart="/home/u/$$HOME/bin/zellij-reaper" --config "/etc/$$x.toml" run --quiet`; !strings.Contains(svc, want) {
		t.Errorf("service lacks %q:\n%s", want, svc)
	}
	// A quote, a backslash or a line break cannot be quoted there: Install
	// refuses before it writes anything.
	for _, o := range []InstallOptions{
		{Exe: `/opt/a"b/zellij-reaper`},
		{Exe: `/opt/a\b/zellij-reaper`},
		{Exe: "/usr/bin/zellij-reaper", ConfigFile: "/etc/zr.toml\nExecStartPre=/bin/false"},
	} {
		s, f, env := newSystem(t)
		o.Config = config.Default(env)
		if _, err := s.Install(o); err == nil {
			t.Errorf("installed with %q / %q", o.Exe, o.ConfigFile)
		}
		if _, err := os.Stat(s.servicePath()); !os.IsNotExist(err) {
			t.Errorf("wrote the service for %q / %q", o.Exe, o.ConfigFile)
		}
		if _, err := os.Stat(config.ConfigFile(env)); !os.IsNotExist(err) {
			t.Errorf("wrote the settings for %q / %q", o.Exe, o.ConfigFile)
		}
		if len(f.calls) != 0 {
			t.Errorf("ran %v for %q / %q", f.calls, o.Exe, o.ConfigFile)
		}
	}
}

func TestInstallWithoutUserInstance(t *testing.T) {
	s, f, _ := newSystem(t)
	f.noUser = true
	if _, err := s.Install(InstallOptions{Exe: "/x", Config: config.Default(s.Env)}); err == nil {
		t.Error("installed with no systemd user instance")
	}
}

// legacyUnit is the shell version's service file.
const legacyUnit = `[Service]
Type=oneshot
# Defaults: 1h threshold, live mode. Set DRY_RUN=1 here to test without deleting.
Environment=DRY_RUN=0
Environment=MAX_AGE_HOURS=1
Environment=PATH=%h/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=%h/.local/bin/zellij-reaper.sh
`

func TestInstallMovesTheShellVersionOver(t *testing.T) {
	s, _, env := newSystem(t)
	home := env.Home()
	unitDir := config.UnitDir(env)
	os.MkdirAll(unitDir, 0o755)
	os.WriteFile(filepath.Join(unitDir, ServiceName), []byte(legacyUnit), 0o644)
	bin := filepath.Join(home, ".local/bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "zellij-reaper.sh"), []byte("#!/usr/bin/env bash\n# zellij-reaper.sh — safely reap\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "zellij-reap"), []byte("#!/usr/bin/env bash\n# reap.sh — manually trigger a zellij-reaper pass.\n"), 0o755)
	// Something else the user keeps there must survive.
	os.WriteFile(filepath.Join(bin, "other-tool"), []byte("#!/bin/sh\n"), 0o755)
	cache := filepath.Join(home, ".cache")
	os.MkdirAll(cache, 0o755)
	os.WriteFile(filepath.Join(cache, "zellij-reaper.log"), []byte("[2026-05-12 13:18:06] === reaper start ===\n"), 0o644)
	os.WriteFile(filepath.Join(cache, "zellij-reaper.log.1"), []byte("older\n"), 0o644)

	cfg := config.Default(env)
	r, err := s.Install(InstallOptions{Exe: "/usr/bin/zellij-reaper", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if r.Migrated == "" || len(r.Moved) != 2 || len(r.Removed) != 2 {
		t.Errorf("result %+v", r)
	}
	c, err := config.Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	// DRY_RUN=0 and MAX_AGE_HOURS=1; the shell version renamed by default.
	if c.DryRun || c.IdleAfter != time.Hour || c.ExitedAfter != time.Hour || !c.AutoRename {
		t.Errorf("migrated settings: dry %v idle %v exited %v rename %v", c.DryRun, c.IdleAfter, c.ExitedAfter, c.AutoRename)
	}
	if b, err := os.ReadFile(cfg.LogPath); err != nil || !strings.Contains(string(b), "reaper start") {
		t.Errorf("log not moved: %v", err)
	}
	if b, _ := os.ReadFile(cfg.LogPath + ".1"); string(b) != "older\n" {
		t.Error("previous log not moved")
	}
	if _, err := os.Stat(filepath.Join(bin, "other-tool")); err != nil {
		t.Error("an unrelated file was removed")
	}
	// The service now runs the new binary.
	if b, _ := os.ReadFile(filepath.Join(unitDir, ServiceName)); strings.Contains(string(b), "zellij-reaper.sh") {
		t.Error("the shell version's service is still in place")
	}
}

func TestMigrationKeepsACustomLogWhereTheSettingsSay(t *testing.T) {
	s, _, env := newSystem(t)
	home := env.Home()
	unitDir := config.UnitDir(env)
	os.MkdirAll(unitDir, 0o755)
	// The shell version was told to log somewhere of its own.
	os.WriteFile(filepath.Join(unitDir, ServiceName), []byte(legacyUnit+"Environment=LOG=%h/logs/reaper.log\n"), 0o644)
	custom := filepath.Join(home, "logs", "reaper.log")
	os.MkdirAll(filepath.Dir(custom), 0o755)
	os.WriteFile(custom, []byte("[2026-05-12 13:18:06] === reaper start ===\n"), 0o644)

	// The settings in effect before the move know nothing of it.
	cfg := config.Default(env)
	r, err := s.Install(InstallOptions{Exe: "/usr/bin/zellij-reaper", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load("", env)
	if err != nil {
		t.Fatal(err)
	}
	if c.LogPath != custom {
		t.Fatalf("migrated log path %q, want %q", c.LogPath, custom)
	}
	// The log stays where the moved settings point.
	if b, err := os.ReadFile(custom); err != nil || !strings.Contains(string(b), "reaper start") {
		t.Errorf("the custom log is gone (%v); moved %v", err, r.Moved)
	}
	if _, err := os.Stat(cfg.LogPath); !os.IsNotExist(err) {
		t.Errorf("the log was moved to the default %s; moved %v", cfg.LogPath, r.Moved)
	}
}

func TestLegacyDefaults(t *testing.T) {
	env := config.MapEnv{"HOME": "/home/u"}
	// With nothing set, the shell version ran dry, waited 3 days, renamed.
	c := (&Legacy{Vars: map[string]string{}}).Config(env)
	if !c.DryRun || c.IdleAfter != 72*time.Hour || !c.AutoRename {
		t.Errorf("defaults: %+v", c)
	}
	c = (&Legacy{Vars: ParseEnvironment(`Environment="PROTECT_REGEX=^keep me" MAX_AGE_DAYS=2 AUTO_RENAME=0`)}).Config(env)
	if c.Protect != "^keep me" || c.IdleAfter != 48*time.Hour || c.AutoRename {
		t.Errorf("parsed: protect %q idle %v rename %v", c.Protect, c.IdleAfter, c.AutoRename)
	}
}

func TestStatus(t *testing.T) {
	s, f, env := newSystem(t)
	f.show = "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nLastTriggerUSec=Wed 2026-11-04 03:11:47 KST\nNextElapseUSecRealtime="
	os.MkdirAll(config.UnitDir(env), 0o755)
	os.WriteFile(filepath.Join(config.UnitDir(env), TimerName), []byte(TimerUnit(time.Hour)), 0o644)
	os.WriteFile(filepath.Join(config.UnitDir(env), ServiceName), []byte(ServiceUnit("/usr/bin/zellij-reaper", "", "", nil)), 0o644)
	st := s.Status()
	if !st.Installed || !st.Enabled || !st.Active || st.Interval != time.Hour {
		t.Errorf("status %+v", st)
	}
	want := time.Date(2026, 11, 4, 4, 11, 47, 0, time.Local)
	if !st.Next.Equal(want) {
		t.Errorf("next %v, want %v", st.Next, want)
	}
	if !strings.Contains(st.ExecStart, "/usr/bin/zellij-reaper") {
		t.Errorf("exec %q", st.ExecStart)
	}
}

func TestAZellijDirectoryWithASpaceStaysOnePath(t *testing.T) {
	svc := ServiceUnit("/usr/bin/zellij-reaper", "/opt/my tools/bin", "", nil)
	if !strings.Contains(svc, `Environment="PATH=/opt/my tools/bin:`) {
		t.Errorf("the PATH assignment is not quoted:\n%s", svc)
	}
	s, _, env := newSystem(t)
	if _, err := s.Install(InstallOptions{Exe: "/x", ZellijDir: "/opt/a\"b", Config: config.Default(env)}); err == nil {
		t.Error("a zellij directory with a quote was written into the unit")
	}
}

func TestStatusBeforeTheFirstRun(t *testing.T) {
	s, f, env := newSystem(t)
	// Installed a minute ago: no run yet, and systemd knows only the
	// monotonic time it will fire at.
	f.show = "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nLastTriggerUSec=n/a\nNextElapseUSecRealtime=\nNextElapseUSecMonotonic=2d 3h 9min 30.5s"
	s.Uptime = func() (time.Duration, bool) { return 2*24*time.Hour + 3*time.Hour + 30*time.Second, true }
	os.MkdirAll(config.UnitDir(env), 0o755)
	os.WriteFile(filepath.Join(config.UnitDir(env), TimerName), []byte(TimerUnit(time.Hour)), 0o644)
	st := s.Status()
	if got := time.Until(st.Next); got < 8*time.Minute || got > 10*time.Minute {
		t.Errorf("next run in %v, want about 9 minutes (%+v)", got, st)
	}
	// A stopped timer does not fire, whatever systemd last computed.
	f.show = "LoadState=loaded\nActiveState=inactive\nUnitFileState=enabled\nNextElapseUSecMonotonic=2d 3h 9min"
	if st := s.Status(); !st.Next.IsZero() {
		t.Errorf("a stopped timer has a next run: %v", st.Next)
	}
}

func TestParseTimespan(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"2d 13h 55min 46.608644s": 2*24*time.Hour + 13*time.Hour + 55*time.Minute + 46608644*time.Microsecond,
		"10min":                   10 * time.Minute,
		"500ms":                   500 * time.Millisecond,
		"1w 2d":                   9 * 24 * time.Hour,
	} {
		if got, ok := parseTimespan(in); !ok || got != want {
			t.Errorf("parseTimespan(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "infinity", "n/a", "5 min", "-3s"} {
		if got, ok := parseTimespan(in); ok {
			t.Errorf("parseTimespan(%q) = %v, want no time", in, got)
		}
	}
}

func TestUninstallPurge(t *testing.T) {
	s, _, env := newSystem(t)
	cfg := config.Default(env)
	if _, err := s.Install(InstallOptions{Exe: "/x", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755)
	os.WriteFile(cfg.LogPath, []byte("x\n"), 0o644)
	r, _ := s.Uninstall(false, cfg.LogPath)
	if len(r.Removed) != 4 {
		t.Errorf("uninstall removed %v", r.Removed)
	}
	if _, err := os.Stat(cfg.LogPath); err != nil {
		t.Error("uninstall without --purge removed the log")
	}
	// A protection leaves the list and its lock file in the state directory.
	pl, _ := protect.Load(config.ProtectedFile(env))
	if err := pl.Set("notes", true); err != nil {
		t.Fatal(err)
	}
	// So do the names a pass gave, and the names someone kept.
	if err := protect.LoadGiven(config.GivenFile(env)).Set("build-watcher", true); err != nil {
		t.Fatal(err)
	}
	if err := protect.LoadKept(config.KeptFile(env)).Set("brave-peach", true); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Uninstall(true, cfg.LogPath)
	if _, err := os.Stat(config.ConfigFile(env)); !os.IsNotExist(err) {
		t.Error("purge kept the settings")
	}
	if _, err := os.Stat(cfg.LogPath); !os.IsNotExist(err) {
		t.Error("purge kept the log")
	}
	if _, err := os.Stat(config.StateDir(env)); !os.IsNotExist(err) {
		left, _ := os.ReadDir(config.StateDir(env))
		t.Errorf("purge kept the state directory: %v", left)
	}
	// The timers were gone before the purge: what it removed is not counted
	// as timers.
	if r.Timers != 0 || len(r.Removed) == 0 {
		t.Errorf("a purge with no timers left: %d timers, removed %v", r.Timers, r.Removed)
	}
}

func TestUninstallWithoutAUserInstanceSaysWhatMayStillRun(t *testing.T) {
	// Installed from a login shell, removed from one systemd does not answer
	// in (su, sudo). Measured on systemd 249: a timer whose files are removed
	// fires on until it is stopped by name.
	s, f, env := newSystem(t)
	if _, err := s.Install(InstallOptions{Exe: "/x", Config: config.Default(env)}); err != nil {
		t.Fatal(err)
	}
	// What `enable` leaves (a plain file stands in for each link, which a
	// Windows machine may not let a test make).
	wants := filepath.Join(config.UnitDir(env), "timers.target.wants")
	os.MkdirAll(wants, 0o755)
	for _, name := range []string{TimerName, NamesTimerName} {
		os.WriteFile(filepath.Join(wants, name), nil, 0o644)
	}
	f.noUser, f.calls = true, nil
	r, err := s.Uninstall(false, "")
	if err != nil || r.Timers != 4 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "`zellij-reaper uninstall` from a login shell") {
		t.Errorf("uninstall without a user instance: %v, %+v", err, r)
	}
	if left, _ := os.ReadDir(wants); len(left) != 0 {
		t.Errorf("%d of the links enable made are left", len(left))
	}
	// A systemd that does not answer is asked nothing more.
	for _, c := range f.calls {
		if !strings.Contains(c, "show-environment") {
			t.Errorf("called %q with no user instance", c)
		}
	}
	// Again from a login shell: the files are gone, so there is nothing to
	// count or to warn of, and the timers still loaded are stopped by name.
	f.noUser, f.calls = false, nil
	r, err = s.Uninstall(false, "")
	if err != nil || r.Timers != 0 || len(r.Warnings) != 0 {
		t.Errorf("uninstall again: %v, %+v", err, r)
	}
	if calls := strings.Join(f.calls, "\n"); !strings.Contains(calls, "systemctl --user stop "+TimerName+" "+NamesTimerName) {
		t.Errorf("the loaded timers were not stopped:\n%s", calls)
	}
	// A machine that never had the timers gets no warning either.
	fresh, ff, _ := newSystem(t)
	ff.noUser = true
	if r, _ := fresh.Uninstall(false, ""); r.Timers != 0 || len(r.Warnings) != 0 {
		t.Errorf("uninstall where nothing was installed: %+v", r)
	}
}

func TestExpandUnitPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd units are Linux paths")
	}
	env := config.MapEnv{"HOME": "/home/u"}
	cases := map[string]string{
		"%h/logs/reaper.log":   "/home/u/logs/reaper.log",
		"$HOME/r.log":          "/home/u/r.log",
		"${HOME}/r.log":        "/home/u/r.log",
		"~/r.log":              "/home/u/r.log",
		"/var/log/zr.log":      "/var/log/zr.log",
		"relative/r.log":       "",
		"":                     "",
		"/home/u/../u/./r.log": "/home/u/r.log",
	}
	for in, want := range cases {
		if got := expandUnitPath(in, env); got != want {
			t.Errorf("expandUnitPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOnlyTheShellVersionsScriptsAreRemoved(t *testing.T) {
	_, _, env := newSystem(t)
	bin := filepath.Join(env.Home(), ".local/bin")
	os.MkdirAll(bin, 0o755)
	// A link named like the script, pointing at something else, stays.
	other := filepath.Join(env.Home(), "elsewhere.sh")
	os.WriteFile(other, []byte("#!/bin/sh\n# zellij-reaper look-alike\n"), 0o755)
	os.Symlink(other, filepath.Join(bin, "zellij-reaper.sh"))
	// A file without a shebang stays too.
	os.WriteFile(filepath.Join(bin, "zellij-reap"), []byte("zellij-reaper notes\n"), 0o644)
	_, removed := (&Legacy{Vars: map[string]string{}}).Retire(env, filepath.Join(env.Home(), "new.log"))
	if len(removed) != 0 {
		t.Errorf("removed %v", removed)
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("the link's target was removed")
	}
}

func TestTheNamesTimerOnlyRenamesAndGoesWithItsSetting(t *testing.T) {
	svc := NamesServiceUnit("/usr/bin/zellij-reaper", "", "", nil)
	if !strings.Contains(svc, `ExecStart="/usr/bin/zellij-reaper" run --names-only --quiet`) {
		t.Errorf("names service:\n%s", svc)
	}
	timer := NamesTimerUnit(5 * time.Minute)
	for _, want := range []string{"OnActiveSec=1min", "OnUnitActiveSec=5m", "AccuracySec=25s"} {
		if !strings.Contains(timer, want) {
			t.Errorf("names timer lacks %q:\n%s", want, timer)
		}
	}
	// rename_interval 0: install takes the names timer away again.
	s, f, env := newSystem(t)
	cfg := config.Default(env)
	if _, err := s.Install(InstallOptions{Exe: "/x", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	cfg.RenameInterval = 0
	r, err := s.Install(InstallOptions{Exe: "/x", Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Wrote) != 2 || len(r.Removed) != 2 || !strings.Contains(strings.Join(f.calls, "\n"), "systemctl --user disable --now zellij-reaper-names.timer") {
		t.Errorf("result %+v, calls %v", r, f.calls)
	}
}

func TestOnlyARunningTimerHasANextPass(t *testing.T) {
	next := time.Now().Add(42 * time.Minute)
	on := TimerStatus{Installed: true, Enabled: true, Active: true, Next: next}
	if !on.NextPass().Equal(next) {
		t.Errorf("a running timer: %v, want %v", on.NextPass(), next)
	}
	// A cron line keeps its next time while the daemon is down.
	for _, st := range []TimerStatus{
		{Installed: true, Enabled: true, Active: false, Next: next},
		{Installed: true, Enabled: false, Active: true, Next: next},
		{Installed: false, Next: next},
	} {
		if !st.NextPass().IsZero() {
			t.Errorf("%+v has a next pass", st)
		}
	}
}
