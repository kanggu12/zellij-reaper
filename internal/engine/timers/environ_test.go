package timers

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// carried is what a shell with its own places for everything would hand
// install: a space, a quote and a % in the values, which every timer
// definition has to keep as they are.
var carried = map[string]string{
	"XDG_STATE_HOME":    "/home/u/my state",
	"XDG_CACHE_HOME":    "/home/u/.cache's",
	"ZELLIJ_SOCKET_DIR": "/run/zj 100%",
}

func TestOnlyAbsolutePlacesAreCarried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix paths; the Task Scheduler takes the user's own environment")
	}
	defer func(g string) { zellij.GOOS = g }(zellij.GOOS)
	zellij.GOOS = "linux"
	env := config.MapEnv{
		"HOME":              "/home/u",
		"XDG_STATE_HOME":    "/home/u/state",
		"XDG_CONFIG_HOME":   "relative/config",
		"ZELLIJ_SOCKET_DIR": "",
		"TMPDIR":            "/tmp/u",
		"XDG_DATA_HOME":     "/home/u/data",
	}
	want := map[string]string{"XDG_STATE_HOME": "/home/u/state", "TMPDIR": "/tmp/u"}
	if got := CarriedEnv(env); !reflect.DeepEqual(got, want) {
		t.Errorf("CarriedEnv = %v, want %v", got, want)
	}
	// With a runtime directory zellij's sockets go there: the shell's TMPDIR
	// decides nothing, and stays out of the timer.
	env["XDG_RUNTIME_DIR"] = "/run/user/1000"
	delete(want, "TMPDIR")
	if got := CarriedEnv(env); !reflect.DeepEqual(got, want) {
		t.Errorf("with a runtime directory: CarriedEnv = %v, want %v", got, want)
	}
	// macOS has no runtime directory for zellij: there TMPDIR still decides.
	zellij.GOOS = "darwin"
	want["TMPDIR"] = "/tmp/u"
	if got := CarriedEnv(env); !reflect.DeepEqual(got, want) {
		t.Errorf("macOS: CarriedEnv = %v, want %v", got, want)
	}
	// A socket directory of zellij's own decides it everywhere.
	env["ZELLIJ_SOCKET_DIR"] = "/run/zj"
	want = map[string]string{"XDG_STATE_HOME": "/home/u/state", "ZELLIJ_SOCKET_DIR": "/run/zj"}
	if got := CarriedEnv(env); !reflect.DeepEqual(got, want) {
		t.Errorf("with a socket directory: CarriedEnv = %v, want %v", got, want)
	}
}

func TestAServiceRunsWithThePlacesInstallSaw(t *testing.T) {
	svc := ServiceUnit("/usr/bin/zellij-reaper", "", "", carried)
	for _, line := range []string{
		`Environment="XDG_STATE_HOME=/home/u/my state"`,
		`Environment="XDG_CACHE_HOME=/home/u/.cache's"`,
		// % is a specifier in a unit file.
		`Environment="ZELLIJ_SOCKET_DIR=/run/zj 100%%"`,
		// The ones the shell does not set are unset, whatever systemd has.
		"UnsetEnvironment=XDG_CONFIG_HOME",
		"UnsetEnvironment=TMPDIR",
	} {
		if !strings.Contains(svc, line+"\n") {
			t.Errorf("no %s in\n%s", line, svc)
		}
	}
	// Read back as install wrote them.
	dir := t.TempDir()
	path := filepath.Join(dir, ServiceName)
	if err := os.WriteFile(path, []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, env := unitFacts(filepath.Join(dir, "none.timer"), path); !reflect.DeepEqual(env, carried) {
		t.Errorf("read back %v, want %v", env, carried)
	}
	// A line break would end the line and start a directive of its own.
	s := Systemd{Env: config.MapEnv{"HOME": dir}}
	if _, err := s.Install(InstallOptions{Exe: "/usr/bin/zellij-reaper", Config: config.Default(s.Env), Env: map[string]string{"TMPDIR": "/tmp/a\nExecStartPre=/bin/false"}}); err == nil || !strings.Contains(err.Error(), "line break") {
		t.Errorf("install with a line break: %v", err)
	}
}

func TestACronJobRunsWithThePlacesInstallSaw(t *testing.T) {
	line := cronLine("5 * * * *", InstallOptions{Exe: "/x/zellij-reaper", Env: carried}, "run", "--quiet")
	// cron cuts a command at a bare %, so it is escaped on the line.
	if !strings.Contains(line, `ZELLIJ_SOCKET_DIR='/run/zj 100\%'`) {
		t.Errorf("line %s", line)
	}
	f := strings.Fields(line)
	if got := cronCarried(strings.Join(f[5:], " ")); !reflect.DeepEqual(got, carried) {
		t.Errorf("read back %v, want %v", got, carried)
	}
	// What sh would make of the words.
	if got := cronWords(`env A='x y' B='it'\''s' 'C'\%d >/dev/null`); !reflect.DeepEqual(got, []string{"env", "A=x y", "B=it's", "C%d", ">/dev/null"}) {
		t.Errorf("cronWords = %q", got)
	}
}

func TestALaunchAgentRunsWithThePlacesInstallSaw(t *testing.T) {
	plist := Plist(LaunchdLabel, "/usr/local/bin/zellij-reaper", "", "", []string{"run", "--quiet"}, time.Hour, carried)
	if !strings.Contains(plist, "<key>XDG_CACHE_HOME</key>\n\t\t<string>/home/u/.cache&#39;s</string>") {
		t.Errorf("plist:\n%s", plist)
	}
	if got := plistCarried([]byte(plist)); !reflect.DeepEqual(got, carried) {
		t.Errorf("read back %v, want %v", got, carried)
	}
}
