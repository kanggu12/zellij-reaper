// Package repo finds the git repository a directory belongs to: the project a
// program working there works on. It reads the file system only, and runs no
// git: a pass reads the directories of every process under a coding agent, and
// a git process for each would cost more than the pass itself.
package repo

import (
	"os"
	"path/filepath"
	"strings"
)

// Finder finds repositories, and remembers what it found for the directories
// it was asked about, for one survey: the processes of one agent often share a
// directory.
type Finder struct {
	// Home is this user's home. A home kept in git (dotfiles) is no project:
	// every directory under it would belong to it.
	Home string
	// roots holds the answer for each directory asked about.
	roots map[string]string
}

// Root is the top of the repository dir is in, "" when it is in none. A git
// worktree counts as the repository it was made from: `git worktree add` puts
// a checkout of the same project somewhere else. The home directory and the
// file system's root are no project, and neither is a relative path.
func (f *Finder) Root(dir string) string {
	// Only an absolute path names one place: a relative one depends on where
	// the reader stands.
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	dir = filepath.Clean(dir)
	// Asked before in this survey: the same answer.
	if root, ok := f.roots[dir]; ok {
		return root
	}
	root := f.find(dir)
	if f.roots == nil {
		f.roots = map[string]string{}
	}
	f.roots[dir] = root
	return root
}

// find walks up from dir to the first directory holding a .git entry.
func (f *Finder) find(dir string) string {
	home := ""
	if f.Home != "" {
		home = filepath.Clean(f.Home)
	}
	for d := dir; ; {
		// The home directory and the root say nothing about a project, and
		// a repository above them would hold every project.
		if d == home || d == filepath.Dir(d) {
			return ""
		}
		if top, ok := topOf(d); ok {
			return top
		}
		d = filepath.Dir(d)
	}
}

// topOf reports whether d is the top of a checkout, and of which repository:
// d itself, or for a worktree the repository it was made from.
func topOf(d string) (string, bool) {
	gitPath := filepath.Join(d, ".git")
	// Followed if it is a link: a .git linked to the data elsewhere is a
	// checkout all the same.
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false
	}
	// A directory: an ordinary checkout.
	if info.IsDir() {
		return d, true
	}
	// A file: a worktree or a submodule, which says where its git data is.
	if !info.Mode().IsRegular() {
		return "", false
	}
	b, err := readSmall(gitPath)
	if err != nil {
		// A .git that cannot be read still marks a checkout.
		return d, true
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return d, true
	}
	gitdir = strings.TrimSpace(gitdir)
	// A relative gitdir is relative to the checkout.
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(d, gitdir)
	}
	gitdir = filepath.Clean(gitdir)
	// A worktree's gitdir is <repository>/.git/worktrees/<name>: the project
	// is the repository. A submodule's is <parent>/.git/modules/<name>, and
	// the submodule is a project of its own.
	parent, name := filepath.Split(gitdir)
	if name == "" || filepath.Base(filepath.Clean(parent)) != "worktrees" {
		return d, true
	}
	dotGit := filepath.Dir(filepath.Clean(parent))
	if filepath.Base(dotGit) != ".git" {
		return d, true
	}
	return filepath.Dir(dotGit), true
}

// maxGitFile is the most of a .git file read: one line, "gitdir: <path>".
const maxGitFile = 4096

// readSmall reads the start of a small file, so a .git that is something
// else cannot take the pass's memory.
func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, maxGitFile)
	n, err := f.Read(b)
	if n > 0 {
		return b[:n], nil
	}
	return nil, err
}
