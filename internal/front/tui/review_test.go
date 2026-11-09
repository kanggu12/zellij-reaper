package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

func TestADryRunPassNeitherAsksNorSaysItReaped(t *testing.T) {
	// With dry_run on, f asked "It reaps 1 session" and then reported
	// "1 reaped", though nothing was deleted.
	b := sample()
	b.cfg.DryRun, b.report.DryRun = true, true
	m := press(t, loaded(t, b, 100, 30), "f")
	if m.mode != modeMain || len(b.passes) != 1 {
		t.Fatalf("mode %v, %d passes: want a pass without a question", m.mode, len(b.passes))
	}
	if !strings.Contains(m.toast, "nothing deleted") {
		t.Errorf("toast %q does not say nothing was deleted", m.toast)
	}
}

func TestDialogTextIsWrappedOnce(t *testing.T) {
	// The text was wrapped two cells too wide, and lipgloss wrapped it again,
	// leaving one word alone on a line.
	for _, w := range []int{40, 52, 60, 100} {
		m := press(t, loaded(t, sample(), w, 30), "F")
		var got []string
		for _, l := range strings.Split(ansi.Strip(m.confirmBox()), "\n") {
			l = strings.Trim(l, "│╭╮╰╯─ ")
			if l == "" {
				if len(got) > 0 {
					break
				}
				continue
			}
			got = append(got, l)
		}
		want := wrapWords(i18n.T("confirm.force"), min(64, w-4)-6)
		if len(got) != len(want) {
			t.Errorf("at %d columns the question takes %d lines, want %d: %q", w, len(got), len(want), got)
		}
	}
}

func TestTheStatusLineCountsRowsAboveUnderAFilter(t *testing.T) {
	b := sample()
	for i := range 30 {
		b.report.Outcomes = append(b.report.Outcomes, session(fmt.Sprintf("job-%02d", i), false, nil))
	}
	m := loaded(t, b, 100, 24)
	m.filter = "job"
	m = press(t, m, "G")
	if s := ansi.Strip(m.statusLine()); !strings.Contains(s, "↑") {
		t.Errorf("filtered and at the bottom, the status line has no count above: %q", s)
	}
}

func TestReapingAnExitedProtectedSessionDoesNotMentionPanes(t *testing.T) {
	o := session("kept-exited", true, func(f *policy.Facts) { f.Protected, f.ProtectedBy = true, "protected list" })
	o.Verdict = policy.Verdict{Action: policy.Keep, Code: policy.ProtectedName, Detail: "protected list"}
	text, _ := ReapQuestion(reaper.Report{Outcomes: []reaper.Outcome{o}}, "kept-exited")
	if strings.Contains(text, "panes") || !strings.Contains(text, "resurrection data") {
		t.Errorf("question for an exited session: %q", text)
	}
}

func TestRenameFailuresAreWordedInTheScreensLanguage(t *testing.T) {
	i18n.Set(i18n.Korean)
	defer i18n.Set(i18n.English)
	for _, err := range []error{lock.ErrBusy, zellij.ErrNameTaken, fmt.Errorf("zellij rename-session a to b: %w", zellij.ErrUnconfirmed)} {
		if got := RenameFailed(err); strings.Contains(got, err.Error()) || strings.Contains(got, "zellij rename-session") {
			t.Errorf("%v came out in English: %q", err, got)
		}
	}
	if got := RenameFailed(errors.New("other")); !strings.Contains(got, "other") {
		t.Errorf("another error lost its words: %q", got)
	}
}

func TestTheHelpScreenShowsTheLogoCardWhereItFits(t *testing.T) {
	// The card measured itself against the widest line of the whole help
	// screen, so it showed only from 126 columns.
	for _, w := range []int{100, 132} {
		if out := ansi.Strip(press(t, loaded(t, sample(), w, 30), "?").View().Content); !strings.Contains(out, "github.com/kanggu12/zellij-reaper") {
			t.Errorf("no logo card on a %d-column help screen:\n%s", w, out)
		}
	}
}

func TestANarrowSetupScreenKeepsItsTitle(t *testing.T) {
	m := newSetup(setupSample(""), true)
	m.width, m.height = 32, 16
	if first := strings.SplitN(ansi.Strip(m.render()), "\n", 2)[0]; !strings.Contains(first, i18n.T("setup.title")) {
		t.Errorf("the title is cut: %q", first)
	}
}

func TestAShortHelpScreenKeepsEveryColor(t *testing.T) {
	out := ansi.Strip(press(t, loaded(t, sample(), 80, 24), "?").View().Content)
	if !strings.Contains(out, i18n.T("help.legend.unknown")) {
		t.Errorf("the last color is cut on an 80x24 help screen:\n%s", out)
	}
}

func TestAnEmptyNameSaysWhyNothingWasRenamed(t *testing.T) {
	m := pick(t, loaded(t, sample(), 100, 30), "scratch")
	m = press(t, m, "r")
	m.input.set("")
	if m = press(t, m, "enter"); m.toast != i18n.T("rename.empty") {
		t.Errorf("toast %q after an empty rename", m.toast)
	}
}

func TestTheFilterIsTheSameBeforeAndAfterEnter(t *testing.T) {
	m := press(t, loaded(t, sample(), 100, 30), "/")
	for _, k := range []string{"s", "c", " "} {
		m = press(t, m, k)
	}
	typing := len(m.rows())
	if m = press(t, m, "enter"); len(m.rows()) != typing || typing == 0 {
		t.Errorf("%d rows while typing \"sc \", %d after enter", typing, len(m.rows()))
	}
}

func TestTheDashboardCountsWhatTheNextPassReaps(t *testing.T) {
	// The fake timer's next pass is 42 minutes away. A session idle 30m with
	// a 1h wait is not due now but is by then: the status line counts it, and
	// its details say the next pass reaps it.
	b := sample()
	now := time.Now()
	b.report.Outcomes = append(b.report.Outcomes, session("soon", false, func(f *policy.Facts) { f.LastActive = now.Add(-30 * time.Minute) }))
	b.report.Thresholds = policy.Thresholds{Idle: time.Hour, Exited: 72 * time.Hour}
	m := loaded(t, b, 132, 40)
	want := b.report.Due() + 1
	if s := ansi.Strip(m.statusLine()); !strings.Contains(s, fmt.Sprintf("%d to reap at the next pass", want)) {
		t.Errorf("status line %q, want %d to reap", s, want)
	}
	m = pick(t, m, "soon")
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "Will be reaped at the next pass, at ") {
		t.Errorf("the details of soon do not say the next pass reaps it:\n%s", out)
	}
}
