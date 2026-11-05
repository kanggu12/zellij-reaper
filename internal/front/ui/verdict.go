package ui

import (
	"regexp"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// Categorize says which color family a verdict belongs to.
func Categorize(v policy.Verdict) Category {
	switch v.Code {
	case policy.IdleOld, policy.ExitedOld:
		return CatReap
	case policy.IdleRecent, policy.ExitedRecent:
		return CatWait
	case policy.Busy:
		return CatBusy
	case policy.Attached:
		return CatAttached
	case policy.ProtectedName, policy.ProtectedCommand:
		return CatSafe
	}
	return CatUnknown
}

// Why is the reason a session is in its state, as a short phrase:
// "running htop", "attached", "claude running", "idle 12m".
func Why(v policy.Verdict) string {
	key := "why." + string(v.Code)
	switch v.Code {
	case policy.ProtectedName:
		by := "protected.list"
		if v.Detail == "protect pattern" {
			by = "protected.pattern"
		}
		return i18n.T(key, i18n.T(by))
	case policy.ProtectedCommand, policy.Busy, policy.IdleRecent, policy.IdleOld, policy.ExitedRecent, policy.ExitedOld:
		return i18n.T(key, v.Detail)
	}
	return i18n.T(key)
}

// NextPass is what the next pass does, as shown in the table's last column:
// "reaped", "reaped in 48m", or the reason it is kept.
func NextPass(v policy.Verdict, dry bool) string {
	switch {
	case v.Action == policy.Reap && dry:
		return i18n.T("verdict.dryreap")
	case v.Action == policy.Reap:
		return i18n.T("verdict.reap")
	case v.Wait > 0:
		return i18n.T("verdict.in", duration.Format(v.Wait))
	}
	return Why(v)
}

// Clock prints a time of day, with the date when it is not today.
func Clock(at time.Time) string {
	now := time.Now()
	if at.YearDay() == now.YearDay() && at.Year() == now.Year() {
		return at.Format("15:04")
	}
	return at.Format("01-02 15:04")
}

// CheckName translates a policy check's label.
func CheckName(name string) string { return i18n.T("check." + name) }

// CheckDetail words a check's evidence in the current language. Values
// (ages, counts) are worded here; text evidence (a command, a pid) is shown
// as it is.
func CheckDetail(c policy.Check) string {
	switch c.Name {
	case "last activity":
		return i18n.T("check.detail.ago", duration.Format(c.Age))
	case "exited long enough", "idle long enough":
		if c.Pass || c.Limit == 0 {
			return duration.Format(c.Age)
		}
		return i18n.T("check.detail.of", duration.Format(c.Age), duration.Format(c.Limit))
	case "every pane at a prompt":
		if c.Pass {
			return i18n.T("check.detail.panes", c.Count)
		}
		return c.Detail
	case "no live server":
		if !c.Pass && c.Detail != "" {
			return i18n.T("check.detail.stillup", c.Detail)
		}
		return c.Detail
	case "no client attached":
		if !c.Pass && c.Count > 0 {
			return i18n.T("check.detail.clients", c.Count)
		}
		return c.Detail
	case "name not protected":
		if c.Detail == "protect pattern" {
			return i18n.T("protected.pattern")
		}
		if c.Detail == "protected list" {
			return i18n.T("protected.list")
		}
	}
	return c.Detail
}

// logVerdicts reads policy.LogText back: each pattern, the code it was written
// for, and whether that code reaps. The first match wins, so the longer forms
// come before the shorter ones they share a start with.
var logVerdicts = []struct {
	re   *regexp.Regexp
	code policy.Code
	reap bool
}{
	{regexp.MustCompile(`^SKIP protected by (.+)$`), policy.ProtectedName, false},
	{regexp.MustCompile(`^SKIP no session_info dir \(unknown state\)$`), policy.NoInfo, false},
	{regexp.MustCompile(`^SKIP protected command running \((.+)\)$`), policy.ProtectedCommand, false},
	{regexp.MustCompile(`^SKIP cannot read activity time$`), policy.NoActivity, false},
	{regexp.MustCompile(`^SKIP EXITED, but a server still serves it \((.+)\)$`), policy.ExitedLive, false},
	{regexp.MustCompile(`^SKIP EXITED (.+) ago, reaped after .+$`), policy.ExitedRecent, false},
	{regexp.MustCompile(`^REAP EXITED, (.+) since last activity$`), policy.ExitedOld, true},
	{regexp.MustCompile(`^SKIP socket file missing for running session$`), policy.NoSocket, false},
	{regexp.MustCompile(`^SKIP server PID not found$`), policy.NoServer, false},
	{regexp.MustCompile(`^SKIP client attached \((.+)\)$`), policy.Attached, false},
	{regexp.MustCompile(`^SKIP attach-check uncertain \((.+)\)$`), policy.AttachUnknown, false},
	{regexp.MustCompile(`^SKIP server has no pane processes \(transient\)$`), policy.NoPanes, false},
	{regexp.MustCompile(`^SKIP running in a pane: (.+)$`), policy.Busy, false},
	{regexp.MustCompile(`^SKIP IDLE for (.+), reaped after .+$`), policy.IdleRecent, false},
	{regexp.MustCompile(`^REAP IDLE, (.+) since last activity$`), policy.IdleOld, true},
}

// VerdictFromLog reads a verdict back from the words the log wrote for it, so
// the dashboard can show a logged decision in the current language. ok is
// false for words it does not know, such as the shell version's.
func VerdictFromLog(text string) (v policy.Verdict, ok bool) {
	for _, lv := range logVerdicts {
		g := lv.re.FindStringSubmatch(text)
		if g == nil {
			continue
		}
		v.Code = lv.code
		if lv.reap {
			v.Action = policy.Reap
		}
		if len(g) > 1 {
			v.Detail = g[1]
		}
		return v, true
	}
	return v, false
}
