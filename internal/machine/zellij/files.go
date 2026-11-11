package zellij

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// contractRe matches zellij's versioned data directories ("contract_version_1").
var contractRe = regexp.MustCompile(`^contract_version_(\d+)$`)

// versionDirRe is the data folder of a zellij before 0.44, named after its
// version ("0.43.1").
var versionDirRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

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
// zellij's cache directory (config.ZellijCacheDir). Before 0.44 it is
// <cache>/<version>/session_info; since then <cache>/contract_version_N/
// session_info, where zellij bumps N when the format changes. The socket
// paths of running servers (sockets) name the folders of the zellij versions
// that run, and the highest N among them wins: the zellij in use is the
// newest that runs, and a folder another zellij left, with a higher N and
// maybe sessions of the same names, would judge those sessions by stale
// files. Without a server to say, the highest N present wins. A socket in a
// folder named after a version, from a zellij before 0.44, names that folder
// when no socket names a contract.
func SessionInfoDir(zellijCache, version string, sockets ...string) string {
	base := zellijCache
	if d := DataDirName(version); !strings.HasPrefix(d, "contract_version_") {
		return filepath.Join(base, d, "session_info")
	}
	running, runningN, older := "", -1, ""
	for _, s := range sockets {
		d := filepath.Base(filepath.Dir(s))
		if m := contractRe.FindStringSubmatch(d); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > runningN {
				running, runningN = d, n
			}
		} else if versionDirRe.MatchString(d) {
			older = d
		}
	}
	switch {
	case running != "":
		return filepath.Join(base, running, "session_info")
	case older != "":
		return filepath.Join(base, older, "session_info")
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

// GOOS is the system the rules below follow; tests set it to check the others.
var GOOS = runtime.GOOS

// SocketDir is where zellij creates a session's socket, following zellij's own
// rule: $ZELLIJ_SOCKET_DIR, else $XDG_RUNTIME_DIR/zellij (Linux only), else
// <temp>/zellij-<uid>, or <temp>/zellij on Windows, which has no uids; each
// with the data directory (DataDirName) below it. Running servers name their
// socket on their command line, which is what the reaper trusts; this is only
// for sessions with no server, such as those `recover` repairs.
func SocketDir(lookup func(string) (string, bool), uid int, version string) string {
	sub := DataDirName(version)
	if v, ok := lookup("ZELLIJ_SOCKET_DIR"); ok && v != "" {
		return filepath.Join(v, sub)
	}
	switch GOOS {
	case "windows":
		return filepath.Join(os.TempDir(), "zellij", sub)
	case "linux":
		if v, ok := lookup("XDG_RUNTIME_DIR"); ok && v != "" {
			return filepath.Join(v, "zellij", sub)
		}
	}
	return filepath.Join(os.TempDir(), "zellij-"+strconv.Itoa(uid), sub)
}

// SocketsUnderTemp reports whether SocketDir's rule ends at the temporary
// directory: neither $ZELLIJ_SOCKET_DIR nor, on Linux, $XDG_RUNTIME_DIR says
// where the sockets go, so $TMPDIR decides it.
func SocketsUnderTemp(lookup func(string) (string, bool)) bool {
	if v, ok := lookup("ZELLIJ_SOCKET_DIR"); ok && v != "" {
		return false
	}
	if GOOS == "linux" {
		if v, ok := lookup("XDG_RUNTIME_DIR"); ok && v != "" {
			return false
		}
	}
	return true
}

// SocketPathLimit is the longest path a session's socket can have: on Linux
// sizeof(sun_path) is 108 and on macOS 104, one byte going to the terminating
// NUL. Windows names a pipe after the path and refuses a path of 256 or more.
var SocketPathLimit = socketPathLimit(GOOS)

// socketPathLimit is SocketPathLimit for one system.
func socketPathLimit(goos string) int {
	switch goos {
	case "darwin":
		return 103
	case "windows":
		return 255
	}
	return 107
}

// NameRoom is how many bytes a session's name can take in a socket directory
// before its socket path passes the limit. A name past it leaves a server no
// zellij command can reach.
func NameRoom(socketDir string) int {
	return SocketPathLimit - len(socketDir) - 1
}

// Sockets lists the names of the socket files in dir: one per session zellij
// serves there, whether it lists that session at this moment or not. On
// Windows they are plain files holding the server's process ID, next to the
// named pipes zellij really talks through.
func Sockets(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if IsSocket(e.Type()) {
			names = append(names, e.Name())
		}
	}
	return names
}

// IsSocket reports whether a file in the socket directory stands for a
// session: a Unix socket, or on Windows a regular file.
func IsSocket(mode os.FileMode) bool {
	if GOOS == "windows" {
		return mode.IsRegular()
	}
	return mode&os.ModeSocket != 0
}

// SocketTaken reports whether a file sits at dir/name. zellij moves a session's
// socket file to its new name on a rename, so a file there is another session's,
// whether zellij lists that session at this moment or not.
func SocketTaken(dir, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, name))
	return err == nil
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
	for line := range strings.SplitSeq(text, "\n") {
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
	for line := range strings.SplitSeq(text, "\n") {
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
	// Born is when Dir was made (Born), zero where the file system does not
	// say. zellij keeps a session's folder when it brings the session back,
	// so it can be older than the session's server.
	Born time.Time
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
	f.Born, _ = Born(f.Dir)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if info.ModTime().After(f.Newest) {
			f.Newest = info.ModTime()
		}
	}
	if raw, err := readAtMost(filepath.Join(f.Dir, "session-metadata.kdl"), maxSessionFile); err == nil {
		f.Metadata = ParseMetadata(string(raw))
	} else if errors.Is(err, errTooLarge) {
		return f, err
	}
	if raw, err := readAtMost(filepath.Join(f.Dir, "session-layout.kdl"), maxSessionFile); err == nil {
		f.Layout = ParseLayout(string(raw))
		f.HasLayout = true
	} else if errors.Is(err, errTooLarge) {
		return f, err
	}
	return f, nil
}

// maxSessionFile is the most of a session file read: zellij writes a few
// kilobytes for a session with many panes. A file grown past it is not read
// at all, so it cannot take the pass's memory with it; read in part, it would
// have lost the commands past the cut, a protected program among them.
const maxSessionFile = 4 << 20

// errTooLarge is a session file past maxSessionFile. ReadFiles gives it as
// its error, and a session whose files cannot be read is kept.
var errTooLarge = errors.New("larger than a session file can be")

// readAtMost reads the file at path when it holds no more than limit bytes.
func readAtMost(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// One byte more than the limit tells a file that is too large.
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes at most)", path, errTooLarge, limit)
	}
	return b, nil
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

// ErrRenameUnreachable is a rename refused on Windows. zellij there talks
// through a named pipe called after the session's first name, and a rename
// moves only the file that lists the session: measured on 0.45.1, every
// command for the new name then waits forever and the old name is refused, so
// the session can no longer be attached to, listed for clients or deleted.
var ErrRenameUnreachable = errors.New("zellij on Windows cannot reach a session after a rename: its pipe keeps the first name")

// RenamesReachable is whether zellij can still reach a session after a rename
// on this system: everywhere but on Windows. Tests set it apart from GOOS.
var RenamesReachable = GOOS != "windows"

// RenameReachable reports whether zellij can still reach a session after a
// rename on this system.
func RenameReachable() bool { return RenamesReachable }
