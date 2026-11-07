package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// keyLine matches a top-level setting's line, set or commented out:
// "idle_after = ...", "# idle_after = ...". The key is the second group.
var keyLine = regexp.MustCompile(`^(#\s*)?([a-z_.]+)\s*=`)

// SetKeys writes values into the settings file at path, one line per key,
// leaving every other line as it is, so notes and settings someone wrote by
// hand stay. A key set in the file has its line replaced; one only commented
// out has that line replaced, in its place among the notes; any other is
// added at the end. Without a file, a complete one is written from c with
// the values in. values holds TOML values as Config.Value words them.
func SetKeys(path string, c *Config, values map[string]string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b, err = []byte(Render(c, "zellij-reaper settings, written by `zellij-reaper setup`."+
			"\nRun `zellij-reaper config show` to see the values in effect.")), nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	// set and commented find each key's last set line and last commented
	// one: the last set line is the one TOML would refuse a twin of, and the
	// one in effect.
	set, commented := map[string]int{}, map[string]int{}
	for i, l := range lines {
		if m := keyLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			if m[1] == "" {
				set[m[2]] = i
			} else {
				commented[m[2]] = i
			}
		}
	}
	var tail []string
	// Keys in a stable order, so the file comes out the same every time.
	for _, k := range Keys {
		v, ok := values[k]
		if !ok {
			continue
		}
		line := k + " = " + v
		if i, ok := set[k]; ok {
			lines[i] = line
		} else if i, ok := commented[k]; ok {
			lines[i] = line
		} else {
			tail = append(tail, line)
		}
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	if len(tail) > 0 {
		out += strings.Join(tail, "\n") + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Written beside and moved over, so a pass never reads half a file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
