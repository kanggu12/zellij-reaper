package rename

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// validName is what every name zellij-reaper gives must be: English letters,
// digits, '-', '_' and '.', starting with a letter or a digit (a leading '-'
// would read as a flag to zellij), within the byte budget and MaxWidth.
func validName(t *testing.T, name string, budget int) {
	t.Helper()
	if name == "" {
		return
	}
	if len(name) > budget || utf8.RuneCountInString(name) > MaxWidth {
		t.Fatalf("%q is longer than %d bytes or %d characters", name, budget, MaxWidth)
	}
	for i, r := range name {
		ok := r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || i > 0 && strings.ContainsRune("-_.", r))
		if !ok {
			t.Fatalf("%q has %q at %d", name, r, i)
		}
	}
	if strings.HasSuffix(name, "-") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, "_") {
		t.Fatalf("%q ends in a separator", name)
	}
}

func FuzzSuggest(f *testing.F) {
	for _, s := range []string{
		"✳ Fix the login bug", "tail -f /var/log/app.log", "me@laptop: ~/projects/api", "Claude Code",
		"Pane #1", "Déploiement prod 本番", "docker compose up -d --build", "https://user:pw@github.com/foo/bar",
		"ssh me@prod-db", "notes v1.2.md", "-rf", "..", "a--b", strings.Repeat("word ", 40), "\x00\x1b[31mred",
	} {
		f.Add(s, "/home/u/web-app", "npm run dev", 50)
	}
	f.Fuzz(func(t *testing.T, title, cwd, busy string, budget int) {
		budget = 8 + abs(budget)%60
		name := Suggest(Input{Titles: []string{title}, Busy: busy, Cwd: cwd, Home: "/home/u", Host: "laptop"}, budget)
		validName(t, name, min(budget, MaxBase))
	})
}

func FuzzFit(f *testing.F) {
	for _, s := range []string{"notes-from-the-long-planning-meeting-about-the-next-release", "a", "é"} {
		f.Add(s, 32)
	}
	f.Fuzz(func(t *testing.T, s string, budget int) {
		budget = 1 + abs(budget)%80
		got := Fit(s, budget)
		if len(got) > budget || !utf8.ValidString(got) && utf8.ValidString(s) {
			t.Fatalf("Fit(%q, %d) = %q", s, budget, got)
		}
	})
}

func FuzzTails(f *testing.F) {
	f.Add("api", "api-2", 3)
	f.Add("web-app_1104", "web-app_1104-0931", 0)
	f.Fuzz(func(t *testing.T, desired, name string, takenCount int) {
		// Free never hands out a name that is taken, and its name is Same as desired.
		taken := map[string]bool{name: true, desired: true}
		for n := 2; n < 2+abs(takenCount)%120; n++ {
			taken[desired+"-"+itoa(n)] = true
		}
		if free, ok := Free(desired, taken); ok && (taken[free] || !Same(free, desired)) {
			t.Fatalf("Free(%q) = %q", desired, free)
		}
		// Next walks forward from a tail of desired and stays Same as desired.
		if next, ok := Next(desired, name); ok && (!Same(next, desired) || next == name) {
			t.Fatalf("Next(%q, %q) = %q", desired, name, next)
		}
	})
}

func abs(n int) int {
	if n < 0 {
		return -(n + 1)
	}
	return n
}
