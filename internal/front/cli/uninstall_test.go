package cli

// What uninstall says and what a purge does to the shells' startup files. No
// test here reaches a real timer: the scheduler is a fake that hands back a
// result, and every startup file is under a temporary home.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/kanggu12/zellij-reaper/internal/app"
	"github.com/kanggu12/zellij-reaper/internal/engine/timers"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// uninstalled is a scheduler whose uninstall did what res says and ended in
// err. Nothing else of it is asked for here.
type uninstalled struct {
	timers.Systemd
	res timers.Result
	err error
}

func (u uninstalled) Uninstall(bool, string) (timers.Result, error) { return u.res, u.err }

// skipAsRoot skips a test about a file the user may not write: root may write
// every file, so the case does not come up for it.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only file all the same")
	}
}

// nothing reports whether removeHooks had nothing to say.
func (u unhooked) nothing() bool { return len(u.removed)+len(u.stays)+len(u.failed) == 0 }

func TestPurgeListsWhatWentThenWhatThereIsToSay(t *testing.T) {
	home := isolate(t)
	env := config.MapEnv{"HOME": home}
	a := fakeApp(t, env)
	// bash's startup file as setup left it, zsh's with a line somebody wrote.
	bashrc, hook := shellHook("bash", home)
	if err := appendLine(bashrc, hook); err != nil {
		t.Fatal(err)
	}
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte(`eval "$(zellij-reaper shell-hook zsh)"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	settings, dir := config.ConfigFile(env), config.ConfigDir(env)
	a.System = uninstalled{res: timers.Result{
		Removed:  []string{timers.CronJobs, settings},
		Timers:   1,
		Warnings: []string{"could not remove /x/reaper.log: permission denied"},
		Left:     []timers.Leftover{{Dir: dir, Names: []string{"a", "b", "c", "d", "e"}}},
	}}
	exe, err := app.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	if err := uninstall(cmd, a, true, errors.New("the settings do not parse")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// Every ✓ before every !, a blank line, then the words that close it.
	want := "  ✓ removed the zellij-reaper lines in your crontab\n" +
		"  ✓ removed " + tilde(settings) + "\n" +
		"  ✓ prompt hook removed from " + tilde(bashrc) + "\n" +
		"  ! the settings did not load, so only the log at its default place is removed: one that `log.path` put elsewhere stays\n" +
		"  ! could not remove /x/reaper.log: permission denied\n" +
		"  ! left " + tilde(dir) + " in place: it still holds a, b, c and 2 more\n" +
		"  ! " + tilde(zshrc) + " still mentions `zellij-reaper shell-hook` in a line setup did not write: remove it by hand\n" +
		"\n" +
		"Removed the timers.\n" +
		"The program itself stays: delete " + tilde(exe) + ", or remove its package.\n"
	if got := out.String(); got != want {
		t.Errorf("purge printed:\n%s\nwant:\n%s", got, want)
	}
	// Paths under the home read from "~" on, where the home can be told.
	if runtime.GOOS != "windows" && !strings.Contains(out.String(), "prompt hook removed from ~"+string(filepath.Separator)+".bashrc\n") {
		t.Errorf("the startup file is not shortened:\n%s", out)
	}
	// bash's line is gone, the one written by hand is as it was.
	if hasHook(bashrc) || !hasHook(zshrc) {
		t.Errorf("hook in %s: %v, in %s: %v", bashrc, hasHook(bashrc), zshrc, hasHook(zshrc))
	}
}

func TestUninstallWithoutPurgeLeavesTheStartupFilesAlone(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	bashrc, hook := shellHook("bash", home)
	if err := appendLine(bashrc, hook); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", timers.TimerName)
	a.System = uninstalled{res: timers.Result{Removed: []string{unit}, Timers: 1}}
	cmd, out := output()
	// Settings that did not load matter to a purge only: nothing here removes
	// a log, so there is nothing to say about where it was looked for.
	if err := uninstall(cmd, a, false, errors.New("the settings do not parse")); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "  ✓ removed " + tilde(unit) + "\n" +
		"\n" +
		"Removed the timers.\n" +
		"Kept the settings and the log; `uninstall --purge` removes them too.\n"
	if got := out.String(); got != want {
		t.Errorf("uninstall printed:\n%s\nwant:\n%s", got, want)
	}
	if b, _ := os.ReadFile(bashrc); string(b) != hook+"\n" {
		t.Errorf("the startup file after an uninstall without --purge:\n%s", b)
	}
}

func TestUninstallWithNothingToListStartsWithItsClosingWords(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	a.System = uninstalled{}
	cmd, out := output()
	if err := uninstall(cmd, a, false, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// No blank line with no list above it.
	want := "Found no timers to remove.\n" +
		"Kept the settings and the log; `uninstall --purge` removes them too.\n"
	if got := out.String(); got != want {
		t.Errorf("uninstall printed:\n%s\nwant:\n%s", got, want)
	}
}

func TestUninstallThatStopsHalfwayLeavesTheHook(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	bashrc, hook := shellHook("bash", home)
	if err := appendLine(bashrc, hook); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", timers.TimerName)
	stopped := errors.New("crontab: cannot write")
	a.System = uninstalled{res: timers.Result{Removed: []string{unit}, Warnings: []string{"half done"}}, err: stopped}
	cmd, out := output()
	if err := uninstall(cmd, a, true, nil); !errors.Is(err, stopped) {
		t.Fatalf("error %v\n%s", err, out)
	}
	// What it got done is said; that it is over is not.
	want := "  ✓ removed " + tilde(unit) + "\n" +
		"  ! half done\n"
	if got := out.String(); got != want {
		t.Errorf("uninstall printed:\n%s\nwant:\n%s", got, want)
	}
	// The reaper is still half installed: its hook stays for the next try.
	if b, _ := os.ReadFile(bashrc); string(b) != hook+"\n" {
		t.Errorf("the startup file after an uninstall that failed:\n%s", b)
	}
}

func TestPurgeSaysWhichStartupFileItCouldNotChange(t *testing.T) {
	home := isolate(t)
	a := fakeApp(t, config.MapEnv{"HOME": home})
	// A directory where bash's startup file would be: it cannot be read as a
	// file, by root either, so the failure is the same for whoever runs this.
	bashrc, _ := shellHook("bash", home)
	if err := os.Mkdir(bashrc, 0o755); err != nil {
		t.Fatal(err)
	}
	_, unreadable := os.ReadFile(bashrc)
	if unreadable == nil {
		t.Fatal("a directory was read as a file")
	}
	// No timers and nothing else removed: the failure is the whole list.
	a.System = uninstalled{}
	exe, err := app.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, out := output()
	// The timers are gone all the same, so a startup file fails nothing.
	if err := uninstall(cmd, a, true, nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The settings loaded, so nothing is said about where the log was looked
	// for; one line is a list, and a blank line follows it.
	want := "  ! could not remove the prompt hook: " + unreadable.Error() + "\n" +
		"\n" +
		"Found no timers to remove.\n" +
		"The program itself stays: delete " + tilde(exe) + ", or remove its package.\n"
	if got := out.String(); got != want {
		t.Errorf("purge printed:\n%s\nwant:\n%s", got, want)
	}
}

func TestRemoveHooksGoesOnPastAFileItCannotRead(t *testing.T) {
	home := t.TempDir()
	// bash's startup file cannot be read (a directory is in its place), and
	// zsh's, which comes after it, has the hook.
	bashrc, _ := shellHook("bash", home)
	if err := os.Mkdir(bashrc, 0o755); err != nil {
		t.Fatal(err)
	}
	zshrc, hook := shellHook("zsh", home)
	if err := os.WriteFile(zshrc, []byte("# mine\n"+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := removeHooks(home)
	// The file that failed is named once, and the ones after it are still
	// gone through.
	if len(u.failed) != 1 || !strings.Contains(u.failed[0].Error(), bashrc) {
		t.Errorf("failed: %v", u.failed)
	}
	if !slices.Equal(u.removed, []string{zshrc}) || len(u.stays) != 0 {
		t.Errorf("removed %q, stays %q", u.removed, u.stays)
	}
	if b, _ := os.ReadFile(zshrc); string(b) != "# mine\n" {
		t.Errorf("%s is now %q", zshrc, b)
	}
}

func TestSomeOfListsThreeNamesAndCountsTheRest(t *testing.T) {
	i18n.Set(i18n.English)
	for _, c := range []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a, b, c"},
		{[]string{"a", "b", "c", "d"}, "a, b, c and 1 more"},
		{[]string{"a", "b", "c", "d", "e", "f"}, "a, b, c and 3 more"},
		// A file name is whatever somebody made it: one with an escape or a
		// line break in it names the file and does nothing to the terminal.
		{[]string{"x\x1b[2Jy", "line\nbreak", "tab\there"}, "x?[2Jy, line?break, tab?here"},
	} {
		if got := someOf(c.names); got != c.want {
			t.Errorf("someOf(%q) = %q, want %q", c.names, got, c.want)
		}
	}
}

func TestRemoveHooksTakesOutOnlyTheLineSetupWrote(t *testing.T) {
	_, hook := shellHook("bash", "home")
	block := "if true; then\n  " + hook + "\nfi\n"
	for _, c := range []struct {
		name, before, after string
		// removed and stays say which of the two lists names the file.
		removed, stays bool
	}{
		{"the last line, as setup adds it", "alias ll='ls -l'\n" + hook + "\n", "alias ll='ls -l'\n", true, false},
		{"in the middle", "export A=1\n" + hook + "\nexport B=2\n", "export A=1\nexport B=2\n", true, false},
		{"the last line with no line break", "export A=1\n" + hook, "export A=1\n", true, false},
		{"the only line", hook + "\n", "", true, false},
		{"twice", hook + "\nexport A=1\n" + hook + "\n", "export A=1\n", true, false},
		{"twice in a row", "export A=1\n" + hook + "\n" + hook + "\nexport B=2\n", "export A=1\nexport B=2\n", true, false},
		// Anything that does not read exactly as setup's line is somebody's
		// own: it stays, and the file is named.
		{"indented in a block", block, block, false, true},
		{"with Windows line ends", "export A=1\r\n" + hook + "\r\n", "export A=1\r\n" + hook + "\r\n", false, true},
		{"with something after it", hook + " # mine\n", hook + " # mine\n", false, true},
		{"commented out", "# " + hook + "\n", "# " + hook + "\n", false, true},
		{"without its guard", `eval "$(zellij-reaper shell-hook bash)"` + "\n", `eval "$(zellij-reaper shell-hook bash)"` + "\n", false, true},
		{"setup's line and one in a block", hook + "\n" + block, block, true, true},
		{"no hook", "export A=1\n", "export A=1\n", false, false},
		{"an empty file", "", "", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(home, ".bashrc")
			// A mode that is not the one a new file would get.
			if err := os.WriteFile(file, []byte(c.before), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			u := removeHooks(home)
			if len(u.failed) != 0 {
				t.Fatalf("failed: %v", u.failed)
			}
			if got := slices.Equal(u.removed, []string{file}); got != c.removed || (!c.removed && len(u.removed) != 0) {
				t.Errorf("removed %q", u.removed)
			}
			if got := slices.Equal(u.stays, []string{file}); got != c.stays || (!c.stays && len(u.stays) != 0) {
				t.Errorf("stays %q", u.stays)
			}
			if b, _ := os.ReadFile(file); string(b) != c.after {
				t.Errorf("the file is now %q, want %q", b, c.after)
			}
			// The same file with the same mode, not a new one in its place.
			after, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Errorf("the file was replaced: mode %v, then %v", before.Mode(), after.Mode())
			}
			// Only the startup file is in the home: no copy, no temporary file.
			if entries, _ := os.ReadDir(home); len(entries) != 1 {
				t.Errorf("%d entries in the home", len(entries))
			}
		})
	}
}

func TestEveryHookLineAsksForTheProgramFirst(t *testing.T) {
	// Run by a shell where the program is gone, a line must say nothing.
	// Measured with fish 3.6: the line without the test printed six lines of
	// error at every start once the program was removed.
	for shell, test := range map[string]string{
		"bash": "command -v zellij-reaper >/dev/null && ",
		"zsh":  "command -v zellij-reaper >/dev/null && ",
		"fish": "type -q zellij-reaper; and ",
	} {
		if _, line := shellHook(shell, "home"); !strings.HasPrefix(line, test) {
			t.Errorf("%s: %q", shell, line)
		}
	}
}

func TestRemoveHooksTakesOutFishsLineFromBefore(t *testing.T) {
	home := t.TempDir()
	file, line := shellHook("fish", home)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	// The line setup wrote up to 0.10, without the test, and the one it
	// writes now: a setup run again after an upgrade finds the hook there
	// and adds none, so a file has one or the other, or both by hand.
	old := "zellij-reaper shell-hook fish | source"
	if err := os.WriteFile(file, []byte("set -x EDITOR vim\n"+old+"\n"+line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u := removeHooks(home)
	if !slices.Equal(u.removed, []string{file}) || len(u.stays)+len(u.failed) != 0 {
		t.Errorf("removed %q, stays %q, failed %v", u.removed, u.stays, u.failed)
	}
	if b, _ := os.ReadFile(file); string(b) != "set -x EDITOR vim\n" {
		t.Errorf("%s is now %q", file, b)
	}
}

func TestRemoveHooksGoesThroughEveryShellsFile(t *testing.T) {
	home := t.TempDir()
	// The shell an uninstall runs in need not be the one setup ran in.
	t.Setenv("SHELL", "/bin/sh")
	var files []string
	for _, shell := range []string{"bash", "zsh", "fish"} {
		file, line := shellHook(shell, home)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := appendLine(file, line); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	u := removeHooks(home)
	if !slices.Equal(u.removed, files) || len(u.stays)+len(u.failed) != 0 {
		t.Errorf("removed %q, stays %q, failed %v", u.removed, u.stays, u.failed)
	}
	for _, file := range files {
		if b, _ := os.ReadFile(file); string(b) != "# mine\n" {
			t.Errorf("%s is now %q", file, b)
		}
	}
	// A second time there is nothing left to take out, and nothing to say.
	if u := removeHooks(home); !u.nothing() {
		t.Errorf("the second time: %+v", u)
	}
}

func TestRemoveHooksMakesNoFileWhereThereIsNone(t *testing.T) {
	home := t.TempDir()
	if u := removeHooks(home); !u.nothing() {
		t.Errorf("a home with no startup files: %+v", u)
	}
	// Not even the directory fish's file would be in.
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("%d entries made in the home", len(entries))
	}
	// A home nobody could tell is no place to look in.
	if u := removeHooks(""); !u.nothing() {
		t.Errorf("no home: %+v", u)
	}
}

func TestRemoveHooksChangesTheFileALinkNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making a link takes a privilege there")
	}
	home := t.TempDir()
	// A startup file kept in a directory of dotfiles and linked into the home.
	link, hook := shellHook("bash", home)
	kept := filepath.Join(home, "dotfiles", "bashrc")
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("export A=1\n"+hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(kept, link); err != nil {
		t.Fatal(err)
	}
	if u := removeHooks(home); !slices.Equal(u.removed, []string{link}) || len(u.stays)+len(u.failed) != 0 {
		t.Errorf("removed %q, stays %q, failed %v", u.removed, u.stays, u.failed)
	}
	// Still a link, to a file that no longer has the line.
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is now %v (%v)", fi.Mode(), err)
	}
	if b, _ := os.ReadFile(kept); string(b) != "export A=1\n" {
		t.Errorf("the linked file is now %q", b)
	}
}

func TestRemoveHooksAsksToWriteOnlyWhereTheHookIs(t *testing.T) {
	skipAsRoot(t)
	home := t.TempDir()
	file, hook := shellHook("bash", home)
	// A startup file the user may not write, with no hook in it: nothing to
	// take out, so nothing to report.
	if err := os.WriteFile(file, []byte("export A=1\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if u := removeHooks(home); !u.nothing() {
		t.Errorf("a read-only file with no hook: %+v", u)
	}
	// With the hook in it, the file that keeps it is named, once.
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(hook+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o444); err != nil {
		t.Fatal(err)
	}
	// Writable again for the test's own cleanup.
	t.Cleanup(func() { os.Chmod(file, 0o644) })
	u := removeHooks(home)
	if len(u.failed) != 1 || !strings.Contains(u.failed[0].Error(), file) || len(u.removed)+len(u.stays) != 0 {
		t.Errorf("a read-only file with the hook: %+v", u)
	}
	if b, _ := os.ReadFile(file); string(b) != hook+"\n" {
		t.Errorf("the file is now %q", b)
	}
}
