package zellij

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`"plain"`:               "plain",
		`"say \"hi\""`:          `say "hi"`,
		`"tab\tand\\slash"`:     "tab\tand\\slash",
		`"\u{e9}t\u{e9}"`:       "été",
		`r"raw \n"`:             `raw \n`,
		`r#"has "quotes""#`:     `has "quotes"`,
		`#"kdl2 raw"#`:          "kdl2 raw",
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
	if got := SessionInfoDir(cache, "0.45.1"); got != filepath.Join(cache, "zellij", "contract_version_1", "session_info") {
		t.Errorf("default = %q", got)
	}
	for _, d := range []string{"contract_version_1", "contract_version_2", "0.45.1"} {
		os.MkdirAll(filepath.Join(cache, "zellij", d), 0o755)
	}
	if got := SessionInfoDir(cache, "0.45.1"); !strings.Contains(got, "contract_version_2") {
		t.Errorf("highest contract not picked: %q", got)
	}
	// Before 0.44, the data directory is named after the version.
	if got := SessionInfoDir(cache, "0.43.1"); got != filepath.Join(cache, "zellij", "0.43.1", "session_info") {
		t.Errorf("0.43 = %q", got)
	}
	// An unknown version is taken to be a current one.
	if got := SessionInfoDir(cache, ""); !strings.Contains(got, "contract_version_2") {
		t.Errorf("unknown version: %q", got)
	}
}

func TestSocketDir(t *testing.T) {
	env := map[string]string{}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	if got := SocketDir(lookup, 1000, "0.45.1"); !strings.HasSuffix(got, "zellij-1000/contract_version_1") {
		t.Errorf("no runtime dir: %q", got)
	}
	env["XDG_RUNTIME_DIR"] = "/run/user/1000"
	if got := SocketDir(lookup, 1000, "0.45.1"); got != "/run/user/1000/zellij/contract_version_1" {
		t.Errorf("runtime dir: %q", got)
	}
	if got := SocketDir(lookup, 1000, "0.43.1"); got != "/run/user/1000/zellij/0.43.1" {
		t.Errorf("0.43: %q", got)
	}
	env["ZELLIJ_SOCKET_DIR"] = "/sock"
	if got := SocketDir(lookup, 1000, "0.45.1"); got != "/sock/contract_version_1" {
		t.Errorf("socket dir override: %q", got)
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
	if err := fake("", "A session by this name already exists\n", exit1).Rename(context.Background(), "a", "b"); !errors.Is(err, ErrNameTaken) {
		t.Errorf("rename taken: %v", err)
	}
	c := fake("zellij 0.45.1\n", "", nil)
	if v, err := c.Version(context.Background()); err != nil || v != "0.45.1" {
		t.Errorf("version: %q %v", v, err)
	}
}
