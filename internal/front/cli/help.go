package cli

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

// helpWidth is the width every help text fits: an 80-column terminal cuts a
// longer line mid-word.
const helpWidth = 80

// usageTemplate is cobra's, with the flags' words wrapped to helpWidth and the
// last line said in fewer words.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsagesWrapped 80 | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Run "{{.CommandPath}} [command] --help" for more about a command.{{end}}
`

// fitHelp fits every command's help under root to helpWidth: the template
// wraps the flags, and the long text is set again in lines of that width.
func fitHelp(root *cobra.Command) {
	root.SetUsageTemplate(usageTemplate)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Long = prose(c.Long)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// prose sets text in lines of helpWidth: each paragraph, the lines between
// blank lines, is run together and broken again between words. A paragraph
// whose first line starts with a space is an example, and stays as written.
func prose(text string) string {
	paragraphs := strings.Split(text, "\n\n")
	for i, p := range paragraphs {
		if p == "" || strings.HasPrefix(p, " ") {
			continue
		}
		var lines []string
		line := ""
		for word := range strings.FieldsSeq(p) {
			switch {
			case line == "":
				line = word
			case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= helpWidth:
				line += " " + word
			default:
				lines = append(lines, line)
				line = word
			}
		}
		paragraphs[i] = strings.Join(append(lines, line), "\n")
	}
	return strings.Join(paragraphs, "\n\n")
}
