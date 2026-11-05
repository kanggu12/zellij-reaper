package tui

import (
	"image/color"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
)

// logRows is how many log lines fit on screen.
func (m model) logRows() int { return max(1, m.height-4) }

// logView shows the decision log, newest at the bottom, colored by kind.
func (m model) logView() string {
	t := m.th
	lines := []string{m.header(), "", " " + lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(i18n.T("log.title"))}
	if len(m.logLines) == 0 {
		lines = append(lines, " "+ui.Fg(t.Muted).Render(i18n.T("log.empty")))
	}
	n := m.logRows() - 1
	top := max(0, min(m.logTop, len(m.logLines)-n))
	for _, raw := range m.logLines[top:min(len(m.logLines), top+n)] {
		lines = append(lines, " "+logLine(t, raw))
	}
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	follow := i18n.T("footer.follow")
	if m.follow {
		follow += " ●"
	}
	pos := ""
	if len(m.logLines) > 0 {
		pos = ui.Fg(t.Muted).Render("  " + strconv.Itoa(min(len(m.logLines), top+n)) + "/" + strconv.Itoa(len(m.logLines)))
	}
	bar := m.keyBar(t, [][2]string{{"↑↓", i18n.T("footer.scroll")}, {"f", follow}, {"esc", i18n.T("footer.back")}}) + pos
	lines = append(lines[:m.height-1], bar)
	return m.frame(lines)
}

// logLine lays out one log line: a pass's start and end as a muted rule,
// a decision as the name, its facts and the verdict, and what a pass did
// to a session as an arrow, in the current language. A line it does not
// know is shown as written.
func logLine(t ui.Theme, raw string) string {
	l := logfile.Parse(raw)
	stamp := ""
	if !l.Time.IsZero() {
		stamp = ui.Fg(t.Muted).Render(l.Time.Format("01-02 15:04")) + "  "
	}
	text := l.Text
	// arrow is a line under a decision: what the pass did.
	arrow := func(c color.Color, s string) string {
		return stamp + ui.Fg(t.Muted).Render("  → ") + ui.Fg(c).Render(s)
	}
	rule := func(s string) string { return stamp + ui.Fg(t.Muted).Render("── "+s) }
	switch {
	case startRe.MatchString(text):
		g := startRe.FindStringSubmatch(text)
		s := i18n.T("log.start", g[2], g[3])
		if g[1] == "true" {
			s += i18n.T("log.start.dry")
		}
		if g[4] != "" {
			s += i18n.T("log.start.force")
		}
		return rule(s)
	case endAllRe.MatchString(text):
		g := endAllRe.FindStringSubmatch(text)
		return rule(i18n.T("log.end", g[1], g[2], g[3]))
	case text == "=== reaper end ===":
		return rule(i18n.T("log.end.bare"))
	case decideRe.MatchString(text):
		g := decideRe.FindStringSubmatch(text)
		name := ui.Fg(t.Ink).Bold(true).Render(g[1])
		// A decision the dashboard knows reads in the current language,
		// colored like its row; the file keeps the log's own words.
		if v, ok := ui.VerdictFromLog(g[3]); ok {
			key := "log.kept.why"
			if v.Action == policy.Reap {
				key = "log.reap.why"
			}
			return stamp + name + "  " + ui.Fg(t.Cat[ui.Categorize(v)]).Render(i18n.T(key, ui.Why(v)))
		}
		c := t.Ink
		if l.Kind() == "reap" {
			c = t.Cat[ui.CatReap]
		}
		return stamp + name + ui.Fg(t.Muted).Render(" "+g[2]+" :: ") + ui.Fg(c).Render(g[3])
	case deletedRe.MatchString(text):
		return arrow(t.Cat[ui.CatReap], i18n.T("log.deleted", deletedRe.FindStringSubmatch(text)[1]))
	case keptRe.MatchString(text):
		g := keptRe.FindStringSubmatch(text)
		return arrow(t.Ink, i18n.T("log.kept", g[1], g[2]))
	case failRe.MatchString(text):
		g := failRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatUnknown], i18n.T("log.failed", g[1], g[2]))
	case renamedRe.MatchString(text):
		g := renamedRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.renamed", g[1], g[2]))
	case renFailRe.MatchString(text):
		g := renFailRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatUnknown], i18n.T("log.rename.failed", g[1], g[2], g[3]))
	case dryDelRe.MatchString(text):
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.dry.delete", dryDelRe.FindStringSubmatch(text)[1]))
	case dryRenRe.MatchString(text):
		g := dryRenRe.FindStringSubmatch(text)
		return arrow(t.Cat[ui.CatBusy], i18n.T("log.dry.rename", g[1], g[2]))
	}
	c := t.Ink
	switch l.Kind() {
	case "start", "end":
		c = t.Muted
	case "reap", "deleted":
		c = t.Cat[ui.CatReap]
	case "failed":
		c = t.Cat[ui.CatUnknown]
	case "renamed", "dry":
		c = t.Cat[ui.CatBusy]
	}
	return stamp + ui.Fg(c).Render(text)
}

// tilde shortens the home directory to "~" for display.
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}
