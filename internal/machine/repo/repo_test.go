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
		// A submodule, a project of its own.
		"srv/web-app/.git/modules/theme", "srv/web-app/theme/css",
		// No repository at all.
		"tmp/scratch",
		// A home kept in git, and a project under it.
		"home/me/.git", "home/me/notes", "home/me/code/api/.git", "home/me/code/api/docs",
	)
	write(t, root, "work/fix-login/.git", "gitdir: "+filepath.Join(root, "srv/web-app/.git/worktrees/fix-login")+"\n")
	write(t, root, "srv/web-app/theme/.git", "gitdir: ../.git/modules/theme\n")
	f := &Finder{Home: filepath.Join(root, "home/me")}
	for dir, want := range map[string]string{
		"srv/web-app":                "srv/web-app",
		"srv/web-app/cmd/server":     "srv/web-app",
		"srv/web-app/vendor/lib":     "srv/web-app/vendor/lib",
		"srv/web-app/vendor/lib/src": "srv/web-app/vendor/lib",
		"work/fix-login/ui":          "srv/web-app",
		"srv/web-app/theme/css":      "srv/web-app/theme",
		"tmp/scratch":                "",
		"home/me":                    "",
		"home/me/notes":              "",
		"home/me/code/api/docs":      "home/me/code/api",
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
}

func TestADotGitFileThatSaysSomethingElseStillMarksACheckout(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "a/src", "b/src", "c/src", "repo.git/worktrees/x", "d/src")
	write(t, root, "a/.git", "not a gitdir line\n")
	write(t, root, "b/.git", "")
	// A worktree of a bare repository: no .git directory to stand for the
	// project, so the worktree is the project.
	write(t, root, "c/.git", "gitdir: "+filepath.Join(root, "repo.git/worktrees/x")+"\n")
	// A .git linked to a checkout's data elsewhere.
	mkdirs(t, root, "data/d.git")
	if err := os.Symlink(filepath.Join(root, "data/d.git"), filepath.Join(root, "d/.git")); err != nil {
		t.Fatal(err)
	}
	f := &Finder{}
	for _, name := range []string{"a", "b", "c", "d"} {
		if got := f.Root(filepath.Join(root, name, "src")); got != filepath.Join(root, name) {
			t.Errorf("%s: %q", name, got)
		}
	}
}
