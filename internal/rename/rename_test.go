package rename

import (
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
		{"ssh me@prod-db", "ssh-prod-db"},
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
	if got := BaseBudget("/run/user/1000/zellij/contract_version_1"); got != MaxBase {
		t.Errorf("usual dir: %d", got)
	}
	// A long one shrinks the budget so the full socket path still fits.
	long := "/" + strings.Repeat("d", 70)
	if got := BaseBudget(long); got != 107-len(long)-1-13 {
		t.Errorf("long dir: %d", got)
	}
	if BaseBudget("/"+strings.Repeat("d", 120)) != 0 {
		t.Error("budget for an impossible dir should be 0")
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
