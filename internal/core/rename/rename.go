// Package rename makes a session's name from what the session shows: a pane's
// title with the terminal noise taken out, or, when no pane says what it is
// about, the directory it works in and the program it runs. Names come out
// short, lower case and stable: the same title gives the same name on every
// pass, so a session keeps its name until what it shows changes.
package rename

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// MaxBase is the longest base name in bytes. It leaves room for the longest
// stamp and a "-NN" collision tail inside the 66 bytes a name may take under
// /run/user/<uid>/zellij/contract_version_1.
const MaxBase = 50

// MaxWidth is the widest base name in terminal columns: about what zellij's
// bars and session list show without crowding, and 16 Hangul syllables. The
// byte limit alone would allow a 50-column name in English.
const MaxWidth = 32

// tailLen is the room kept for the longest stamp ("_MMDD-HHMM") and a "-NN"
// tail, whichever stamp is set, so changing the setting cannot make a name
// that fitted stop fitting.
const tailLen = 13

// Stamp is the setting auto_rename_stamp: what a name ends in.
type Stamp string

const (
	// StampNone adds nothing. The name changes only when what the session
	// shows changes; it is the default because the shell version's stamp
	// made 99% of its renames: 20,536 of the 20,839 its log recorded moved
	// nothing but the time.
	StampNone Stamp = "none"
	// StampDate adds the day of last use, "_1104": at most one more rename
	// a day.
	StampDate Stamp = "date"
	// StampTime adds the minute of last use, "_1104-0931", the shell
	// version's name: a session in use is renamed on every pass.
	StampTime Stamp = "time"
)

// Input is what a session offers to be named after.
type Input struct {
	// Titles are its pane titles, the one the session shows first.
	Titles []string
	// Busy is the command line keeping it busy, if any.
	Busy string
	// Cwd is the directory it works in.
	Cwd string
	// Home is this user's home, which says nothing about a session.
	Home string
	// Host is this machine's name: a prompt title from another machine
	// (an ssh session) names that machine, and this one is not news.
	Host string
}

// Suggest picks a base name: the first title that says what the session is
// about; else the directory it works in with the program it runs; else "".
func Suggest(in Input, maxBytes int) string {
	program := ""
	for _, title := range in.Titles {
		w := in.clean(title)
		if w == "" {
			continue
		}
		// A title that only names the program, like "Claude Code" before
		// its first task, gives way to a later pane that says more, and
		// otherwise goes after the directory.
		if in.isProgram(w) {
			if program == "" {
				program = w
			}
			continue
		}
		return Fit(w, maxBytes)
	}
	if program == "" {
		program = in.running()
	}
	return Fit(joinWords(in.directory(), program), maxBytes)
}

// joinWords puts a directory before a program, once if they are the same word.
func joinWords(dir, program string) string {
	switch {
	case dir == "" || dir == program:
		return program
	case program == "":
		return dir
	}
	return dir + "-" + program
}

// directory is the word for the directory the session works in; "" for one
// that says nothing about it.
func (in Input) directory() string {
	return normalize(in.pathWord(in.Cwd))
}

// pathWord is the last part of a path, "" for the home directory and the root.
func (in Input) pathWord(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" || p == "~" {
		return ""
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok && in.Home != "" {
		p = filepath.Join(in.Home, rest)
	}
	if p == strings.TrimRight(in.Home, "/") {
		return ""
	}
	return filepath.Base(p)
}

// agentTitles are the titles coding agents show before they have a task, in
// their normalized form. They name a program as surely as a process name does,
// though not always the same word ("Claude Code" runs as claude).
var agentTitles = map[string]bool{
	"claude": true, "claude-code": true, "codex": true, "gemini": true, "gemini-cli": true,
	"aider": true, "opencode": true,
}

// isProgram reports whether a normalized title only names the program running.
func (in Input) isProgram(w string) bool {
	if agentTitles[w] {
		return true
	}
	if in.Busy == "" {
		return false
	}
	busy := in.clean(in.Busy)
	first, _, _ := strings.Cut(busy, "-")
	return w == busy || w == first
}

// runningWords is how many words of a command line a name takes: enough for
// `npm run dev`, short of a long list of arguments.
const runningWords = 3

// running is what a name says about the command keeping a session busy, for a
// session whose titles say nothing. A title is on the screen anyway, but a command
// line is not, and its arguments can hold a password. So this takes the program and
// the plain words after it, and stops at the first flag or at anything else: a
// flag's value cannot be told from a word, and a word with ":", "@" or "=" in it
// may be a credential.
func (in Input) running() string {
	fields := strings.Fields(dropMarks(in.Busy))
	var words []string
	for i, f := range fields {
		if len(words) == runningWords {
			break
		}
		switch {
		case i == 0:
			words = append(words, in.pathWord(strings.TrimPrefix(f, "-")))
		case isPath(f):
			words = append(words, in.pathWord(f))
		case plainWord(f):
			words = append(words, f)
		default:
			return in.named(words)
		}
	}
	return in.named(words)
}

// named joins words into a name, "" when they only name a shell.
func (in Input) named(words []string) string {
	w := normalize(strings.Join(words, " "))
	if uninformative[w] {
		return ""
	}
	return w
}

// plainWord reports whether a word holds nothing but letters, digits, "_", "-"
// and ".", and does not start like a flag.
func plainWord(f string) bool {
	if strings.HasPrefix(f, "-") {
		return false
	}
	for _, r := range f {
		if !isWordRune(r) && !unicode.IsMark(r) && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return f != ""
}

// promptRe and bracketRe match the titles shells set for themselves:
// "me@host: ~/projects/web-app", "me@host:/srv/app#" and "[me@host app]$".
var (
	promptRe  = regexp.MustCompile(`^[\w.-]+@([\w.-]+):\s*(\S*?)\s*[#$%>]?$`)
	bracketRe = regexp.MustCompile(`^\[[\w.-]+@([\w.-]+)\s+(\S+)\]\s*[#$%>]?$`)
)

// clean turns a pane title into a name in its normal form, "" when it says
// nothing: a shell's name, zellij's "Pane #1", a prompt at the home directory.
func (in Input) clean(title string) string {
	s := strings.TrimSpace(dropMarks(title))
	if w, ok := in.fromPrompt(s); ok {
		return w
	}
	// A title that is only a path names the directory it ends in, and the
	// same goes for each path inside a command line.
	fields := strings.Fields(s)
	var words []string
	for _, f := range fields {
		if w := in.word(f, remoteCommand(fields)); w != "" {
			words = append(words, w)
		}
	}
	w := normalize(strings.Join(words, " "))
	if uninformative[w] || defaultTitleRe.MatchString(w) {
		return ""
	}
	return w
}

// uninformative are titles that say nothing about a session: a bare shell. Every
// shell inspect knows is here, and a test keeps it that way.
var uninformative = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ash": true, "ksh": true, "mksh": true,
	"oksh": true, "pdksh": true, "yash": true, "csh": true, "tcsh": true, "nu": true, "elvish": true, "xonsh": true,
	"pwsh": true, "ion": true, "osh": true, "oil": true, "hush": true, "rc": true, "es": true, "murex": true,
	"login": true,
}

// defaultTitleRe matches zellij's own pane and tab titles once normalized.
var defaultTitleRe = regexp.MustCompile(`^(pane|tab)-\d+$`)

// fromPrompt reads a title a shell set for itself. ok is false for any other
// title. A prompt at the home directory on this machine says nothing; on
// another machine it says which machine.
func (in Input) fromPrompt(s string) (string, bool) {
	m := promptRe.FindStringSubmatch(s)
	if m == nil {
		m = bracketRe.FindStringSubmatch(s)
	}
	if m == nil {
		return "", false
	}
	host, dir := m[1], in.pathWord(m[2])
	if in.isHere(host) {
		host = ""
	}
	return normalize(joinWords(host, dir)), true
}

// isHere reports whether a prompt's host is this machine, by its short name.
func (in Input) isHere(host string) bool {
	short := func(h string) string {
		h, _, _ = strings.Cut(strings.ToLower(h), ".")
		return h
	}
	return short(host) == "localhost" || short(host) == short(in.Host)
}

// remoteCommands take a target in the form user@host.
var remoteCommands = map[string]bool{"ssh": true, "mosh": true, "sftp": true, "scp": true, "autossh": true, "et": true}

// remoteCommand reports whether a title's first word is one of them. Elsewhere an
// "@" means something else: `npm install react@18.2.0` is not a user on a host.
func remoteCommand(fields []string) bool {
	return len(fields) > 0 && remoteCommands[strings.ToLower(filepath.Base(fields[0]))]
}

// word reduces one word of a title: a flag says nothing, a path says its last
// part, a URL loses its scheme and any password in it, and an ssh target its user.
func (in Input) word(f string, remote bool) string {
	switch {
	// "-f", "--build", "--port=80", and "--" and "-" on their own.
	case strings.HasPrefix(f, "-"):
		return ""
	case isPath(f):
		return in.pathWord(f)
	}
	if _, rest, ok := strings.Cut(f, "://"); ok {
		host, path, _ := strings.Cut(rest, "/")
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		return host + "/" + path
	}
	if at := strings.LastIndexByte(f, '@'); remote && at > 0 && at < len(f)-1 {
		return f[at+1:]
	}
	return f
}

// isPath reports whether a word is a file path rather than a word that happens
// to hold a slash, like "Reply-All/Reply-To".
func isPath(f string) bool {
	return f == "~" || strings.HasPrefix(f, "/") || strings.HasPrefix(f, "~/") ||
		strings.HasPrefix(f, "./") || strings.HasPrefix(f, "../")
}

// dropMarks removes the symbols terminal programs put in titles (spinners,
// stars, emoji), wherever they are, with the invisible characters that come with
// them: a variation selector would stay in the name, unseen, on the letter before.
func dropMarks(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Co, r) || unicode.Is(unicode.Sk, r) ||
			unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) {
			return -1
		}
		return r
	}, s)
}

// normalize is the shape of every name: lower case, letters of every script
// and digits, one "-" for each run of anything else. A "." stays between two
// characters of a word, so "app.log" and "v1.2" still read as what they are.
func normalize(s string) string {
	runes := []rune(s)
	var b strings.Builder
	dash := false
	for i, r := range runes {
		switch {
		// A combining mark belongs to the letter before it, and means
		// nothing after a symbol that was dropped.
		case unicode.IsMark(r):
			if !dash && b.Len() > 0 {
				b.WriteRune(r)
			}
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_':
			b.WriteRune(r)
			dash = false
		case r == '.' && i > 0 && i < len(runes)-1 && isWordRune(runes[i-1]) && isWordRune(runes[i+1]):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
		}
	}
	return strings.ToLower(strings.TrimRight(b.String(), "-_."))
}

// isWordRune reports whether a rune is part of a word.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// Fit cuts a name to MaxWidth columns and maxBytes bytes, at a word boundary
// when one leaves something to read, and never inside a character.
func Fit(s string, maxBytes int) string {
	if maxBytes > MaxBase {
		maxBytes = MaxBase
	}
	if len(s) <= maxBytes && ansi.StringWidth(s) <= MaxWidth {
		return s
	}
	// The longest prefix within both limits.
	end, width := 0, 0
	for i, r := range s {
		width += ansi.StringWidth(string(r))
		if i+utf8.RuneLen(r) > maxBytes || width > MaxWidth {
			break
		}
		end = i + utf8.RuneLen(r)
	}
	cut := s[:end]
	// Back to the last word's end, unless that throws away most of it: a
	// long first word is better cut than lost.
	if at := strings.LastIndexAny(cut, "-_"); at > 0 && ansi.StringWidth(cut[:at]) >= MaxWidth/2 {
		cut = cut[:at]
	}
	return strings.TrimRight(dropDangling(cut), "-_.")
}

// danglingWords are words that promise more after them. A name cut right after
// one reads as cut off ("disk-check-and").
var danglingWords = map[string]bool{
	"a": true, "an": true, "and": true, "at": true, "for": true, "from": true, "in": true, "of": true,
	"on": true, "or": true, "the": true, "to": true, "with": true,
}

// dropDangling removes such a word from the end of a name that was cut.
func dropDangling(s string) string {
	at := strings.LastIndexAny(s, "-_")
	if at > 0 && danglingWords[s[at+1:]] {
		return s[:at]
	}
	return s
}

// BaseBudget is the byte budget for a base name when room bytes are left for a
// whole name in the session's socket directory: enough for a stamp and a
// collision tail to fit as well. It never exceeds MaxBase.
func BaseBudget(room int) int {
	room -= tailLen
	if room > MaxBase {
		return MaxBase
	}
	if room < 1 {
		return 0
	}
	return room
}

// Desired is the full name for a base and a last-use time, in local time,
// which is how people remember when they last used something.
func Desired(base string, stamp Stamp, lastActive time.Time) string {
	switch stamp {
	case StampDate:
		return base + lastActive.Local().Format("_0102")
	case StampTime:
		return base + lastActive.Local().Format("_0102-1504")
	}
	return base
}

// Same reports whether a session already has the desired name, or the desired
// name with a collision tail. Without the tail check two sessions showing the
// same title would swap between -2 and -3 on every pass.
func Same(name, desired string) bool {
	if name == desired {
		return true
	}
	tail, ok := strings.CutPrefix(name, desired+"-")
	// A tail is 2 to 99. Anything longer is another name: "api_1104-0931" is
	// not "api_1104" with a tail, it is the stamp with the minute in it.
	if !ok || tail == "" || len(tail) > 2 {
		return false
	}
	for _, r := range tail {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
