package cli

import (
	"os"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
)

// TestMain runs the commands without the wait for a copy zellij may leave
// after a rename: a fake that leaves one leaves it before the rename returns.
func TestMain(m *testing.M) {
	reaper.LeftoverSettles = 0
	os.Exit(m.Run())
}
