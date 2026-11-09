package cli

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/core/duration"
	"github.com/kanggu12/zellij-reaper/internal/core/policy"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
)

func cmdPlan() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "plan",
		Aliases: []string{"status", "ls"},
		Short:   "Show what the next pass would do, changing nothing",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlan(cmd.Context(), cmd.OutOrStdout(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON for scripts")
	return cmd
}

// runPlan prints the plan as a table, or as JSON.
func runPlan(ctx context.Context, w io.Writer, asJSON bool) error {
	a, err := load()
	if err != nil {
		return err
	}
	r, err := reaper.Plan(ctx, a.Reaper, reaper.Options{})
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(w, r)
	}
	t := theme()
	println(w, headline(ctx, a, t))
	println(w)
	if r.NoSessions {
		println(w, ui.Fg(t.Muted).Render("  "+i18n.T("plan.none")))
		return nil
	}
	width := 100
	if isTerminal(stdoutFile(w)) {
		width = terminalWidth()
	}
	for _, line := range t.Table(ui.Sorted(ui.RowsFrom(r, time.Now())), width, -1) {
		println(w, trimRight(line))
	}
	// "At the next pass": counted at the time it runs, when the timer says.
	reaped := r.DueAtNext(a.System.Status().Next, time.Now())
	key := "plan.summary"
	if r.DryRun {
		key = "plan.summary.dry"
	}
	println(w)
	println(w, ui.Fg(t.Muted).Render("  "+i18n.TN(key, len(r.Outcomes), len(r.Outcomes), reaped)+" · "+i18n.T("plan.dashboard")))
	return nil
}

// trimRight drops the blanks a table pads its lines with, also those inside
// the last color, so printed lines do not end in spaces.
func trimRight(line string) string {
	return ansi.Truncate(line, lipgloss.Width(strings.TrimRight(ansi.Strip(line), " ")), "")
}

// planJSON is the stable JSON shape of `plan --json`.
type planJSON struct {
	Generated   time.Time     `json:"generated"`
	DryRun      bool          `json:"dry_run"`
	IdleAfter   string        `json:"idle_after"`
	ExitedAfter string        `json:"exited_after"`
	Sessions    []sessionJSON `json:"sessions"`
}

type sessionJSON struct {
	Name        string     `json:"name"`
	State       string     `json:"state"`
	LastActive  *time.Time `json:"last_active,omitempty"`
	IdleSeconds int64      `json:"idle_seconds"`
	Clients     int        `json:"clients"`
	Action      string     `json:"action"`
	Code        string     `json:"code"`
	Reason      string     `json:"reason"`
	WaitSeconds int64      `json:"wait_seconds,omitempty"`
	ServerPID   int        `json:"server_pid,omitempty"`
	Panes       []paneJSON `json:"panes,omitempty"`
	WouldRename string     `json:"would_rename,omitempty"`
}

type paneJSON struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Prompt  bool   `json:"prompt"`
	Busy    string `json:"busy,omitempty"`
}

// writeJSON prints the plan as JSON. Reasons are the log's words, which do
// not change with the language.
func writeJSON(w io.Writer, r reaper.Report) error {
	out := planJSON{Generated: r.Started, DryRun: r.DryRun, IdleAfter: duration.Setting(r.Thresholds.Idle), ExitedAfter: duration.Setting(r.Thresholds.Exited), Sessions: []sessionJSON{}}
	for _, o := range r.Outcomes {
		s := sessionJSON{
			Name: o.Session.Name(), State: "running", Clients: o.Session.Clients,
			Action: "keep", Code: string(o.Verdict.Code), Reason: o.Verdict.LogText(),
			WaitSeconds: int64(o.Verdict.Wait / time.Second), ServerPID: o.Session.Facts.ServerPID,
			WouldRename: o.WouldRename,
		}
		if o.Session.Listed.Exited {
			s.State = "exited"
		}
		if o.Verdict.Action == policy.Reap {
			s.Action = "reap"
		}
		if t := o.Session.Facts.LastActive; !t.IsZero() {
			s.LastActive = &t
			s.IdleSeconds = int64(r.Started.Sub(t) / time.Second)
		}
		for _, p := range o.Session.Panes {
			s.Panes = append(s.Panes, paneJSON{PID: p.PID, Command: p.Command, Prompt: p.Prompt, Busy: p.Busy})
		}
		out.Sessions = append(out.Sessions, s)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
