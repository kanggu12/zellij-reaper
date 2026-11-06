package zellij

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// versions are the zellij releases the fixtures were recorded from.
var versions = []string{"0.44.2", "0.45.1"}

func fixture(t *testing.T, version, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", version, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSessionsFixtures(t *testing.T) {
	for _, v := range versions {
		got := ParseSessions(fixture(t, v, "list-sessions.txt"))
		names := map[string]bool{}
		for _, s := range got {
			names[s.Name] = true
			if s.Exited || s.Created != "7s ago" && s.Created != "18s ago" {
				t.Errorf("%s: %+v", v, s)
			}
		}
		for _, want := range []string{"attached", "busy", "agent", "idle"} {
			if !names[want] {
				t.Errorf("%s: %q missing from %v", v, want, got)
			}
		}
		exited := ParseSessions(fixture(t, v, "list-sessions-exited.txt"))
		if len(exited) != 2 || exited[0].Name != "typed" || exited[0].Exited || exited[1].Name != "doomed" || !exited[1].Exited {
			t.Errorf("%s exited fixture: %+v", v, exited)
		}
	}
}

func TestParseSessionsShapes(t *testing.T) {
	got := ParseSessions("my session [Created 2h ago] (current)\nold [Created 3d ago] (EXITED - attach to resurrect)\n\t\nweird-line\n\n")
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "my session" || !got[0].Current || got[0].Exited {
		t.Errorf("name with a space: %+v", got[0])
	}
	if !got[1].Exited || got[1].Created != "3d ago" {
		t.Errorf("exited: %+v", got[1])
	}
	// An unknown shape keeps its first word and is never treated as exited.
	if got[2].Name != "weird-line" || got[2].Exited {
		t.Errorf("unknown shape: %+v", got[2])
	}
}

func TestParseClientsFixtures(t *testing.T) {
	for _, v := range versions {
		rows, err := ParseClients(fixture(t, v, "list-clients-attached.txt"))
		if err != nil || len(rows) != 1 || rows[0].ID != 1 || rows[0].Pane != "terminal_0" || rows[0].Command != "N/A" {
			t.Errorf("%s attached: %+v %v", v, rows, err)
		}
		if rows, err := ParseClients(fixture(t, v, "list-clients-none.txt")); err != nil || len(rows) != 0 {
			t.Errorf("%s none: %+v %v", v, rows, err)
		}
	}
	// Any other shape is an error, never "nobody attached".
	for _, bad := range []string{"", "Session not found\n", "CLIENT_ID PANE\nyou terminal_0\n", "ID NAME\n"} {
		if rows, err := ParseClients(bad); err == nil {
			t.Errorf("ParseClients(%q) = %+v, want an error", bad, rows)
		}
	}
	// A row that starts with a client number counts, whatever follows: an
	// extra column can only make a session look more attached.
	if rows, err := ParseClients("CLIENT_ID PANE\n1 (you) terminal_0\n"); err != nil || len(rows) != 1 {
		t.Errorf("a row with an extra column: %+v %v", rows, err)
	}
}

func TestParseMetadataFixtures(t *testing.T) {
	for _, v := range versions {
		md := ParseMetadata(fixture(t, v, "metadata-attached.kdl"))
		if !md.HasClientCount || md.ConnectedClients != 1 {
			t.Errorf("%s: clients %+v", v, md)
		}
		if titles := md.TerminalTitles(); len(titles) != 1 || titles[0] != "Pane #1" {
			t.Errorf("%s: terminal titles %q", v, titles)
		}
		plugins := 0
		for _, p := range md.Panes {
			if p.Plugin {
				plugins++
			}
		}
		if plugins < 2 {
			t.Errorf("%s: plugin panes not recognized: %+v", v, md.Panes)
		}
		idle := ParseMetadata(fixture(t, v, "metadata-idle.kdl"))
		if !idle.HasClientCount || idle.ConnectedClients != 0 {
			t.Errorf("%s idle: %+v", v, idle)
		}
	}
}

func TestParseLayoutFixtures(t *testing.T) {
	for _, v := range versions {
		agent := ParseLayout(fixture(t, v, "layout-agent.kdl"))
		joined := strings.Join(agent.Commands, " ")
		if !strings.Contains(joined, "claude") || agent.Cwd != "/" {
			t.Errorf("%s agent: %+v", v, agent)
		}
		// 0.44 records the command as written ("claude" with "--resume");
		// 0.45 records what ran ("/bin/sh" with the script and "--resume").
		found := false
		for _, a := range agent.Args {
			switch strings.Join(a, " ") {
			case "--resume", "/usr/local/bin/claude --resume":
				found = true
			}
		}
		if !found {
			t.Errorf("%s agent args: %q", v, agent.Args)
		}
		busy := ParseLayout(fixture(t, v, "layout-busy.kdl"))
		if !strings.Contains(strings.Join(busy.Commands, " "), "sleep") {
			t.Errorf("%s busy: %+v", v, busy)
		}
	}
}

func TestUnquoteKDL(t *testing.T) {
	cases := map[string]string{
		`"plain"`:                 "plain",
		`"say \"hi\""`:            `say "hi"`,
		`"tab\tand\\slash"`:       "tab\tand\\slash",
		`"\u{e9}t\u{e9}"`:         "été",
		`r"raw \n"`:               `raw \n`,
		`r#"has "quotes""#`:       `has "quotes"`,
		`#"kdl2 raw"#`:            "kdl2 raw",
		`"✳ Release notes draft"`: "✳ Release notes draft",
	}
	for in, want := range cases {
		if got, ok := unquoteKDL(in); !ok || got != want {
			t.Errorf("unquoteKDL(%s) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{`unquoted`, `"open`, `r#"mismatch"`} {
		if _, ok := unquoteKDL(bad); ok {
			t.Errorf("unquoteKDL(%s) accepted", bad)
		}
	}
}

func TestSessionInfoDir(t *testing.T) {
	cache := t.TempDir()
	if got := SessionInfoDir(filepath.Join(cache, "zellij"), "0.45.1"); got != filepath.Join(cache, "zellij", "contract_version_1", "session_info") {
		t.Errorf("default = %q", got)
	}
	for _, d := range []string{"contract_version_1", "contract_version_2", "0.45.1"} {
		os.MkdirAll(filepath.Join(cache, "zellij", d), 0o755)
	}
	if got := SessionInfoDir(filepath.Join(cache, "zellij"), "0.45.1"); !strings.Contains(got, "contract_version_2") {
		t.Errorf("highest contract not picked: %q", got)
	}
	// Before 0.44, the data directory is named after the version.
	if got := SessionInfoDir(filepath.Join(cache, "zellij"), "0.43.1"); got != filepath.Join(cache, "zellij", "0.43.1", "session_info") {
		t.Errorf("0.43 = %q", got)
	}
	// An unknown version is taken to be a current one.
	if got := SessionInfoDir(filepath.Join(cache, "zellij"), ""); !strings.Contains(got, "contract_version_2") {
		t.Errorf("unknown version: %q", got)
	}
}

func TestSocketDir(t *testing.T) {
	defer func(g string) { GOOS = g }(GOOS)
	GOOS = "linux"
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if got := SocketDir(lookup, 1000, "0.45.1"); !strings.HasSuffix(got, filepath.Join("zellij-1000", "contract_version_1")) {
		t.Errorf("no runtime dir: %q", got)
	}
	env["XDG_RUNTIME_DIR"] = "/run/user/1000"
	if got := SocketDir(lookup, 1000, "0.45.1"); got != filepath.Join("/run/user/1000", "zellij", "contract_version_1") {
		t.Errorf("runtime dir: %q", got)
	}
	if got := SocketDir(lookup, 1000, "0.43.1"); got != filepath.Join("/run/user/1000", "zellij", "0.43.1") {
		t.Errorf("0.43: %q", got)
	}
	env["ZELLIJ_SOCKET_DIR"] = "/sock"
	if got := SocketDir(lookup, 1000, "0.45.1"); got != filepath.Join("/sock", "contract_version_1") {
		t.Errorf("socket dir override: %q", got)
	}
	delete(env, "ZELLIJ_SOCKET_DIR")
	// macOS has no runtime directory for zellij, whatever the variable says;
	// Windows has no uid in the path.
	GOOS = "darwin"
	if got := SocketDir(lookup, 501, "0.45.1"); got != filepath.Join(os.TempDir(), "zellij-501", "contract_version_1") {
		t.Errorf("macOS: %q", got)
	}
	GOOS = "windows"
	if got := SocketDir(lookup, 0, "0.45.1"); got != filepath.Join(os.TempDir(), "zellij", "contract_version_1") {
		t.Errorf("Windows: %q", got)
	}
	if socketPathLimit("darwin") != 103 || socketPathLimit("linux") != 107 || socketPathLimit("windows") != 255 {
		t.Error("socket path limits")
	}
}

func TestCLIErrors(t *testing.T) {
	// fake returns canned output for one command.
	fake := func(stdout, stderr string, err error) *CLI {
		c := New("zellij")
		c.Run = func(ctx context.Context, args ...string) (string, string, error) { return stdout, stderr, err }
		return c
	}
	exit1 := errors.New("exit status 1")
	if _, err := fake("", "No active zellij sessions found.\n", exit1).ListSessions(context.Background()); !errors.Is(err, ErrNoSessions) {
		t.Errorf("no sessions: %v", err)
	}
	if _, err := fake("", "", nil).ListSessions(context.Background()); !errors.Is(err, ErrNoSessions) {
		t.Errorf("empty list with status 0: %v", err)
	}
	_, err := fake("", "Error occurred: Os { code: 13 }\nsecond line\n", exit1).ListSessions(context.Background())
	if err == nil || errors.Is(err, ErrNoSessions) || !strings.Contains(err.Error(), "Error occurred: Os { code: 13 } second line") {
		t.Errorf("real failure: %v", err)
	}
	c := fake("zellij 0.45.1\n", "", nil)
	if v, err := c.Version(context.Background()); err != nil || v != "0.45.1" {
		t.Errorf("version: %q %v", v, err)
	}
}

// script answers zellij's commands by name and records every call.
type script struct {
	// listed is what list-sessions prints, and the first rename adds to it
	// unless ignoreRename is set.
	listed       string
	ignoreRename bool
	// renameOut and renameErr are what rename-session says.
	renameOut string
	renameErr error
	// clients are the answers list-clients gives, one per call.
	clients []string
	calls   []string
}

func (s *script) run(_ context.Context, args ...string) (string, string, error) {
	s.calls = append(s.calls, strings.Join(args, " "))
	switch {
	case args[0] == "list-sessions":
		return s.listed, "", nil
	case len(args) == 5 && args[3] == "rename-session":
		if s.renameErr != nil {
			return "", s.renameOut, s.renameErr
		}
		if !s.ignoreRename {
			s.listed += args[4] + " [Created 1m ago]\n"
		}
		return "", "", nil
	case len(args) == 4 && args[3] == "list-clients":
		out := s.clients[0]
		s.clients = s.clients[1:]
		return out, "", nil
	}
	return "", "unexpected call", errors.New("exit status 2")
}

// count says how many calls contain a word.
func (s *script) count(word string) int {
	n := 0
	for _, c := range s.calls {
		if strings.Contains(c, word) {
			n++
		}
	}
	return n
}

func (s *script) cli() *CLI { return &CLI{Bin: "zellij", Timeout: time.Second, Run: s.run} }

// deleter answers delete-session, then lists whatever each call scripts next.
type deleter struct {
	// after is what list-sessions prints, one answer per call; the last one repeats.
	after []string
	// again makes the second delete-session fail.
	again error
	calls []string
}

func (d *deleter) cli() *CLI {
	return &CLI{Bin: "zellij", Timeout: time.Second, Run: func(_ context.Context, args ...string) (string, string, error) {
		d.calls = append(d.calls, strings.Join(args, " "))
		switch args[0] {
		case "list-sessions":
			out := d.after[0]
			if len(d.after) > 1 {
				d.after = d.after[1:]
			}
			return out, "", nil
		case "delete-session":
			if len(args) == 2 && d.again != nil {
				return "", d.again.Error(), d.again
			}
			return "", "", nil
		}
		return "", "unexpected call", errors.New("exit status 2")
	}}
}

func TestDeleteLooksAtWhatIsLeft(t *testing.T) {
	const (
		running = "work [Created 1h ago]\n"
		exited  = "work [Created 1h ago] (EXITED - attach to resurrect)\n"
		other   = "notes [Created 1h ago]\n"
	)
	old := deleteSettles
	deleteSettles = 80 * time.Millisecond
	defer func() { deleteSettles = old }()
	forced, plain := "delete-session work --force", "delete-session work"
	deletes := func(d *deleter) []string {
		var got []string
		for _, c := range d.calls {
			if strings.HasPrefix(c, "delete-session") {
				got = append(got, c)
			}
		}
		return got
	}
	for _, c := range []struct {
		what    string
		after   []string
		again   error
		want    []string
		wantErr string
	}{
		{what: "gone at once", after: []string{other}, want: []string{forced}},
		{what: "no session left", after: []string{""}, want: []string{forced}},
		{what: "stopping, then gone", after: []string{running, running, other}, want: []string{forced}},
		// The server wrote its layout back as it stopped.
		{what: "back as exited", after: []string{exited}, want: []string{forced, plain}},
		{what: "stopping, then back as exited", after: []string{running, exited}, want: []string{forced, plain}},
		{what: "someone brought it back as it was deleted again", after: []string{exited}, again: errors.New("exists and is active"), want: []string{forced, plain}, wantErr: "exists and is active"},
		{what: "never stops", after: []string{running}, want: []string{forced}, wantErr: "still running"},
	} {
		t.Run(c.what, func(t *testing.T) {
			d := &deleter{after: c.after, again: c.again}
			err := d.cli().Delete(context.Background(), "work")
			if (err != nil) != (c.wantErr != "") || (err != nil && !strings.Contains(err.Error(), c.wantErr)) {
				t.Errorf("error %v, want %q", err, c.wantErr)
			}
			if got := deletes(d); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("asked zellij for %v, want %v", got, c.want)
			}
		})
	}
}

func TestARenameCutShortIsUnconfirmed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake zellij is a shell script")
	}
	// The command reached the server, which may have acted on it, and it never
	// came back. exec, so the time limit stops the sleep and not just the shell.
	bin := filepath.Join(t.TempDir(), "zellij")
	body := "#!/bin/sh\ncase \"$*\" in\n  *rename-session*) exec sleep 30 ;;\n  *) echo 'work [Created 1h ago]' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &CLI{Bin: bin, Timeout: 200 * time.Millisecond}
	if err := c.Rename(context.Background(), "work", "journal"); !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("past the time limit: %v", err)
	}
	// The caller giving up while it runs says no more about what the server did.
	c.Timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if err := c.Rename(ctx, "work", "journal"); !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("cancelled: %v", err)
	}
	// zellij saying no is an answer: that one was refused.
	s := &script{listed: "work [Created 1h ago]\n", renameErr: errors.New("exit status 1")}
	if err := s.cli().Rename(context.Background(), "work", "journal"); err == nil || errors.Is(err, ErrUnconfirmed) {
		t.Errorf("refused: %v", err)
	}
}

func TestRename(t *testing.T) {
	// The rules where a rename works; on Windows zellij cannot reach the
	// session afterwards, and Rename refuses before asking zellij anything.
	defer func(g string) { GOOS = g }(GOOS)
	GOOS = "windows"
	if err := (&CLI{Bin: "zellij", Timeout: time.Second, Run: func(context.Context, ...string) (string, string, error) {
		t.Error("asked zellij on Windows")
		return "", "", nil
	}}).Rename(context.Background(), "a", "b"); !errors.Is(err, ErrRenameUnreachable) {
		t.Errorf("Windows: %v", err)
	}
	GOOS = "linux"
	ctx := context.Background()
	// A rename returns once zellij lists the new name.
	s := &script{listed: "a [Created 1h ago]\n"}
	if err := s.cli().Rename(ctx, "a", "b"); err != nil {
		t.Errorf("plain rename: %v", err)
	}
	// A taken name never reaches zellij, because since 0.44 it would accept
	// it and cut the other session off.
	s = &script{listed: "a [Created 1h ago]\nb [Created 1h ago]\n"}
	if err := s.cli().Rename(ctx, "a", "b"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("taken name: %v", err)
	}
	if s.count("rename-session") != 0 {
		t.Errorf("asked zellij to rename onto a taken name: %v", s.calls)
	}
	// A name zellij remembers without listing it, as 0.43 says it.
	s = &script{listed: "a [Created 1h ago]\n", renameOut: "A session by this name already exists.\n", renameErr: errors.New("exit status 2")}
	if err := s.cli().Rename(ctx, "a", "b"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("name refused by zellij: %v", err)
	}
	// A rename zellij accepts and does not do is not reported as done.
	old := renameSettles
	renameSettles = 60 * time.Millisecond
	defer func() { renameSettles = old }()
	s = &script{listed: "a [Created 1h ago]\n", ignoreRename: true}
	if err := s.cli().Rename(ctx, "a", "b"); err == nil || !errors.Is(err, ErrUnconfirmed) {
		t.Errorf("rename without effect: %v", err)
	}
}

func TestListClientsAsksAgainWhenTheAnswerCannotHold(t *testing.T) {
	ctx := context.Background()
	const none = "CLIENT_ID ZELLIJ_PANE_ID RUNNING_COMMAND\n"
	const one = none + "1 terminal_0 bash\n"
	cases := []struct {
		name    string
		answers []string
		want    int
		wantErr bool
		calls   int
	}{
		{"nobody: asked once", []string{none}, 0, false, 1},
		// zellij 0.44.2 prints nothing when commands overlap.
		{"no answer, then an answer", []string{"", one, one}, 1, false, 2},
		{"no answer twice stays an error", []string{"", ""}, 0, true, 2},
		// zellij 0.43 names the creating client the first time only.
		{"a client that was never there", []string{one, none}, 0, false, 2},
		// A real client is there both times.
		{"a real client", []string{one, one}, 1, false, 2},
	}
	for _, c := range cases {
		s := &script{clients: append([]string(nil), c.answers...)}
		rows, err := s.cli().ListClients(ctx, "a")
		if (err != nil) != c.wantErr || len(rows) != c.want || len(s.calls) != c.calls {
			t.Errorf("%s: %d rows, err %v, %d calls", c.name, len(rows), err, len(s.calls))
		}
	}
}

func TestTitlesByFocus(t *testing.T) {
	md := ParseMetadata(`name "x"
tabs {
    tab {
        position 0
        name "Tab #1"
        active false
    }
    tab {
        position 1
        name "Tab #2"
        active true
    }
}
panes {
    pane {
        is_plugin false
        is_focused true
        title "focused in a tab nobody sees"
        exited false
        tab_position 0
    }
    pane {
        is_plugin false
        is_focused false
        title "shown beside it"
        exited false
        tab_position 1
    }
    pane {
        is_plugin false
        is_focused true
        title "the one the session shows"
        exited false
        tab_position 1
    }
    pane {
        is_plugin false
        is_focused false
        title "its command finished"
        exited true
        tab_position 1
    }
    pane {
        is_plugin true
        is_focused true
        title "tab-bar"
        tab_position 1
    }
}
`)
	got := strings.Join(md.TitlesByFocus(), " | ")
	want := "the one the session shows | focused in a tab nobody sees | shown beside it"
	if got != want {
		t.Errorf("titles by focus: %s", got)
	}
}

func TestTitlesByFocusWithNobodyAttached(t *testing.T) {
	// Once the last client leaves, zellij marks no tab active.
	md := ParseMetadata(`tabs {
    tab {
        position 0
        active false
    }
    tab {
        position 1
        active false
    }
}
panes {
    pane {
        is_plugin false
        is_focused false
        title "beside it"
        tab_position 0
    }
    pane {
        is_plugin false
        is_focused true
        title "focused in the first tab"
        tab_position 0
    }
    pane {
        is_plugin false
        is_focused true
        title "focused in the second tab"
        tab_position 1
    }
}
`)
	if md.ActiveTab != -1 {
		t.Errorf("active tab: %d", md.ActiveTab)
	}
	got := strings.Join(md.TitlesByFocus(), " | ")
	if got != "focused in the first tab | focused in the second tab | beside it" {
		t.Errorf("titles by focus: %s", got)
	}
}

// A real file from each release, with a client attached: the fields this reads
// are still where it reads them. Renaming any of them in a recorded file fails.
func TestTitlesByFocusOnRecordedFiles(t *testing.T) {
	for _, v := range versions {
		b, err := os.ReadFile(filepath.Join("testdata", v, "metadata-attached.kdl"))
		if err != nil {
			t.Fatal(err)
		}
		md := ParseMetadata(string(b))
		if md.ActiveTab != 0 {
			t.Errorf("%s: active tab %d", v, md.ActiveTab)
		}
		titles := md.TitlesByFocus()
		var focused []PaneMeta
		for _, p := range md.Panes {
			if p.Focused && !p.Plugin {
				focused = append(focused, p)
			}
		}
		if len(focused) != 1 || focused[0].Tab != md.ActiveTab || len(titles) == 0 || titles[0] != focused[0].Title {
			t.Errorf("%s: focused panes %+v, titles %q", v, focused, titles)
		}
	}
}

func TestSwitchToSaysWhichSessionItRunsIn(t *testing.T) {
	c := &CLI{Bin: "zellij"}
	cmd := c.SwitchTo(context.Background(), "there", "here-renamed")
	if strings.Join(cmd.Args, " ") != "zellij action switch-session there" {
		t.Errorf("args %q", cmd.Args)
	}
	if len(cmd.Env) == 0 || cmd.Env[len(cmd.Env)-1] != "ZELLIJ_SESSION_NAME=here-renamed" {
		t.Errorf("the session's name now is not the last ZELLIJ_SESSION_NAME: %q", cmd.Env)
	}
	if cmd := c.SwitchTo(context.Background(), "there", ""); cmd.Env != nil {
		t.Errorf("the environment changed with no name to give: %q", cmd.Env)
	}
}
