// Package internal holds no code of its own, only the rule its packages follow.
package internal

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// layers are the folders under internal/, lowest first. A package may import
// from its own layer and the ones below it, never from a layer above: what
// decides knows nothing of what reads the machine, and what reads the machine
// knows nothing of a dashboard. docs/internals.md draws the same order.
var layers = map[string]int{
	"core":     0, // Rules and names, with no I/O.
	"machine":  1, // /proc, zellij, the lock, the log.
	"settings": 2, // What people set: the settings file, the protected list.
	"engine":   3, // What the commands do: a pass, recover, resize, install.
	"app":      4, // Wires the layers below together once.
	"front":    5, // What people use: the commands and the dashboard.
}

// leaves are small packages anyone may import, which import nothing of ours.
var leaves = map[string]bool{"version": true, "testutil": true}

// noIO is what the core layer may not import: it is what makes its rules
// testable as plain tables.
var noIO = map[string]bool{"os": true, "os/exec": true, "net": true, "syscall": true, "io/fs": true}

const module = "github.com/kanggu12/zellij-reaper/internal/"

// imports lists the imports of every package's own code, by the folder the
// package sits in relative to internal/. Tests may reach further, to build a
// fake machine, so they are left out.
func imports(t *testing.T) map[string][]string {
	t.Helper()
	found := map[string][]string{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imp, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			found[filepath.Dir(path)] = append(found[filepath.Dir(path)], imp)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// top is the first folder of a path under internal/.
func top(path string) string {
	first, _, _ := strings.Cut(path, "/")
	return first
}

func TestPackagesFollowTheLayers(t *testing.T) {
	found := imports(t)
	if len(found) < len(layers) {
		t.Fatalf("read only %d packages: the walk is not seeing the tree", len(found))
	}
	for pkg, deps := range found {
		from := top(pkg)
		level, layered := layers[from]
		// A folder nobody placed would escape the rule without a word.
		if !layered && !leaves[from] {
			t.Errorf("%s: internal/%s is in no layer: add it to the table above", pkg, from)
			continue
		}
		for _, imp := range deps {
			if from == "core" && noIO[imp] {
				t.Errorf("%s imports %s: the core layer does no I/O", pkg, imp)
			}
			rest, ours := strings.CutPrefix(imp, module)
			if !ours {
				continue
			}
			to := top(rest)
			if leaves[from] {
				t.Errorf("%s imports %s: a leaf package imports nothing of ours", pkg, imp)
				continue
			}
			if leaves[to] {
				continue
			}
			if layers[to] > level {
				t.Errorf("%s (%s) imports %s (%s): an import goes down the layers, not up", pkg, from, imp, to)
			}
		}
	}
}
