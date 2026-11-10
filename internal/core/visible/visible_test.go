package visible

import (
	"strings"
	"testing"
	"time"
)

func TestControlsAreWrittenOut(t *testing.T) {
	for _, c := range []struct{ in, text, terminal string }{
		// Nothing to write out: the same text.
		{"notes", "notes", "notes"},
		{"세션 이름 · déjà", "세션 이름 · déjà", "세션 이름 · déjà"},
		// OSC 52 writes the clipboard; ESC and BEL are shown.
		{"x\x1b]52;c;aGk=\x07y", `x\x1b]52;c;aGk=\x07y`, `x\x1b]52;c;aGk=\x07y`},
		// A cursor movement, a carriage return writing over the line.
		{"a\x1b[2Jb\rc", `a\x1b[2Jb\x0dc`, `a\x1b[2Jb\x0dc`},
		// C1 CSI as a character, and as a lone byte that is no UTF-8.
		{"a\u009b31mb", `a\u009b31mb`, `a\u009b31mb`},
		{"a\x9bb", `a\x9bb`, `a\x9bb`},
		// A direction override shows what follows backwards.
		{"evil\u202egnp.exe", `evil\u202egnp.exe`, `evil\u202egnp.exe`},
		// Line breaks and tabs: written out in a log line, kept on a screen.
		{"a\nb\tc", `a\x0ab\x09c`, "a\nb\tc"},
		// Colors: written out in a log line, kept on a screen.
		{"\x1b[1;38;2;1;2;3mbold\x1b[m", `\x1b[1;38;2;1;2;3mbold\x1b[m`, "\x1b[1;38;2;1;2;3mbold\x1b[m"},
		{"\x1b[4:3mcurly\x1b[0m", `\x1b[4:3mcurly\x1b[0m`, "\x1b[4:3mcurly\x1b[0m"},
		// An escape at the end, or an SGR cut short, is no color.
		{"a\x1b", `a\x1b`, `a\x1b`},
		{"a\x1b[31", `a\x1b[31`, `a\x1b[31`},
		{"\x7f", `\x7f`, `\x7f`},
	} {
		if got := Text(c.in); got != c.text {
			t.Errorf("Text(%q) = %q, want %q", c.in, got, c.text)
		}
		if got := Terminal(c.in); got != c.terminal {
			t.Errorf("Terminal(%q) = %q, want %q", c.in, got, c.terminal)
		}
	}
}

func TestALongTextTakesLinearTime(t *testing.T) {
	// Escapes that start sequences which never end, and bytes that are no
	// UTF-8: each is looked at a bounded number of times.
	long := strings.Repeat("\x1b[1111111111\xa5", 200_000)
	start := time.Now()
	for range 3 {
		Terminal(long)
		Text(long)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("%d bytes took %s", len(long), took)
	}
}

func FuzzTerminalLeavesNoControl(f *testing.F) {
	for _, s := range []string{"notes", "x\x1b]52;c;aGk=\x07y", "\x1b[1mb\x1b[m", "a\x9bb", "e\u202ef"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, out := range []string{Text(s), Terminal(s)} {
			// What comes out holds no control but the screen's own.
			if i := firstUnsafe(out, true); i >= 0 {
				t.Fatalf("%q came out as %q, with a control at %d", s, out, i)
			}
		}
		// A log line holds no escape at all.
		text := Text(s)
		for i := 0; i < len(text); i++ {
			if c := text[i]; c < 0x20 || c == 0x7f {
				t.Fatalf("%q came out of Text as %q", s, text)
			}
		}
	})
}
