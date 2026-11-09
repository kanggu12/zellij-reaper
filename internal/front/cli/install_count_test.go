package cli

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
)

func TestInstallCountsAtTheTimeOfTheFirstPass(t *testing.T) {
	// Idle 11h55m with a 12h wait: not due now, due at a pass ten minutes
	// on, which the setup screen had already said it would reap.
	now := time.Now()
	f := policy.Facts{Name: "almost", HasInfo: true, LastActive: now.Add(-(11*time.Hour + 55*time.Minute)),
		ActivitySource: "pane terminals", SocketOK: true, ServerPID: 7, Attached: policy.No, PaneCount: 1}
	r := reaper.Report{Outcomes: []reaper.Outcome{{Session: inspect.Session{Facts: f}}}}
	if n := dueAt(r, 12*time.Hour, 72*time.Hour, now); n != 0 {
		t.Errorf("due now: %d, want 0", n)
	}
	if n := dueAt(r, 12*time.Hour, 72*time.Hour, now.Add(10*time.Minute)); n != 1 {
		t.Errorf("due in ten minutes: %d, want 1", n)
	}
}
