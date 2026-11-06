package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestANarrowTableKeepsTheVerdictBeforeTheCounts(t *testing.T) {
	th := NewTheme(true)
	rows := []Row{
		{Name: "docs-review", Idle: "25m", Clients: "0", Next: "reap in 34m", Cat: CatWait},
		{Name: "notes", Idle: "0s", Clients: "1", Next: "attached", Cat: CatSafe},
	}
	for _, c := range []struct {
		width          int
		state, clients bool
	}{
		// Room for every column.
		{80, true, true},
		// The client count goes first, then the state.
		{48, true, false},
		{38, false, false},
	} {
		lines := th.Table(rows, c.width, 0)
		head, row := ansi.Strip(lines[0]), ansi.Strip(lines[1])
		for _, l := range lines {
			if w := lipgloss.Width(l); w > c.width {
				t.Errorf("width %d: a line is %d wide: %q", c.width, w, ansi.Strip(l))
			}
		}
		if !strings.Contains(row, "reap in 34m") {
			t.Errorf("width %d: the verdict does not fit: %q", c.width, row)
		}
		if got := strings.Contains(head, "CLIENTS"); got != c.clients {
			t.Errorf("width %d: CLIENTS shown %v, want %v: %q", c.width, got, c.clients, head)
		}
		if got := strings.Contains(head, "STATE"); got != c.state {
			t.Errorf("width %d: STATE shown %v, want %v: %q", c.width, got, c.state, head)
		}
	}
}
