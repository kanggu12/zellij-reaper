package timers

import (
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

func (a auto) Kind() string                               { return a.chosen().Kind() }
func (a auto) Available() bool                            { return a.chosen().Available() }
func (a auto) Status() TimerStatus                        { return a.chosen().Status() }
func (a auto) NamesStatus() TimerStatus                   { return a.chosen().NamesStatus() }
func (a auto) Persists() (on, known bool)                 { return a.chosen().Persists() }
func (a auto) Uninstall(p bool, l string) (Result, error) { return a.chosen().Uninstall(p, l) }

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
