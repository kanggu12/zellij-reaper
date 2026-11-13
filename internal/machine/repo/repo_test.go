package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// mkdirs makes directories under root.
func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// write writes a file under root.
func write(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRootFindsTheRepositoryADirectoryIsIn(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"srv/web-app/.git", "srv/web-app/cmd/server",
		// A repository inside another: the inner one is the project.
		"srv/web-app/vendor/lib/.git", "srv/web-app/vendor/lib/src",
		// A worktree of web-app elsewhere, and its git data in web-app.
		"srv/web-app/.git/worktrees/fix-login", "work/fix-login/ui",
		// A submodule, a project of its own, and one in a folder named
		// worktrees.
		"srv/web-app/.git/modules/theme", "srv/web-app/theme/css",
		"srv/web-app/.git/modules/worktrees/foo", "srv/web-app/worktrees/foo/src",
		// No repository at all.
		"tmp/scratch",
		// A home kept in git, and a project under it.
		"home/me/.git", "home/me/notes", "home/me/code/api/.git", "home/me/code/api/docs",
	)
	// As git writes them: a worktree's .git names its gitdir, which holds
	// the way back to the repository's data; a submodule's names its own.
	write(t, root, "work/fix-login/.git", "gitdir: "+filepath.Join(root, "srv/web-app/.git/worktrees/fix-login")+"\n")
	write(t, root, "srv/web-app/.git/worktrees/fix-login/commondir", "../..\n")
	write(t, root, "srv/web-app/theme/.git", "gitdir: ../.git/modules/theme\n")
	write(t, root, "srv/web-app/worktrees/foo/.git", "gitdir: ../../.git/modules/worktrees/foo\n")
	f := &Finder{Home: filepath.Join(root, "home/me")}
	for dir, want := range map[string]string{
		"srv/web-app":                   "srv/web-app",
		"srv/web-app/cmd/server":        "srv/web-app",
		"srv/web-app/vendor/lib":        "srv/web-app/vendor/lib",
		"srv/web-app/vendor/lib/src":    "srv/web-app/vendor/lib",
		"work/fix-login/ui":             "srv/web-app",
		"srv/web-app/theme/css":         "srv/web-app/theme",
		"srv/web-app/worktrees/foo/src": "srv/web-app/worktrees/foo",
		"tmp/scratch":                   "",
		"home/me":                       "",
		"home/me/notes":                 "",
		"home/me/code/api/docs":         "home/me/code/api",
	} {
		got := f.Root(filepath.Join(root, dir))
		if want != "" {
			want = filepath.Join(root, want)
		}
		if got != want {
			t.Errorf("Root(%s) = %q, want %q", dir, got, want)
		}
	}
}

func TestRootIsNothingForAPathThatSaysNothing(t *testing.T) {
	f := &Finder{}
	for _, dir := range []string{"", "web-app", "./web-app", "/", string(filepath.Separator)} {
		if got := f.Root(dir); got != "" {
			t.Errorf("Root(%q) = %q", dir, got)
		}
	}
}

func TestRootRemembersWhatItFoundForOneSurvey(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "web-app/.git", "web-app/src")
	f := &Finder{}
	dir := filepath.Join(root, "web-app/src")
	if got := f.Root(dir); got != filepath.Join(root, "web-app") {
		t.Fatalf("Root = %q", got)
	}
	// The same survey asks again after the checkout went: the same answer,
	// with no second look.
	if err := os.RemoveAll(filepath.Join(root, "web-app/.git")); err != nil {
		t.Fatal(err)
	}
	if got := f.Root(dir); got != filepath.Join(root, "web-app") {
		t.Errorf("asked again: %q", got)
	}
	// A new survey looks again.
	if got := (&Finder{}).Root(dir); got != "" {
		t.Errorf("a new finder: %q", got)
	}
	// What a walk up passed is known too: another file of the same folder,
	// or the folder above, needs no look of its own.
	mkdirs(t, root, "billing/.git", "billing/src/pkg")
	f = &Finder{}
	if got := f.Root(filepath.Join(root, "billing/src/pkg/a.go")); got != filepath.Join(root, "billing") {
		t.Fatalf("Root = %q", got)
	}
	if err := os.RemoveAll(filepath.Join(root, "billing/.git")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"billing/src/pkg/b.go", "billing/src", "billing"} {
		if got := f.Root(filepath.Join(root, p)); got != filepath.Join(root, "billing") {
			t.Errorf("%s after the walk: %q", p, got)
		}
	}
}

func TestADotGitFileThatSaysSomethingElseStillMarksACheckout(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a/src", "b/src", "d/src")
	write(t, root, "a/.git", "not a gitdir line\n")
	write(t, root, "b/.git", "")
	// A .git linked to a checkout's data elsewhere.
	mkdirs(t, root, "data/d.git")
	if err := os.Symlink(filepath.Join(root, "data/d.git"), filepath.Join(root, "d/.git")); err != nil {
		t.Fatal(err)
	}
	f := &Finder{}
	for _, name := range []string{"a", "b", "d"} {
		if got := f.Root(filepath.Join(root, name, "src")); got != filepath.Join(root, name) {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestAWorktreeOfABareRepositoryIsThatRepository(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		// A bare repository on its own, and a worktree of it elsewhere.
		"srv/billing.git/worktrees/feature", "work/feature/src",
		// A bare repository in a hidden folder of the project, next to its
		// worktrees, which name it with a relative gitdir.
		"code/api/.bare/worktrees/main", "code/api/main/cmd",
	)
	write(t, root, "work/feature/.git", "gitdir: "+filepath.Join(root, "srv/billing.git/worktrees/feature")+"\n")
	write(t, root, "srv/billing.git/worktrees/feature/commondir", "../..\n")
	write(t, root, "code/api/main/.git", "gitdir: ../.bare/worktrees/main\n")
	write(t, root, "code/api/.bare/worktrees/main/commondir", "../..\n")
	f := &Finder{}
	for dir, want := range map[string]string{
		"work/feature/src":  "srv/billing.git",
		"code/api/main/cmd": "code/api",
	} {
		if got := f.Root(filepath.Join(root, dir)); got != filepath.Join(root, want) {
			t.Errorf("Root(%s) = %q, want %s", dir, got, want)
		}
	}
}

func TestAHomeReachedThroughALinkIsNoProjectEither(t *testing.T) {
	// A process's directory is read with every link resolved, so the test
	// starts from the temporary folder's real path: on macOS it is under a
	// link (/var), and on Windows it may be a short name (RUNNER~1).
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The home is a link to a folder kept in git.
	mkdirs(t, root, "data/me/.git", "data/me/notes")
	if err := os.Symlink(filepath.Join(root, "data/me"), filepath.Join(root, "home")); err != nil {
		// Windows makes links only for an administrator or in developer mode.
		t.Skipf("cannot make a link here: %v", err)
	}
	f := &Finder{Home: filepath.Join(root, "home")}
	if got := f.Root(filepath.Join(root, "data/me/notes")); got != "" {
		t.Errorf("Root under the home's real folder = %q", got)
	}
	// Both are the home, for whoever asks.
	if got := f.Homes(); len(got) != 2 || got[0] != filepath.Join(root, "data/me") || got[1] != filepath.Join(root, "home") {
		t.Errorf("Homes = %q", got)
	}
}
