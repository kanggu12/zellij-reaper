package timers

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// Carried are the variables a timer runs with as install found them. They
// say where the reaper keeps its settings and its state (the protected list,
// the lock, the log), and where zellij keeps its sessions. A shell that sets
// one the timer does not have would have the dashboard protect a session in
// one list while the timer reads another, or zellij list its sessions from a
// place the timer's zellij does not look.
var Carried = []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "ZELLIJ_SOCKET_DIR", "TMPDIR"}

// CarriedEnv is the carried variables env sets to an absolute path. A
// relative value means nothing to a timer started in another directory, and
// the XDG rule ignores it anyway.
func CarriedEnv(env config.Env) map[string]string {
	out := map[string]string{}
	for _, k := range Carried {
		if v, ok := env.Lookup(k); ok && filepath.IsAbs(v) {
			out[k] = v
		}
	}
	// TMPDIR is carried for one thing: zellij puts its sockets under it when
	// nothing else says where. Where something else does, the TMPDIR of the
	// shell at hand (a build shell's, a sandbox's) decides nothing, and may
	// be gone long before the timer is.
	if !zellij.SocketsUnderTemp(env.Lookup) {
		delete(out, "TMPDIR")
	}
	return out
}

// checkCarried refuses a value no timer definition can hold safely: a line
// break would end the line it sits on.
func checkCarried(env map[string]string) error {
	for k, v := range env {
		if err := quotable(k, v); err != nil {
			return err
		}
	}
	return nil
}

// carriedUnitLines are the service's lines for the carried variables: each
// one install saw is set, and the others are unset, so a variable the user's
// systemd has from elsewhere does not point the timer at another place than
// the shell that installed it. env nil writes none (tests of other lines).
func carriedUnitLines(env map[string]string) string {
	if env == nil {
		return ""
	}
	var b strings.Builder
	for _, k := range Carried {
		if v, ok := env[k]; ok {
			fmt.Fprintf(&b, "Environment=\"%s=%s\"\n", k, escape(v))
		} else {
			fmt.Fprintf(&b, "UnsetEnvironment=%s\n", k)
		}
	}
	return b.String()
}

// unitEnvRe reads a carried Environment= line back.
var unitEnvRe = regexp.MustCompile(`^Environment="([A-Z_]+)=(.*)"$`)

// unitCarried reads the carried variables back from a service unit's lines.
func unitCarried(lines []string) map[string]string {
	env := map[string]string{}
	for _, line := range lines {
		m := unitEnvRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || !isCarried(m[1]) {
			continue
		}
		env[m[1]] = strings.ReplaceAll(m[2], "%%", "%")
	}
	return env
}

// isCarried reports whether k is one of Carried.
func isCarried(k string) bool { return slices.Contains(Carried, k) }
