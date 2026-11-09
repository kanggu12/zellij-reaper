package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

func TestInstallCountsAtTheTimeOfTheNextPass(t *testing.T) {
	i18n.Set(i18n.English)
	// Idle 55m with a 1h wait: not due now, due at a pass ten minutes on,
	// which the setup screen had already said it would reap.
	now := time.Now()
	th := policy.Thresholds{Idle: time.Hour, Exited: 72 * time.Hour}
	f := policy.Facts{Name: "almost", HasInfo: true, LastActive: now.Add(-55 * time.Minute), ActivitySource: "pane terminals",
		SocketOK: true, ServerPID: 7, Attached: policy.No, PaneCount: 1}
	r := reaper.Report{Thresholds: th, Outcomes: []reaper.Outcome{{Session: inspect.Session{Facts: f}, Verdict: policy.Decide(f, th, now)}}}
	line := nextPassLine(ui.NewTheme(true), r, nil, now.Add(10*time.Minute), false, now)
	if !strings.Contains(line, "it would reap 1 session") {
		t.Errorf("install said %q", line)
	}
}
