package timers

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// purgeState removes the settings, the log, the name lists and the lock, and
// their directories when nothing else is in them, for an uninstall with purge.
func purgeState(env config.Env, logPath string, r *Result) {
	// The name lists' own lock files go too, or they alone would keep the
	// state directory from being removed.
	for _, p := range []string{config.ConfigFile(env), logPath, logPath + ".1", config.ProtectedFile(env),
		protect.LockPath(config.ProtectedFile(env)), config.GivenFile(env), protect.LockPath(config.GivenFile(env)),
		config.KeptFile(env), protect.LockPath(config.KeptFile(env)),
		config.LockFile(env), config.QuietFile(env)} {
		if err := os.Remove(p); err == nil {
			r.Removed = append(r.Removed, p)
		}
	}
	os.Remove(config.ConfigDir(env))
	os.Remove(config.StateDir(env))
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
