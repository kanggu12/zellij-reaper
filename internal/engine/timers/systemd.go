// Package systemd installs zellij-reaper as a systemd user timer, removes it,
// reports its state, and moves an install of the earlier shell version over.
package timers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Unit names. They are the same as the shell version's, so installing over
// it replaces its units in place.
const (
	ServiceName = "zellij-reaper.service"
	TimerName   = "zellij-reaper.timer"
	// The names timer runs passes that only rename, every rename_interval.
	NamesServiceName = "zellij-reaper-names.service"
	NamesTimerName   = "zellij-reaper-names.timer"
)

// Runner runs a command and returns its combined output. Tests replace it.
type Runner func(name string, args ...string) (string, error)

// commandLimit is how long a scheduler's command may take: systemctl waits on
// the user's bus, PowerShell on the Task Scheduler, and one that never answers
// held install, uninstall and doctor until someone pressed ctrl+c.
var commandLimit = time.Minute

// Exec runs the real command, within commandLimit.
func Exec(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	// Killed at the limit, it lets go of its output a second later, even
	// with something it started still holding it.
	c.WaitDelay = time.Second
	out, err := c.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return strings.TrimSpace(string(out)), fmt.Errorf("%s gave no answer within %s", name, commandLimit)
	}
	return strings.TrimSpace(string(out)), err
}

// System is the part of the machine the installer touches.
type Systemd struct {
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

// FirstPass comes FirstPassDelay after the install starts the timer.
func (s Systemd) FirstPass(now time.Time, _ time.Duration) time.Time { return now.Add(FirstPassDelay) }

// systemctl runs `systemctl --user ...`.
func (s Systemd) systemctl(args ...string) (string, error) {
	return s.Run("systemctl", append([]string{"--user"}, args...)...)
}

// Available reports whether a systemd user instance answers.
func (s Systemd) Available() bool {
	_, err := s.systemctl("show-environment")
	return err == nil
}

// servicePath and timerPath are the unit files.
func (s Systemd) servicePath() string { return filepath.Join(config.UnitDir(s.Env), ServiceName) }
func (s Systemd) timerPath() string   { return filepath.Join(config.UnitDir(s.Env), TimerName) }
func (s Systemd) namesServicePath() string {
	return filepath.Join(config.UnitDir(s.Env), NamesServiceName)
}
func (s Systemd) namesTimerPath() string { return filepath.Join(config.UnitDir(s.Env), NamesTimerName) }

// PathDirs are put in the service's PATH so it finds zellij wherever the usual
// installers put it (cargo, nix, Homebrew on Linux, distro packages). systemd
// expands %h to the home directory.
var PathDirs = []string{
	"%h/.local/bin", "%h/.cargo/bin", "%h/.nix-profile/bin", "/nix/var/nix/profiles/default/bin",
	"/home/linuxbrew/.linuxbrew/bin", "/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin",
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
