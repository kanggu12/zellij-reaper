package cli

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// stdoutFile returns w as a file when it is one (for terminal checks), or nil.
func stdoutFile(w io.Writer) *os.File {
	if f, ok := w.(*os.File); ok {
		return f
	}
	return nil
}

// terminalWidth is the width of the terminal on stdout, 100 if unknown.
func terminalWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 100
}
