// Package config holds zellij-reaper's settings: their defaults, the TOML file
// that overrides them, and the ZELLIJ_REAPER_* environment variables that
// override the file. It also knows where the tool keeps its files.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
)

// Source says where a setting's value came from, for `config show` and doctor.
type Source string

const (
	// FromDefault is the built-in value.
	FromDefault Source = "default"
	// FromFile is a value read from the settings file.
	FromFile Source = "file"
	// FromEnv is a value read from a ZELLIJ_REAPER_* variable.
	FromEnv Source = "env"
)

// Config is the effective set of settings for one run.
type Config struct {
	// DryRun logs every decision but deletes and renames nothing.
	DryRun bool
	// IdleAfter is how long a running session with no client and every pane
	// at a prompt may sit before it is reaped. 0 turns the age check off.
	IdleAfter time.Duration
	// ExitedAfter is how long an exited (resurrectable) session is kept.
	// 0 turns the age check off.
	ExitedAfter time.Duration
	// Protect is a regular expression; matching session names are never
	// reaped or renamed. Empty means none.
	Protect string
	// ProtectCommands are programs that keep a running session safe while
	// they run, matched against process names and the session's layout.
	ProtectCommands []string
	// Shells are extra process names that count as an idle prompt, on top of
	// the common shells the tool knows.
	Shells []string
	// AutoRename names surviving sessions after what they show.
	AutoRename bool
	// AutoRenameAttached lets auto_rename rename a session with a client
	// attached too, while someone works in it.
	AutoRenameAttached bool
	// AutoRenameStamp is what those names end in: "none", "date" or "time".
	AutoRenameStamp string
	// Interval is how often the systemd timer starts a pass.
	Interval time.Duration
	// RenameInterval is how often a second timer runs a pass that only
	// renames, so a name follows its session sooner than a full pass. 0 is off.
	RenameInterval time.Duration
	// AutoRenameDropTail gives a session the name without its collision tail
	// (-2, -3, ...) once that name is free again, if a pass gave the tail.
	AutoRenameDropTail bool
	// ZellijPath is the zellij binary to run; empty means look it up in PATH.
	ZellijPath string
	// LogPath is the decision log.
	LogPath string
	// LogMaxBytes is the size past which the log moves to LogPath + ".1".
	// 0 keeps one ever-growing file.
	LogMaxBytes int64

	// File is the settings file this config was read from ("" if none).
	File string
	// Sources records where each setting (by its TOML key) came from.
	Sources map[string]Source
	// protectRe is Protect compiled once.
	protectRe *regexp.Regexp
}

// Keys lists every setting in the order `config show` prints them.
var Keys = []string{
	"dry_run", "idle_after", "exited_after", "protect", "protect_commands", "shells",
	"auto_rename", "auto_rename_attached", "auto_rename_stamp", "auto_rename_drop_tail", "interval",
	"rename_interval", "zellij",
	"log.path", "log.max_bytes",
}

// DefaultProtectCommands are coding agents and other long-lived programs that
// people leave running in a pane on purpose. A session with one of them alive
// is never reaped, even if its pane process looks like a prompt.
var DefaultProtectCommands = []string{"claude", "codex", "gemini", "aider", "opencode"}

// Default returns the built-in settings, with paths under the XDG directories
// of the given environment.
func Default(env Env) *Config {
	c := &Config{
		DryRun:             false,
		IdleAfter:          12 * time.Hour,
		ExitedAfter:        3 * 24 * time.Hour,
		Protect:            "",
		ProtectCommands:    append([]string(nil), DefaultProtectCommands...),
		Shells:             nil,
		AutoRename:         false,
		AutoRenameAttached: true,
		AutoRenameStamp:    "none",
		Interval:           time.Hour,
		RenameInterval:     5 * time.Minute,
		AutoRenameDropTail: false,
		ZellijPath:         "",
		LogPath:            filepath.Join(StateDir(env), "reaper.log"),
		LogMaxBytes:        1 << 20,
		Sources:            map[string]Source{},
	}
	// Every key starts out as a default; Load overwrites the ones it sets.
	for _, k := range Keys {
		c.Sources[k] = FromDefault
	}
	return c
}

// fileShape mirrors the TOML file. Pointers tell "absent" apart from a zero
// value, so an explicit `dry_run = false` in the file is still recorded as
// coming from the file.
type fileShape struct {
	DryRun             *bool     `toml:"dry_run"`
	IdleAfter          *string   `toml:"idle_after"`
	ExitedAfter        *string   `toml:"exited_after"`
	Protect            *string   `toml:"protect"`
	ProtectCommands    *[]string `toml:"protect_commands"`
	Shells             *[]string `toml:"shells"`
	AutoRename         *bool     `toml:"auto_rename"`
	AutoRenameAttached *bool     `toml:"auto_rename_attached"`
	AutoRenameStamp    *string   `toml:"auto_rename_stamp"`
	Interval           *string   `toml:"interval"`
	RenameInterval     *string   `toml:"rename_interval"`
	AutoRenameDropTail *bool     `toml:"auto_rename_drop_tail"`
	Zellij             *string   `toml:"zellij"`
	Log                struct {
		Path     *string `toml:"path"`
		MaxBytes *int64  `toml:"max_bytes"`
	} `toml:"log"`
}

// Load builds the effective config: defaults, then the file at path (if it
// exists), then the environment. A missing file is not an error; a file that
// exists but cannot be parsed is, because silently running on defaults would
// ignore a protection the user wrote down.
func Load(path string, env Env) (*Config, error) {
	c := Default(env)
	// The file path itself can come from the environment.
	if path == "" {
		path = ConfigFile(env)
	}
	if err := c.loadFile(path, env); err != nil {
		return nil, &FileError{Path: path, Err: err}
	}
	if err := c.loadEnv(env); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// FileError is a settings file that cannot be read or holds something that
// is not a setting. Its message is the underlying one, which names the file.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// loadFile applies the settings file at path, if there is one.
func (c *Config) loadFile(path string, env Env) error {
	raw, err := os.ReadFile(path)
	// No file: keep the defaults, nothing to report.
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	c.File = path
	var f fileShape
	md, err := toml.Decode(string(raw), &f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// An unknown key stops loading like a value that cannot be read. It is most
	// likely a typo ("protct"), and a pass that runs without a setting someone
	// believes is active can delete a session they meant to keep.
	if err := unknownKeys(path, md.Undecoded()); err != nil {
		return err
	}
	// set records a value and its origin in one step.
	set := func(key string) { c.Sources[key] = FromFile }
	if f.DryRun != nil {
		c.DryRun = *f.DryRun
		set("dry_run")
	}
	if f.IdleAfter != nil {
		d, err := duration.Parse(*f.IdleAfter)
		if err != nil {
			return fmt.Errorf("%s: idle_after: %w", path, err)
		}
		c.IdleAfter = d
		set("idle_after")
	}
	if f.ExitedAfter != nil {
		d, err := duration.Parse(*f.ExitedAfter)
		if err != nil {
			return fmt.Errorf("%s: exited_after: %w", path, err)
		}
		c.ExitedAfter = d
		set("exited_after")
	}
	if f.Protect != nil {
		c.Protect = *f.Protect
		set("protect")
	}
	if f.ProtectCommands != nil {
		c.ProtectCommands = cleanList(*f.ProtectCommands)
		set("protect_commands")
	}
	if f.Shells != nil {
		c.Shells = cleanList(*f.Shells)
		set("shells")
	}
	if f.AutoRename != nil {
		c.AutoRename = *f.AutoRename
		set("auto_rename")
	}
	if f.AutoRenameAttached != nil {
		c.AutoRenameAttached = *f.AutoRenameAttached
		set("auto_rename_attached")
	}
	if f.AutoRenameStamp != nil {
		if err := checkStamp(*f.AutoRenameStamp); err != nil {
			return fmt.Errorf("%s: auto_rename_stamp: %w", path, err)
		}
		c.AutoRenameStamp = *f.AutoRenameStamp
		set("auto_rename_stamp")
	}
	if f.Interval != nil {
		d, err := duration.Parse(*f.Interval)
		if err != nil {
			return fmt.Errorf("%s: interval: %w", path, err)
		}
		c.Interval = d
		set("interval")
	}
	if f.RenameInterval != nil {
		d, err := duration.Parse(*f.RenameInterval)
		if err != nil {
			return fmt.Errorf("%s: rename_interval: %w", path, err)
		}
		c.RenameInterval = d
		set("rename_interval")
	}
	if f.AutoRenameDropTail != nil {
		c.AutoRenameDropTail = *f.AutoRenameDropTail
		set("auto_rename_drop_tail")
	}
	if f.Zellij != nil {
		c.ZellijPath = expandHome(*f.Zellij, env)
		set("zellij")
	}
	if f.Log.Path != nil {
		c.LogPath = expandHome(*f.Log.Path, env)
		set("log.path")
	}
	if f.Log.MaxBytes != nil {
		c.LogMaxBytes = *f.Log.MaxBytes
		set("log.max_bytes")
	}
	return nil
}

// EnvNames maps settings to the variables that override them.
var EnvNames = map[string]string{
	"dry_run":               "ZELLIJ_REAPER_DRY_RUN",
	"idle_after":            "ZELLIJ_REAPER_IDLE_AFTER",
	"exited_after":          "ZELLIJ_REAPER_EXITED_AFTER",
	"protect":               "ZELLIJ_REAPER_PROTECT",
	"auto_rename":           "ZELLIJ_REAPER_AUTO_RENAME",
	"auto_rename_drop_tail": "ZELLIJ_REAPER_AUTO_RENAME_DROP_TAIL",
	"auto_rename_attached":  "ZELLIJ_REAPER_AUTO_RENAME_ATTACHED",
	"auto_rename_stamp":     "ZELLIJ_REAPER_AUTO_RENAME_STAMP",
	"zellij":                "ZELLIJ_REAPER_ZELLIJ",
	"log.path":              "ZELLIJ_REAPER_LOG",
	"log.max_bytes":         "ZELLIJ_REAPER_LOG_MAX_BYTES",
}

// loadEnv applies ZELLIJ_REAPER_* overrides. A malformed value is an error
// rather than a fallback, for the same reason a malformed file is.
func (c *Config) loadEnv(env Env) error {
	get := func(key string) (string, bool) {
		v, ok := env.Lookup(EnvNames[key])
		return strings.TrimSpace(v), ok && strings.TrimSpace(v) != ""
	}
	if v, ok := get("dry_run"); ok {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvNames["dry_run"], err)
		}
		c.DryRun = b
		c.Sources["dry_run"] = FromEnv
	}
	for _, key := range []string{"idle_after", "exited_after"} {
		if v, ok := get(key); ok {
			d, err := duration.Parse(v)
			if err != nil {
				return fmt.Errorf("%s: %w", EnvNames[key], err)
			}
			if key == "idle_after" {
				c.IdleAfter = d
			} else {
				c.ExitedAfter = d
			}
			c.Sources[key] = FromEnv
		}
	}
	if v, ok := get("protect"); ok {
		c.Protect = v
		c.Sources["protect"] = FromEnv
	}
	if v, ok := get("auto_rename"); ok {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvNames["auto_rename"], err)
		}
		c.AutoRename = b
		c.Sources["auto_rename"] = FromEnv
	}
	if v, ok := get("auto_rename_attached"); ok {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvNames["auto_rename_attached"], err)
		}
		c.AutoRenameAttached = b
		c.Sources["auto_rename_attached"] = FromEnv
	}
	if v, ok := get("auto_rename_drop_tail"); ok {
		b, err := parseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvNames["auto_rename_drop_tail"], err)
		}
		c.AutoRenameDropTail = b
		c.Sources["auto_rename_drop_tail"] = FromEnv
	}
	if v, ok := get("auto_rename_stamp"); ok {
		if err := checkStamp(v); err != nil {
			return fmt.Errorf("%s: %w", EnvNames["auto_rename_stamp"], err)
		}
		c.AutoRenameStamp = v
		c.Sources["auto_rename_stamp"] = FromEnv
	}
	if v, ok := get("zellij"); ok {
		c.ZellijPath = expandHome(v, env)
		c.Sources["zellij"] = FromEnv
	}
	if v, ok := get("log.path"); ok {
		c.LogPath = expandHome(v, env)
		c.Sources["log.path"] = FromEnv
	}
	if v, ok := get("log.max_bytes"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %q is not a whole number of bytes", EnvNames["log.max_bytes"], v)
		}
		c.LogMaxBytes = n
		c.Sources["log.max_bytes"] = FromEnv
	}
	return nil
}

// Validate checks the values that the types alone cannot, and compiles the
// protect pattern.
func (c *Config) Validate() error {
	if c.IdleAfter < 0 || c.ExitedAfter < 0 {
		return fmt.Errorf("idle_after and exited_after cannot be negative")
	}
	// The timer cannot usefully fire more than once a minute: a pass alone
	// can take a few seconds on a busy machine.
	if c.Interval < time.Minute {
		return fmt.Errorf("interval %s is shorter than one minute", duration.Setting(c.Interval))
	}
	// 0 turns the names timer off; otherwise the same floor as interval.
	if c.RenameInterval != 0 && c.RenameInterval < time.Minute {
		return fmt.Errorf("rename_interval %s is shorter than one minute (0 turns it off)", duration.Setting(c.RenameInterval))
	}
	if c.LogMaxBytes < 0 {
		return fmt.Errorf("log.max_bytes cannot be negative")
	}
	if c.Protect != "" {
		re, err := regexp.Compile(c.Protect)
		if err != nil {
			return fmt.Errorf("protect: %w", err)
		}
		c.protectRe = re
	} else {
		c.protectRe = nil
	}
	return nil
}

// ProtectMatches reports whether name is protected by the protect pattern.
func (c *Config) ProtectMatches(name string) bool {
	return c.protectRe != nil && c.protectRe.MatchString(name)
}

// Value prints a setting the way the TOML file would spell it.
func (c *Config) Value(key string) string {
	switch key {
	case "dry_run":
		return strconv.FormatBool(c.DryRun)
	case "idle_after":
		return strconv.Quote(duration.Setting(c.IdleAfter))
	case "exited_after":
		return strconv.Quote(duration.Setting(c.ExitedAfter))
	case "protect":
		return strconv.Quote(c.Protect)
	case "protect_commands":
		return quoteList(c.ProtectCommands)
	case "shells":
		return quoteList(c.Shells)
	case "auto_rename":
		return strconv.FormatBool(c.AutoRename)
	case "auto_rename_attached":
		return strconv.FormatBool(c.AutoRenameAttached)
	case "auto_rename_stamp":
		return strconv.Quote(c.AutoRenameStamp)
	case "interval":
		return strconv.Quote(duration.Setting(c.Interval))
	case "rename_interval":
		return strconv.Quote(duration.Setting(c.RenameInterval))
	case "auto_rename_drop_tail":
		return strconv.FormatBool(c.AutoRenameDropTail)
	case "zellij":
		return strconv.Quote(c.ZellijPath)
	case "log.path":
		return strconv.Quote(c.LogPath)
	case "log.max_bytes":
		return strconv.FormatInt(c.LogMaxBytes, 10)
	}
	return ""
}

// unknownKeys describes the keys a settings file has that nothing reads. A key
// that belongs at the top but sits under [log], because a line was added below
// that table, gets told where it goes.
func unknownKeys(path string, keys []toml.Key) error {
	if len(keys) == 0 {
		return nil
	}
	k := keys[0]
	if len(k) == 2 && k[0] == "log" && slices.Contains(Keys, k[1]) {
		return fmt.Errorf("%s: %s is under [log], where it does nothing: move it above the [log] line", path, k[1])
	}
	return fmt.Errorf("%s: unknown setting %q", path, k.String())
}

// checkStamp refuses a stamp setting that is none of the three, the way a
// malformed duration is refused: silently renaming with another stamp would
// rename every session once more when the typo is fixed.
func checkStamp(v string) error {
	switch v {
	case "none", "date", "time":
		return nil
	}
	return fmt.Errorf("%q is not none, date or time", v)
}

// quoteList prints a list as a TOML array of strings.
func quoteList(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = strconv.Quote(x)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// cleanList trims entries and drops empty and repeated ones, keeping order.
func cleanList(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// parseBool accepts the spellings people use in environment variables.
func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%q is not true or false", v)
}

// expandHome turns a leading "~/" into the home directory, so paths in the
// file can be written the way people write them in a shell.
func expandHome(p string, env Env) string {
	if p == "~" {
		return env.Home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(env.Home(), p[2:])
	}
	return p
}
