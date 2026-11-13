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
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
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
	System    timers.Scheduler
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
	uid := procfs.CurrentUID()
	zj.Settle = func(ctx context.Context) { procfs.Settle(ctx, procfs.Native, uid) }
	// The data directory's name depends on the zellij version (0.43 and
	// earlier name it after the version), so ask zellij first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ver, _ := zj.Version(ctx)
	// The folder zellij's servers keep their sessions' files in now: their
	// sockets name it, where another zellij may have left one beside it.
	var sockets []string
	if t, err := procfs.ReadZellij(procfs.Native); err == nil {
		for _, s := range t.Servers(uid) {
			sockets = append(sockets, s.Socket)
		}
	}
	info := zellij.SessionInfoDir(config.ZellijCacheDir(env), ver, sockets...)
	a := &App{Env: env, Config: cfg, Protected: pl, Zellij: zj, InfoDir: info, ZellijVersion: ver}
	a.Reaper = &reaper.Env{
		Config:       cfg,
		Protected:    pl,
		Given:        protect.LoadGiven(config.GivenFile(env)),
		Kept:         protect.LoadKept(config.KeptFile(env)),
		Zellij:       zj,
		Sources:      a.Sources(),
		Log:          logfile.New(cfg.LogPath, cfg.LogMaxBytes),
		LockPath:     config.LockFile(env),
		QuietPath:    config.QuietFile(env),
		ProjectsPath: config.ProjectsFile(env),
		FormerPath:   config.FormerFile(env),
		// The version the zellij on the PATH reported: its servers are the
		// ones a pass asks.
		RefusesFormerNames: zellij.RefusesFormerNames(ver),
		Now:                time.Now,
		Home:               homeDir(),
		Host:               hostName(),
	}
	a.System = timers.For(env, userName())
	return a, nil
}

// ForUninstall is an App with only what removing the timers takes: the
// settings, for where the log is, and the scheduler. What stops Load (a
// settings file or a protected list that cannot be read) must not keep the
// timers from being removed, so the settings are the defaults where they do
// not load, and the error says why they did not.
func ForUninstall(configPath string) (*App, error) {
	var env config.Env = config.OSEnv{}
	// The settings file --config names is the one a purge removes. A purge
	// finds it where ZELLIJ_REAPER_CONFIG would name it, and without this it
	// removed the default file and kept the one named.
	if configPath != "" {
		env = config.Override{Env: env, Vars: map[string]string{"ZELLIJ_REAPER_CONFIG": configPath}}
	}
	cfg, err := config.Load(configPath, env)
	if err != nil {
		cfg = config.Default(env)
	}
	return &App{Env: env, Config: cfg, System: timers.For(env, userName())}, err
}

// Sources are the real machine's: /proc, the kernel's sockets, zellij.
func (a *App) Sources() inspect.Sources {
	return inspect.Sources{
		ProcRoot:   procfs.Native,
		InfoDir:    a.InfoDir,
		SocketDirs: []string{zellij.SocketDir(os.LookupEnv, procfs.CurrentUID(), a.ZellijVersion)},
		UID:        procfs.CurrentUID(),
		Clients:    a.Zellij,
		Sockets:    procfs.UnixSockets,
		Now:        time.Now,
	}
}

// RepairInputs are the inputs for `recover`. An orphan server is judged by a
// pass's settings: the protected names and programs, and the shells. What a
// repair does goes to the decision log, beside what the passes did: recover
// deletes sessions and stops servers, and the log is where that is looked up.
func (a *App) RepairInputs() repair.Inputs {
	s := a.Sources()
	return repair.Inputs{Zellij: a.Zellij, ProcRoot: s.ProcRoot, InfoDir: s.InfoDir, SocketDirs: s.SocketDirs, UID: s.UID, Sockets: s.Sockets,
		Protected:       func(name string) bool { return a.Config.ProtectMatches(name) || a.Protected.Has(name) },
		ProtectCommands: a.Config.ProtectCommands, Shells: a.Config.Shells,
		// A line that cannot be written does not stop a repair half way: the
		// terminal shows the step all the same.
		Log: func(line string) { a.Reaper.Log.Printf("%s", line) }}
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
