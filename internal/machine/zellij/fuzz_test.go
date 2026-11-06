package zellij

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seeds adds every recorded zellij output whose file name has prefix.
func seeds(f *testing.F, prefix string) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*", prefix+"*"))
	for _, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(string(b))
		}
	}
}

// What zellij prints can be anything a later release prints: parsing it must
// not panic, and a session name read from it is one line, not blank.
func FuzzParseSessions(f *testing.F) {
	seeds(f, "list-sessions")
	f.Fuzz(func(t *testing.T, out string) {
		for _, l := range ParseSessions(out) {
			if strings.TrimSpace(l.Name) == "" || strings.Contains(l.Name, "\n") {
				t.Fatalf("name %q from %q", l.Name, out)
			}
		}
	})
}

func FuzzParseClients(f *testing.F) {
	seeds(f, "list-clients")
	f.Fuzz(func(t *testing.T, out string) { _, _ = ParseClients(out) })
}

func FuzzParseMetadata(f *testing.F) {
	seeds(f, "metadata")
	f.Fuzz(func(t *testing.T, text string) { _ = ParseMetadata(text).TitlesByFocus() })
}

func FuzzParseLayout(f *testing.F) {
	seeds(f, "layout")
	f.Fuzz(func(t *testing.T, text string) { _ = ParseLayout(text) })
}
