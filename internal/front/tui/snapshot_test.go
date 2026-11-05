package tui

import (
	"os"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
)

// TestWriteSnapshots writes sample frames for looking at the design, when
// ZR_SNAPSHOT_DIR is set. It checks nothing on its own.
func TestWriteSnapshots(t *testing.T) {
	dir := os.Getenv("ZR_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set ZR_SNAPSHOT_DIR to write sample frames")
	}
	for _, lang := range []i18n.Lang{i18n.English, i18n.Korean} {
		i18n.Set(lang)
		for _, dark := range []bool{true, false} {
			theme := "light"
			if dark {
				theme = "dark"
			}
			for _, sz := range [][2]int{{140, 34}, {100, 30}} {
				name := dir + "/" + string(lang) + "-" + theme + "-" + itoa(sz[0]) + ".ansi"
				os.WriteFile(name, []byte(snapshot(sample(), sz[0], sz[1], dark)), 0o644)
			}
		}
	}
	i18n.Set(i18n.English)
}

func itoa(n int) string {
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
