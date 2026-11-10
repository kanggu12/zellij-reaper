package reaper

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

// Stuck explains a zellij list that gave no answer. zellij list-sessions asks
// every session's server, and a server a signal stopped (state T, or t under
// a debugger) never answers: every pass then stops there and keeps every
// session, until the server goes on or ends. The stopped servers are named in
// err, with the command that lets them go on. A server stuck any other way
// cannot be told from here, and err is returned as it was.
func Stuck(err error, table *procfs.Table, uid int, idx zellij.Index) error {
	if err == nil || table == nil || !zellij.CutShort(err) {
		return err
	}
	var names, pids []string
	for _, s := range table.Servers(uid) {
		p := table.ByPID[s.PID]
		if p == nil || (p.State != "T" && p.State != "t") {
			continue
		}
		// The session by the name the index gives its socket's id, or the
		// name the server started under.
		names = append(names, fmt.Sprintf("%s (pid %d)", idx.Name(s.Session()), s.PID))
		pids = append(pids, strconv.Itoa(s.PID))
	}
	switch len(names) {
	case 0:
		return err
	case 1:
		return fmt.Errorf("%w; the server of %s is stopped: `kill -CONT %s` lets it go on", err, names[0], pids[0])
	}
	return fmt.Errorf("%w; the servers of %s are stopped: `kill -CONT %s` lets them go on", err, and(names), strings.Join(pids, " "))
}

// and joins words as a sentence does: "a", "a and b", "a, b and c".
func and(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
