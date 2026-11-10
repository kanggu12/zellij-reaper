package timers

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// Scheduler is the service manager that runs the passes: systemd's user
// timers on Linux, launchd on macOS, the Task Scheduler on Windows. Each runs
// `zellij-reaper run --quiet` every interval and `run --names-only --quiet`
// every rename_interval.
type Scheduler interface {
	// Kind names it for messages: "systemd", "launchd", "Task Scheduler".
	Kind() string
	// Available reports whether it answers.
	Available() bool
	// Status reports on the hourly timer, NamesStatus on the names timer.
	Status() TimerStatus
	NamesStatus() TimerStatus
	// Install writes both timers and starts them.
	Install(o InstallOptions) (Result, error)
	// Uninstall stops and removes both; with purge, the settings and the
	// state too.
	Uninstall(purge bool, logPath string) (Result, error)
	// Persists reports whether the timers keep running after logout, and
	// whether that can be told (systemd's lingering).
	Persists() (on, known bool)
	// FirstPass is when the first pass runs after an install at now with
	// passes every interval, as this scheduler starts them: what the setup
	// screen promises nothing is deleted before.
	FirstPass(now time.Time, interval time.Duration) time.Time
}

// For returns the scheduler of the system this runs on.
func For(env config.Env, user string) Scheduler {
	switch runtime.GOOS {
	case "darwin":
		return Launchd{Env: env, Run: Exec}
	case "windows":
		return Tasks{Env: env, Run: Exec}
	}
	return newAuto(env, user)
}

// purgeState removes the settings, the log, the name lists and the lock, with
// what a save of one of them cut short left beside it, and their directories
// when nothing else is in them, for an uninstall with purge. A directory that
// stays is reported with what it holds.
func purgeState(env config.Env, logPath string, r *Result) {
	// In the order the lines read best: the settings, the log, the lists.
	settings := config.ConfigFile(env)
	// An edit of the settings writes settings+".tmp" first, an install a
	// temporary file of its own.
	files := append([]string{settings, settings + ".tmp"}, tempsOf(settings)...)
	// No log path names no file, and logPath+".1" alone would name a file in
	// the directory this happens to run in.
	if logPath != "" {
		files = append(files, logPath, logPath+".1")
	}
	for _, list := range []string{config.ProtectedFile(env), config.GivenFile(env), config.KeptFile(env)} {
		// A list's own lock file goes too, or it alone would keep the state
		// directory from being removed.
		files = append(files, list, protect.LockPath(list))
		files = append(files, tempsOf(list)...)
	}
	// The quiet book is written as quiet.new first.
	files = append(files, config.LockFile(env), config.QuietFile(env), config.QuietFile(env)+".new")
	for _, p := range files {
		err := os.Remove(p)
		switch {
		case err == nil:
			r.Removed = append(r.Removed, p)
		case !errors.Is(err, os.ErrNotExist):
			// A file that is there and stays is said, not passed over.
			r.Warnings = append(r.Warnings, "could not remove "+p+": "+why(err))
		}
	}
	// The two directories, which are one where both are told to be.
	dirs := []string{config.ConfigDir(env)}
	if state := config.StateDir(env); state != dirs[0] {
		dirs = append(dirs, state)
	}
	for _, d := range dirs {
		err := os.Remove(d)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			continue
		}
		// It holds something that is not the reaper's: named, so nobody has
		// to go and look why the directory is still there.
		entries, readErr := os.ReadDir(d)
		if readErr != nil || len(entries) == 0 {
			r.Warnings = append(r.Warnings, "could not remove "+d+": "+why(err))
			continue
		}
		left := Leftover{Dir: d}
		for _, e := range entries {
			left.Names = append(left.Names, e.Name())
		}
		r.Left = append(r.Left, left)
	}
}

// tempsOf lists the temporary files a save of path cut short left beside it:
// "."+name+"-"+digits, as os.CreateTemp names them for writeFile and the lists.
func tempsOf(path string) []string {
	dir, prefix := filepath.Dir(path), "."+filepath.Base(path)+"-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		rest, found := strings.CutPrefix(e.Name(), prefix)
		// Digits only after the prefix, and a regular file: a name anyone
		// else chose is not taken for one of these.
		if !found || rest == "" || strings.Trim(rest, "0123456789") != "" || !e.Type().IsRegular() {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// why is an error's cause without the path a file operation puts in front of
// it, for a message that has named the file already.
func why(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// ensureSettings writes a settings file with every key commented out at its
// default, when there is none: at configFile, or at the default place.
func ensureSettings(env config.Env, configFile string, r *Result) error {
	path := config.ConfigFile(env)
	if configFile != "" {
		path = configFile
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeFile(path, config.Render(config.Default(env), "zellij-reaper settings. Run `zellij-reaper config show` to see the values in effect.")); err != nil {
		return err
	}
	r.Created = path
	return nil
}
