package config

import (
	"os"
	"path/filepath"
)

// TightenOld takes the program's own folders, the settings folder and the
// state folder, and the files in them from the modes releases before 0.13
// made, 0755 and 0644, to 0700 and 0600: the log holds pane titles and command
// lines, the lists session names, and any user could read them. A mode other
// than those two was chosen, and is left as it is, and so is a link and what
// it points to. Windows has no such modes. It returns what it changed.
func TightenOld(env Env) []string {
	if GOOS == "windows" {
		return nil
	}
	var changed []string
	for _, dir := range []string{ConfigDir(env), StateDir(env)} {
		if info, err := os.Lstat(dir); err == nil && info.IsDir() && info.Mode().Perm() == 0o755 {
			if os.Chmod(dir, 0o700) == nil {
				changed = append(changed, dir)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o644 {
				if os.Chmod(p, 0o600) == nil {
					changed = append(changed, p)
				}
			}
		}
	}
	return changed
}
