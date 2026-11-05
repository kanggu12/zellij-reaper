package ui

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
)

// Every verdict the log can hold reads back as the code it was written for.
func TestVerdictFromLogReadsEveryLogText(t *testing.T) {
	codes := []policy.Code{policy.ProtectedName, policy.NoInfo, policy.ProtectedCommand, policy.NoActivity,
		policy.ExitedRecent, policy.ExitedOld, policy.ExitedLive, policy.NoSocket, policy.NoServer, policy.Attached,
		policy.AttachUnknown, policy.NoPanes, policy.Busy, policy.IdleRecent, policy.IdleOld}
	for _, code := range codes {
		v := policy.Verdict{Code: code, Detail: "3h 2m", Limit: time.Hour}
		got, ok := VerdictFromLog(v.LogText())
		if !ok || got.Code != code {
			t.Errorf("%s: %q read back as %q (ok %v)", code, v.LogText(), got.Code, ok)
		}
		if reaps := code == policy.IdleOld || code == policy.ExitedOld; (got.Action == policy.Reap) != reaps {
			t.Errorf("%s: reap %v", code, got.Action == policy.Reap)
		}
	}
	if _, ok := VerdictFromLog("SKIP something the shell version wrote"); ok {
		t.Error("unknown words read as a verdict")
	}
}
