// Package zellij talks to zellij: its command line for listing, deleting and
// renaming sessions and for asking which clients are attached, and the files it
// keeps for every session under the cache directory.
package zellij

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrNoSessions is returned by ListSessions when zellij has nothing to list.
// zellij reports that with exit status 1, the same status as a real failure,
// so it is told apart by the message it prints.
var ErrNoSessions = errors.New("no sessions")

// ErrNameTaken is returned by Rename when zellij refuses the new name because
// a session (or a name it still remembers) already has it.
var ErrNameTaken = errors.New("a session by this name already exists")

// ErrUnconfirmed is returned by Rename when zellij took the request but the new
// name could not be seen listed. The session may have the new name or the old
// one, so a caller must not act as if nothing happened.
var ErrUnconfirmed = errors.New("zellij took the rename but does not list the new name")

// cutShort is a command stopped before it finished, by the time limit or by the
// caller. It may have done what it was asked all the same.
type cutShort string

func (e cutShort) Error() string { return string(e) }

// errNoAnswer is list-clients exiting 0 with nothing printed: no answer, which
// must not read as "nobody attached".
var errNoAnswer = errors.New("list-clients printed nothing")

// Runner runs zellij with arguments and returns what it printed. Tests replace
// it with a fake that never starts a process.
type Runner func(ctx context.Context, args ...string) (stdout, stderr string, err error)

// CLI is a zellij binary plus the limits every call runs under.
type CLI struct {
	// Bin is the zellij executable, a path or a name looked up in PATH.
	Bin string
	// Timeout bounds every call: a server that stopped answering must not
	// hang a pass started by a timer that nobody is watching.
	Timeout time.Duration
	// Run executes Bin; nil means start the real process.
	Run Runner
}

// New returns a CLI for bin (empty means "zellij" from PATH).
func New(bin string) *CLI {
	if bin == "" {
		bin = "zellij"
	}
	return &CLI{Bin: bin, Timeout: 10 * time.Second}
}

// call runs one zellij command under the timeout.
func (c *CLI) call(parent context.Context, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(parent, c.Timeout)
	defer cancel()
	// A fake runner (tests) takes the place of the process.
	if c.Run != nil {
		return c.Run(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	// Name a timeout plainly ("signal: killed" says nothing to a reader), and
	// tell this call's limit apart from the caller giving up.
	switch {
	case parent.Err() != nil:
		return out.String(), "", cutShort(fmt.Sprintf("zellij %s: cancelled", strings.Join(args, " ")))
	case ctx.Err() == context.DeadlineExceeded:
		return out.String(), "", cutShort(fmt.Sprintf("zellij %s: no answer within %s", strings.Join(args, " "), c.Timeout))
	}
	return out.String(), errOut.String(), err
}

// Available reports whether the zellij binary can be found.
func (c *CLI) Available() bool {
	if c.Run != nil {
		return true
	}
	_, err := exec.LookPath(c.Bin)
	return err == nil
}

// versionRe pulls "0.45.1" out of "zellij 0.45.1".
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// Version returns zellij's version, like "0.45.1".
func (c *CLI) Version(ctx context.Context) (string, error) {
	out, _, err := c.call(ctx, "--version")
	if err != nil {
		return "", err
	}
	m := versionRe.FindString(out)
	if m == "" {
		return "", fmt.Errorf("unexpected version output %q", strings.TrimSpace(out))
	}
	return m, nil
}

// Listed is one line of `zellij list-sessions --no-formatting`.
type Listed struct {
	// Name is the session's name.
	Name string
	// Exited is true for a session whose server is gone but whose layout is
	// kept so it can be resurrected ("EXITED - attach to resurrect").
	Exited bool
	// Current is true for the session the command was run from.
	Current bool
	// Created is zellij's own wording, like "3h ago".
	Created string
}

// lineRe splits "name [Created 3h ago] (EXITED - attach to resurrect)". The
// name is everything before " [Created", so names with spaces survive.
var lineRe = regexp.MustCompile(`^(.*?) \[Created ([^\]]*)\](.*)$`)

// ParseSessions reads the output of `list-sessions --no-formatting`.
func ParseSessions(out string) []Listed {
	var res []Listed
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, " \r")
		// Blank lines, including ones of tabs only, carry no session.
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := lineRe.FindStringSubmatch(line)
		// A line in a shape this parser does not know is kept with its
		// first word as the name, the way the shell version read it, and it
		// is never treated as exited, so it can only be kept or judged idle
		// with every other check still applied.
		if m == nil {
			res = append(res, Listed{Name: strings.Fields(line)[0]})
			continue
		}
		rest := m[3]
		res = append(res, Listed{
			Name:    m[1],
			Created: strings.TrimSpace(m[2]),
			Exited:  strings.Contains(rest, "EXITED"),
			Current: strings.Contains(rest, "(current)"),
		})
	}
	return res
}

// ListSessions lists running and exited sessions.
func (c *CLI) ListSessions(ctx context.Context) ([]Listed, error) {
	out, errOut, err := c.call(ctx, "list-sessions", "--no-formatting")
	if err != nil {
		// zellij 0.44 and 0.45 print this and exit 1 when nothing is there.
		if strings.Contains(errOut+out, "No active zellij sessions found") {
			return nil, ErrNoSessions
		}
		return nil, fmt.Errorf("zellij list-sessions: %s", oneLine(errOut, err))
	}
	sessions := ParseSessions(out)
	// An empty list with exit status 0 means the same thing.
	if len(sessions) == 0 {
		return nil, ErrNoSessions
	}
	return sessions, nil
}

// Delete deletes a session: a running one is killed first (-f), an exited one
// loses its resurrection data.
func (c *CLI) Delete(ctx context.Context, name string) error {
	_, errOut, err := c.call(ctx, "delete-session", name, "--force")
	if err != nil {
		return fmt.Errorf("zellij delete-session %s: %s", name, oneLine(errOut, err))
	}
	return nil
}

// Rename renames a running session, and returns only once zellij lists it
// under the new name.
//
// A name that is taken is refused here, because zellij may not refuse it:
// since 0.44 `rename-session` exits 0 and moves the session's socket file over
// the other session's, which leaves that other server running with no way to
// reach it. 0.43 still says "already exists", which comes back as ErrNameTaken
// too, and so does a name zellij remembers without listing it.
func (c *CLI) Rename(ctx context.Context, name, to string) error {
	taken, err := c.lists(ctx, to)
	if err != nil {
		return fmt.Errorf("zellij rename-session %s: %w", name, err)
	}
	if taken {
		return ErrNameTaken
	}
	out, errOut, err := c.call(ctx, "--session", name, "action", "rename-session", to)
	if err != nil {
		if strings.Contains(errOut+out, "already exists") {
			return ErrNameTaken
		}
		// Stopped before it finished: the server may have taken it all the same.
		if errors.As(err, new(cutShort)) {
			return fmt.Errorf("%w: %v", ErrUnconfirmed, err)
		}
		return fmt.Errorf("zellij rename-session %s: %s", name, oneLine(errOut, err))
	}
	return c.waitListed(ctx, name, to)
}

// renameSettles is how long a server gets to act on a rename it was sent. The
// command returns before the server has acted; in the integration containers
// the new name is listed about 15 ms later.
var renameSettles = 2 * time.Second

// waitListed waits for a session to be listed under its new name, so a rename
// reported as done has been done.
func (c *CLI) waitListed(ctx context.Context, name, to string) error {
	deadline := time.Now().Add(renameSettles)
	for {
		listed, err := c.lists(ctx, to)
		if err != nil {
			return fmt.Errorf("zellij rename-session %s to %s: %w: %v", name, to, ErrUnconfirmed, err)
		}
		if listed {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("zellij rename-session %s to %s: %w", name, to, ErrUnconfirmed)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("zellij rename-session %s to %s: %w: %v", name, to, ErrUnconfirmed, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// lists reports whether zellij lists a session by this name.
func (c *CLI) lists(ctx context.Context, name string) (bool, error) {
	sessions, err := c.ListSessions(ctx)
	// Nothing listed is an answer, not a failure: the name is free.
	if errors.Is(err, ErrNoSessions) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, l := range sessions {
		if l.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// ClientRow is one attached client from `action list-clients`.
type ClientRow struct {
	// ID is zellij's client number.
	ID int
	// Pane is the pane the client is looking at, like "terminal_0".
	Pane string
	// Command is the command running in that pane, or "N/A".
	Command string
}

// ParseClients reads the table printed by `action list-clients`: a header
// line, then one line per attached client. Output in any other shape is an
// error rather than "nobody attached": a format that changes in a later zellij
// must not make attached sessions look free.
func ParseClients(out string) ([]ClientRow, error) {
	var rows []ClientRow
	header := false
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		// The header ("CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND") comes first.
		if !header {
			if f[0] != "CLIENT_ID" {
				return nil, fmt.Errorf("unexpected list-clients output %q", strings.TrimSpace(line))
			}
			header = true
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("unexpected list-clients row %q", strings.TrimSpace(line))
		}
		row := ClientRow{ID: id}
		if len(f) > 1 {
			row.Pane = f[1]
		}
		if len(f) > 2 {
			row.Command = strings.Join(f[2:], " ")
		}
		rows = append(rows, row)
	}
	if !header {
		return nil, errNoAnswer
	}
	return rows, nil
}

// ListClients asks a running session which clients are attached to it. It asks
// again in two cases where zellij's first answer is known not to hold:
//
// No answer at all: zellij 0.44.2, and rarely 0.45.1, exits 0 with nothing
// printed when another zellij command overlaps this one, even one going to
// another server.
//
// A client: zellij 0.43 names the client that created a background session
// the first time it is asked, long after that client left. A real client is
// still there the second time, so this cannot make an attached session look
// free.
func (c *CLI) ListClients(ctx context.Context, name string) ([]ClientRow, error) {
	rows, err := c.askClients(ctx, name)
	if errors.Is(err, errNoAnswer) || (err == nil && len(rows) > 0) {
		rows, err = c.askClients(ctx, name)
	}
	return rows, err
}

// askClients asks once.
func (c *CLI) askClients(ctx context.Context, name string) ([]ClientRow, error) {
	out, errOut, err := c.call(ctx, "--session", name, "action", "list-clients")
	if err != nil {
		return nil, fmt.Errorf("zellij list-clients %s: %s", name, oneLine(errOut, err))
	}
	return ParseClients(out)
}

// Attach returns the command that attaches to a session; the dashboard hands
// the terminal to it.
func (c *CLI) Attach(name string) *exec.Cmd {
	return exec.Command(c.Bin, "attach", name)
}

// SwitchTo returns the command that moves the zellij client this process runs
// in over to another session.
func (c *CLI) SwitchTo(name string) *exec.Cmd {
	return exec.Command(c.Bin, "action", "switch-session", name)
}

// oneLine keeps an error message to one line for the log.
func oneLine(stderr string, err error) string {
	msg := strings.TrimSpace(stderr)
	if msg == "" && err != nil {
		msg = err.Error()
	}
	return strings.Join(strings.Fields(msg), " ")
}
