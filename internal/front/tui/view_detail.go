package tui

import (
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
)

// detail explains the selected session: the verdict in a sentence, every
// check with its evidence, its panes, and where the facts came from.
func (m model) detail(width, height int) []string {
	t := m.th
	if height <= 0 {
		return nil
	}
	o, ok := m.selected()
	if !ok {
		return []string{ui.Fg(t.Muted).Render(i18n.T("detail.none"))}
	}
	s, v := o.Session, o.Verdict
	cat := ui.Categorize(v)
	var lines []string
	// In the side-by-side layout the name heads the panel; stacked, the
	// rule above already shows it.
	if m.width >= sideBySide {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(t.Ink).Render(s.Name()))
	}
	// The pass the timer runs next reaps it once its wait is over by then,
	// judged as the count in the status line is.
	next := m.nextPass()
	byNext := !next.IsZero() && v.Action != policy.Reap && m.report.ReapsAt(o, next)
	lines = append(lines, ui.Fg(t.Cat[cat]).Bold(true).Render(verdictSentence(v, m.report.DryRun, byNext, next)))
	lines = append(lines, "")
	lines = append(lines, ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.why")))
	for _, c := range v.Checks {
		mark := ui.Fg(t.Cat[ui.CatAttached]).Render("✓")
		label := ui.Fg(t.Ink).Render(ui.CheckName(c.Name))
		if !c.Pass {
			mark = ui.Fg(t.Cat[cat]).Render("✗")
			label = ui.Fg(t.Cat[cat]).Render(ui.CheckName(c.Name))
		}
		line := "  " + mark + " " + label
		d := ui.CheckDetail(c)
		// Where a fact came from goes on its own check, said once.
		switch {
		case c.Name == "last activity" && s.Facts.ActivitySource != "":
			d = join(d, i18n.T("source."+s.Facts.ActivitySource))
		case c.Name == "server" && c.Pass && s.ServerMatch != "":
			d = join(d, i18n.T("match."+s.ServerMatch))
		}
		if d != "" {
			line += ui.Fg(t.Muted).Render("  " + d)
		}
		lines = append(lines, line)
	}
	if len(s.Panes) > 0 {
		lines = append(lines, "", ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.panes")))
		for _, p := range s.Panes {
			// A pane that runs its program directly says "running"; a
			// shell says what it runs.
			var state string
			switch {
			case p.Busy == "":
				state = ui.Fg(t.Muted).Render(i18n.T("detail.pane.prompt"))
			case p.Busy == p.Command:
				state = ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("detail.pane.running"))
			default:
				state = ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("detail.pane.busy", p.Busy))
			}
			used := ""
			if !p.LastIO.IsZero() {
				used = ui.Fg(t.Muted).Render(" · " + i18n.T("detail.pane.used", duration.Format(m.now.Sub(p.LastIO))))
			}
			lines = append(lines, "  "+ui.Fg(t.Ink).Render(p.Command)+ui.Fg(t.Muted).Render(" · ")+state+used)
		}
	}
	if o.WouldRename != "" {
		lines = append(lines, "", ui.Fg(t.Muted).Render(i18n.T("detail.rename", o.WouldRename)))
	}
	// A name kept by hand stays through every pass. The dashboard keeps one
	// with r; the command that lets it go is said here, as it has no key.
	if m.b.NameKept(s.Name()) {
		lines = append(lines, "", ui.Fg(t.Muted).Render(i18n.T("detail.namekept")),
			"  "+ui.Fg(t.Ink).Render("zellij-reaper forget-name "+shellWord(s.Name())))
	}
	// The settings that shaped this verdict, when the panel has room.
	if settings := m.settingsLines(); len(lines)+len(settings)+1 <= height {
		lines = append(lines, "")
		lines = append(lines, settings...)
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	// Cut short, a mark right after the last line kept says there is more.
	if len(lines) > height {
		lines = lines[:height-1]
		for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, ui.Fg(t.Muted).Render("  …"))
	}
	return lines
}

// shellWord is name as one word of a shell command, to paste as it is: bare
// when it holds nothing a shell reads, else in single quotes.
func shellWord(name string) string {
	plain := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_./@%+=:,", r)
	}
	if name != "" && strings.IndexFunc(name, func(r rune) bool { return !plain(r) }) < 0 {
		return name
	}
	return "'" + strings.ReplaceAll(name, "'", `'\''`) + "'"
}

// join adds a fact to a check's evidence with a dot between.
func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

// settingsLines summarizes the settings that decide verdicts.
func (m model) settingsLines() []string {
	t := m.th
	c := m.b.Config()
	onOff := func(b bool) string {
		if b {
			return i18n.T("on")
		}
		return i18n.T("off")
	}
	lines := []string{
		ui.Fg(t.Muted).Bold(true).Render(i18n.T("detail.settings")),
		"  " + ui.Fg(t.Ink).Render(i18n.T("settings.waits", duration.Format(c.IdleAfter), duration.Format(c.ExitedAfter))),
	}
	if len(c.ProtectCommands) > 0 {
		lines = append(lines, "  "+ui.Fg(t.Ink).Render(i18n.T("settings.programs", strings.Join(c.ProtectCommands, ", "))))
	}
	if c.Protect != "" {
		lines = append(lines, "  "+ui.Fg(t.Ink).Render(i18n.T("settings.pattern", c.Protect)))
	}
	rename := i18n.T("settings.rename", onOff(c.AutoRename))
	// Leaving the sessions someone is attached to alone is worth seeing.
	if c.AutoRename && !c.AutoRenameAttached {
		rename = i18n.T("settings.rename.unattached")
	}
	lines = append(lines, "  "+ui.Fg(t.Ink).Render(rename))
	if c.File != "" {
		lines = append(lines, "  "+ui.Fg(t.Muted).Render(i18n.T("settings.file", tilde(c.File))))
	} else {
		lines = append(lines, "  "+ui.Fg(t.Muted).Render(i18n.T("settings.defaults")))
	}
	return lines
}

// recentLines are the last changes, newest last, as short events: what was
// reaped and why, what was renamed, what failed, and passes that did
// something. The log keeps the full wording.
func (m model) recentLines(n int) []string {
	t := m.th
	why := map[string]string{}
	var events []string
	for _, raw := range m.logLines {
		l := logfile.Parse(raw)
		stamp := ""
		if !l.Time.IsZero() {
			stamp = ui.Fg(t.Muted).Render(l.Time.Format("01-02 15:04")) + "  "
		}
		text := l.Text
		switch {
		case reapRe.MatchString(text):
			g := reapRe.FindStringSubmatch(text)
			key := "recent.idle"
			if g[3] == "EXITED" {
				key = "recent.exited"
			}
			why[g[1]] = i18n.T(key, g[4])
		case deletedRe.MatchString(text):
			name := deletedRe.FindStringSubmatch(text)[1]
			line := stamp + ui.Fg(t.Cat[ui.CatReap]).Render(i18n.T("recent.reaped", name))
			// The reason comes from the REAP line just before.
			if w := why[name]; w != "" {
				line += ui.Fg(t.Muted).Render(" · " + w)
			}
			events = append(events, line)
		case renamedRe.MatchString(text):
			g := renamedRe.FindStringSubmatch(text)
			events = append(events, stamp+ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("recent.renamed", g[1], g[2])))
		case failedRe.MatchString(text):
			events = append(events, stamp+ui.Fg(t.Cat[ui.CatUnknown]).Render(i18n.T("recent.failed", failedRe.FindStringSubmatch(text)[1])))
		case handRe.MatchString(text):
			g := handRe.FindStringSubmatch(text)
			by := ui.Fg(t.Muted).Render(" · " + i18n.T("recent.hand"))
			switch {
			case g[3] != "":
				events = append(events, stamp+ui.Fg(t.Cat[ui.CatBusy]).Render(i18n.T("recent.renamed", g[1], g[3]))+by)
			case strings.HasPrefix(g[2], "name kept from"):
				events = append(events, stamp+ui.Fg(t.Cat[ui.CatSafe]).Render(i18n.T("recent.kept", g[1]))+by)
			default:
				events = append(events, stamp+ui.Fg(t.Cat[ui.CatReap]).Render(i18n.T("recent.reaped", g[1]))+by)
			}
		case endRe.MatchString(text):
			g := endRe.FindStringSubmatch(text)
			// A pass that changed nothing is not news.
			if g[1] != "0" {
				events = append(events, stamp+ui.Fg(t.Muted).Render(i18n.T("recent.pass", g[1], g[2])))
			}
		}
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	return events
}

// verdictSentence says in a sentence what the next pass does. byNext is set
// when the timer's next pass, at next, reaps a session not due now.
func verdictSentence(v policy.Verdict, dry, byNext bool, next time.Time) string {
	switch {
	case v.Action == policy.Reap && dry:
		return i18n.T("detail.verdict.dry")
	case v.Action == policy.Reap:
		return i18n.T("detail.verdict.reap")
	// The wait ends before the next pass: that pass reaps it, not the end
	// of the wait.
	case byNext && !dry:
		return i18n.T("detail.verdict.next", ui.Clock(next))
	case v.Wait > 0:
		return i18n.T("detail.verdict.in", duration.Format(v.Wait))
	}
	return i18n.T("detail.verdict.keep", ui.Why(v))
}
