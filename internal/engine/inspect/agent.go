package inspect

import (
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/repo"
)

// A coding agent's session says little about what it works on: the title the
// agent sets is the topic its conversation began with, its program is the same
// in every session, and so is the directory it was started in, often a folder
// of many projects. The commands it runs say more: each runs in the directory
// of the project the agent is working on at that moment. AgentWork is what a
// survey sees of that, for a pass to name the session after the project (see
// reaper's project book).

// maxAgentProcs bounds how many processes under one agent a survey reads: an
// agent running a build can have many, and the newest few say where it works.
const maxAgentProcs = 512

// AgentWork is where a coding agent in a session works, as its processes show
// at one moment.
type AgentWork struct {
	// Program is the agent's name ("claude"), "" when no agent runs.
	Program string
	// ID tells this agent from one started later in the same session: its
	// pid and when it started. "" when its start cannot be read.
	ID string
	// Started is the repository of the directory the agent was started in,
	// "" when that is in none.
	Started string
	// Work lists the repositories the agent's commands run in now, newest
	// command first: each with when its newest command there started.
	Work []Sighting
}

// Sighting is a repository an agent's command runs in, and when that command
// started (procfs.StartMark).
type Sighting struct {
	Repo string
	Mark int64
}

// agentsAt finds the coding agent under each of the session's panes, the
// first under each, in the panes' order, and what each works on, each on its
// own: two agents at work in two projects would otherwise take the session's
// name back and forth between them. Two in one session are rare; a pass lets
// one speak for the session (reaper's project book).
func agentsAt(t *procfs.Table, panes []Pane, agents []string, repos *repo.Finder, root string) []AgentWork {
	var out []AgentWork
	for _, p := range panes {
		if a := findAgent(t, p.PID, agents); a != nil {
			out = append(out, workOf(t, a, agents, repos, root))
		}
	}
	return out
}

// workOf is where an agent works, as the commands it runs now show.
func workOf(t *procfs.Table, a *procfs.Proc, agents []string, repos *repo.Finder, root string) AgentWork {
	var w AgentWork
	w.Program = agentName(a, agents)
	// The agent as it was found names it: the process it may have started
	// again under itself comes and goes with what it does.
	if mark, ok := procfs.StartMarkOf(root, a); ok {
		w.ID = strconv.Itoa(a.PID) + ":" + strconv.FormatInt(mark, 10)
	}
	w.Started = repos.Root(t.Cwd(a.PID))
	own := settingsOf(root, a, repos.Homes(), t.Cwd(a.PID))
	a = innermost(t, a, w.Program)
	// newest holds, per repository, the start of the newest command there.
	newest := map[string]int64{}
	budget := maxAgentProcs
	// Each child of the agent's is a command it runs, or a helper it keeps
	// (a language server, a tool's server), and what runs under it is part
	// of it. A command counts from when it started: a loop or a watcher that
	// keeps starting processes is one command, however long it runs.
	for _, c := range newestFirst(t.Children(a.PID), root) {
		if budget <= 0 {
			break
		}
		// The command and what runs under it, within what is left to read.
		procs := append([]*procfs.Proc{c.proc}, t.Descendants(c.proc.PID, budget-1)...)
		budget -= len(procs)
		r := commandRepo(t, procs, repos, own)
		if r == "" {
			continue
		}
		if m, seen := newest[r]; !seen || c.mark > m {
			newest[r] = c.mark
		}
	}
	for r, m := range newest {
		w.Work = append(w.Work, Sighting{Repo: r, Mark: m})
	}
	// Newest first. Two commands that started at once keep an order that does
	// not change between runs.
	sort.Slice(w.Work, func(i, j int) bool {
		if w.Work[i].Mark != w.Work[j].Mark {
			return w.Work[i].Mark > w.Work[j].Mark
		}
		return w.Work[i].Repo < w.Work[j].Repo
	})
	return w
}

// command is a child of an agent's, and when it started.
type command struct {
	proc *procfs.Proc
	mark int64
}

// newestFirst orders an agent's children by when they started, the newest
// first, so a big old command, such as a build server with many workers,
// cannot take what a survey reads from the newer ones. A child whose start
// cannot be read cannot be told new from old, and is left out rather than
// guessed at.
func newestFirst(children []*procfs.Proc, root string) []command {
	var out []command
	for _, c := range children {
		// An agent with more children than a survey reads has a fork bomb
		// under it, not commands.
		if len(out) == maxAgentProcs {
			break
		}
		// A child that ended runs nowhere, and a pile of them must not take
		// the places of the commands.
		if c == nil || c.IsZombie() {
			continue
		}
		if mark, ok := procfs.StartMarkOf(root, c); ok {
			out = append(out, command{c, mark})
		}
	}
	// Equal starts keep the table's order, by pid.
	sort.SliceStable(out, func(i, j int) bool { return out[i].mark > out[j].mark })
	return out
}

// commandRepo is the repository a command works in: the one its processes'
// command lines name most (namedPaths), since a command may act on files away
// from where it runs (Claude Code runs a helper agent's commands where the
// agent stood when it started the helper, and they name the files they work on
// in full); else the one most of its processes run in.
//
// Only a path within the command's reach counts: under the home directory,
// under a directory one of its processes runs in, or beside the repository
// one runs in (in the folder that holds it, unless that is right under the
// root). A tool's files elsewhere say
// nothing of the work, and some are git checkouts: Homebrew's prefix on Apple
// silicon (/opt/homebrew) is one. Nor is a path on another disk looked up,
// which may be a network mount that hangs. A tie among the paths named goes to
// the one of them the processes run in; else a tie says nothing.
func commandRepo(t *procfs.Table, procs []*procfs.Proc, repos *repo.Finder, own []string) string {
	homes := repos.Homes()
	// reach holds the directories under which a named path counts.
	reach := map[string]bool{}
	for _, h := range homes {
		reach[h] = true
	}
	ran := map[string]int{}
	var live []*procfs.Proc
	for _, d := range procs {
		// A process that ended and waits to be collected runs nowhere.
		if d == nil || d.IsZombie() {
			continue
		}
		live = append(live, d)
		cwd := t.Cwd(d.PID)
		if cwd == "" {
			continue
		}
		reach[filepath.Clean(cwd)] = true
		if r := repos.Root(cwd); r != "" {
			ran[r]++
			// The repository's neighbours: a helper standing in one project
			// works on another beside it.
			if parent, ok := beside(r); ok {
				reach[parent] = true
			}
		}
	}
	// The root of the file system holds everything: it reaches nothing.
	delete(reach, string(filepath.Separator))
	named := map[string]int{}
	for _, d := range live {
		for _, p := range namedPaths(d.Argv, repos.Home, homes, own) {
			if !within(p, reach) {
				continue
			}
			if r := repos.Root(p); r != "" {
				named[r]++
			}
		}
	}
	if len(named) == 0 {
		r, _ := most(ran)
		return r
	}
	if r, ok := most(named); ok {
		return r
	}
	// A tie among the named: the one the command's processes run in.
	tied := map[string]int{}
	for r := range leaders(named) {
		tied[r] = ran[r]
	}
	r, _ := most(tied)
	return r
}

// beside is the folder that holds a repository, where its neighbours are, and
// false when that folder is right under the root (/opt, /srv, /home): such a
// folder holds unrelated things, Homebrew's prefix among them.
func beside(root string) (string, bool) {
	parent := filepath.Dir(root)
	return parent, strings.Count(filepath.ToSlash(parent), "/") >= 2
}

// within reports whether p is one of the directories in reach, or under one.
func within(p string, reach map[string]bool) bool {
	for d := p; ; d = path.Dir(d) {
		if reach[d] {
			return true
		}
		// The top: "/" for an absolute path.
		if d == path.Dir(d) {
			return false
		}
	}
}

// most is the key with the highest count, and false for none or a tie.
func most(counts map[string]int) (string, bool) {
	best, top, tie := "", 0, false
	for k, n := range counts {
		switch {
		case n > top:
			best, top, tie = k, n, false
		case n == top:
			tie = true
		}
	}
	if top == 0 || tie {
		return "", false
	}
	return best, true
}

// leaders are the keys that share the highest count.
func leaders(counts map[string]int) map[string]bool {
	top := 0
	for _, n := range counts {
		top = max(top, n)
	}
	out := map[string]bool{}
	for k, n := range counts {
		if n == top {
			out[k] = true
		}
	}
	return out
}

// maxNamedPaths bounds the paths read from one command line: a compiler can be
// given hundreds of files of one project, and the first say as much.
const maxNamedPaths = 32

// maxArgBytes bounds how much of one argument is read: a script handed to a
// shell whole can run to megabytes, and its paths of note come early.
const maxArgBytes = 64 << 10

// maxCodeDepth bounds how deep shell code inside shell code is read: Claude
// Code's commands are a bash -c string with an eval in it, two levels.
const maxCodeDepth = 4

// namedPaths are the absolute paths a command line names, "~/" ones too, but
// not the programs it runs: a program is no work, and a tool's own checkout
// would otherwise name every command (Homebrew's prefix on Apple silicon, a
// CLAUDE_CONFIG_DIR kept in git, whose shell snapshot Claude Code sources
// before each command). The command line is read as commands (commandPaths):
// the first word of each is its program, and so is the word after one that
// runs a program (sudo, env, timeout, xargs) and the script source and "."
// read; what eval and a shell's -c run is shell code, read the same way, so a
// quoted path with blanks in it is one path. Nor does a path in a bin folder
// count, where programs are installed (the script an interpreter runs, as in
// python3 /opt/homebrew/bin/pytest, or PATH=/x/bin). Any other argument is a
// word as it stands, blanks and all, split only at "=", ":" and ","
// (--dir=/x, PATH=/a:/b).
//
// The paths are a Unix command line's, read with slashes on every system
// (package path): Windows tells no process's directory, and renames nothing. A
// path in a hidden folder of the home directory (~/.claude, ~/.cache,
// ~/.config) is a tool's, not a project's, and is left out; homes holds the
// home as given and as its links resolve. So is a path in the agent's own
// settings folder (own), wherever it is kept: its hooks and its status line
// run from there, a CLAUDE_CONFIG_DIR in a dotfiles repository say.
func namedPaths(argv []string, home string, homes, own []string) []string {
	var out []string
	emit := func(word string) bool {
		// "~/" as the shell turns it into the home.
		if strings.HasPrefix(word, "~/") && home != "" {
			word = path.Join(home, word[2:])
		}
		// Only an absolute path names one place.
		if !strings.HasPrefix(word, "/") {
			return true
		}
		p := path.Clean(word)
		if inBin(p) || hiddenInHome(p, homes) || underAny(p, own) {
			return true
		}
		out = append(out, p)
		return len(out) < maxNamedPaths
	}
	// A process's command line is one command: its first word is the program.
	commandPaths(argv, 0, emit)
	return out
}

// shells run the code given with -c, as bash -c CODE and bash -lc CODE do, and
// the code a heredoc hands them.
var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "fish": true}

// runners are words that run the program after them, or come before a command
// in the shell's grammar: that program is no work either. source and "." run
// a script in the shell itself.
var runners = map[string]bool{
	"sudo": true, "doas": true, "env": true, "exec": true, "nohup": true, "nice": true, "time": true,
	"timeout": true, "xargs": true, "command": true, "builtin": true, "setsid": true, "stdbuf": true,
	"strace": true, "ltrace": true, "valgrind": true,
	"source": true, ".": true,
	"if": true, "then": true, "else": true, "elif": true, "do": true, "while": true, "until": true, "!": true,
}

// runnerValues are the options of a runner that take the next word as their
// value: sudo -u postgres, timeout -s KILL. That word is no program.
var runnerValues = map[string]map[string]bool{
	"sudo": {"-u": true, "-g": true, "-C": true, "-D": true, "-h": true, "-p": true, "-r": true, "-t": true, "-U": true, "-T": true,
		"--user": true, "--group": true, "--close-from": true, "--chdir": true, "--host": true, "--prompt": true, "--role": true,
		"--type": true, "--other-user": true, "--command-timeout": true, "--chroot": true},
	"doas":    {"-u": true, "-C": true},
	"env":     {"-u": true, "-C": true, "-S": true, "--unset": true, "--chdir": true, "--split-string": true},
	"timeout": {"-s": true, "-k": true, "--signal": true, "--kill-after": true},
	"nice":    {"-n": true, "--adjustment": true},
	"xargs": {"-I": true, "-L": true, "-n": true, "-P": true, "-d": true, "-E": true, "-s": true, "-a": true,
		"--max-args": true, "--max-procs": true, "--delimiter": true, "--max-chars": true, "--arg-file": true},
	"stdbuf":   {"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true},
	"strace":   {"-e": true, "-o": true, "-p": true, "-s": true, "-u": true, "--output": true, "--attach": true, "--user": true},
	"ltrace":   {"-e": true, "-o": true, "-p": true, "-s": true, "-u": true, "--output": true},
	"valgrind": {},
}

// execFlags are the options of find after which it runs a program.
var execFlags = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}

// numberRe is a number in a program's place: timeout's duration (10, 1.5m,
// .5). A digit is in it, so "." stays the shell's source.
var numberRe = regexp.MustCompile(`^([0-9][0-9.]*|\.[0-9]+)[smhd]?$`)

// shellValueFlags are a shell's options that take the next word as their
// value (bash -o pipefail -c CODE, as make's .SHELLFLAGS and just run their
// recipes): that word is no script.
var shellValueFlags = map[string]bool{"--rcfile": true, "--init-file": true}

// isShellValueFlag reports whether a shell's option takes the next word: -o,
// +o, -O, +O, a bundle that ends in one (-euo pipefail), --rcfile.
func isShellValueFlag(w string) bool {
	if shellValueFlags[w] {
		return true
	}
	return len(w) > 1 && (w[0] == '-' || w[0] == '+') && w[1] != '-' && strings.ContainsAny(w[len(w)-1:], "oO")
}

// commandPaths hands emit the paths in the words of one command, and reports
// false once emit wants no more.
func commandPaths(words []string, depth int, emit func(string) bool) bool {
	// program: the next word that can be a program is one. runner: the
	// runner that made it so, whose options may take a value (value: the
	// next word is one). shell: the program is a shell, which runs code given
	// with -c until its script is named. code: the next word that is no
	// option is shell code.
	program, value, shell, code := true, false, false, false
	runner := ""
	// shellValue: the next word is a shell option's value (-o pipefail).
	shellValue := false
	for _, w := range words {
		if len(w) > maxArgBytes {
			w = w[:maxArgBytes]
		}
		// A file a redirection names, never a program.
		if strings.HasPrefix(w, redirected) {
			if !argPaths(strings.TrimPrefix(w, redirected), depth, emit) {
				return false
			}
			continue
		}
		switch {
		case value:
			// An option's value (sudo -u postgres): no program, no path to
			// count either way.
			value = false
			continue
		case shellValue:
			shellValue = false
			continue
		case program && runnerValues[runner][w]:
			value = true
			continue
		case program && mayRun(w):
			name := path.Base(w)
			program, shell, code = runners[name], shells[name], name == "eval"
			runner = ""
			if program {
				runner = name
			}
			continue
		case code && !strings.HasPrefix(w, "-"):
			code = false
			if !codePaths(w, depth+1, emit) {
				return false
			}
			continue
		case shell && (isCodeFlag(w) || isShellValueFlag(w)):
			// -c gives code; -o pipefail a value; -euo pipefail -c both.
			code = code || isCodeFlag(w)
			shellValue = isShellValueFlag(w)
			continue
		case execFlags[w]:
			// find -exec PROGRAM {} ;
			program = true
			continue
		}
		// A shell's first word that is no option is its script: an option
		// after it is the script's (bash ./deploy.sh -c prod.yml).
		if shell && !strings.HasPrefix(w, "-") {
			shell = false
		}
		if !argPaths(w, depth, emit) {
			return false
		}
	}
	return true
}

// mayRun reports whether a word can stand in a program's place: not an
// option, an assignment before the command (X=1) or a number.
func mayRun(w string) bool {
	return w != "" && !strings.HasPrefix(w, "-") && !strings.HasPrefix(w, redirected) && !strings.Contains(w, "=") &&
		!numberRe.MatchString(w)
}

// isCodeFlag reports whether a shell's option word holds -c, alone or with
// others (-lc, -ec): the shell's first word that is no option is then code.
func isCodeFlag(w string) bool {
	return len(w) > 1 && w[0] == '-' && w[1] != '-' && strings.ContainsRune(w[1:], 'c')
}

// codePaths hands emit the paths in shell code, each of its commands read as
// commandPaths reads one, down to maxCodeDepth. A heredoc's lines are what the
// command reads: code for a shell (bash <<EOF), else words to look through, a
// script's or a file's (python3 - <<'EOF', cat >file <<EOF).
func codePaths(code string, depth int, emit func(string) bool) bool {
	if depth > maxCodeDepth {
		return true
	}
	for _, cmd := range shellCommands(code) {
		if cmd.body == "" {
			if !commandPaths(cmd.words, depth, emit) {
				return false
			}
			continue
		}
		if cmd.code {
			if !codePaths(cmd.body, depth+1, emit) {
				return false
			}
			continue
		}
		for _, w := range shellWords(cmd.body) {
			if !argPaths(w, depth+1, emit) {
				return false
			}
		}
	}
	return true
}

// isCode tells shell code from a plain word: the shell's operators and line
// breaks make code, and so do quotes, except in a path with no blank in it,
// which a quote can be part of (/srv/it's). A word the shell took the quotes
// off is plain, unless it is code handed on (the string an eval runs).
func isCode(arg string) bool {
	if strings.ContainsAny(arg, ";&|<>(){}`\n") {
		return true
	}
	if strings.ContainsAny(arg, `'"`) {
		return !isPathWord(arg) || strings.ContainsAny(arg, " \t")
	}
	return false
}

// isPathWord reports whether a word begins as a path the shell would read as
// one: absolute, or under the home.
func isPathWord(w string) bool {
	return strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~/")
}

// argPaths hands emit the words of one argument that may be paths, and
// reports false once emit wants no more.
func argPaths(arg string, depth int, emit func(string) bool) bool {
	// Shell code: its words, each read as an argument of its own, which may
	// be code again (the string an eval runs).
	if isCode(arg) {
		if depth >= maxCodeDepth {
			return true
		}
		for _, w := range shellWords(arg) {
			if !argPaths(w, depth+1, emit) {
				return false
			}
		}
		return true
	}
	for _, piece := range strings.FieldsFunc(arg, isListBreak) {
		// A path, blanks and all: an argument is a word as it stands.
		if isPathWord(piece) {
			if !emit(piece) {
				return false
			}
			continue
		}
		// Words with nothing but blanks between them, as in code that needs
		// no quotes (sh -c "ls /a /b").
		for _, w := range strings.Fields(piece) {
			if isPathWord(w) {
				if !emit(w) {
					return false
				}
			}
		}
	}
	return true
}

// isListBreak ends a path within a word: "=" and ":" (--dir=/x, PATH=/a:/b),
// and "," (--paths=/a,/b).
func isListBreak(r rune) bool {
	return r == '=' || r == ':' || r == ','
}

// redirected marks a word that is no program: the file a redirection names
// (> FILE, < FILE), or a value of an array (x=(a b)). No word of shell code
// holds this byte.
const redirected = "\x00"

// shellCommand is one command of shell code: its words, or the lines of a
// heredoc, which are code when the command that reads them is a shell.
type shellCommand struct {
	words []string
	body  string
	code  bool
}

// shellWords is the words of shell code, its commands run together, a
// heredoc's lines among them.
func shellWords(s string) []string { return wordsOf(s, 0) }

// wordsOf is shellWords, a heredoc's lines read down to maxCodeDepth: a
// heredoc in a heredoc's lines (a line of C++ with << in it) would otherwise
// make each line read again.
func wordsOf(s string, depth int) []string {
	var words []string
	for _, cmd := range shellCommands(s) {
		for _, w := range cmd.words {
			words = append(words, strings.TrimPrefix(w, redirected))
		}
		if cmd.body != "" && depth < maxCodeDepth {
			words = append(words, wordsOf(cmd.body, depth+1)...)
		}
	}
	return words
}

// heredoc is a heredoc whose lines come after the line its command is on.
type heredoc struct {
	delim string
	tabs  bool
	code  bool
}

// shellCommands splits shell code into commands, and each into words, as the
// shell does, with the quotes taken away: blanks end a word; ;, &, |, a line
// break and an unmatched ) end a command too; < and > end a word, and the word
// after one is a file (redirected). A part in brackets, braces or backquotes
// is commands of its own, after which the command around it goes on: its
// program, when the part stands in its place ($(which go) /b/x), is that part.
// A heredoc's lines, up to its word, are a command of their own (body), and
// an array (x=(a b)) stays in its word. Quotes keep what is in them together,
// a backslash outside single quotes takes the next character as it is, and a
// # that begins a word begins a comment. Claude Code hands eval its command in
// single quotes, each quote in it written '"'"': the command comes out as one
// word, to be read again.
func shellCommands(s string) []shellCommand {
	var done []shellCommand
	// frame is the part of the code being read: the command in it, and what
	// opened it ('(' '{' '`', or '[' for an array; 0 at the top).
	type frame struct {
		cmd    shellCommand
		opener byte
		// program is the program of the command in it, once a word told it;
		// cases counts the case statements open in it, whose patterns end in
		// a ) that closes nothing.
		program string
		cases   int
	}
	stack := []frame{{}}
	var cur strings.Builder
	// started is true once a word began: '' is a word, if an empty one.
	started, redir := false, false
	var pending []heredoc
	wantDelim, tabs := false, false
	top := func() *frame { return &stack[len(stack)-1] }
	end := func() {
		if !started {
			return
		}
		w := cur.String()
		cur.Reset()
		started = false
		if wantDelim {
			// The heredoc's word, quotes taken away; the command that reads
			// it is known now.
			pending = append(pending, heredoc{delim: w, tabs: tabs, code: shells[top().program]})
			wantDelim = false
			return
		}
		if redir {
			w, redir = redirected+w, false
		}
		f := top()
		// The command's program, once: past the words that run one.
		if f.program == "" && mayRun(w) && !runners[path.Base(w)] {
			f.program = path.Base(w)
		}
		// case and esac where a command starts.
		if len(f.cmd.words) == 0 {
			switch w {
			case "case":
				f.cases++
			case "esac":
				f.cases = max(0, f.cases-1)
			}
		}
		f.cmd.words = append(f.cmd.words, w)
	}
	finish := func() {
		end()
		if c := top().cmd; len(c.words) > 0 {
			done = append(done, c)
		}
		top().cmd = shellCommand{}
		top().program = ""
	}
	open := func(by byte, inPlace bool) {
		end()
		// >(cmd) and <(cmd) are commands, not files.
		redir = false
		if inPlace {
			// $(...) or `...` where a word goes: the command gets a word there,
			// so what follows is not taken for its program.
			top().cmd.words = append(top().cmd.words, "$(…)")
		}
		stack = append(stack, frame{opener: by})
	}
	closeAs := func(by byte) bool {
		if len(stack) == 1 || top().opener != by {
			return false
		}
		end()
		inner := *top()
		stack = stack[:len(stack)-1]
		if by == '[' {
			// An array's values are words of the command, none its program.
			for _, w := range inner.cmd.words {
				top().cmd.words = append(top().cmd.words, redirected+strings.TrimPrefix(w, redirected))
			}
			return true
		}
		if len(inner.cmd.words) > 0 {
			done = append(done, inner.cmd)
		}
		return true
	}
	// bodies reads the lines of the heredocs waiting for this line break, from
	// i, the line break, and returns where the last of them ends.
	bodies := func(i int) int {
		for _, h := range pending {
			var lines []string
			for {
				start := i + 1
				if start >= len(s) {
					i = len(s)
					break
				}
				stop := strings.IndexByte(s[start:], '\n')
				if stop < 0 {
					stop = len(s) - start
				}
				line := s[start : start+stop]
				i = start + stop
				check := line
				if h.tabs {
					check = strings.TrimLeft(line, "\t")
				}
				if check == h.delim {
					break
				}
				lines = append(lines, line)
			}
			done = append(done, shellCommand{body: strings.Join(lines, "\n"), code: h.code})
		}
		pending = nil
		return i
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			// Everything up to the next single quote, as it is.
			started = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i+1:])
				i = len(s)
				continue
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += 1 + j
		case c == '"':
			// Up to the next double quote; a backslash in it escapes only
			// what the shell lets it escape there.
			started = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
					// A backslash and a line break join two lines.
					if s[i] == '\n' {
						continue
					}
				}
				cur.WriteByte(s[i])
			}
		case c == '\\':
			// The next character as it is; a line break after it joins two
			// lines.
			if i+1 < len(s) {
				i++
				if s[i] != '\n' {
					started = true
					cur.WriteByte(s[i])
				}
			}
		case c == '#' && !started && (i == 0 || strings.IndexByte(" \t\n;&|()<>", s[i-1]) >= 0):
			// A comment, to the end of the line.
			if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
				i += j - 1
			} else {
				i = len(s)
			}
		case c == '<' && strings.HasPrefix(s[i:], "<<") && !strings.HasPrefix(s[i:], "<<<"):
			// A heredoc: its word comes next, its lines after this line.
			end()
			i++
			tabs = i+1 < len(s) && s[i+1] == '-'
			if tabs {
				i++
			}
			wantDelim = true
		case c == '<' || c == '>':
			// A redirection: >>, >|, >& (2>&1) and <<< are one, and the word
			// after it is a file.
			end()
			for i+1 < len(s) && strings.IndexByte("<>|&", s[i+1]) >= 0 {
				i++
			}
			redir = true
		case c == '&' && i+1 < len(s) && s[i+1] == '>':
			// &> and &>> write both outputs to a file, the word after.
			end()
			for i+1 < len(s) && s[i+1] == '>' {
				i++
			}
			redir = true
		case c == '(' && i+1 < len(s) && s[i+1] == '(':
			// (( ... )) and $(( ... )): arithmetic, no command and no file in
			// it, its << a shift.
			inPlace := started && strings.HasSuffix(cur.String(), "$")
			if inPlace {
				w := strings.TrimSuffix(cur.String(), "$")
				cur.Reset()
				cur.WriteString(w)
				started = w != ""
			}
			end()
			if inPlace {
				top().cmd.words = append(top().cmd.words, "$(…)")
			}
			depth := 0
			for i < len(s) {
				if s[i] == '(' {
					depth++
				} else if s[i] == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
				i++
			}
		case c == '(' && top().opener == '[':
			// A ( in an array's values is a character of a value.
			started = true
			cur.WriteByte(c)
		case c == '(':
			if w := cur.String(); started && strings.HasSuffix(w, "=") {
				// x=( ... ): an array, which stays in its word.
				end()
				stack = append(stack, frame{opener: '['})
				continue
			}
			// $( ... ): stands where a word goes; ( ... ) alone is a subshell.
			inPlace := started && strings.HasSuffix(cur.String(), "$")
			if inPlace {
				w := strings.TrimSuffix(cur.String(), "$")
				cur.Reset()
				cur.WriteString(w)
				started = w != ""
			}
			open('(', inPlace)
		case c == ')':
			// A case pattern's ) closes nothing: a command ends.
			if top().cases > 0 || (!closeAs('(') && !closeAs('[')) {
				finish()
			}
		case c == '{' && started && strings.HasSuffix(cur.String(), "$"):
			// ${ ... } stands where a word goes.
			w := strings.TrimSuffix(cur.String(), "$")
			cur.Reset()
			cur.WriteString(w)
			started = w != ""
			open('$', true)
		case c == '}' && closeAs('$'):
			// ${ ... } ends, right after its word: the command goes on.
		case c == '{' && !started && i+1 < len(s) && strings.IndexByte(" \t\n", s[i+1]) >= 0:
			// { ...; } is a group: { and a blank.
			open('{', false)
		case c == '}' && !started && closeAs('{'):
			// The group ends, its } a word of its own; the command around it
			// goes on. A } within a word ({}, {a,b}) is the word's.
		case c == '`':
			if !closeAs('`') {
				open('`', true)
			}
		case c == '\n':
			finish()
			if len(pending) > 0 {
				i = bodies(i)
			}
		case strings.IndexByte(";&|", c) >= 0:
			finish()
		case c == ' ' || c == '\t':
			end()
		default:
			started = true
			cur.WriteByte(c)
		}
	}
	// Whatever is still open ends with the code.
	for len(stack) > 1 {
		finish()
		inner := *top()
		stack = stack[:len(stack)-1]
		if inner.opener == '[' {
			continue
		}
		if len(inner.cmd.words) > 0 {
			done = append(done, inner.cmd)
		}
	}
	finish()
	if len(pending) > 0 {
		bodies(len(s) - 1)
	}
	return done
}

// inBin reports whether p is a folder programs are installed in, bin or sbin,
// or a file right in one. npm's .bin is not: it is in the project that
// installed it, and a program there names that project.
func inBin(p string) bool {
	for _, d := range []string{path.Base(p), path.Base(path.Dir(p))} {
		switch d {
		case "bin", "sbin":
			return true
		}
	}
	return false
}

// settingsVars name an agent's own settings folder when it is not the default
// one in the home (~/.claude, ~/.codex), which the home's hidden folders cover.
var settingsVars = []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME"}

// settingsOf is the agent's own settings folders, from the environment it
// started with; none when that cannot be read (Windows, another user's). A
// folder that holds the home, or the folder the agent was started in, holds
// projects too, and is left out of them: CLAUDE_CONFIG_DIR=$HOME would leave
// every project out.
func settingsOf(root string, a *procfs.Proc, homes []string, started string) []string {
	var out []string
	holds := func(dir, p string) bool {
		p = path.Clean(filepath.ToSlash(p))
		return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
	}
	for _, v := range settingsVars {
		dir, ok := procfs.EnvVar(root, a.PID, v)
		if !ok || !path.IsAbs(filepath.ToSlash(dir)) {
			continue
		}
		dir = path.Clean(filepath.ToSlash(dir))
		if dir == "/" || (started != "" && holds(dir, started)) || slices.ContainsFunc(homes, func(h string) bool { return holds(dir, h) }) {
			continue
		}
		out = append(out, dir)
	}
	return out
}

// underAny reports whether p is one of dirs, or under one.
func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// hiddenInHome reports whether p is in a hidden folder of the home directory,
// as given or as its links resolve.
func hiddenInHome(p string, homes []string) bool {
	for _, h := range homes {
		if strings.HasPrefix(p, path.Clean(filepath.ToSlash(h))+"/.") {
			return true
		}
	}
	return false
}

// maxRestarts bounds how far innermost goes down: a process tree has no
// loops, and the bound keeps a broken table from making one.
const maxRestarts = 8

// innermost is the process an agent's commands run under. An agent may start
// itself again as its only child, with other settings (Gemini CLI does, for
// more memory), or start its real program from a launcher of the same name;
// its commands then run under that child.
func innermost(t *procfs.Table, a *procfs.Proc, program string) *procfs.Proc {
	for range maxRestarts {
		var only *procfs.Proc
		n := 0
		for _, c := range t.Children(a.PID) {
			// A child that ended runs nothing.
			if c == nil || c.IsZombie() {
				continue
			}
			only, n = c, n+1
		}
		// With other children next to it, the agent runs its commands
		// itself, and a child of the same name is one of them.
		if n != 1 || agentName(only, []string{program}) != program {
			return a
		}
		a = only
	}
	return a
}

// findAgent is the first coding agent at or under a pane's process: the pane
// may run the agent itself, or a shell the agent was started from.
func findAgent(t *procfs.Table, pid int, agents []string) *procfs.Proc {
	if len(agents) == 0 {
		return nil
	}
	if p := t.ByPID[pid]; p != nil && isAgent(p, agents) {
		return p
	}
	// Nearest first: an agent's own commands may run another agent.
	for _, d := range t.Descendants(pid, maxAgentProcs) {
		if d != nil && isAgent(d, agents) {
			return d
		}
	}
	return nil
}

// isAgent reports whether a process is one of the agents.
func isAgent(p *procfs.Proc, agents []string) bool {
	return agentName(p, agents) != ""
}

// agentName is the agent a process is, "" when it is none: by its kernel name
// or its program, or, for an interpreter, by the script it runs (scriptIs).
// Another program's argument names nothing: "vim codex" edits a file.
func agentName(p *procfs.Proc, agents []string) string {
	script := ""
	if isInterpreter(p.Name()) {
		script = scriptOf(p.Argv)
	}
	for _, a := range agents {
		if p.Comm == a || p.Name() == a || (script != "" && scriptIs(script, a)) {
			return a
		}
	}
	return ""
}

// scriptIs reports whether a script is the agent: by its file's name, without
// what follows a dot ("/usr/local/bin/claude", ".../bin/codex.js", the module
// "aider.main"), or by the package it is installed in (".../node_modules/
// @anthropic-ai/claude-code/cli.js"). A folder of someone's own with the
// agent's name in it ("~/claude-workspace/server.py") names nothing.
func scriptIs(script, agent string) bool {
	parts := strings.FieldsFunc(script, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return false
	}
	file := parts[len(parts)-1]
	if i := strings.IndexByte(file, '.'); i > 0 {
		file = file[:i]
	}
	if file == agent {
		return true
	}
	// The package is the folder after the last node_modules, past its
	// @scope.
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] != "node_modules" {
			continue
		}
		// The package's own folder may be the script: node runs its main.
		pkg := parts[i+1]
		if strings.HasPrefix(pkg, "@") && i+2 < len(parts) {
			pkg = parts[i+2]
		}
		return wordMatch(pkg, agent)
	}
	return false
}

// isInterpreter reports whether a program runs a script it is given: a coding
// agent written in JavaScript or Python runs as one.
func isInterpreter(name string) bool {
	switch name {
	case "node", "nodejs", "bun", "deno", "sh", "bash", "dash", "zsh", "ruby", "perl":
		return true
	}
	return strings.HasPrefix(name, "python")
}

// scriptOf is the script an interpreter's command line runs: the first word
// after the interpreter's long options ("node --max-old-space-size=8192
// /usr/bin/gemini"), or the module of "python3 -m aider". Another short
// option may give code to run in place of a script ("sh -c ..."), and then
// there is none.
func scriptOf(argv []string) string {
	for i := 1; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "-m" && i+1 < len(argv):
			return argv[i+1]
		case strings.HasPrefix(a, "--"):
			continue
		case strings.HasPrefix(a, "-"):
			return ""
		default:
			return a
		}
	}
	return ""
}
