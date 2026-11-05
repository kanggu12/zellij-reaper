package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// keyPress is a key press: text for a plain character, by code for the rest.
func keyPress(s string) tea.KeyPressMsg {
	codes := map[string]tea.Key{
		"left":      {Code: tea.KeyLeft},
		"right":     {Code: tea.KeyRight},
		"home":      {Code: tea.KeyHome},
		"end":       {Code: tea.KeyEnd},
		"backspace": {Code: tea.KeyBackspace},
		"delete":    {Code: tea.KeyDelete},
		"ctrl+u":    {Code: 'u', Mod: tea.ModCtrl},
		"ctrl+k":    {Code: 'k', Mod: tea.ModCtrl},
		"ctrl+w":    {Code: 'w', Mod: tea.ModCtrl},
		"ctrl+a":    {Code: 'a', Mod: tea.ModCtrl},
		"ctrl+e":    {Code: 'e', Mod: tea.ModCtrl},
		"alt+b":     {Code: 'b', Mod: tea.ModAlt},
		"alt+f":     {Code: 'f', Mod: tea.ModAlt},
		"alt+d":     {Code: 'd', Mod: tea.ModAlt},
	}
	if k, ok := codes[s]; ok {
		return tea.KeyPressMsg(k)
	}
	r := []rune(s)
	return tea.KeyPressMsg(tea.Key{Code: r[0], Text: s})
}

func TestFieldEdits(t *testing.T) {
	cases := []struct {
		start string
		keys  []string
		want  string
		at    int
	}{
		{"", []string{"a", "b", "c"}, "abc", 3},
		{"abc", []string{"left", "x"}, "abxc", 3},
		{"abc", []string{"home", "x"}, "xabc", 1},
		{"abc", []string{"ctrl+a", "ctrl+e", "x"}, "abcx", 4},
		{"abc", []string{"backspace"}, "ab", 2},
		{"abc", []string{"home", "delete"}, "bc", 0},
		{"abc", []string{"home", "backspace"}, "abc", 0},
		{"abc", []string{"delete"}, "abc", 3},
		// Words, as a shell's line editor takes them.
		{"web-app notes", []string{"ctrl+w"}, "web-app ", 8},
		{"web-app notes", []string{"ctrl+w", "ctrl+w"}, "web-", 4},
		{"web-app notes", []string{"alt+b", "alt+b", "x"}, "web-xapp notes", 5},
		{"web-app notes", []string{"home", "alt+f", "x"}, "webx-app notes", 4},
		{"web-app notes", []string{"home", "alt+d"}, "-app notes", 0},
		// To the line's ends.
		{"web-app", []string{"left", "left", "ctrl+u"}, "pp", 0},
		{"web-app", []string{"left", "left", "ctrl+k"}, "web-a", 5},
		// A Korean syllable moves and deletes as one character.
		{"한글이름", []string{"backspace"}, "한글이", 3},
		{"한글이름", []string{"left", "left", "x"}, "한글x이름", 3},
		{"이름", []string{"한"}, "이름한", 3},
		// A key combination that types nothing leaves the text alone.
		{"abc", []string{"ctrl+u", "ctrl+u"}, "", 0},
	}
	for _, c := range cases {
		var f field
		f.set(c.start)
		for _, k := range c.keys {
			f.key(keyPress(k))
		}
		if f.value() != c.want || f.cursor != c.at {
			t.Errorf("%q + %v: %q at %d, want %q at %d", c.start, c.keys, f.value(), f.cursor, c.want, c.at)
		}
	}
}

func TestFieldKeepsPastesToOneLine(t *testing.T) {
	var f field
	f.set("a")
	f.insert("b\nc\td")
	if f.value() != "ab c d" {
		t.Errorf("pasted %q", f.value())
	}
	f.insert(strings.Repeat("x", 2*fieldLimit))
	if len(f.text) != fieldLimit {
		t.Errorf("limit: %d characters", len(f.text))
	}
}

func TestFieldSaysWhenTheTextChanged(t *testing.T) {
	var f field
	f.set("ab")
	if f.key(keyPress("left")) {
		t.Error("moving the cursor changed the text")
	}
	if !f.key(keyPress("x")) {
		t.Error("typing did not change the text")
	}
}

func TestFieldViewIsAlwaysItsWidth(t *testing.T) {
	values := []string{"", "short", "exactly-ten", strings.Repeat("long", 30), "한글", strings.Repeat("한글", 30), "a한b글c" + strings.Repeat("é", 40)}
	for _, width := range []int{1, 2, 3, 10, 40} {
		for _, v := range values {
			var f field
			f.width = width
			f.set(v)
			// Wherever the cursor stands, the field takes its width, give or
			// take the one column a wide character cannot be split over.
			for at := len(f.text); at >= 0; at-- {
				f.cursor = at
				got := lipgloss.Width(f.view())
				if got != width && got != width+1 {
					t.Errorf("width %d, %q, cursor %d: drew %d columns", width, v, at, got)
				}
				if got == width+1 && width > 1 && !hasWide(f.text) {
					t.Errorf("width %d, %q, cursor %d: one column over without a wide character", width, v, at)
				}
			}
		}
	}
}

// hasWide reports whether text holds a character two columns wide.
func hasWide(rs []rune) bool {
	for i := range rs {
		if columns(rs[i:i+1]) > 1 {
			return true
		}
	}
	return false
}

func TestFieldKeepsTheCursorInSight(t *testing.T) {
	var f field
	f.width = 5
	f.set(strings.Repeat("a", 20) + "END")
	if got := ansi.Strip(f.view()); !strings.Contains(got, "END") {
		t.Errorf("the end of the text is not in sight: %q", got)
	}
	f.cursor = 0
	if got := ansi.Strip(f.view()); got != "aaaaa" || f.first != 0 {
		t.Errorf("the start is not in sight: %q, first %d", got, f.first)
	}
}
