// Package systemd installs zellij-reaper as a systemd user timer, removes it,
// reports its state, and moves an install of the earlier shell version over.
package systemd

import (
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
