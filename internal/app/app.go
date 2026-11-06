// Package app wires the pieces together once: settings, the protected list,
// zellij, the sources of facts and the log. The command line and the TUI both
// start from an App.
package app

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/engine/repair"
	"github.com/kanggu12/zellij-reaper/internal/engine/systemd"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
	"github.com/kanggu12/zellij-reaper/internal/settings/protect"
)

// App is a loaded configuration and everything built from it.
type App struct {
	Env       config.Env
	Config    *config.Config
	Protected *protect.List
	Zellij    *zellij.CLI
	Reaper    *reaper.Env
	System    systemd.System
	// InfoDir is zellij's session_info directory.
	InfoDir string
	// ZellijVersion is the version zellij reported at load ("" if none).
	ZellijVersion string
}

// Load reads the settings (from configPath, or the default location) and
// builds the App.
func Load(configPath string) (*App, error) {
	env := config.OSEnv{}
	cfg, err := config.Load(configPath, env)
	if err != nil {
		return nil, err
	}
	pl, err := protect.Load(config.ProtectedFile(env))
	if err != nil {
		return nil, err
	}
	zj := zellij.New(cfg.ZellijPath)
	uid := os.Getuid()
	zj.Settle = func(ctx context.Context) { procfs.Settle(ctx, "/proc", uid) }
	// The data directory's name depends on the zellij version (0.43 and
	// earlier name it after the version), so ask zellij first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ver, _ := zj.Version(ctx)
	info := zellij.SessionInfoDir(config.CacheDir(env), ver)
	a := &App{Env: env, Config: cfg, Protected: pl, Zellij: zj, InfoDir: info, ZellijVersion: ver}
	a.Reaper = &reaper.Env{
		Config:    cfg,
		Protected: pl,
		Given:     protect.LoadGiven(config.GivenFile(env)),
		Kept:      protect.LoadKept(config.KeptFile(env)),
		Zellij:    zj,
		Sources:   a.Sources(),
		Log:       logfile.New(cfg.LogPath, cfg.LogMaxBytes),
		LockPath:  config.LockFile(env),
		Now:       time.Now,
		Home:      homeDir(),
		Host:      hostName(),
	}
	a.System = systemd.System{Env: env, Run: systemd.Exec, User: userName()}
	return a, nil
}

// Sources are the real machine's: /proc, the kernel's sockets, zellij.
func (a *App) Sources() inspect.Sources {
	return inspect.Sources{
		ProcRoot:   "/proc",
		InfoDir:    a.InfoDir,
		SocketDirs: []string{zellij.SocketDir(os.LookupEnv, os.Getuid(), a.ZellijVersion)},
		UID:        os.Getuid(),
		Clients:    a.Zellij,
		Sockets:    procfs.UnixSockets,
		Now:        time.Now,
	}
}

// RepairInputs are the inputs for `recover`. An orphan server is judged by a
// pass's settings: the protected names and programs, and the shells.
func (a *App) RepairInputs() repair.Inputs {
	s := a.Sources()
	return repair.Inputs{Zellij: a.Zellij, ProcRoot: s.ProcRoot, InfoDir: s.InfoDir, SocketDirs: s.SocketDirs, UID: s.UID, Sockets: s.Sockets,
		Protected:       func(name string) bool { return a.Config.ProtectMatches(name) || a.Protected.Has(name) },
		ProtectCommands: a.Config.ProtectCommands, Shells: a.Config.Shells}
}

// Executable is the absolute, symlink-resolved path of this binary, which the
// service unit runs.
func Executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Abs(p)
}

// homeDir is this user's home, "" when it cannot be told: a name then simply
// says more than it needs to.
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// hostName is this machine's name, "" when it cannot be told.
func hostName() string {
	h, _ := os.Hostname()
	return h
}

// userName is the login name for loginctl.
func userName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}
