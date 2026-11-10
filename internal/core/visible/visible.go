// Package visible makes text from other programs safe to show. Session names,
// pane titles and command lines are chosen by whatever runs in a pane, and can
// hold control characters a terminal acts on: an escape sequence that writes
// the clipboard (OSC 52), retitles the window or moves the cursor, a carriage
// return that writes over what was printed, a direction override that shows a
// name backwards. Each such character is written out instead, as \x1b or
// \u202e, so the text reads as it is and does nothing.
package visible

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Text returns s with every control character written out: the C0 controls
// (line breaks and tabs too), DEL, the C1 controls, the direction overrides
// and isolates, and bytes that are not UTF-8. For one line of the log, which
// is read in a terminal and grepped. Text with none comes back as it is.
func Text(s string) string { return clean(s, false) }

// Terminal is Text for what the program prints to a screen: it keeps line
// breaks, tabs and the SGR escape sequences (colors and type faces) its own
// styling writes. An SGR sequence from another program goes through as well:
// it can only color text.
func Terminal(s string) string { return clean(s, true) }

// clean writes out the controls in s; screen keeps what a screen needs.
func clean(s string, screen bool) string {
	// Most text has nothing to write out: no copy then.
	i := firstUnsafe(s, screen)
	if i < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteString(s[:i])
	for i < len(s) {
		c := s[i]
		switch {
		// The program's own colors, on a screen.
		case c == 0x1b && screen && sgrLen(s[i:]) > 0:
			n := sgrLen(s[i:])
			b.WriteString(s[i : i+n])
			i += n
		// Line breaks and tabs lay out what a screen shows.
		case (c == '\n' || c == '\t') && screen:
			b.WriteByte(c)
			i++
		// The other C0 controls, ESC among them, and DEL.
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
			i++
		// Printable ASCII.
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			switch {
			// A byte that is no UTF-8: some terminals take 0x9b for CSI.
			case r == utf8.RuneError && size == 1:
				fmt.Fprintf(&b, `\x%02x`, c)
			case unsafeRune(r):
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				b.WriteString(s[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// firstUnsafe is the index of the first byte of s clean must write out, or
// -1 when there is none.
func firstUnsafe(s string, screen bool) int {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b && screen && sgrLen(s[i:]) > 0:
			i += sgrLen(s[i:])
			continue
		case (c == '\n' || c == '\t') && screen:
		case c < 0x20 || c == 0x7f:
			return i
		case c >= 0x80:
			r, size := utf8.DecodeRuneInString(s[i:])
			if (r == utf8.RuneError && size == 1) || unsafeRune(r) {
				return i
			}
			i += size
			continue
		}
		i++
	}
	return -1
}

// unsafeRune reports the characters above ASCII that act rather than show:
// the C1 controls (U+0080 to U+009F, CSI among them as U+009B), the
// bidirectional embeddings, overrides and isolates, which reorder what
// follows them on a line, and the direction marks (U+200E, U+200F, U+061C),
// which move what stands next to them.
func unsafeRune(r rune) bool {
	return (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
		r == 0x200e || r == 0x200f || r == 0x061c
}

// sgrLen is the length of the SGR sequence s starts with, ESC [ then digits,
// semicolons and colons, then m; 0 when it starts with none.
func sgrLen(s string) int {
	if len(s) < 3 || s[0] != 0x1b || s[1] != '[' {
		return 0
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 'm':
			return i + 1
		case (c >= '0' && c <= '9') || c == ';' || c == ':':
			continue
		default:
			return 0
		}
	}
	return 0
}
