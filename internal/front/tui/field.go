package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// fieldLimit is the most characters a field holds. A session name fits in far
// less; this only stops a stray paste from making the field unusable.
const fieldLimit = 200

// field is the one-line text field of the rename dialog and the name filter.
//
// It stands in for bubbles' textinput, the only thing this program used from
// that module. textinput brings in atotto/clipboard, whose init looks for xclip,
// xsel and wl-copy in every PATH directory as soon as the program starts,
// whatever the command: 34 ms of a 40 ms start on WSL, where PATH lists Windows
// directories. A terminal's paste reaches the field without any of that.
type field struct {
	// prompt is shown before the text.
	prompt string
	// text is the value and cursor the position in it, both in characters,
	// so Hangul and accented letters move and delete as one.
	text   []rune
	cursor int
	// width is how many columns the text takes on screen, cursor included.
	width int
	// first is the first character shown when the text is wider than that.
	first int
	// textStyle fills the field; promptStyle draws the prompt.
	textStyle, promptStyle lipgloss.Style
}

// value is the text as a string.
func (f field) value() string { return string(f.text) }

// set replaces the text and puts the cursor at its end.
func (f *field) set(value string) {
	f.text = nil
	f.cursor, f.first = 0, 0
	f.insert(value)
}

// insert puts text at the cursor. Control characters become spaces: a pasted
// line break must not end up in a session name.
func (f *field) insert(s string) {
	add := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if room := fieldLimit - len(f.text); len(add) > room {
		add = add[:max(room, 0)]
	}
	// A new slice each time: the model is copied by value between updates.
	text := make([]rune, 0, len(f.text)+len(add))
	text = append(append(append(text, f.text[:f.cursor]...), add...), f.text[f.cursor:]...)
	f.text, f.cursor = text, f.cursor+len(add)
}

// remove deletes the characters from position a up to position b.
func (f *field) remove(a, b int) {
	text := make([]rune, 0, len(f.text)-(b-a))
	f.text = append(append(text, f.text[:a]...), f.text[b:]...)
	f.cursor = a
}

// wordLeft is where the word before the cursor starts.
func (f field) wordLeft() int {
	i := f.cursor
	for i > 0 && !isWord(f.text[i-1]) {
		i--
	}
	for i > 0 && isWord(f.text[i-1]) {
		i--
	}
	return i
}

// wordRight is where the word after the cursor ends.
func (f field) wordRight() int {
	i := f.cursor
	for i < len(f.text) && !isWord(f.text[i]) {
		i++
	}
	for i < len(f.text) && isWord(f.text[i]) {
		i++
	}
	return i
}

// isWord reports whether a character is part of a word.
func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) }

// key applies a key press, the same keys a shell's line editor takes, and
// reports whether the text changed.
func (f *field) key(k tea.KeyPressMsg) bool {
	before := f.value()
	switch k.String() {
	case "left", "ctrl+b":
		f.cursor = max(f.cursor-1, 0)
	case "right", "ctrl+f":
		f.cursor = min(f.cursor+1, len(f.text))
	case "home", "ctrl+a":
		f.cursor = 0
	case "end", "ctrl+e":
		f.cursor = len(f.text)
	case "alt+left", "ctrl+left", "alt+b":
		f.cursor = f.wordLeft()
	case "alt+right", "ctrl+right", "alt+f":
		f.cursor = f.wordRight()
	case "backspace", "ctrl+h":
		if f.cursor > 0 {
			f.remove(f.cursor-1, f.cursor)
		}
	case "delete", "ctrl+d":
		if f.cursor < len(f.text) {
			f.remove(f.cursor, f.cursor+1)
		}
	case "ctrl+w", "alt+backspace", "ctrl+backspace":
		f.remove(f.wordLeft(), f.cursor)
	case "alt+d", "alt+delete", "ctrl+delete":
		f.remove(f.cursor, f.wordRight())
	case "ctrl+u":
		f.remove(0, f.cursor)
	case "ctrl+k":
		f.remove(f.cursor, len(f.text))
	default:
		// Text is what the key types; a key combination that types nothing
		// has none, and one with ctrl or alt is not meant as text.
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			f.insert(k.Text)
		}
	}
	return f.value() != before
}

// columns is how wide a run of characters is on screen.
func columns(rs []rune) int { return ansi.StringWidth(string(rs)) }

// view draws the prompt and exactly width columns of field, scrolled so the
// cursor stays in sight. The cursor is one reversed cell, on the character it
// stands before, or on an empty cell at the end.
func (f *field) view() string {
	// Keep the cursor's cell inside the window.
	if f.cursor < f.first {
		f.first = f.cursor
	}
	for f.first < f.cursor && columns(f.text[f.first:f.cursor])+1 > f.width {
		f.first++
	}
	// The characters after the cursor take what room is left.
	end := f.cursor
	used := columns(f.text[f.first:f.cursor])
	under := " "
	if f.cursor < len(f.text) {
		under = string(f.text[f.cursor])
		end = f.cursor + 1
	}
	used += ansi.StringWidth(under)
	for end < len(f.text) && used+columns(f.text[end:end+1]) <= f.width {
		used += columns(f.text[end : end+1])
		end++
	}
	after := ""
	if f.cursor < len(f.text) {
		after = string(f.text[f.cursor+1 : end])
	}
	return f.promptStyle.Render(f.prompt) +
		f.textStyle.Render(string(f.text[f.first:f.cursor])) +
		f.textStyle.Reverse(true).Render(under) +
		f.textStyle.Render(after+strings.Repeat(" ", max(f.width-used, 0)))
}
