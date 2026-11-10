package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

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
