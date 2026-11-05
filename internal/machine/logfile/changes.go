package logfile

import (
	"bufio"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/core/rename"
)

// passRenamed and handRenamed are the two lines a rename leaves in the log: a
// pass's, and one asked for in the dashboard. A pass's line starts right after the
// time, where no pane title can reach.
var (
	passRenamed = regexp.MustCompile(`^\[[^\]]*\]   -> renamed: (.+) → (.+)$`)
	handRenamed = regexp.MustCompile(`^\[[^\]]*\] (.+) :: renamed from the dashboard to (.+)$`)
)

// Changes returns the renames across Path.1 and Path, oldest first. A missing file
// holds none.
func Changes(path string) ([]rename.Change, error) {
	var out []rename.Change
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		sc := bufio.NewScanner(f)
		// Session names and pane titles can make a line long; allow 1 MiB.
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			// Nearly every line is neither, and this test is cheap.
			if !strings.Contains(line, "renamed") {
				continue
			}
			if m := passRenamed.FindStringSubmatch(line); m != nil {
				out = append(out, rename.Change{From: m[1], To: m[2]})
			} else if m := handRenamed.FindStringSubmatch(line); m != nil {
				out = append(out, rename.Change{From: m[1], To: m[2], ByHand: true})
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}
