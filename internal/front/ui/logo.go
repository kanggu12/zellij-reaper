package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// logoPixels is the logo's mark, the text cursor whose top became a scythe,
// on a grid of square pixels: "b" is the blade, "c" the cursor, "." nothing.
// Two pixels stack in one terminal cell, which is about twice as tall as it
// is wide, so 8×8 pixels draw as 8 cells by 4 rows.
var logoPixels = []string{
	"....bbbc",
	"..bbbbbc",
	".bb...bc",
	"b......c",
	".......c",
	".......c",
	".......c",
	".......c",
}

// LogoWidth and LogoHeight are the mark's size in cells.
const (
	LogoWidth  = 8
	LogoHeight = 4
)

// Logo draws the mark as LogoHeight lines of LogoWidth cells, with half-block
// characters: the blade in the logo's amber, the cursor in the text color.
func (t Theme) Logo() []string {
	ink := func(p byte) color.Color {
		switch p {
		case 'b':
			return t.Blade
		case 'c':
			return t.Ink
		}
		return nil
	}
	out := make([]string, 0, LogoHeight)
	for y := 0; y+1 < len(logoPixels); y += 2 {
		var b strings.Builder
		for x := range len(logoPixels[y]) {
			top, bottom := ink(logoPixels[y][x]), ink(logoPixels[y+1][x])
			switch {
			case top == nil && bottom == nil:
				b.WriteByte(' ')
			case bottom == nil:
				b.WriteString(Fg(top).Render("▀"))
			case top == nil:
				b.WriteString(Fg(bottom).Render("▄"))
			case top == bottom:
				b.WriteString(Fg(top).Render("█"))
			default:
				// Two colors in one cell: the upper half is the letter,
				// the lower half the cell's background.
				b.WriteString(Fg(top).Background(bottom).Render("▀"))
			}
		}
		out = append(out, b.String())
	}
	return out
}

// Wordmark is the name as the logo writes it: "zellij" in the text color,
// a quiet hyphen, "reaper" in the blade's amber.
func (t Theme) Wordmark() string {
	bold := lipgloss.NewStyle().Bold(true)
	return bold.Foreground(t.Ink).Render("zellij") + Fg(t.Muted).Render("-") + bold.Foreground(t.Blade).Render("reaper")
}

// Brand is the wordmark and a version after it, the way every screen
// names this build.
func (t Theme) Brand(version string) string {
	return t.Wordmark() + "  " + Fg(t.Muted).Render(version)
}

// LogoBlock is the mark with up to LogoHeight-1 lines of text beside it,
// starting on its second row, where the blade meets the cursor: the
// wordmark and what follows it. Every line is LogoWidth+2 cells plus its text.
func (t Theme) LogoBlock(text ...string) []string {
	mark := t.Logo()
	out := make([]string, len(mark))
	for i, m := range mark {
		out[i] = m
		if j := i - 1; j >= 0 && j < len(text) && text[j] != "" {
			out[i] += "  " + text[j]
		}
	}
	return out
}
