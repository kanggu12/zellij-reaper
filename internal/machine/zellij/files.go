package zellij

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// contractRe matches zellij's versioned data directories ("contract_version_1").
var contractRe = regexp.MustCompile(`^contract_version_(\d+)$`)

// DataDirName is the directory zellij names its sockets and session data
// after: "contract_version_1" since 0.44, the version itself ("0.43.1") before.
// An unknown version is taken to be a current one.
func DataDirName(version string) string {
	var major, minor int
	if _, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err == nil && major == 0 && minor < 44 {
		return version
	}
	return "contract_version_1"
}

// SessionInfoDir finds the directory with one subdirectory per session, under
// the user's cache directory. Before 0.44 it is <cache>/zellij/<version>/
// session_info; since then <cache>/zellij/contract_version_N/session_info,
// where zellij bumps N when the format changes, so the highest N present wins.
func SessionInfoDir(cacheDir, version string) string {
	base := filepath.Join(cacheDir, "zellij")
	if d := DataDirName(version); !strings.HasPrefix(d, "contract_version_") {
		return filepath.Join(base, d, "session_info")
	}
	best, bestN := "", -1
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		m := contractRe.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if n > bestN {
			best, bestN = e.Name(), n
		}
	}
	if best == "" {
		best = "contract_version_1"
	}
	return filepath.Join(base, best, "session_info")
}

// SocketDir is where zellij creates a session's socket, following zellij's own
// rule: $ZELLIJ_SOCKET_DIR, else $XDG_RUNTIME_DIR/zellij, else /tmp/zellij-<uid>,
// each with the data directory (DataDirName) below it. Running servers name
// their socket on their command line, which is what the reaper trusts; this is
// only for sessions with no server, such as those `recover` repairs.
func SocketDir(lookup func(string) (string, bool), uid int, version string) string {
	sub := DataDirName(version)
	if v, ok := lookup("ZELLIJ_SOCKET_DIR"); ok && v != "" {
		return filepath.Join(v, sub)
	}
	if v, ok := lookup("XDG_RUNTIME_DIR"); ok && v != "" {
		return filepath.Join(v, "zellij", sub)
	}
	return filepath.Join(os.TempDir(), "zellij-"+strconv.Itoa(uid), sub)
}

// SocketPathLimit is the longest path a Unix socket can have on Linux
// (sizeof(sun_path) is 108, one byte goes to the terminating NUL).
const SocketPathLimit = 107

// NameRoom is how many bytes a session's name can take in a socket directory
// before its socket path passes the limit. A name past it leaves a server no
// zellij command can reach.
func NameRoom(socketDir string) int {
	return SocketPathLimit - len(socketDir) - 1
}

// PaneMeta is one pane as session-metadata.kdl describes it.
type PaneMeta struct {
	// Title is the pane's title as zellij shows it.
	Title string
	// Plugin is true for plugin panes (tab bar, status bar, ...).
	Plugin bool
	// Exited is true for a command pane whose command has finished.
	Exited bool
	// Focused is true for the pane that has the focus in its tab, and Tab is
	// the position of that tab.
	Focused bool
	Tab     int
}

// Metadata is what the reaper reads from session-metadata.kdl.
type Metadata struct {
	// ConnectedClients is zellij's own count of attached clients.
	ConnectedClients int
	// HasClientCount is false when the file had no connected_clients line,
	// so 0 is not mistaken for "nobody attached".
	HasClientCount bool
	// WebClients is the number of browser clients (zellij's web client).
	WebClients int
	// Panes lists every pane, plugins included, in the file's order.
	Panes []PaneMeta
	// ActiveTab is the position of the tab a client looks at, -1 when no tab
	// is marked active, as when nobody is attached.
	ActiveTab int
}

// ParseMetadata reads session-metadata.kdl line by line. zellij writes it in
// a fixed shape (top-level keys at column 0, panes four spaces in, their
// fields eight in), and only a few fields matter here, so a full KDL parser
// would add a dependency without making the reading safer.
func ParseMetadata(text string) Metadata {
	md := Metadata{ActiveTab: -1}
	var cur *PaneMeta
	// tabPos is the position of the tab block being read, -1 outside one.
	tabPos := -1
	for _, line := range strings.Split(text, "\n") {
		switch {
		// The client count is a top-level node.
		case strings.HasPrefix(line, "connected_clients "):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "connected_clients "))); err == nil {
				md.ConnectedClients, md.HasClientCount = n, true
			}
		case strings.HasPrefix(line, "web_client_count "):
			md.WebClients, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "web_client_count ")))
		// A tab block opens four spaces in, like a pane block.
		case line == "    tab {":
			tabPos = 0
		// A pane block opens four spaces in.
		case strings.HasPrefix(line, "    pane ") || line == "    pane {":
			md.Panes = append(md.Panes, PaneMeta{})
			cur = &md.Panes[len(md.Panes)-1]
		// ... and either closes at the same depth.
		case line == "    }":
			cur, tabPos = nil, -1
		// The position comes before "active" in the file.
		case tabPos >= 0:
			field := strings.TrimSpace(line)
			if v, ok := strings.CutPrefix(field, "position "); ok {
				tabPos, _ = strconv.Atoi(v)
			}
			if field == "active true" {
				md.ActiveTab = tabPos
			}
		case cur != nil:
			field := strings.TrimSpace(line)
			switch {
			case field == "is_plugin true":
				cur.Plugin = true
			case field == "exited true":
				cur.Exited = true
			case field == "is_focused true":
				cur.Focused = true
			case strings.HasPrefix(field, "tab_position "):
				cur.Tab, _ = strconv.Atoi(strings.TrimPrefix(field, "tab_position "))
			case strings.HasPrefix(field, "title "):
				if s, ok := unquoteKDL(strings.TrimPrefix(field, "title ")); ok {
					cur.Title = s
				}
			}
		}
	}
	return md
}

// TerminalTitles returns the titles of the panes that are not plugins.
func (m Metadata) TerminalTitles() []string {
	var t []string
	for _, p := range m.Panes {
		if !p.Plugin && p.Title != "" {
			t = append(t, p.Title)
		}
	}
	return t
}

// TitlesByFocus returns the titles of the panes still running, the ones a person
// was looking at first: the focused pane of the tab a client looks at, then the
// focused pane of each other tab, then the rest. Once the last client leaves,
// zellij marks no tab active, so a pass starts from the first tab's focused pane.
// It is what a session is named after; the log keeps TerminalTitles, whose order
// does not move with the focus.
func (m Metadata) TitlesByFocus() []string {
	var shown, focused, rest []string
	for _, p := range m.Panes {
		// A plugin says nothing about the session, and a command pane whose
		// command has finished only says what used to run there.
		if p.Plugin || p.Exited || p.Title == "" {
			continue
		}
		switch {
		case p.Focused && p.Tab == m.ActiveTab:
			shown = append(shown, p.Title)
		case p.Focused:
			focused = append(focused, p.Title)
		default:
			rest = append(rest, p.Title)
		}
	}
	return append(append(shown, focused...), rest...)
}

// Layout is what the reaper reads from session-layout.kdl, the file zellij
// writes so an exited session can be resurrected.
type Layout struct {
	// Commands are the command="..." values of command panes.
	Commands []string
	// Args are the arguments given to those commands, one list per args line.
	Args [][]string
	// Cwd is the directory the session was started in.
	Cwd string
}

// commandRe finds command="..." on a pane line.
var commandRe = regexp.MustCompile(`\bcommand=("(?:[^"\\]|\\.)*")`)

// quotedRe finds every quoted string on a line.
var quotedRe = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// ParseLayout reads session-layout.kdl. Template blocks (new_tab_template,
// swap layouts) are read too: a command named there is one the user chose to
// run in this session, which errs on the side of keeping it.
func ParseLayout(text string) Layout {
	var l Layout
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		// command="..." can sit among other properties on a pane line.
		for _, m := range commandRe.FindAllStringSubmatch(line, -1) {
			if s, ok := unquoteKDL(m[1]); ok {
				l.Commands = append(l.Commands, s)
			}
		}
		// args "a" "b" lists a command's arguments.
		if strings.HasPrefix(trimmed, "args ") {
			var args []string
			for _, q := range quotedRe.FindAllString(trimmed, -1) {
				if s, ok := unquoteKDL(q); ok {
					args = append(args, s)
				}
			}
			l.Args = append(l.Args, args)
		}
		// The session's own cwd is the first one, four spaces in.
		if l.Cwd == "" && strings.HasPrefix(line, "    cwd ") {
			if s, ok := unquoteKDL(strings.TrimPrefix(line, "    cwd ")); ok {
				l.Cwd = s
			}
		}
	}
	return l
}

// unquoteKDL decodes one KDL string: "..." with escapes, or a raw string
// r"..." / r#"..."#. It returns false for anything else.
func unquoteKDL(s string) (string, bool) {
	s = strings.TrimSpace(s)
	// Raw strings keep their contents as written: r"..." or r#"..."# in KDL 1,
	// #"..."# in KDL 2. The number of #s after the quote must match the start.
	if strings.HasPrefix(s, "r") || strings.HasPrefix(s, "#") {
		rest := strings.TrimPrefix(s, "r")
		// Count the opening #s, then expect the opening quote.
		n := len(rest) - len(strings.TrimLeft(rest, "#"))
		rest = rest[n:]
		closing := `"` + strings.Repeat("#", n)
		if len(rest) >= 1+len(closing) && rest[0] == '"' && strings.HasSuffix(rest, closing) {
			return rest[1 : len(rest)-len(closing)], true
		}
		return "", false
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' || i+1 == len(body) {
			b.WriteByte(c)
			continue
		}
		i++
		switch body[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			// \u{1F600}: up to six hex digits in braces.
			end := strings.IndexByte(body[i:], '}')
			if i+1 < len(body) && body[i+1] == '{' && end > 1 {
				if r, err := strconv.ParseUint(body[i+2:i+end], 16, 32); err == nil {
					b.WriteRune(rune(r))
					i += end
					continue
				}
			}
			b.WriteString(`\u`)
		default:
			// \" \\ \/ and anything unknown: the character itself.
			b.WriteByte(body[i])
		}
	}
	return b.String(), true
}

// Files is a session's directory under session_info.
type Files struct {
	// Dir is the directory; Exists is false when zellij has none for it.
	Dir    string
	Exists bool
	// Metadata and Layout are parsed from their files (zero if missing).
	Metadata  Metadata
	Layout    Layout
	HasLayout bool
	// Newest is the latest modification time of any file in Dir.
	Newest time.Time
}

// ReadFiles reads a session's directory. A missing directory is reported
// through Exists, not as an error, because zellij can list a session a moment
// before its directory appears.
func ReadFiles(infoDir, name string) (Files, error) {
	f := Files{Dir: filepath.Join(infoDir, name)}
	entries, err := os.ReadDir(f.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	f.Exists = true
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if info.ModTime().After(f.Newest) {
			f.Newest = info.ModTime()
		}
	}
	if raw, err := os.ReadFile(filepath.Join(f.Dir, "session-metadata.kdl")); err == nil {
		f.Metadata = ParseMetadata(string(raw))
	}
	if raw, err := os.ReadFile(filepath.Join(f.Dir, "session-layout.kdl")); err == nil {
		f.Layout = ParseLayout(string(raw))
		f.HasLayout = true
	}
	return f, nil
}

// SessionDirs lists the names that have a directory under session_info,
// sorted, for `recover`, which also looks at sessions zellij no longer lists.
func SessionDirs(infoDir string) []string {
	entries, _ := os.ReadDir(infoDir)
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
