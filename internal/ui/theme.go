// Package ui holds what the TUI and the command line share: colors, the
// short phrases that describe a verdict, and the session table.
package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Category groups verdicts by what they mean to the person looking, which is
// what the color says.
type Category int

const (
	// CatReap: reaped at the next pass.
	CatReap Category = iota
	// CatWait: idle, waiting out its threshold.
	CatWait
	// CatBusy: something runs in it.
	CatBusy
	// CatAttached: somebody is looking at it.
	CatAttached
	// CatSafe: protected by name or by a program.
	CatSafe
	// CatUnknown: kept because a fact could not be read.
	CatUnknown
)

// Theme is one set of colors and styles, for a dark or a light terminal.
type Theme struct {
	Dark bool
	// Ink and Muted are the two text levels; Rule draws borders.
	Ink, Muted, Rule color.Color
	// Mark is the brand highlighter, from the logo.
	Mark color.Color
	// Cat holds the color of each category.
	Cat map[Category]color.Color
	// Selected is the background of the selected row; Key the keycaps'.
	Selected, Key color.Color
}

// NewTheme returns the theme for a dark or a light background. On a dark
// background the colors are bright and saturated, on a light one deep enough
// to read on white; both keep text contrast first.
func NewTheme(dark bool) Theme {
	ld := lipgloss.LightDark(dark)
	return Theme{
		Dark:  dark,
		Ink:   ld(lipgloss.Color("#1D1C1D"), lipgloss.Color("#E3E4E5")),
		Muted: ld(lipgloss.Color("#616061"), lipgloss.Color("#9198A1")),
		Rule:  ld(lipgloss.Color("#D1D9E0"), lipgloss.Color("#3D444D")),
		Mark:  ld(lipgloss.Color("#F2C744"), lipgloss.Color("#ECB22E")),
		Cat: map[Category]color.Color{
			CatReap:     ld(lipgloss.Color("#A86A00"), lipgloss.Color("#ECB22E")),
			CatWait:     ld(lipgloss.Color("#616061"), lipgloss.Color("#9198A1")),
			CatBusy:     ld(lipgloss.Color("#1264A3"), lipgloss.Color("#36C5F0")),
			CatAttached: ld(lipgloss.Color("#007A5A"), lipgloss.Color("#2EB67D")),
			CatSafe:     ld(lipgloss.Color("#7B3FA0"), lipgloss.Color("#C9A7EB")),
			CatUnknown:  ld(lipgloss.Color("#C0123E"), lipgloss.Color("#E01E5A")),
		},
		Selected: ld(lipgloss.Color("#E8F1FB"), lipgloss.Color("#262A31")),
		Key:      ld(lipgloss.Color("#ECECEE"), lipgloss.Color("#2C2F35")),
	}
}

// Fg is a style with a foreground color.
func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Dot is the colored "●" that leads a session's row.
func (t Theme) Dot(c Category) string { return Fg(t.Cat[c]).Render("●") }

// KeyCap draws a key as a small raised cap followed by what it does.
func (t Theme) KeyCap(key, label string) string {
	cap := lipgloss.NewStyle().Background(t.Key).Foreground(t.Ink).Bold(true).Padding(0, 1).Render(key)
	return cap + " " + Fg(t.Muted).Render(label)
}
