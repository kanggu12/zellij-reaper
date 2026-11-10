package timers

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// stateOf writes everything the reaper keeps for env, as a run of it would
// have: the settings, both logs, the three lists with their locks, the pass
// lock and the quiet book. It returns the log's path.
func stateOf(t *testing.T, env config.Env) string {
	t.Helper()
	cfg := config.Default(env)
	// The lists are written by the code that writes them for real, so their
	// lock files are the real ones too.
	if pl, err := protect.Load(config.ProtectedFile(env)); err != nil || pl.Set("notes", true) != nil {
		t.Fatalf("the protected list: %v", err)
	}
	if err := protect.LoadGiven(config.GivenFile(env)).Set("build-watcher", true); err != nil {
		t.Fatal(err)
	}
	if err := protect.LoadKept(config.KeptFile(env)).Set("brave-peach", true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{config.ConfigFile(env), cfg.LogPath, cfg.LogPath + ".1", config.LockFile(env), config.QuietFile(env)} {
		put(t, p)
	}
	return cfg.LogPath
}

// put writes a small file at p, making its directory.
func put(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gone reports whether nothing is at p.
func gone(p string) bool {
	_, err := os.Lstat(p)
	return os.IsNotExist(err)
}

func TestPurgeTakesWhatACutShortSaveLeft(t *testing.T) {
	// A save is a temporary file and a rename: stopped between the two, the
	// temporary file stays. One of them used to keep the whole directory.
	env := config.MapEnv{"HOME": t.TempDir()}
	logPath := stateOf(t, env)
	state, settings := config.StateDir(env), config.ConfigFile(env)
	left := []string{
		// The quiet book's, the lists' (one each), an edit of the settings',
		// and an install's write of the settings.
		config.QuietFile(env) + ".new",
		filepath.Join(state, ".protected-123456"),
		filepath.Join(state, ".named-7"),
		filepath.Join(state, ".kept-4096000000"),
		settings + ".tmp",
		filepath.Join(filepath.Dir(settings), ".config.toml-42"),
	}
	for _, p := range left {
		put(t, p)
	}
	var r Result
	purgeState(env, logPath, &r)
	for _, p := range left {
		if !gone(p) {
			t.Errorf("%s is still there", p)
		}
		if !slices.Contains(r.Removed, p) {
			t.Errorf("%s is not among the removed: %v", p, r.Removed)
		}
	}
	for _, d := range []string{state, config.ConfigDir(env)} {
		if !gone(d) {
			names, _ := os.ReadDir(d)
			t.Errorf("%s is still there, with %v", d, names)
		}
	}
	// Nothing stayed, so there is nothing to say.
	if len(r.Left) != 0 || len(r.Warnings) != 0 {
		t.Errorf("left %v, warnings %v", r.Left, r.Warnings)
	}
}

func TestPurgeLeavesWhatIsNotItsOwnAndNamesIt(t *testing.T) {
	env := config.MapEnv{"HOME": t.TempDir()}
	logPath := stateOf(t, env)
	state, conf := config.StateDir(env), config.ConfigDir(env)
	// Somebody's notes beside the settings; in the state directory a name
	// that starts like a temporary file's and does not end in digits, one
	// with nothing after the dash, and a directory with a temporary file's
	// whole name.
	mine := []string{
		filepath.Join(conf, "notes.txt"),
		filepath.Join(state, ".protected-notes"),
		filepath.Join(state, ".named-"),
		filepath.Join(state, ".kept-12a"),
	}
	for _, p := range mine {
		put(t, p)
	}
	dir := filepath.Join(state, ".kept-123")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var r Result
	purgeState(env, logPath, &r)
	for _, p := range append(mine, dir) {
		if gone(p) {
			t.Errorf("%s was removed, and is not the reaper's", p)
		}
		if slices.Contains(r.Removed, p) {
			t.Errorf("%s is listed as removed", p)
		}
	}
	// Everything of the reaper's is gone all the same.
	for _, p := range []string{config.ConfigFile(env), logPath, config.ProtectedFile(env), config.QuietFile(env), config.LockFile(env)} {
		if !gone(p) {
			t.Errorf("%s is still there", p)
		}
	}
	// Both directories stay, each reported with exactly what it holds.
	want := []Leftover{
		{Dir: conf, Names: []string{"notes.txt"}},
		{Dir: state, Names: []string{".kept-123", ".kept-12a", ".named-", ".protected-notes"}},
	}
	if len(r.Left) != len(want) {
		t.Fatalf("left %+v, want %+v", r.Left, want)
	}
	for i, w := range want {
		if r.Left[i].Dir != w.Dir || !slices.Equal(r.Left[i].Names, w.Names) {
			t.Errorf("left[%d] = %+v, want %+v", i, r.Left[i], w)
		}
	}
	// A directory that holds something is no failure: it is not a warning.
	if len(r.Warnings) != 0 {
		t.Errorf("warnings: %v", r.Warnings)
	}
}

func TestPurgeSaysWhatItCouldNotRemove(t *testing.T) {
	env := config.MapEnv{"HOME": t.TempDir()}
	logPath := stateOf(t, env)
	// The quiet book cannot be removed: in its place stands a directory that
	// is not empty, which no system removes, whoever asks.
	quiet := config.QuietFile(env)
	if err := os.Remove(quiet); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(quiet, "inside"))
	var r Result
	purgeState(env, logPath, &r)
	if slices.Contains(r.Removed, quiet) {
		t.Errorf("%s is listed as removed", quiet)
	}
	// One warning, which names the file once and gives the system's reason.
	if len(r.Warnings) != 1 || !strings.HasPrefix(r.Warnings[0], "could not remove "+quiet+": ") || strings.Count(r.Warnings[0], quiet) != 1 {
		t.Fatalf("warnings: %q", r.Warnings)
	}
	if reason := strings.TrimPrefix(r.Warnings[0], "could not remove "+quiet+": "); reason == "" || strings.Contains(reason, "remove") {
		t.Errorf("the reason reads %q", reason)
	}
	// The directory it sits in stays for it, and says so.
	if len(r.Left) != 1 || r.Left[0].Dir != config.StateDir(env) || !slices.Equal(r.Left[0].Names, []string{"quiet"}) {
		t.Errorf("left %+v", r.Left)
	}
}

func TestPurgeReportsOneDirectoryOnceWhereBothAreTheSame(t *testing.T) {
	// The settings and the state told to share a directory.
	home, both := t.TempDir(), t.TempDir()
	env := config.MapEnv{"HOME": home, "XDG_CONFIG_HOME": both, "XDG_STATE_HOME": both}
	if config.ConfigDir(env) != config.StateDir(env) {
		t.Fatalf("%s and %s", config.ConfigDir(env), config.StateDir(env))
	}
	logPath := stateOf(t, env)
	put(t, filepath.Join(config.StateDir(env), "notes.txt"))
	var r Result
	purgeState(env, logPath, &r)
	if len(r.Left) != 1 || !slices.Equal(r.Left[0].Names, []string{"notes.txt"}) {
		t.Errorf("left %+v", r.Left)
	}
}

func TestPurgeWithNoLogPathTouchesNoFileNamedForIt(t *testing.T) {
	// `path = ""` under [log] is accepted, and "" + ".1" names a file in the
	// directory the command runs in.
	env := config.MapEnv{"HOME": t.TempDir()}
	stateOf(t, env)
	here := t.TempDir()
	t.Chdir(here)
	put(t, filepath.Join(here, ".1"))
	var r Result
	purgeState(env, "", &r)
	if gone(filepath.Join(here, ".1")) {
		t.Error("a file named .1 in the working directory was removed")
	}
	// The empty path is no file that could not be removed, either.
	if len(r.Warnings) != 0 {
		t.Errorf("warnings: %v", r.Warnings)
	}
}

func TestEveryInstallAndUninstallReportsTheCrontabByItsName(t *testing.T) {
	// The front shows this word as "the zellij-reaper lines in your
	// crontab": what it looks for has to be what cron reports.
	env := config.MapEnv{"HOME": t.TempDir()}
	f := &fakeCrontab{}
	c := Cron{Env: env, Run: f.run, Running: func() bool { return true }}
	in, err := c.Install(InstallOptions{Exe: "/x", Config: config.Default(env)})
	if err != nil || !slices.Equal(in.Wrote, []string{CronJobs}) {
		t.Errorf("install wrote %v (%v)", in.Wrote, err)
	}
	out, err := c.Uninstall(false, "")
	if err != nil || !slices.Equal(out.Removed, []string{CronJobs}) {
		t.Errorf("uninstall removed %v (%v)", out.Removed, err)
	}
}
