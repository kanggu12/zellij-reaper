package internal

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The changelog is each release's notes (the release workflow takes a tag's
// section as its body), so its shape is checked like code: Keep a Changelog
// 1.1.0, newest first, one link per version.
var (
	versionHead  = regexp.MustCompile(`^## \[(\d+)\.(\d+)\.(\d+)\] - (\d{4}-\d{2}-\d{2})$`)
	subHead      = regexp.MustCompile(`^### (.+)$`)
	linkLine     = regexp.MustCompile(`^\[([^\]]+)\]: (\S+)$`)
	knownSubHead = map[string]bool{"Added": true, "Changed": true, "Deprecated": true, "Removed": true, "Fixed": true, "Security": true}
)

func TestTheChangelogIsInShape(t *testing.T) {
	b, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var versions []string
	var prev [3]int
	links := map[string]string{}
	unreleased := false
	for i, l := range lines {
		switch {
		case l == "## [Unreleased]":
			if len(versions) > 0 || unreleased {
				t.Errorf("line %d: [Unreleased] must come first, once", i+1)
			}
			unreleased = true
		case strings.HasPrefix(l, "## "):
			m := versionHead.FindStringSubmatch(l)
			if m == nil {
				t.Errorf("line %d: %q is not ## [X.Y.Z] - YYYY-MM-DD", i+1, l)
				continue
			}
			var v [3]int
			for j := range v {
				v[j], _ = strconv.Atoi(m[j+1])
			}
			// Newest first: every version is below the one above it.
			if len(versions) > 0 && !(v[0] < prev[0] || v[0] == prev[0] && (v[1] < prev[1] || v[1] == prev[1] && v[2] < prev[2])) {
				t.Errorf("line %d: %d.%d.%d is not older than the version above it", i+1, v[0], v[1], v[2])
			}
			prev = v
			versions = append(versions, m[1]+"."+m[2]+"."+m[3])
		case subHead.MatchString(l):
			if name := subHead.FindStringSubmatch(l)[1]; !knownSubHead[name] {
				t.Errorf("line %d: ### %s is not a Keep a Changelog heading", i+1, name)
			}
		case linkLine.MatchString(l):
			m := linkLine.FindStringSubmatch(l)
			links[m[1]] = m[2]
		}
	}
	if !unreleased {
		t.Error("no [Unreleased] section")
	}
	if len(versions) == 0 {
		t.Fatal("no release")
	}
	// Every version links to its release, and Unreleased compares the newest
	// one with HEAD.
	for _, v := range versions {
		if want := "https://github.com/kanggu12/zellij-reaper/releases/tag/v" + v; links[v] != want {
			t.Errorf("[%s] links to %q, want %q", v, links[v], want)
		}
	}
	if want := "https://github.com/kanggu12/zellij-reaper/compare/v" + versions[0] + "...HEAD"; links["Unreleased"] != want {
		t.Errorf("[Unreleased] links to %q, want %q", links["Unreleased"], want)
	}
}
