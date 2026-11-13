// Package repo finds the git repository a directory belongs to: the project a
// program working there works on. It reads the file system only, and runs no
// git: a pass reads the directories of every process under a coding agent, and
// a git process for each would cost more than the pass itself.
package repo

import (
	"os"
	"path/filepath"
	"sort"
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
	// home holds Home as given and where its links lead: a process's
	// directory is read with its links resolved (/proc/<pid>/cwd).
	home map[string]bool
}

// homes is the home directory as given and as its links resolve, worked out
// once per Finder.
func (f *Finder) homes() map[string]bool {
	if f.home != nil || f.Home == "" {
		return f.home
	}
	f.home = map[string]bool{filepath.Clean(f.Home): true}
	if real, err := filepath.EvalSymlinks(f.Home); err == nil {
		f.home[filepath.Clean(real)] = true
	}
	return f.home
}

// Homes is the home directory as given and as its links resolve, in order.
func (f *Finder) Homes() []string {
	var out []string
	for h := range f.homes() {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
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
	return f.find(filepath.Clean(dir))
}

// find walks up from dir to the first directory holding a .git entry. Every
// directory it passes on the way has the same answer, and keeps it for the
// rest of the survey: a command line names many files of one folder, and on a
// slow file system (a Windows drive under WSL, a network mount) each look
// costs.
func (f *Finder) find(dir string) string {
	if f.roots == nil {
		f.roots = map[string]string{}
	}
	homes := f.homes()
	var passed []string
	root := ""
	for d := dir; ; d = filepath.Dir(d) {
		// Asked before in this survey, on its own or on the way up from
		// another: the same answer.
		if r, ok := f.roots[d]; ok {
			root = r
			break
		}
		passed = append(passed, d)
		// The home directory and the root say nothing about a project, and
		// a repository above them would hold every project.
		if homes[d] || d == filepath.Dir(d) {
			break
		}
		if top, ok := topOf(d); ok {
			root = top
			break
		}
	}
	for _, d := range passed {
		f.roots[d] = root
	}
	return root
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
	// git gives a worktree's gitdir a commondir file, the way to the git
	// data of the repository it was made from ("../.."): the project is that
	// repository. A submodule's gitdir has none, whatever its path, and the
	// submodule is a project of its own.
	common, err := readSmall(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return d, true
	}
	data := strings.TrimSpace(string(common))
	if data == "" {
		return d, true
	}
	// A relative commondir is relative to the worktree's gitdir.
	if !filepath.IsAbs(data) {
		data = filepath.Join(gitdir, data)
	}
	data = filepath.Clean(data)
	// The data is a checkout's .git, or a bare repository kept in a hidden
	// folder of the project's (.bare, next to its worktrees): the folder
	// that holds it is the repository. A bare repository on its own
	// (billing.git) is the repository itself; its name loses .git when the
	// session is named (rename.Suggest).
	if strings.HasPrefix(filepath.Base(data), ".") {
		return filepath.Dir(data), true
	}
	return data, true
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
