package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestTheTableSaysWhatTheNextPassRenames(t *testing.T) {
	th := NewTheme(true)
	rows := []Row{
		{Name: "brave-peach", Idle: "3h", Clients: "1", Next: "protected: claude", Cat: CatSafe, Rename: "fix-the-login-bug"},
		{Name: "notes", Idle: "1s", Clients: "1", Next: "attached", Cat: CatSafe},
	}
	for _, w := range []int{120, 80, 64, 52} {
		lines := th.Table(rows, w, 0)
		for _, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d: a line is %d wide: %q", w, lipgloss.Width(l), ansi.Strip(l))
			}
		}
		row := ansi.Strip(lines[1])
		// The verdict comes first; the mark only when there is room after it.
		if w >= 64 && !strings.Contains(row, "protected: claude") {
			t.Errorf("width %d: no verdict in %q", w, row)
		}
		if w >= 80 && !strings.Contains(row, "→ fix-the-login-bug") {
			t.Errorf("width %d: no new name in %q", w, row)
		}
		if strings.Contains(ansi.Strip(lines[2]), "→") {
			t.Errorf("width %d: a mark on a session nothing renames", w)
		}
	}
}
