// Package version reports which build this is. Release builds set the values
// with -ldflags; a build from source reads the module's own build info.
package version

import (
	"regexp"
	"runtime/debug"
)

// Set at release build time: -X github.com/kanggu12/zellij-reaper/internal/version.Version=v0.1.0
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// pseudo matches a Go pseudo-version, which `go build` stamps on a commit
// after a tag: v0.9.1-0.20261108003831-1839aebf23d4. It names no release.
var pseudo = regexp.MustCompile(`-(0\.)?\d{14}-[0-9a-f]{12}(\+dirty)?$`)

// parts returns the version, the short commit and the date, from the release
// values or, failing those, from the module's build info.
func parts() (v, c, d string) {
	v, c, d = Version, Commit, Date
	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, s := range info.Settings {
			switch {
			case s.Key == "vcs.revision" && c == "":
				c = s.Value
			case s.Key == "vcs.time" && d == "":
				d = s.Value
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	if len(c) > 7 {
		c = c[:7]
	}
	if len(d) > 10 {
		d = d[:10]
	}
	return v, c, d
}

// String returns "v0.1.0 (abc1234, 2026-11-04)" or the best a source build knows.
func String() string {
	v, c, d := parts()
	switch {
	case c != "" && d != "":
		return v + " (" + c + ", " + d + ")"
	case c != "":
		return v + " (" + c + ")"
	}
	return v
}

// Short is the version as a screen shows it: "v0.9.0" for a release, and
// "dev 1839aeb" for anything else, rather than a pseudo-version's 40 letters.
func Short() string {
	v, c, _ := parts()
	return short(v, c)
}

// short is Short on given values, for the tests.
func short(v, c string) string {
	if v != "dev" && !pseudo.MatchString(v) {
		return v
	}
	if c == "" {
		return "dev"
	}
	return "dev " + c
}
