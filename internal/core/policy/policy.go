// Package policy decides, from facts gathered about one session, whether a
// pass may reap it. It does no I/O, so every rule can be tested on its own.
//
// The rules fail closed: whenever a fact is missing or uncertain the session
// is kept. Only a session that passes every check is reaped.
package policy

import (
	"fmt"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
)

// Action is what a pass does with a session.
type Action int

const (
	// Keep leaves the session alone.
	Keep Action = iota
	// Reap deletes it.
	Reap
)

// Code names the rule that decided, so the log, the TUI and the JSON output
// can all say the same thing in their own words.
type Code string

const (
	// ProtectedName: the name matches `protect` or the protected list.
	ProtectedName Code = "protected-name"
	// NameUnknown: the session's name cannot be told, and so neither can
	// whether it is protected: zellij's session index cannot be read.
	NameUnknown Code = "name-unknown"
	// NoInfo: zellij keeps no directory for the session (yet).
	NoInfo Code = "no-info"
	// ProtectedCommand: a program from protect_commands is running in it.
	ProtectedCommand Code = "protected-command"
	// NoActivity: the time of last activity cannot be read.
	NoActivity Code = "no-activity"
	// ExitedRecent: exited, but not for long enough.
	ExitedRecent Code = "exited-recent"
	// ExitedOld: exited for longer than exited_after.
	ExitedOld Code = "exited-old"
	// ExitedLive: listed as exited, but a server still serves its socket.
	ExitedLive Code = "exited-live"
	// NoSocket: running, but its socket is missing.
	NoSocket Code = "no-socket"
	// NoServer: running, but no server process serves it.
	NoServer Code = "no-server"
	// Attached: a client is attached.
	Attached Code = "attached"
	// AttachUnknown: whether a client is attached cannot be told.
	AttachUnknown Code = "attach-unknown"
	// NoPanes: the server has no pane processes (starting or stopping).
	NoPanes Code = "no-panes"
	// Busy: a pane runs something other than an idle shell prompt.
	Busy Code = "busy"
	// IdleRecent: idle, but not for long enough.
	IdleRecent Code = "idle-recent"
	// IdleOld: idle for longer than idle_after.
	IdleOld Code = "idle-old"
)

// Tri is a yes/no answer that may also be unknown.
type Tri int

const (
	// No is a definite no.
	No Tri = iota
	// Yes is a definite yes.
	Yes
	// Unknown means the check could not be made.
	Unknown
)

// Facts is everything the rules look at for one session.
type Facts struct {
	// Name is the session's name; Exited is true for a resurrectable one.
	Name   string
	Exited bool
	// Protected is set when the name is protected; ProtectedBy says by what
	// ("protect pattern" or "protected list").
	Protected   bool
	ProtectedBy string
	// NameUnknown is set when zellij's session index, which holds the names
	// of the sessions it keeps by id, cannot be read: zellij then lists such
	// a session by its id, which no protection names.
	NameUnknown bool
	// HasInfo is true when zellij keeps a directory for the session.
	HasInfo bool
	// ProtectedCommand names the protected program found running (or
	// recorded in the layout) in a running session, or is empty.
	ProtectedCommand string
	// LastActive is the last time anything happened in the session; zero if
	// it could not be read. ActivitySource says where it came from.
	LastActive     time.Time
	ActivitySource string
	// SocketOK is true when the session's socket exists; ServerPID is its
	// server process (0 if none was found).
	SocketOK  bool
	ServerPID int
	// Attached says whether a client is attached; AttachDetail is the
	// evidence ("2 clients", "zellij reports 1 connected client") and
	// Clients the number attached when it is known (-1 when not).
	Attached     Tri
	AttachDetail string
	Clients      int
	// PaneCount is the number of pane processes; BusyWith is the command
	// running in the first busy pane, or empty when every pane is at a prompt.
	PaneCount int
	BusyWith  string
}

// Thresholds are the two waits before reaping. Zero means no wait.
type Thresholds struct {
	Idle   time.Duration
	Exited time.Duration
}

// Check is one rule as the TUI lists it: what was checked, whether it let the
// session go on towards being reaped, and the evidence.
type Check struct {
	// Name is a short label like "client attached".
	Name string
	// Pass is true when this rule did not keep the session.
	Pass bool
	// Detail is the evidence as text (a command, a pid), possibly empty.
	Detail string
	// Age, Limit and Count are evidence as values, for the screen to word in
	// its own language: how long ago, against what wait, how many.
	Age   time.Duration
	Limit time.Duration
	Count int
}

// Verdict is the decision for one session.
type Verdict struct {
	Action Action
	Code   Code
	// Detail is the variable part of the reason ("vim notes.md", "2h 14m").
	Detail string
	// Age is how long ago the session was last active (0 if unknown).
	Age time.Duration
	// Wait is how long the session must stay as it is before it is reaped
	// (0 when it is reaped now or kept for another reason).
	Wait time.Duration
	// Limit is the threshold that applied (idle_after or exited_after) when
	// the session was kept only because it had not waited long enough.
	Limit time.Duration
	// Checks lists the rules in the order they ran, ending with the one that
	// decided.
	Checks []Check
}

// Decide applies the rules in order. The order matters: a protected name is
// reported as such even if the session is also busy, which is the reason that
// would stay true longest.
func Decide(f Facts, th Thresholds, now time.Time) Verdict {
	var checks []Check
	// pass records a rule that did not stop the session.
	pass := func(name, detail string) { checks = append(checks, Check{Name: name, Pass: true, Detail: detail}) }
	// keep records the rule that stopped it and returns the verdict.
	keep := func(name string, code Code, detail string) Verdict {
		checks = append(checks, Check{Name: name, Pass: false, Detail: detail})
		return Verdict{Action: Keep, Code: code, Detail: detail, Checks: checks}
	}

	if f.Protected {
		return keep("name not protected", ProtectedName, f.ProtectedBy)
	}
	// A name that cannot be read cannot be checked against the protected
	// ones: the session may be one of them.
	if f.NameUnknown {
		return keep("name not protected", NameUnknown, "")
	}
	pass("name not protected", "")

	if !f.HasInfo {
		return keep("session data", NoInfo, "")
	}
	pass("session data", "")

	// zellij lists a session as exited from its files; a server that still
	// answers on its socket means the listing is out of date. Keep it.
	if f.Exited {
		if f.ServerPID != 0 {
			return keep("no live server", ExitedLive, fmt.Sprintf("pid %d", f.ServerPID))
		}
		pass("no live server", "")
	}

	// A protected program only protects a running session; once the session
	// has exited, nothing is running and the normal wait decides.
	if !f.Exited {
		if f.ProtectedCommand != "" {
			return keep("no protected program", ProtectedCommand, f.ProtectedCommand)
		}
		pass("no protected program", "")
	}

	if f.LastActive.IsZero() {
		return keep("last activity", NoActivity, "")
	}
	age := now.Sub(f.LastActive)
	// A clock that moved backwards, or a file touched "in the future", must
	// not make a session look older than it is.
	if age < 0 {
		age = 0
	}
	checks = append(checks, Check{Name: "last activity", Pass: true, Detail: duration.Format(age) + " ago", Age: age})

	if f.Exited {
		if th.Exited > 0 && age < th.Exited {
			v := keep("exited long enough", ExitedRecent, fmt.Sprintf("%s of %s", duration.Format(age), duration.Format(th.Exited)))
			v.Detail, v.Age, v.Wait, v.Limit = duration.Format(age), age, th.Exited-age, th.Exited
			v.Checks[len(v.Checks)-1].Age, v.Checks[len(v.Checks)-1].Limit = age, th.Exited
			return v
		}
		checks = append(checks, Check{Name: "exited long enough", Pass: true, Detail: duration.Format(age), Age: age})
		return Verdict{Action: Reap, Code: ExitedOld, Detail: duration.Format(age), Age: age, Checks: checks}
	}

	if !f.SocketOK {
		return keep("socket", NoSocket, "")
	}
	pass("socket", "")
	if f.ServerPID == 0 {
		return keep("server", NoServer, "")
	}
	pass("server", fmt.Sprintf("pid %d", f.ServerPID))

	switch f.Attached {
	case Yes:
		v := keep("no client attached", Attached, f.AttachDetail)
		v.Checks[len(v.Checks)-1].Count = max(1, f.Clients)
		return v
	case Unknown:
		return keep("no client attached", AttachUnknown, f.AttachDetail)
	}
	pass("no client attached", "")

	if f.PaneCount == 0 {
		return keep("panes", NoPanes, "")
	}
	if f.BusyWith != "" {
		return keep("every pane at a prompt", Busy, f.BusyWith)
	}
	checks = append(checks, Check{Name: "every pane at a prompt", Pass: true, Detail: fmt.Sprintf("%d pane(s)", f.PaneCount), Count: f.PaneCount})

	if th.Idle > 0 && age < th.Idle {
		v := keep("idle long enough", IdleRecent, fmt.Sprintf("%s of %s", duration.Format(age), duration.Format(th.Idle)))
		v.Detail, v.Age, v.Wait, v.Limit = duration.Format(age), age, th.Idle-age, th.Idle
		v.Checks[len(v.Checks)-1].Age, v.Checks[len(v.Checks)-1].Limit = age, th.Idle
		return v
	}
	checks = append(checks, Check{Name: "idle long enough", Pass: true, Detail: duration.Format(age), Age: age})
	return Verdict{Action: Reap, Code: IdleOld, Detail: duration.Format(age), Age: age, Checks: checks}
}

// LogText is the reason as written to the log: "REAP ..." or "SKIP ...".
// The words stay stable across releases so people can grep old logs.
func (v Verdict) LogText() string {
	switch v.Code {
	case ProtectedName:
		return "SKIP protected by " + v.Detail
	case NameUnknown:
		return "SKIP name unknown (zellij's session index cannot be read)"
	case NoInfo:
		return "SKIP no session_info dir (unknown state)"
	case ProtectedCommand:
		return "SKIP protected command running (" + v.Detail + ")"
	case NoActivity:
		return "SKIP cannot read activity time"
	case ExitedRecent:
		return "SKIP EXITED " + v.Detail + " ago, reaped after " + duration.Format(v.Limit)
	case ExitedOld:
		return "REAP EXITED, " + v.Detail + " since last activity"
	case ExitedLive:
		return "SKIP EXITED, but a server still serves it (" + v.Detail + ")"
	case NoSocket:
		return "SKIP socket file missing for running session"
	case NoServer:
		return "SKIP server PID not found"
	case Attached:
		return "SKIP client attached (" + v.Detail + ")"
	case AttachUnknown:
		return "SKIP attach-check uncertain (" + v.Detail + ")"
	case NoPanes:
		return "SKIP server has no pane processes (transient)"
	case Busy:
		return "SKIP running in a pane: " + v.Detail
	case IdleRecent:
		return "SKIP IDLE for " + v.Detail + ", reaped after " + duration.Format(v.Limit)
	case IdleOld:
		return "REAP IDLE, " + v.Detail + " since last activity"
	}
	return "SKIP " + string(v.Code)
}

// MayRename reports whether a kept, running session's verdict lets auto_rename
// rename it: never one whose state is uncertain, never a protected one. A
// session kept only for its client says no here; auto_rename_attached decides
// about those.
func (v Verdict) MayRename() bool {
	if v.Action != Keep {
		return false
	}
	switch v.Code {
	case Attached, AttachUnknown, ProtectedName, NameUnknown, NoInfo, NoSocket, NoServer:
		return false
	}
	return true
}
