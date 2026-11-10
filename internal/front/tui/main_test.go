package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// TestMain runs the dashboard's tests under Linux's zellij rules on every
// system: they check what the dashboard does with sessions, renames by hand
// included, which zellij refuses outright on Windows. TestRSaysWhyOnWindows
// checks that refusal.
func TestMain(m *testing.M) {
	zellij.GOOS = "linux"
	zellij.RenamesReachable = true
	// No wait for a copy zellij may leave after a rename: the fake leaves
	// it, when a test asks, before the rename returns.
	reaper.LeftoverSettles = 0
	// A pass that holds the lock is a test's own, which holds it on purpose.
	handLockWait = 500 * time.Millisecond
	os.Exit(m.Run())
}

func TestRSaysWhyOnWindows(t *testing.T) {
	i18n.Set(i18n.English)
	defer func(g bool) { zellij.RenamesReachable = g }(zellij.RenamesReachable)
	zellij.RenamesReachable = false
	b := sample()
	m := press(t, pick(t, loaded(t, b, 120, 40), "notes"), "r")
	if m.mode == modeRename {
		t.Error("r opened the rename dialog on Windows")
	}
	if !strings.Contains(m.statusLine(), "cannot reach a session after a rename") {
		t.Errorf("status %q", m.statusLine())
	}
}
