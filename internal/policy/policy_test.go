package policy

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// idle is a running session that passes every rule and was last used 2h ago.
func idle() Facts {
	return Facts{
		Name: "work", HasInfo: true, LastActive: now.Add(-2 * time.Hour), ActivitySource: "pane terminals",
		SocketOK: true, ServerPID: 42, Attached: No, AttachDetail: "no client attached", PaneCount: 2,
	}
}

// exited is an exited session last seen 4 days ago.
func exited() Facts {
	return Facts{Name: "old", Exited: true, HasInfo: true, LastActive: now.Add(-96 * time.Hour)}
}

var th = Thresholds{Idle: time.Hour, Exited: 72 * time.Hour}

func TestDecideTable(t *testing.T) {
	cases := []struct {
		name   string
		facts  func() Facts
		th     Thresholds
		action Action
		code   Code
	}{
		{"idle long enough", idle, th, Reap, IdleOld},
		{"protected name", func() Facts { f := idle(); f.Protected, f.ProtectedBy = true, "protect pattern"; return f }, th, Keep, ProtectedName},
		{"no session data", func() Facts { f := idle(); f.HasInfo = false; return f }, th, Keep, NoInfo},
		{"protected command", func() Facts { f := idle(); f.ProtectedCommand = "claude"; return f }, th, Keep, ProtectedCommand},
		{"unknown activity", func() Facts { f := idle(); f.LastActive = time.Time{}; return f }, th, Keep, NoActivity},
		{"no socket", func() Facts { f := idle(); f.SocketOK = false; return f }, th, Keep, NoSocket},
		{"no server", func() Facts { f := idle(); f.ServerPID = 0; return f }, th, Keep, NoServer},
		{"attached", func() Facts { f := idle(); f.Attached = Yes; return f }, th, Keep, Attached},
		{"attach unknown", func() Facts { f := idle(); f.Attached = Unknown; return f }, th, Keep, AttachUnknown},
		{"no panes", func() Facts { f := idle(); f.PaneCount = 0; return f }, th, Keep, NoPanes},
		{"busy", func() Facts { f := idle(); f.BusyWith = "htop"; return f }, th, Keep, Busy},
		{"idle too short", func() Facts { f := idle(); f.LastActive = now.Add(-10 * time.Minute); return f }, th, Keep, IdleRecent},
		{"exited long enough", exited, th, Reap, ExitedOld},
		{"exited too short", func() Facts { f := exited(); f.LastActive = now.Add(-5 * time.Hour); return f }, th, Keep, ExitedRecent},
		// An exited session has nothing running: a protected command in its
		// layout does not keep it, the wait does.
		{"exited ignores protected command", func() Facts { f := exited(); f.ProtectedCommand = "claude"; return f }, th, Reap, ExitedOld},
		{"exited, protected name", func() Facts { f := exited(); f.Protected = true; return f }, th, Keep, ProtectedName},
		{"exited, but a server serves it", func() Facts { f := exited(); f.ServerPID = 99; return f }, th, Keep, ExitedLive},
		{"exited, served, forced", func() Facts { f := exited(); f.ServerPID = 99; return f }, Thresholds{}, Keep, ExitedLive},
		// Force (zero thresholds) skips the waits only.
		{"force: idle 10m", func() Facts { f := idle(); f.LastActive = now.Add(-10 * time.Minute); return f }, Thresholds{}, Reap, IdleOld},
		{"force: still attached", func() Facts { f := idle(); f.Attached = Yes; return f }, Thresholds{}, Keep, Attached},
		{"force: still busy", func() Facts { f := idle(); f.BusyWith = "vim"; return f }, Thresholds{}, Keep, Busy},
		// Activity in the future (clock skew) counts as just now.
		{"future activity", func() Facts { f := idle(); f.LastActive = now.Add(time.Hour); return f }, th, Keep, IdleRecent},
		{"future activity, forced", func() Facts { f := idle(); f.LastActive = now.Add(time.Hour); return f }, Thresholds{}, Reap, IdleOld},
	}
	for _, c := range cases {
		v := Decide(c.facts(), c.th, now)
		if v.Action != c.action || v.Code != c.code {
			t.Errorf("%s: got %v %s, want %v %s", c.name, v.Action, v.Code, c.action, c.code)
		}
		// The last check is always the one that decided.
		if len(v.Checks) == 0 {
			t.Errorf("%s: no checks", c.name)
			continue
		}
		last := v.Checks[len(v.Checks)-1]
		if (v.Action == Keep) == last.Pass {
			t.Errorf("%s: last check %+v does not match %v", c.name, last, v.Action)
		}
	}
}

func TestThresholdBoundary(t *testing.T) {
	// Exactly at the threshold is reaped; a second before is kept.
	f := idle()
	f.LastActive = now.Add(-time.Hour)
	if v := Decide(f, th, now); v.Action != Reap {
		t.Errorf("at the threshold: %v %s", v.Action, v.Code)
	}
	f.LastActive = now.Add(-time.Hour + time.Second)
	v := Decide(f, th, now)
	if v.Action != Keep || v.Wait != time.Second || v.Limit != time.Hour {
		t.Errorf("a second before: %+v", v)
	}
}

func TestLogText(t *testing.T) {
	f := idle()
	f.LastActive = now.Add(-10 * time.Minute)
	if got := Decide(f, th, now).LogText(); got != "SKIP IDLE for 10m, reaped after 1h" {
		t.Errorf("idle recent: %q", got)
	}
	if got := Decide(idle(), th, now).LogText(); got != "REAP IDLE, 2h since last activity" {
		t.Errorf("idle old: %q", got)
	}
	g := exited()
	g.LastActive = now.Add(-5 * time.Hour)
	if got := Decide(g, th, now).LogText(); got != "SKIP EXITED 5h ago, reaped after 3d" {
		t.Errorf("exited recent: %q", got)
	}
	b := idle()
	b.BusyWith = "sleep 99999"
	if got := Decide(b, th, now).LogText(); got != "SKIP running in a pane: sleep 99999" {
		t.Errorf("busy: %q", got)
	}
	// Every code has its own text, none falls through to the generic one.
	for _, c := range []Code{ProtectedName, NoInfo, ProtectedCommand, NoActivity, ExitedRecent, ExitedOld, ExitedLive, NoSocket, NoServer, Attached, AttachUnknown, NoPanes, Busy, IdleRecent, IdleOld} {
		if text := (Verdict{Code: c}).LogText(); text == "SKIP "+string(c) || !(strings.HasPrefix(text, "SKIP ") || strings.HasPrefix(text, "REAP ")) {
			t.Errorf("%s: %q", c, text)
		}
	}
}

func TestMayRename(t *testing.T) {
	for code, want := range map[Code]bool{Busy: true, IdleRecent: true, ProtectedCommand: true, Attached: false, AttachUnknown: false, ProtectedName: false, NoServer: false} {
		if got := (Verdict{Action: Keep, Code: code}).MayRename(); got != want {
			t.Errorf("%s: MayRename = %v", code, got)
		}
	}
	if (Verdict{Action: Reap, Code: IdleOld}).MayRename() {
		t.Error("a reaped session must not be renamed")
	}
}
