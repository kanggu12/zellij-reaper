package config

import (
	"fmt"
	"strings"
)

// Render writes a complete settings file for c: every key with a short note on
// what it does, so the file documents itself. Keys that still hold their
// default are written commented out, which keeps a later change of a default
// reaching users who never touched that key.
func Render(c *Config, intro string) string {
	def := Default(MapEnv{"HOME": "~"})
	var b strings.Builder
	// line writes one setting, commented out when it equals the default.
	line := func(key, value, defValue string) {
		if value == defValue {
			fmt.Fprintf(&b, "# %s = %s\n", key, value)
		} else {
			fmt.Fprintf(&b, "%s = %s\n", key, value)
		}
	}
	// The intro says why the file exists (first install, migration, ...).
	for _, l := range strings.Split(strings.TrimSpace(intro), "\n") {
		if l != "" {
			fmt.Fprintf(&b, "# %s\n", l)
		}
	}
	b.WriteString("# Every setting is optional. A commented-out line shows the default.\n")
	b.WriteString("# Durations take s, m, h, d and w, and can be combined: \"1d12h\".\n\n")

	b.WriteString("# Log what each pass would do, but delete and rename nothing.\n")
	line("dry_run", c.Value("dry_run"), def.Value("dry_run"))
	b.WriteString("\n# A running session with no client attached and every pane at a shell prompt\n")
	b.WriteString("# is reaped once nothing has happened in it for this long. \"0\" skips the wait.\n")
	line("idle_after", c.Value("idle_after"), def.Value("idle_after"))
	b.WriteString("\n# An exited session (kept by zellij so it can be resurrected) is deleted once\n")
	b.WriteString("# it has been gone this long. \"0\" skips the wait.\n")
	line("exited_after", c.Value("exited_after"), def.Value("exited_after"))
	b.WriteString("\n# Sessions whose name matches this regular expression are never reaped or renamed.\n")
	line("protect", c.Value("protect"), def.Value("protect"))
	b.WriteString("\n# While one of these programs runs in a session, the session is kept.\n")
	line("protect_commands", c.Value("protect_commands"), def.Value("protect_commands"))
	b.WriteString("\n# Extra programs that count as an idle prompt (bash, zsh, fish, nu and other\n")
	b.WriteString("# common shells already do).\n")
	line("shells", c.Value("shells"), def.Value("shells"))
	b.WriteString("\n# Name sessions nobody named after what they show: a pane's title, or the\n")
	b.WriteString("# directory and the program, for example \"vim-notes.md\" or \"web-app\".\n")
	b.WriteString("# A name someone chose stays.\n")
	line("auto_rename", c.Value("auto_rename"), def.Value("auto_rename"))
	b.WriteString("\n# What those names end in: \"none\", the day of last use (\"date\", web-app_1104)\n")
	b.WriteString("# or its minute (\"time\", web-app_1104-0931). A stamp renames a session you\n")
	b.WriteString("# keep using again each day or each pass; without one a name changes when its\n")
	b.WriteString("# title does.\n")
	line("auto_rename_stamp", c.Value("auto_rename_stamp"), def.Value("auto_rename_stamp"))
	b.WriteString("\n# How often the systemd timer runs a pass. Run `zellij-reaper install` again\n")
	b.WriteString("# after changing it.\n")
	line("interval", c.Value("interval"), def.Value("interval"))
	b.WriteString("\n# The zellij binary. Empty means the first one in PATH.\n")
	line("zellij", c.Value("zellij"), def.Value("zellij"))

	// Written as dotted keys, with no [log] table: a line added at the end of
	// the file must stay a top-level setting, and under a table it would not.
	b.WriteString("\n# Where every decision is written.\n")
	// The default log path depends on the home directory, so compare it with
	// the default for this user rather than the placeholder above.
	if c.Sources["log.path"] == FromDefault {
		b.WriteString("# log.path = \"~/.local/state/zellij-reaper/reaper.log\"\n")
	} else {
		fmt.Fprintf(&b, "log.path = %s\n", c.Value("log.path"))
	}
	b.WriteString("# Past this size the log moves to <path>.1 and a new one starts. 0 = never.\n")
	line("log.max_bytes", c.Value("log.max_bytes"), def.Value("log.max_bytes"))
	return b.String()
}
