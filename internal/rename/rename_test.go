package rename

import (
	"strings"
	"testing"
	"time"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"vim foo.txt":                  "vim-foo-txt",
		"  tail -f app.log":            "tail-f-app-log",
		"✳ Release notes draft":          "release-notes-draft",
		"⠋ Building the thing":         "building-the-thing",
		"★★ Notes ★★":                  "notes",
		"API_server -- prod":           "api_server-prod",
		"bash":                         "",
		"Pane #1":                      "",
		"Tab #2":                       "",
		"zsh":                          "",
		"":                             "",
		"!!!":                          "",
		"ÉTÉ 2026":                     "été-2026",
		"docker compose up -d --build": "docker-compose-up-d-build",
	}
	for in, want := range cases {
		if got := Sanitize(in, MaxBase); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeByteBudget(t *testing.T) {
	// 20 Hangul syllables are 60 bytes: the cut must land on a character.
	in := strings.Repeat("x", 20)
	got := Sanitize(in, MaxBase)
	if len(got) > MaxBase || len(got)%3 != 0 || !strings.HasPrefix(in, got) {
		t.Errorf("cut %q (%d bytes)", got, len(got))
	}
	// A cut that ends on a dash drops it.
	if got := Sanitize(strings.Repeat("a", 49)+" b", MaxBase); got != strings.Repeat("a", 49) {
		t.Errorf("trailing dash kept: %q", got)
	}
}

func TestBaseBudget(t *testing.T) {
	// The usual socket directory leaves more than MaxBase.
	if got := BaseBudget("/run/user/1000/zellij/contract_version_1"); got != MaxBase {
		t.Errorf("usual dir: %d", got)
	}
	// A long one shrinks the budget so the full socket path still fits.
	long := "/" + strings.Repeat("d", 70)
	got := BaseBudget(long)
	if got != 107-len(long)-1-13 {
		t.Errorf("long dir: %d", got)
	}
	if BaseBudget("/"+strings.Repeat("d", 120)) != 0 {
		t.Error("budget for an impossible dir should be 0")
	}
}

func TestSuggest(t *testing.T) {
	if got := Suggest("Pane #1", "/home/u/projects/web-app", MaxBase); got != "web-app" {
		t.Errorf("cwd fallback: %q", got)
	}
	if got := Suggest("htop", "/home/u", MaxBase); got != "htop" {
		t.Errorf("title first: %q", got)
	}
	if got := Suggest("bash", "", MaxBase); got != "" {
		t.Errorf("nothing informative: %q", got)
	}
}

func TestStampAndFree(t *testing.T) {
	when := time.Date(2026, 11, 4, 9, 31, 0, 0, time.Local)
	if got := Desired("notes", when); got != "notes_1104-0931" {
		t.Errorf("Desired = %q", got)
	}
	taken := map[string]bool{"notes_1104-0931": true, "notes_1104-0931-2": true}
	if got, ok := Free("notes_1104-0931", taken); !ok || got != "notes_1104-0931-3" {
		t.Errorf("Free = %q %v", got, ok)
	}
	if got, ok := Next("x_1104-0931", "x_1104-0931"); !ok || got != "x_1104-0931-2" {
		t.Errorf("Next first = %q %v", got, ok)
	}
	if got, ok := Next("x_1104-0931", "x_1104-0931-7"); !ok || got != "x_1104-0931-8" {
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
