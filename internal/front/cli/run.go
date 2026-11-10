package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/logfile"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

func cmdRun() *cobra.Command {
	var opts reaper.Options
	var quiet bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run one pass now: reap what is due, log every decision",
		Long: `Run one pass, as the timer does: every session is judged, the ones that pass every
check are deleted, the rest are named after what they show if auto_rename is on
and nobody chose their name, and each decision is written to the log. --dry-run
logs without changing anything; --force skips the waits (idle_after, exited_after)
but keeps every other check. The status is 1 when zellij refused a delete, or on
an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			r, err := reaper.Run(cmd.Context(), a.Reaper, opts)
			w := cmd.OutOrStdout()
			switch {
			// Another pass holds the lock: that pass does the work.
			case errors.Is(err, lock.ErrBusy):
				if !quiet {
					fmt.Fprintln(w, i18n.T("run.busy"))
				}
				return nil
			// No zellij: nothing can need reaping. The timer must not fail
			// for it, and doctor reports it.
			case errors.Is(err, reaper.ErrNoZellij):
				if !quiet {
					fmt.Fprintln(os.Stderr, i18n.T("error.zellij"))
					fmt.Fprintln(os.Stderr, i18n.T("error.zellij.fix"))
				}
				return nil
			case err != nil:
				return err
			}
			return showPass(cmd.Context(), w, a, r, opts.NamesOnly, quiet)
		},
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "log what would happen, change nothing")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "skip the waits; every other check still applies")
	cmd.Flags().BoolVar(&opts.NamesOnly, "names-only", false, "only rename, delete nothing (the names timer's mode)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only errors and failed deletes (the timer's mode); the log gets all")
	return cmd
}

// showPass prints what a pass did, unless quiet, and ends with the error a
// failed delete makes.
func showPass(ctx context.Context, w io.Writer, a *app.App, r reaper.Report, namesOnly, quiet bool) error {
	// The timer's pass says nothing more, but its status says whether a
	// delete failed: systemd, launchd and the Task Scheduler show it.
	if quiet {
		return failedDeletes(r)
	}
	t := theme()
	// A names pass says only what it renamed.
	if namesOnly {
		n := 0
		for _, o := range r.Outcomes {
			switch {
			case o.RenamedTo != "":
				println(w, "  "+o.Session.Name()+" → "+o.RenamedTo)
				n++
			case o.WouldRename != "":
				println(w, "  "+o.Session.Name()+" → "+o.WouldRename+ui.Fg(t.Muted).Render(" "+i18n.T("run.dry.mark")))
				n++
			case o.RenameErr != nil:
				println(w, "  "+ui.Fg(t.Cat[ui.CatUnknown]).Render(o.Session.Name()+": "+i18n.T("run.rename.failed", o.RenameErr)))
			}
		}
		switch {
		case n == 0 && r.NoRenames != nil:
			println(w, ui.Fg(t.Muted).Render("  "+i18n.T("run.names.none")+" "+r.NoRenames.Error()))
		case n == 0:
			println(w, ui.Fg(t.Muted).Render("  "+i18n.T("run.names.none")))
		}
		return nil
	}
	if r.NoSessions {
		println(w, ui.Fg(t.Muted).Render(i18n.T("plan.none")))
		return nil
	}
	// The settings this pass ran with, then what it did to each session.
	width := terminalWidthFor(w)
	for _, line := range headline(ctx, a, t, width) {
		println(w, line)
	}
	println(w)
	for _, line := range t.TableWith(ui.Sorted(ui.RowsAfterPass(r, time.Now())), width, -1, i18n.T("col.result")) {
		println(w, trimRight(line))
	}
	// Then the details a table cell cannot hold.
	for _, o := range r.Outcomes {
		switch {
		case o.DeleteErr != nil:
			println(w, "  "+ui.Fg(t.Cat[ui.CatUnknown]).Render(o.Session.Name()+": "+i18n.T("run.failed", o.DeleteErr)))
		case o.RenamedTo != "":
			println(w, "  "+ui.Fg(t.Muted).Render(o.Session.Name()+": "+i18n.T("run.renamed", o.RenamedTo)))
		case o.RenameErr != nil:
			println(w, "  "+ui.Fg(t.Cat[ui.CatUnknown]).Render(o.Session.Name()+": "+i18n.T("run.rename.failed", o.RenameErr)))
		case o.KeptName != "":
			println(w, "  "+ui.Fg(t.Muted).Render(o.Session.Name()+": "+i18n.T("run.keptname", o.KeptName)))
		}
	}
	// The totals, as `plan` ends with them.
	reaped, kept, _ := r.Counts()
	summary := i18n.TN("run.summary", len(r.Outcomes), len(r.Outcomes), reaped, kept)
	if r.DryRun {
		summary = i18n.TN("plan.summary.dry", len(r.Outcomes), len(r.Outcomes), r.Due())
	}
	println(w)
	println(w, ui.Fg(t.Muted).Render("  "+summary))
	return failedDeletes(r)
}

// failedDeletes is the error a pass ends with when zellij refused a delete:
// the exit status says so to a script, and to the timer. A kept session, or a
// rename zellij refused, is no failure: a pass keeps sessions as it should,
// and a refused tail is tried again in an hour.
func failedDeletes(r reaper.Report) error {
	n := 0
	for _, o := range r.Outcomes {
		if o.DeleteErr != nil {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return errors.New(i18n.TN("run.failed.some", n, n))
}

// terminalWidthFor is the width to render for w: the terminal's, or 100.
func terminalWidthFor(w any) int {
	if f, ok := w.(*os.File); ok && isTerminal(f) {
		return terminalWidth()
	}
	return 100
}

func cmdLogs() *cobra.Command {
	var n int
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the decision log",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// A count below zero means nothing sensible; say so before
			// reading anything.
			if n < 0 {
				return errors.New(i18n.T("logs.negative", n))
			}
			// Only the settings, for where the log is. A protected list that
			// cannot be read stops every pass, and the log is where one
			// looks for why.
			cfg, err := config.Load(configPath, config.OSEnv{})
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			lines, err := logfile.Tail(cfg.LogPath, n)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			t := theme()
			// -n 0 asks for no lines, which is not the same as none written.
			if n == 0 && !follow {
				return nil
			}
			if len(lines) == 0 && !follow {
				println(w, ui.Fg(t.Muted).Render(i18n.T("log.empty")))
				return nil
			}
			for _, l := range lines {
				println(w, colorLine(t, l))
			}
			if !follow {
				return nil
			}
			// Follow until interrupted; rotation is followed to the new file.
			return logfile.Follow(cfg.LogPath, lineWriter{w: w, t: t}, cmd.Context().Done())
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 50, "how many lines to show")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	return cmd
}

// colorLine colors one log line by what it records.
func colorLine(t ui.Theme, raw string) string {
	l := logfile.Parse(raw)
	stamp := ""
	if !l.Time.IsZero() {
		stamp = ui.Fg(t.Muted).Render(l.Time.Format("01-02 15:04:05")) + "  "
	}
	text := l.Text
	switch l.Kind() {
	case "start", "end":
		text = ui.Fg(t.Muted).Render(text)
	case "reap", "deleted":
		text = ui.Fg(t.Cat[ui.CatReap]).Render(text)
	case "failed":
		text = ui.Fg(t.Cat[ui.CatUnknown]).Render(text)
	case "renamed", "dry":
		text = ui.Fg(t.Cat[ui.CatBusy]).Render(text)
	default:
		text = ui.Fg(t.Ink).Render(text)
	}
	return stamp + text
}

// lineWriter colors followed log output line by line. Follow copies whole
// lines, because the log writes one line per write.
type lineWriter struct {
	w io.Writer
	t ui.Theme
}

func (lw lineWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		println(lw.w, colorLine(lw.t, line))
	}
	return len(p), nil
}
