// Package rename derives a session name from what the session is doing: the
// first pane's title (or the directory it was started in) plus the time it
// was last active, as in "vim-notes-md_1104-0931".
package rename

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kanggu12/zellij-reaper/internal/zellij"
)

// MaxBase is the longest base name in bytes. It leaves room for the
// "_MMDD-HHMM" stamp (10 bytes) and a "-NN" collision tail (3 bytes) inside
// the 66 bytes a name may take under /run/user/<uid>/zellij/contract_version_1.
const MaxBase = 50

// stampLen is the length of the "_MMDD-HHMM" stamp plus a "-NN" tail.
const stampLen = 13

// uninformative are names that say nothing about a session: zellij's own pane
// and tab titles and a bare shell name.
var uninformative = map[string]bool{"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true}

// Sanitize turns a free-form pane title into a session name: decorations
// (the spinners and stars coding agents put in titles) are dropped, anything
// that is not a letter, a digit, "_" or "-" becomes a single "-", letters are
// lowered, and the result is cut to maxBytes without splitting a character.
// Letters of every script are kept, so two Korean titles stay two names.
// It returns "" when nothing informative is left.
func Sanitize(title string, maxBytes int) string {
	s := strings.TrimLeftFunc(title, unicode.IsSpace)
	// Drop symbols, dingbats (U+2600-27BF) and braille (U+2800-28FF): the
	// characters terminal programs use for spinners and status marks.
	s = strings.Map(func(r rune) rune {
		if (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2800 && r <= 0x28FF) {
			return -1
		}
		return r
	}, s)
	// Drop anything before the first letter or digit.
	s = strings.TrimLeftFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	// Replace each run of other characters with one dash.
	var b strings.Builder
	dash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' {
			// A literal dash joins a run of dashes, like a replaced one.
			if r == '-' {
				if !dash {
					b.WriteRune('-')
				}
				dash = true
				continue
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteRune('-')
		}
		dash = true
	}
	s = strings.ToLower(strings.Trim(b.String(), "-"))
	// Cut to the byte budget at a character boundary.
	if len(s) > maxBytes {
		cut := 0
		for i, r := range s {
			if i+utf8.RuneLen(r) > maxBytes {
				break
			}
			cut = i + utf8.RuneLen(r)
		}
		s = strings.TrimRight(s[:cut], "-")
	}
	if s == "" || uninformative[s] || strings.HasPrefix(s, "pane-") || strings.HasPrefix(s, "tab-") {
		return ""
	}
	return s
}

// BaseBudget is the byte budget for a base name when the session's socket
// lives in socketDir: the socket path ("<dir>/<name>") must fit Linux's limit
// with room left for the stamp and a collision tail. It never exceeds MaxBase.
func BaseBudget(socketDir string) int {
	room := zellij.SocketPathLimit - len(socketDir) - 1 - stampLen
	if room > MaxBase || socketDir == "" {
		return MaxBase
	}
	if room < 1 {
		return 0
	}
	return room
}

// Suggest picks a base name: the first terminal pane's title, else the name of
// the directory the session started in, else "".
func Suggest(firstTitle, cwd string, maxBytes int) string {
	if s := Sanitize(firstTitle, maxBytes); s != "" {
		return s
	}
	if cwd == "" {
		return ""
	}
	return Sanitize(filepath.Base(cwd), maxBytes)
}

// Stamp formats a time as the "_MMDD-HHMM" suffix, in local time, which is
// how people remember when they last used something.
func Stamp(t time.Time) string {
	return t.Local().Format("_0102-1504")
}

// Desired is the full name for a base and a last-activity time.
func Desired(base string, lastActive time.Time) string {
	return base + Stamp(lastActive)
}

// Free returns desired, or desired-2, -3, ... up to -99, whichever is not in
// taken. ok is false when all of them are taken.
func Free(desired string, taken map[string]bool) (name string, ok bool) {
	if !taken[desired] {
		return desired, true
	}
	for n := 2; n <= 99; n++ {
		c := fmt.Sprintf("%s-%d", desired, n)
		if !taken[c] {
			return c, true
		}
	}
	return "", false
}

// Next is the candidate after name in the -2, -3, ... sequence, used when
// zellij refuses a name it remembers but does not list.
func Next(desired, name string) (string, bool) {
	if name == desired {
		return desired + "-2", true
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(name, desired+"-"), "%d", &n); err != nil || n >= 99 {
		return "", false
	}
	return fmt.Sprintf("%s-%d", desired, n+1), true
}
