package timers

import (
	"fmt"
	"sync"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// auto is Linux's scheduler: systemd's user timers where a user instance
// answers, otherwise cron where a daemon runs it, so an install adapts to a
// machine without systemd instead of failing there. The choice is made on
// first use, since asking costs a process and a pass never asks.
type auto struct {
	sys  Systemd
	cron Cron
	once *sync.Once
	pick *Scheduler
}

// newAuto returns the scheduler for a Linux user.
func newAuto(env config.Env, user string) auto {
	var s Scheduler
	return auto{sys: Systemd{Env: env, Run: Exec, User: user}, cron: NewCron(env), once: &sync.Once{}, pick: &s}
}

// chosen picks systemd, or cron when systemd has no user instance and cron
// either runs or already holds the jobs.
func (a auto) chosen() Scheduler {
	a.once.Do(func() {
		*a.pick = a.sys
		if !a.sys.Available() && (a.cron.Available() || a.cron.HasJobs()) {
			*a.pick = a.cron
		}
	})
	return *a.pick
}

func (a auto) Kind() string               { return a.chosen().Kind() }
func (a auto) Available() bool            { return a.chosen().Available() }
func (a auto) Status() TimerStatus        { return a.chosen().Status() }
func (a auto) NamesStatus() TimerStatus   { return a.chosen().NamesStatus() }
func (a auto) Persists() (on, known bool) { return a.chosen().Persists() }

// Uninstall removes the timers of both schedulers, not the chosen one's
// alone. A machine moves between the two (systemd turned on under WSL after
// an install put the jobs in the crontab, or off again), and the timers of
// the one not chosen today would run on with the uninstall saying they are
// gone. The chosen one goes last and does the purge.
func (a auto) Uninstall(purge bool, logPath string) (Result, error) {
	var other Scheduler = a.cron
	if a.chosen().Kind() == "cron" {
		other = a.sys
	}
	r, err := other.Uninstall(false, "")
	switch {
	case err != nil:
		// It found its timers and could not remove them, which is said; the
		// chosen one's still go.
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s's timers stay: %v", other.Kind(), err))
	case r.Timers == 0:
		// With no timers of its own, the other one's troubles are not worth
		// a line: on most machines it is not there at all.
		r.Warnings = nil
	}
	c, err := a.chosen().Uninstall(purge, logPath)
	// The chosen one's result is the one handed on, with the other's timers
	// and words put in front of its own. Copying its fields into the other's
	// one by one dropped what a purge had left in place: a field added since.
	c.Removed = append(r.Removed, c.Removed...)
	c.Warnings = append(r.Warnings, c.Warnings...)
	c.Timers += r.Timers
	return c, err
}

func (a auto) FirstPass(now time.Time, every time.Duration) time.Time {
	return a.chosen().FirstPass(now, every)
}

// Install installs with the chosen scheduler. Once systemd's timers run, jobs
// an earlier install left in the crontab go, so passes do not run twice.
func (a auto) Install(o InstallOptions) (Result, error) {
	r, err := a.chosen().Install(o)
	if err == nil && a.chosen().Kind() == "systemd" && a.cron.HasJobs() {
		if u, err := a.cron.Uninstall(false, ""); err == nil {
			r.Removed = append(r.Removed, u.Removed...)
		}
	}
	return r, err
}
