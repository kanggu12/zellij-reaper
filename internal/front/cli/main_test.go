package cli

import (
	"os"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
)

// TestMain runs the commands without the wait for a copy zellij may leave
// after a rename: the fakes never leave one, and the dashboard's and the
// passes' tests check what happens when zellij does.
func TestMain(m *testing.M) {
	reaper.LeftoverSettles = 0
	os.Exit(m.Run())
}
