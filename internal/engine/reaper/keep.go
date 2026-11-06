package reaper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// ErrNoSession says no session has the name asked about.
var ErrNoSession = errors.New("no session has that name")

// KeepName puts a session's name on the list of kept names, or takes it off
// (on false), and logs it as done from by ("the dashboard", "the command
// line"). It runs under the pass lock, waiting up to wait: a pass that read the
// list a moment before would rename the session anyway. A name to keep must be
// a session's now; one to take off need not be.
func KeepName(ctx context.Context, e *Env, name string, on bool, by string, wait time.Duration) error {
	l, err := lock.Acquire(e.LockPath, wait)
	if err != nil {
		return err
	}
	defer l.Release()
	if on {
		listed, err := e.Zellij.ListSessions(ctx)
		if err != nil && !errors.Is(err, zellij.ErrNoSessions) {
			return err
		}
		if !slices.ContainsFunc(listed, func(l zellij.Listed) bool { return l.Name == name }) {
			return fmt.Errorf("%w: %s", ErrNoSession, name)
		}
	}
	// On the kept list, a name stays whatever else says it may go.
	if e.Kept != nil {
		if err := e.Kept.Set(name, on); err != nil {
			return err
		}
	}
	if !on {
		e.Log.Printf("%s :: name no longer kept, from %s", name, by)
		return nil
	}
	// A pass no longer gave this name: someone chose to keep it.
	if e.Given != nil {
		_ = e.Given.Set(name, false)
	}
	e.Log.Printf("%s :: name kept from %s", name, by)
	return nil
}
