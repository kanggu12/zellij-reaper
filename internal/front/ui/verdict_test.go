package ui

import (
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// Every verdict the log can hold reads back as the code it was written for.
func TestVerdictFromLogReadsEveryLogText(t *testing.T) {
	codes := []policy.Code{policy.ProtectedName, policy.NameUnknown, policy.NoInfo, policy.ProtectedCommand, policy.NoActivity,
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

// A name that could not be read says why, in the session's checks and in the
// plan's column, in the language on screen.
func TestANameThatCannotBeReadSaysWhy(t *testing.T) {
	for lang, want := range map[i18n.Lang][2]string{
		i18n.English: {"zellij's session index cannot be read", "name unknown"},
		i18n.Korean:  {"zellij 세션 색인을 못 읽음", "이름 모름"},
	} {
		i18n.Set(lang)
		v := policy.Decide(policy.Facts{Name: "6f54b763ec59", NameUnknown: true}, policy.Thresholds{}, time.Now())
		check := v.Checks[len(v.Checks)-1]
		if got := CheckDetail(check); got != want[0] || check.Pass {
			t.Errorf("%v: check %+v reads %q", lang, check, got)
		}
		if got := Why(v); got != want[1] {
			t.Errorf("%v: why %q", lang, got)
		}
	}
	i18n.Set(i18n.English)
	// A protection that matches still names what protects it: the list.
	v := policy.Decide(policy.Facts{Protected: true, ProtectedBy: "protected list", NameUnknown: true}, policy.Thresholds{}, time.Now())
	if got := CheckDetail(v.Checks[len(v.Checks)-1]); got != "list" {
		t.Errorf("protected: %q", got)
	}
}
