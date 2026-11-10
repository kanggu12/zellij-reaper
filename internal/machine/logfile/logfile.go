// Package logfile writes the decision log, keeps it bounded, and reads it back
// for `logs` and the TUI.
//
// Every line is "[YYYY-MM-DD HH:MM:SS] message". One pass is a block that
// starts with "=== reaper start" and ends with "=== reaper end".
package logfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/core/visible"
)

// Log appends lines to a file.
type Log struct {
	// Path is the log file; Path + ".1" is the previous one.
	Path string
	// MaxBytes is the size past which Rotate moves the file aside; 0 never.
	MaxBytes int64
	// Now is the clock for timestamps (tests fix it).
	Now func() time.Time
	// held, while not nil, keeps the lines Printf is given instead of
	// writing them (see Hold).
	held *[]string
}

// Hold keeps the lines Printf is given in memory, with their times, until Take
// hands them over. A names pass holds its lines and writes them only when it
// renamed something.
func (l *Log) Hold() { l.held = &[]string{} }

// Take returns the held lines and writes to the file again from then on.
func (l *Log) Take() []string {
	if l.held == nil {
		return nil
	}
	lines := *l.held
	l.held = nil
	return lines
}

// Write appends lines Take returned, as they were.
func (l *Log) Write(lines []string) error {
	for _, s := range lines {
		if err := l.append(s); err != nil {
			return err
		}
	}
	return nil
}

// New returns a log at path that rotates past maxBytes.
func New(path string, maxBytes int64) *Log {
	return &Log{Path: path, MaxBytes: maxBytes, Now: time.Now}
}

// Rotate moves the log to Path.1 when it has grown past MaxBytes, replacing
// the previous copy, so the two together stay near twice the limit. It is
// called once at the start of a pass, under the pass lock, so a whole pass
// always lands in one file. A rename that fails is reported in the log itself
// and does not stop the pass.
func (l *Log) Rotate() {
	if l.MaxBytes <= 0 {
		return
	}
	info, err := os.Stat(l.Path)
	if err != nil || info.Size() <= l.MaxBytes {
		return
	}
	// os.Rename replaces a file but refuses to replace a directory, so a
	// stray directory at .1 is reported instead of swallowing the log.
	if err := os.Rename(l.Path, l.Path+".1"); err != nil {
		l.Printf("could not rotate %s (%d bytes) to %s.1: %v", l.Path, info.Size(), l.Path, err)
	}
}

// Printf appends one timestamped line. The file is opened for every line, so
// a rotation by another pass is picked up at once and nothing holds the file
// open between passes.
func (l *Log) Printf(format string, args ...any) error {
	// One line each: a line break in a message becomes a space. Names,
	// titles and command lines come from other programs, and their control
	// characters are written out, so that reading the log in a terminal runs
	// none of them.
	msg := visible.Text(lineBreaks.Replace(fmt.Sprintf(format, args...)))
	line := fmt.Sprintf("[%s] %s\n", l.Now().Format("2006-01-02 15:04:05"), msg)
	if l.held != nil {
		*l.held = append(*l.held, line)
		return nil
	}
	return l.append(line)
}

// lineBreaks puts a message on one line.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ")

// append writes one whole line to the file.
func (l *Log) append(line string) error {
	// The log holds pane titles and command lines: for this user only.
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Releases before 0.13 made it 0644. That mode, and only that one, is
	// theirs: one the user chose is left as it is.
	if info, err := f.Stat(); err == nil && info.Mode().Perm() == 0o644 {
		f.Chmod(0o600)
	}
	// One write per line keeps lines whole even if two writers interleave.
	_, err = f.WriteString(line)
	return err
}

// Writable says why a line could not be added to the log at path, or nil. It
// writes nothing: a log that is there is opened for appending and closed, and
// for one not made yet, the nearest directory that is there is checked, as a
// pass makes the ones in between. Either way the file system has to have room
// left for a pass's lines: a full one takes the open, and no line after it.
func Writable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err == nil {
		f.Close()
		return roomIn(filepath.Dir(path))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for dir := filepath.Dir(path); ; {
		info, err := os.Stat(dir)
		switch {
		case err == nil && !info.IsDir():
			return fmt.Errorf("%s is not a directory", dir)
		case err == nil:
			if err := dirWritable(dir); err != nil {
				return err
			}
			return roomIn(dir)
		case !errors.Is(err, os.ErrNotExist):
			return err
		}
		// Gone too: the one above it decides.
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// minRoom is the least free space a pass's lines need: a pass over a hundred
// sessions writes some 20 KiB.
const minRoom = 64 << 10

// freeBytes is how much the file system a directory is on lets this user
// write; a variable, so tests can fill a disk.
var freeBytes = spaceLeft

// roomIn says when the file system dir is on has too little room left for a
// pass's lines, or nil. Where the space cannot be read, there is taken to be
// room: the open above already said what it could.
func roomIn(dir string) error {
	n, err := freeBytes(dir)
	if err != nil || n >= minRoom {
		return nil
	}
	return fmt.Errorf("only %d bytes left on the file system %s is on", n, dir)
}

// Line is one parsed log line.
type Line struct {
	// Time is the timestamp (zero if the line had none).
	Time time.Time
	// Text is everything after the timestamp.
	Text string
}

// Parse splits a raw log line into its timestamp and text.
func Parse(raw string) Line {
	if len(raw) > 22 && raw[0] == '[' && raw[20] == ']' {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw[1:20], time.Local); err == nil {
			return Line{Time: t, Text: strings.TrimPrefix(raw[21:], " ")}
		}
	}
	return Line{Text: raw}
}

// Kind classifies a line for coloring: "start", "end", "reap", "skip",
// "deleted", "renamed", "kept", "failed", "dry", or "" for anything else.
func (l Line) Kind() string {
	t := l.Text
	switch {
	case strings.HasPrefix(t, "=== reaper start"):
		return "start"
	case strings.HasPrefix(t, "=== reaper end"):
		return "end"
	case strings.Contains(t, ":: REAP"):
		return "reap"
	case strings.Contains(t, ":: SKIP"):
		return "skip"
	case strings.HasPrefix(t, "  -> deleted"):
		return "deleted"
	case strings.HasPrefix(t, "  -> renamed"):
		return "renamed"
	// A session kept at the last moment (or kept its name): the safety
	// check working, not a failure, even when its reason says "could not".
	// A server recover kept on its second look is the same.
	case strings.HasPrefix(t, "  -> kept:") || strings.HasPrefix(t, "  -> kept name:") || strings.Contains(t, " :: recover: kept orphan server: "):
		return "kept"
	case strings.HasPrefix(t, "  -> DRY RUN") || strings.HasPrefix(t, "  -> DRY_RUN"):
		return "dry"
	case strings.Contains(t, "FAILED") || strings.Contains(t, "failed") || strings.Contains(t, "could not"):
		return "failed"
	}
	return ""
}

// Pass is what the log says about a pass: when it started and how it ended.
type Pass struct {
	// Found is whether the lines hold a start line at all.
	Found bool
	// Started is the time on its start line.
	Started time.Time
	// Ended is whether an end line follows the start line. A pass without one
	// is running, or was stopped from outside before it could write it.
	Ended bool
	// Acted is whether its end line has counts: a pass that stopped before it
	// acted has none.
	Acted bool
	// Reaped, Kept and Renamed are the end line's counts.
	Reaped, Kept, Renamed int
	// Failed is how many of its deletes zellij refused; those sessions are
	// among the kept.
	Failed int
	// Stopped is why a pass that did not act stopped: the line it wrote
	// before its end.
	Stopped string
}

// endCounts reads the counts off the end line of a pass that acted. Logs
// from before renames end with two counts.
var endCounts = regexp.MustCompile(`^=== reaper end \((\d+) reaped, (\d+) kept(?:, (\d+) renamed)?\) ===$`)

// LastPass is the last pass in lines, oldest first, as Tail returns them. A
// names pass, which writes only when it renames, is not one: its block has
// lines of its own.
func LastPass(lines []string) Pass {
	var p Pass
	// prev is the text of the line before, which says why a pass stopped.
	prev := ""
	for _, raw := range lines {
		l := Parse(raw)
		switch {
		case strings.HasPrefix(l.Text, "=== reaper start"):
			p = Pass{Found: true, Started: l.Time}
		// A delete zellij refused, in the pass that is still open. A rename
		// it refused is "  -> rename FAILED", and is no failure of the pass.
		case strings.HasPrefix(l.Text, "  -> FAILED: ") && p.Found && !p.Ended:
			p.Failed++
		case strings.HasPrefix(l.Text, "=== reaper end") && p.Found && !p.Ended:
			p.Ended = true
			if m := endCounts.FindStringSubmatch(l.Text); m != nil {
				p.Acted = true
				p.Reaped, _ = strconv.Atoi(m[1])
				p.Kept, _ = strconv.Atoi(m[2])
				// An empty third count is no renames.
				p.Renamed, _ = strconv.Atoi(m[3])
			} else if !strings.HasPrefix(prev, "=== reaper start") {
				p.Stopped = prev
			}
		}
		prev = l.Text
	}
	return p
}

// tailWindow is how much of the end of a log file Tail reads.
const tailWindow = 4 << 20

// Tail returns up to n of the last lines across Path.1 and Path, oldest first.
// Reading whole files is fine: the log is bounded by MaxBytes.
func Tail(path string, n int) ([]string, error) {
	var lines []string
	found := false
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = true
		// Read at most the last 4 MiB of a file: enough for any n shown on a
		// screen, and bounded even with rotation turned off.
		r := bufio.NewReader(f)
		if info, err := f.Stat(); err == nil && info.Size() > tailWindow {
			f.Seek(info.Size()-tailWindow, io.SeekStart)
			r.Reset(f)
			// The first line read from the middle is partial: drop it.
			r.ReadString('\n')
		}
		sc := bufio.NewScanner(r)
		// Session names and pane titles can make a line long; allow 1 MiB.
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	// A count below zero asks for no lines, like zero, instead of cutting
	// past the end of the slice.
	n = max(n, 0)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// Follow writes new lines of path to w as they are appended, and keeps going
// across rotations, until stop is closed. It starts at the end of the file.
func Follow(path string, w io.Writer, stop <-chan struct{}) error {
	var off int64
	var ino uint64
	if info, err := os.Stat(path); err == nil {
		off, ino = info.Size(), inode(path, info)
	}
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-tick.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		// A new inode means the log was rotated: read the new file from 0.
		if i := inode(path, info); i != ino {
			ino, off = i, 0
		}
		if info.Size() <= off {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		if _, err := f.Seek(off, io.SeekStart); err == nil {
			n, _ := io.Copy(w, f)
			off += n
		}
		f.Close()
	}
}
