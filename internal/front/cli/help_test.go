package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func TestAScreenGetsOnlyItsOwnEscapes(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "screen")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// A table row: the program's colors around a name that would write the
	// clipboard.
	row := "\x1b[1mnotes\x1b]52;c;aGk=\x07\x1b[m idle\n"
	if n, err := (screen{f}).Write([]byte(row)); err != nil || n != len(row) {
		t.Fatalf("wrote %d of %d: %v", n, len(row), err)
	}
	b, _ := os.ReadFile(f.Name())
	if got := string(b); got != "\x1b[1mnotes\\x1b]52;c;aGk=\\x07\x1b[m idle\n" {
		t.Errorf("on the screen: %q", got)
	}
	// The terminal checks see through it; a file that is no terminal gets
	// the bytes as they are.
	if stdoutFile(screen{f}) != f || screenOf(f) != io.Writer(f) {
		t.Error("a screen hides its file, or a file became a screen")
	}
}

// The line above plan's and run's tables keeps within the screen, as the
// tables do, and loses no part.
func TestTheHeadlineBreaksBetweenItsPartsToFit(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	th := ui.NewTheme(true)
	whole := headline(context.Background(), a, th, 1000)
	if len(whole) != 1 || lipgloss.Width(whole[0]) <= 80 {
		t.Fatalf("on a wide screen: %q", whole)
	}
	for _, width := range []int{80, 60, 40} {
		lines := headline(context.Background(), a, th, width)
		for _, l := range lines {
			if lipgloss.Width(l) > width {
				t.Errorf("at %d columns, %d: %q", width, lipgloss.Width(l), ansi.Strip(l))
			}
		}
		// Broken between parts only: joined again, it is the whole line.
		if got := ansi.Strip(strings.Join(lines, " · ")); got != ansi.Strip(whole[0]) {
			t.Errorf("at %d columns: %q", width, got)
		}
	}
	// A part wider than the screen gets a line of its own, whole.
	if got := joinWithin([]string{"a", "bbbbbbbbbb", "c"}, " · ", 5); strings.Join(got, "|") != "a|bbbbbbbbbb|c" {
		t.Errorf("a part wider than the screen: %q", got)
	}
}

// Every command's help, as `--help` prints it, fits an 80-column terminal: a
// longer line wraps where the terminal cuts it, mid-word.
func TestEveryHelpFitsIn80Columns(t *testing.T) {
	isolate(t)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		var buf bytes.Buffer
		c.SetOut(&buf)
		c.SetErr(&buf)
		if err := c.Help(); err != nil {
			t.Fatalf("%s: %v", c.CommandPath(), err)
		}
		for i, line := range strings.Split(buf.String(), "\n") {
			if w := ansi.StringWidth(line); w > 80 {
				t.Errorf("%s --help, line %d: %d columns: %q", c.CommandPath(), i+1, w, line)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRoot())
}
