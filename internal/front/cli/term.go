package cli

import (
	"io"
	"os"

	"github.com/charmbracelet/x/term"

	"github.com/kanggu12/zellij-reaper/internal/core/visible"
)

// stdoutFile returns w as a file when it is one (for terminal checks), or nil.
func stdoutFile(w io.Writer) *os.File {
	switch f := w.(type) {
	case *os.File:
		return f
	case screen:
		return f.File
	}
	return nil
}

// screen is a terminal the program prints to. Session names, pane titles and
// command lines come from other programs, and reach it through
// visible.Terminal: only the program's own colors go through as escape
// sequences, and a name that would write the clipboard or move the cursor is
// shown as it is spelt.
type screen struct{ *os.File }

// Write writes p through visible.Terminal, and counts all of p as written.
func (s screen) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.File, visible.Terminal(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// screenOf is f for printing: a screen when f is a terminal. A pipe or a file
// gets the bytes as they are, which is what a script, or the prompt hook
// reading `here`, needs.
func screenOf(f *os.File) io.Writer {
	if isTerminal(f) {
		return screen{f}
	}
	return f
}

// terminalWidth is the width of the terminal on stdout, 100 if unknown.
func terminalWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 100
}
