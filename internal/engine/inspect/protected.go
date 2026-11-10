package inspect

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// protectedCommand finds a protected program in a running session: in its
// process tree, or among the commands its layout starts.
func protectedCommand(t *procfs.Table, server *procfs.Server, f zellij.Files, protect []string) string {
	if len(protect) == 0 {
		return ""
	}
	if server != nil {
		if want := ProtectedProgram(t, server.PID, protect); want != "" {
			return want
		}
	}
	for _, want := range protect {
		for _, c := range f.Layout.Commands {
			if filepath.Base(c) == want || wordMatch(c, want) {
				return want
			}
		}
		for _, args := range f.Layout.Args {
			for _, a := range args {
				if wordMatch(a, want) {
					return want
				}
			}
		}
	}
	return ""
}

// ProtectedProgram finds a protected program running under a server, by the
// names a pass recognizes it by; "" when none runs.
func ProtectedProgram(t *procfs.Table, server int, protect []string) string {
	for _, p := range t.Descendants(server, 512) {
		for _, want := range protect {
			if slices.Contains(p.Names(), want) {
				return want
			}
			// An interpreter running a script from a package named after
			// the program ("node .../claude-code/cli.js").
			if len(p.Argv) > 1 && !strings.HasPrefix(p.Argv[1], "-") && wordMatch(p.Argv[1], want) {
				return want
			}
		}
	}
	return ""
}

// wordMatch reports whether word appears in s as a whole word: not preceded or
// followed by a letter, a digit or "_" ("claude-code" contains "claude",
// "claudette" does not).
func wordMatch(s, word string) bool {
	isWord := func(b byte) bool {
		return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		if (start == 0 || !isWord(s[start-1])) && (end == len(s) || !isWord(s[end])) {
			return true
		}
		i = start + 1
	}
}
