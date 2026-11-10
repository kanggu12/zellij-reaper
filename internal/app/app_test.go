package app

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// home points the settings and state directories at a new, empty home.
func home(t *testing.T) config.Env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
	t.Setenv("ZELLIJ_REAPER_CONFIG", "")
	t.Setenv("ZELLIJ_REAPER_ZELLIJ", filepath.Join(dir, "no-zellij"))
	return config.OSEnv{}
}

func TestForUninstallGoesOnWhereLoadStops(t *testing.T) {
	// Only the App is built here: its scheduler is the real machine's, and
	// calling it would reach the real timers.
	t.Run("a settings file that does not parse", func(t *testing.T) {
		env := home(t)
		path := config.ConfigFile(env)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("idle_after = = 4h\n[[[\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(""); err == nil {
			t.Fatal("the settings loaded: the case is not the one meant")
		}
		a, err := ForUninstall("")
		// The error is the file's, so the caller can say the log's place is a guess.
		var fe *config.FileError
		if !errors.As(err, &fe) || fe.Path != path {
			t.Errorf("error %v", err)
		}
		if a == nil || a.System == nil || a.Config == nil {
			t.Fatalf("app %+v", a)
		}
		// The log is looked for at the default place.
		if want := filepath.Join(config.StateDir(env), "reaper.log"); a.Config.LogPath != want {
			t.Errorf("log path %q, want %q", a.Config.LogPath, want)
		}
	})
	t.Run("a protected list that cannot be read", func(t *testing.T) {
		env := home(t)
		if err := os.MkdirAll(config.ProtectedFile(env), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(""); err == nil {
			t.Fatal("loaded without the protected list: the case is not the one meant")
		}
		// Removing the timers asks nothing of that list.
		if a, err := ForUninstall(""); err != nil || a.System == nil {
			t.Errorf("app %+v, error %v", a, err)
		}
	})
	t.Run("settings that load", func(t *testing.T) {
		env := home(t)
		path, log := config.ConfigFile(env), filepath.Join(t.TempDir(), "elsewhere.log")
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte("[log]\npath = "+strconv.Quote(log)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The log a purge removes is the one the settings name.
		if a, err := ForUninstall(""); err != nil || a.Config.LogPath != log {
			t.Errorf("log path %q, error %v", a.Config.LogPath, err)
		}
	})
	t.Run("a settings file named with --config", func(t *testing.T) {
		env := home(t)
		// The default file is there too, and is not the one named.
		def, named := config.ConfigFile(env), filepath.Join(t.TempDir(), "other.toml")
		for _, p := range []string{def, named} {
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte("idle_after = \"4h\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// The settings file of the uninstall's environment is the one a purge
		// removes: the one named, not the default one.
		a, err := ForUninstall(named)
		if err != nil || config.ConfigFile(a.Env) != named {
			t.Errorf("settings file %q, error %v", config.ConfigFile(a.Env), err)
		}
		// The rest of the environment is the process's.
		if a.Env.Home() != env.Home() || config.StateDir(a.Env) != config.StateDir(env) {
			t.Errorf("home %q, state %q", a.Env.Home(), config.StateDir(a.Env))
		}
	})
}

func TestRecoverWritesItsStepsToTheDecisionLog(t *testing.T) {
	home(t)
	// The log is told where to be, so nothing set on this machine decides it.
	log := filepath.Join(t.TempDir(), "reaper.log")
	t.Setenv("ZELLIJ_REAPER_LOG", log)
	a, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	in := a.RepairInputs()
	if in.Log == nil {
		t.Fatal("recover has nowhere to write its steps")
	}
	// A step as repair words it. The pid is above the kernel's limit, though
	// nothing here signals anything.
	in.Log("pid 5000042 (name unknown) :: recover: stopped orphan server")
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	// One line, with the time in front as every line of the log has it.
	if !regexp.MustCompile(`^\[\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\] pid 5000042 \(name unknown\) :: recover: stopped orphan server\n$`).Match(b) {
		t.Errorf("the log holds %q", b)
	}
}

func TestAListThatCannotBeReadStopsOnlyWhatItGuards(t *testing.T) {
	// A directory where the file should be: reading it fails.
	broken := func(t *testing.T, path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Without the names a pass gave, those names stay as they are: nothing to stop for.
	t.Run("the given names", func(t *testing.T) {
		broken(t, config.GivenFile(home(t)))
		if _, err := Load(""); err != nil {
			t.Errorf("load: %v", err)
		}
	})
	// Without the kept names, a pass renames nothing; the rest goes on.
	t.Run("the kept names", func(t *testing.T) {
		broken(t, config.KeptFile(home(t)))
		if _, err := Load(""); err != nil {
			t.Errorf("load: %v", err)
		}
	})
	// Without the protected list, nobody can tell which sessions it protects.
	t.Run("the protected list", func(t *testing.T) {
		broken(t, config.ProtectedFile(home(t)))
		if _, err := Load(""); err == nil {
			t.Error("loaded without the protected list")
		}
	})
}
