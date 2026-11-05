package rename

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// here is a machine: a home, a name, and a session working in a project.
func here(titles ...string) Input {
	return Input{Titles: titles, Home: "/home/me", Host: "laptop", Cwd: "/home/me/projects/web-app"}
}

func TestSuggestFromTitles(t *testing.T) {
	cases := []struct{ title, want string }{
		// Marks programs put in titles go, wherever they are.
		{"✳ Release notes draft", "release-notes-draft"},
		{"⠋ Building the thing", "building-the-thing"},
		{"★★ Notes ★★", "notes"},
		{"🚀 deploy 🚀", "deploy"},
		// A variation selector stays with the symbol it came with.
		{"build⚠️", "build"},
		// Words of every script stay, and a Thai vowel stays on its letter.
		{"ÉTÉ 2026", "été-2026"},
		{"เกม", "เกม"},
		// A "." stays inside a word, so a file name still reads as one.
		{"vim notes.md", "vim-notes.md"},
		{"draw.io diagrams", "draw.io-diagrams"},
		{"release v1.2.3.", "release-v1.2.3"},
		// A command line loses its flags, and its paths their directories.
		{"tail -f /var/log/app.log", "tail-app.log"},
		{"docker compose up -d --build", "docker-compose-up"},
		{"/usr/bin/python3 -m http.server 8000", "python3-http.server-8000"},
		{"vim ~/notes/today.md", "vim-today.md"},
		{"API_server -- prod", "api_server-prod"},
		// A word with a slash in it is not a path.
		{"Reply-All/Reply-To fix", "reply-all-reply-to-fix"},
		// An address loses its scheme, and an ssh target its user.
		{"https://github.com/foo/bar", "github.com-foo-bar"},
		{"curl https://user:s3cret@host/path", "curl-host-path"},
		{"ssh me@prod-db", "ssh-prod-db"},
		{"/usr/bin/ssh -p 22 me@prod-db", "ssh-22-prod-db"},
		// Elsewhere an @ is no ssh target.
		{"npm install react@18.2.0", "npm-install-react-18.2.0"},
		{"npx create-vite@latest my-app", "npx-create-vite-latest-my-app"},
		// A shell's own title names its directory.
		{"me@laptop: ~/projects/api", "api"},
		{"me@laptop:/srv/billing#", "billing"},
		{"[me@laptop billing]$", "billing"},
		// On another machine it names that machine as well.
		{"me@buildbox:~", "buildbox"},
		{"me@buildbox:/srv/app$", "buildbox-app"},
		{"me@buildbox.example.org:~", "buildbox.example.org"},
		// A title that is only a path.
		{"~/projects/api", "api"},
		{"/srv/billing/", "billing"},
	}
	for _, c := range cases {
		if got := Suggest(here(c.title), MaxBase); got != c.want {
			t.Errorf("%q: got %q, want %q", c.title, got, c.want)
		}
	}
}

func TestTitlesThatSayNothingFallBackToTheDirectory(t *testing.T) {
	for _, title := range []string{
		"", "   ", "!!!", "bash", "-bash", "zsh", "/bin/bash", "Pane #1", "Tab #2",
		// A shell at home on this machine.
		"me@laptop:~", "me@laptop: /home/me", "~", "/home/me", "/",
		"me@localhost:~",
	} {
		if got := Suggest(here(title), MaxBase); got != "web-app" {
			t.Errorf("%q: got %q", title, got)
		}
	}
	// zellij's own words only match whole: these are about something.
	for title, want := range map[string]string{"Pane manager": "pane-manager", "Tab bar fix": "tab-bar-fix"} {
		if got := Suggest(here(title), MaxBase); got != want {
			t.Errorf("%q: got %q", title, got)
		}
	}
}

func TestAProgramsTitleSaysLessThanADirectory(t *testing.T) {
	// A coding agent before its first task.
	for title, want := range map[string]string{
		"✳ Claude Code":                         "web-app-claude-code",
		"claude --dangerously-skip-permissions": "web-app-claude",
		"codex":                                 "web-app-codex",
	} {
		if got := Suggest(here(title), MaxBase); got != want {
			t.Errorf("%q: got %q, want %q", title, got, want)
		}
	}
	// A title that repeats what the pane runs.
	in := here("nvim")
	in.Busy = "nvim main.go"
	if got := Suggest(in, MaxBase); got != "web-app-nvim" {
		t.Errorf("nvim: %q", got)
	}
	// Once the agent has a task, the task is the name.
	if got := Suggest(here("✳ Fix the login bug"), MaxBase); got != "fix-the-login-bug" {
		t.Errorf("task: %q", got)
	}
	// With no directory to say more, the program is what is left.
	in = here("Claude Code")
	in.Cwd = "/home/me"
	if got := Suggest(in, MaxBase); got != "claude-code" {
		t.Errorf("at home: %q", got)
	}
}

func TestALaterPaneCanSayMore(t *testing.T) {
	if got := Suggest(here("bash", "Claude Code", "✳ Fix the login bug"), MaxBase); got != "fix-the-login-bug" {
		t.Errorf("got %q", got)
	}
}

func TestBusyNamesASessionWithNoTitle(t *testing.T) {
	in := here("Pane #1")
	in.Busy = "npm run dev --host"
	if got := Suggest(in, MaxBase); got != "web-app-npm-run-dev" {
		t.Errorf("got %q", got)
	}
	in.Cwd = ""
	if got := Suggest(in, MaxBase); got != "npm-run-dev" {
		t.Errorf("no directory: %q", got)
	}
}

func TestACommandLineGivesNoCredentials(t *testing.T) {
	// A command line is not on the screen the way a title is, and its arguments
	// can hold a password: a name takes the plain words up to the first flag.
	for busy, want := range map[string]string{
		"curl -u user:s3cret https://example.com":     "curl",
		"curl https://user:s3cret@example.com/path":   "curl",
		"mysql --password s3cret app_db":              "mysql",
		"mysql -ps3cret app_db":                       "mysql",
		"env TOKEN=s3cret ./deploy.sh":                "env",
		"npm run dev --host":                          "npm-run-dev",
		"docker compose up -d --build":                "docker-compose-up",
		"psql app_dev":                                "psql-app_dev",
		"/usr/bin/python3 /srv/app/main.py --port 80": "python3-main.py",
		"a b c d e":      "a-b-c",
		"bash -c 'x; y'": "",
		"-bash":          "",
	} {
		in := Input{Titles: []string{"Pane #1"}, Busy: busy, Home: "/home/me", Host: "laptop"}
		if got := Suggest(in, MaxBase); got != want {
			t.Errorf("%q: got %q, want %q", busy, got, want)
		}
	}
}

func TestDirectoriesThatSayNothing(t *testing.T) {
	for _, cwd := range []string{"", "/", "/home/me", "/home/me/"} {
		in := here("Pane #1")
		in.Cwd = cwd
		if got := Suggest(in, MaxBase); got != "" {
			t.Errorf("cwd %q: got %q", cwd, got)
		}
	}
	// A directory repeating the program's name appears once.
	in := here("aider")
	in.Cwd = "/srv/aider"
	if got := Suggest(in, MaxBase); got != "aider" {
		t.Errorf("repeated word: %q", got)
	}
}

func TestFit(t *testing.T) {
	// Short names pass through.
	if got := Fit("web-app", MaxBase); got != "web-app" {
		t.Errorf("short: %q", got)
	}
	// Cut at a word's end, by columns: Hangul takes two.
	got := Fit("game-server-evolved-local-container-host", MaxBase)
	if got != "game-server-evolved-local" {
		t.Errorf("by columns: %q", got)
	}
	// Words that promise more go with the cut.
	if got := Fit("check-the-cluster-state-and-then-repair-it", MaxBase); got != "check-the-cluster-state" {
		t.Errorf("dangling and: %q", got)
	}
	if got := Fit("node-sshd-issue-analysis-and-monitoring", MaxBase); got != "node-sshd-issue-analysis" {
		t.Errorf("dangling and: %q", got)
	}
	// An underscore is a word's end too.
	if got := Fit("snapshot_disk_session_20260101_phase_a", MaxBase); got != "snapshot_disk_session_20260101" {
		t.Errorf("underscores: %q", got)
	}
	// One long word is cut rather than lost, on a character.
	got = Fit(strings.Repeat("x", 30), MaxBase)
	if ansi.StringWidth(got) != MaxWidth || !strings.HasPrefix(strings.Repeat("x", 30), got) {
		t.Errorf("long word: %q", got)
	}
	// The byte budget binds before the width in a tight socket directory.
	got = Fit(strings.Repeat("x", 30), 10)
	if len(got) != 9 {
		t.Errorf("tight budget: %q (%d bytes)", got, len(got))
	}
	// Nothing is ever past either limit.
	for _, s := range []string{strings.Repeat("a", 200), strings.Repeat("ab ", 60), strings.Repeat("ab-", 60)} {
		got := Fit(normalize(s), MaxBase)
		if len(got) > MaxBase || ansi.StringWidth(got) > MaxWidth || strings.HasSuffix(got, "-") {
			t.Errorf("past a limit: %q", got)
		}
	}
}

func TestBaseBudget(t *testing.T) {
	// The usual socket directory leaves more than MaxBase.
	if got := BaseBudget(66); got != MaxBase {
		t.Errorf("usual room: %d", got)
	}
	// Tighter room shrinks the budget, with space kept for a stamp and a tail.
	if got := BaseBudget(30); got != 30-13 {
		t.Errorf("tight room: %d", got)
	}
	if BaseBudget(10) != 0 || BaseBudget(-5) != 0 {
		t.Error("no room should give no budget")
	}
}

func TestDesired(t *testing.T) {
	when := time.Date(2026, 11, 4, 9, 31, 0, 0, time.Local)
	for stamp, want := range map[Stamp]string{
		StampNone: "notes",
		StampDate: "notes_1104",
		StampTime: "notes_1104-0931",
		// An empty setting is what a test's zero value holds.
		"": "notes",
	} {
		if got := Desired("notes", stamp, when); got != want {
			t.Errorf("stamp %q: got %q", stamp, got)
		}
	}
}

func TestSame(t *testing.T) {
	for _, c := range []struct {
		name, desired string
		want          bool
	}{
		{"web-app", "web-app", true},
		// The tail from a collision: renaming again would swap it each pass.
		{"web-app-2", "web-app", true},
		{"web-app-12", "web-app", true},
		// These are other names.
		{"web-app-api", "web-app", false},
		{"web-app-", "web-app", false},
		{"web", "web-app", false},
		// The old stamped name is another name, so it gets the new one.
		{"web-app_1104-0931", "web-app", false},
		// Four digits are the minute of a stamp, not a tail.
		{"api_1104-0931", "api_1104", false},
	} {
		if got := Same(c.name, c.desired); got != c.want {
			t.Errorf("Same(%q, %q) = %v", c.name, c.desired, got)
		}
	}
}

func TestFreeAndNext(t *testing.T) {
	taken := map[string]bool{"notes": true, "notes-2": true}
	if got, ok := Free("notes", taken); !ok || got != "notes-3" {
		t.Errorf("Free = %q %v", got, ok)
	}
	if got, ok := Next("x", "x"); !ok || got != "x-2" {
		t.Errorf("Next first = %q %v", got, ok)
	}
	if got, ok := Next("x", "x-7"); !ok || got != "x-8" {
		t.Errorf("Next = %q %v", got, ok)
	}
	if _, ok := Next("x", "x-99"); ok {
		t.Error("Next past 99")
	}
	all := map[string]bool{"a": true}
	for n := 2; n <= 99; n++ {
		all["a-"+itoa(n)] = true
	}
	if _, ok := Free("a", all); ok {
		t.Error("Free found a name with every variant taken")
	}
}

// itoa avoids importing strconv for one test.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}

func TestGenerated(t *testing.T) {
	// Names zellij made up, from the log of real sessions.
	for _, n := range []string{"brave-peach", "glowing-duck", "excellent-muskrat", "rusty-pepper", "exquisite-xylophone", "polished-duck", "quiet-mouse", "loyal-mountain"} {
		if !Generated(n) {
			t.Errorf("%q should read as made up by zellij", n)
		}
	}
	// Names of that shape someone chose, and other shapes.
	for _, n := range []string{"web-app", "api-server", "claude-code", "quiet-room", "lucky-branch", "work", "brave", "brave-peach-2", "Brave-Peach", "fix-the-login-bug", "brave-peach_1104"} {
		if Generated(n) {
			t.Errorf("%q should read as chosen", n)
		}
	}
}

func TestStamped(t *testing.T) {
	for _, n := range []string{"web-app_1104-0931", "web-app-notes_1105-0931", "web-app_1104-0931-2", "a_0101-0000", "a_1231-2359"} {
		if !Stamped(n) {
			t.Errorf("%q should read as stamped", n)
		}
	}
	// The shape of a stamp without being a date: someone chose these.
	for _, n := range []string{
		"web-app", "web-app_1104", "release_2026", "a_1104-093",
		"report_2025-2026", "release_2026-1105", "ticket_1234-5678",
		"a_0001-0000", "a_1301-0000", "a_0100-0000", "a_0132-0000", "a_0101-2400", "a_0101-0060",
	} {
		if Stamped(n) {
			t.Errorf("%q should not read as stamped", n)
		}
	}
}

// TestKnowsTheWordsOfARelease compares the word lists with a release's own source.
// `make integration` downloads that file for every release it tests and names it in
// ZELLIJ_SESSIONS_RS, so a new word in any release fails there, every time.
func TestKnowsTheWordsOfARelease(t *testing.T) {
	path := os.Getenv("ZELLIJ_SESSIONS_RS")
	if path == "" {
		t.Skip("ZELLIJ_SESSIONS_RS names no file of zellij's source")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for list, known := range map[string][]string{"ADJECTIVES": zellijAdjectives, "NOUNS": zellijNouns} {
		words := wordsOf(string(src), list)
		// Far fewer words than ever means the source is laid out differently now,
		// and that a list read as short would pass for one with nothing new.
		if len(words) < 50 {
			t.Fatalf("%s: read %d words from %s", list, len(words), path)
		}
		for _, w := range words {
			if !slices.Contains(known, w) {
				t.Errorf("%s: zellij has %q, internal/core/rename/generated.go does not", list, w)
			}
		}
	}
}

// wordsOf reads a Rust list of strings: `const NOUNS: &[&'static str] = &[ "a", "b", ];`.
func wordsOf(src, list string) []string {
	_, rest, _ := strings.Cut(src, "const "+list+":")
	body, _, _ := strings.Cut(rest, "];")
	var words []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(body, -1) {
		words = append(words, m[1])
	}
	return words
}
