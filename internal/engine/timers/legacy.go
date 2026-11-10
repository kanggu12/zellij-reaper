package timers

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Legacy is an install of the shell version: ~/.local/bin/zellij-reaper.sh
// run by the same unit names, configured through Environment= lines.
type Legacy struct {
	// Vars are the Environment= values of its service file.
	Vars map[string]string
}

// legacyMarker is in the ExecStart= line of the shell version's service.
const legacyMarker = "zellij-reaper.sh"

// DetectLegacy returns the shell version's install, or nil if there is none.
func DetectLegacy(env config.Env) *Legacy {
	b, err := os.ReadFile(filepath.Join(config.UnitDir(env), ServiceName))
	if err != nil || !strings.Contains(string(b), legacyMarker) {
		return nil
	}
	return &Legacy{Vars: ParseEnvironment(string(b))}
}

// ParseEnvironment collects the assignments of every Environment= line of a
// unit file. A line may hold several, and values may be quoted.
func ParseEnvironment(unit string) map[string]string {
	vars := map[string]string{}
	for line := range strings.SplitSeq(unit, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Environment=")
		if !ok {
			continue
		}
		for _, word := range splitWords(rest) {
			if k, v, ok := strings.Cut(word, "="); ok && k != "" {
				vars[k] = v
			}
		}
	}
	return vars
}

// splitWords splits on spaces, keeping double-quoted words together.
func splitWords(s string) []string {
	var words []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// Config turns the shell version's variables into settings. Its defaults
// differ from this version's: it ran in dry-run mode unless DRY_RUN=0, waited
// three days unless told otherwise, and renamed sessions unless AUTO_RENAME=0.
func (l *Legacy) Config(env config.Env) *config.Config {
	c := config.Default(env)
	v := l.Vars
	c.DryRun = v["DRY_RUN"] != "0"
	c.Sources["dry_run"] = config.FromFile
	wait := 3 * 24 * time.Hour
	if h, err := strconv.Atoi(v["MAX_AGE_HOURS"]); err == nil && h >= 0 {
		wait = time.Duration(h) * time.Hour
	} else if d, err := strconv.Atoi(v["MAX_AGE_DAYS"]); err == nil && d >= 0 {
		wait = time.Duration(d) * 24 * time.Hour
	}
	c.IdleAfter, c.ExitedAfter = wait, wait
	c.Sources["idle_after"], c.Sources["exited_after"] = config.FromFile, config.FromFile
	if p := v["PROTECT_REGEX"]; p != "" {
		c.Protect = p
		c.Sources["protect"] = config.FromFile
	}
	c.AutoRename = v["AUTO_RENAME"] != "0"
	c.Sources["auto_rename"] = config.FromFile
	if n, err := strconv.ParseInt(v["LOG_MAX_BYTES"], 10, 64); err == nil && n >= 0 {
		c.LogMaxBytes = n
		c.Sources["log.max_bytes"] = config.FromFile
	}
	// The shell version kept its log in the cache directory; a log path set
	// on purpose stays where it is.
	if p := expandUnitPath(v["LOG"], env); p != "" && p != legacyLog(env) {
		c.LogPath = p
		c.Sources["log.path"] = config.FromFile
	}
	return c
}

// expandUnitPath resolves the forms a path takes in a unit file's
// Environment= line: %h, $HOME and ~ for the home directory. Anything that
// is still not absolute is dropped rather than used relative to some
// unknown directory.
func expandUnitPath(p string, env config.Env) string {
	if p == "" {
		return ""
	}
	home := env.Home()
	for _, prefix := range []string{"%h", "$HOME", "${HOME}", "~"} {
		if p == prefix {
			return home
		}
		if rest, ok := strings.CutPrefix(p, prefix+"/"); ok {
			p = filepath.Join(home, rest)
			break
		}
	}
	if !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// isShellVersionScript reads at most the first 4 KiB of a regular file and
// checks for a shebang and the project's name.
func isShellVersionScript(p string) bool {
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	return strings.HasPrefix(string(head), "#!") && strings.Contains(string(head), "zellij-reaper")
}

// legacyLog is where the shell version wrote its log by default.
func legacyLog(env config.Env) string {
	return filepath.Join(env.Home(), ".cache", "zellij-reaper.log")
}

// Retire moves the shell version's log next to the new one (when no new log
// exists yet) and removes its scripts and lock, returning what it did. Only
// files that are recognizably the shell version's are removed.
func (l *Legacy) Retire(env config.Env, newLog string) (moved, removed []string) {
	old := legacyLog(env)
	if p := expandUnitPath(l.Vars["LOG"], env); p != "" {
		old = p
	}
	if _, err := os.Stat(newLog); os.IsNotExist(err) && old != newLog {
		if err := os.MkdirAll(filepath.Dir(newLog), 0o700); err == nil {
			for _, suffix := range []string{".1", ""} {
				if err := os.Rename(old+suffix, newLog+suffix); err == nil {
					moved = append(moved, old+suffix+" -> "+newLog+suffix)
				}
			}
		}
	}
	bin := filepath.Join(env.Home(), ".local", "bin")
	for _, p := range []string{filepath.Join(bin, "zellij-reaper.sh"), filepath.Join(bin, "zellij-reap")} {
		// Only a regular file (not a link, not a FIFO) that starts like the
		// shell version's scripts: both name the project in their first lines.
		if !isShellVersionScript(p) {
			continue
		}
		if os.Remove(p) == nil {
			removed = append(removed, p)
		}
	}
	lock := filepath.Join(env.Home(), ".cache", "zellij-reaper.lock")
	if os.Remove(lock) == nil {
		removed = append(removed, lock)
	}
	return moved, removed
}
