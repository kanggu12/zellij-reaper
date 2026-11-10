package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kanggu12/zellij-reaper/internal/engine/inspect"
	"github.com/kanggu12/zellij-reaper/internal/engine/reaper"
	"github.com/kanggu12/zellij-reaper/internal/front/i18n"
	"github.com/kanggu12/zellij-reaper/internal/front/ui"
	"github.com/kanggu12/zellij-reaper/internal/machine/lock"
	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
	"github.com/kanggu12/zellij-reaper/internal/settings/config"
)

// cmdHere builds `here`: the name the session this shell runs in has now.
func cmdHere() *cobra.Command {
	return &cobra.Command{
		Use:   "here",
		Short: "Print the current name of this shell's zellij session",
		Long: `Print the current name of this shell's zellij session. A pane keeps the name
its session had when the pane opened, in ZELLIJ_SESSION_NAME, and a zellij
command run there waits for a session by that name once the session is renamed.
It starts no zellij process, so a prompt can afford it: see shell-hook.
Exits 1, printing nothing, outside zellij or when the name cannot be told.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No settings and no zellij process: only the process table, the
			// kernel's socket table on Linux and the socket directory, read up
			// from this process (the shell that asked, its pane, the server).
			// zellij's session index, which names sessions since 0.46, sits in
			// its newest data directory: no need to ask zellij its version.
			info := zellij.SessionInfoDir(config.ZellijCacheDir(config.OSEnv{}), "")
			src := inspect.Sources{ProcRoot: procfs.Native, UID: procfs.CurrentUID(), Sockets: procfs.UnixSockets, InfoDir: info}
			name, err := inspect.Here(src, os.Getpid())
			// Outside zellij, or with no name to give: nothing printed, status 1,
			// so a prompt hook leaves ZELLIJ_SESSION_NAME as it is.
			if err != nil {
				os.Exit(1)
			}
			fmt.Fprintln(cmd.OutOrStdout(), name)
			return nil
		},
	}
}

// hooks are the prompt hooks shell-hook prints. Each keeps the status of the
// command before the prompt, which prompts show, and does nothing outside zellij
// or when here cannot tell.
var hooks = map[string]string{
	"bash": `# zellij-reaper: keep ZELLIJ_SESSION_NAME at the name this pane's session has now.
__zellij_reaper_here() {
  local status=$? name
  if [ -n "${ZELLIJ:-}" ] && name=$(command zellij-reaper here 2>/dev/null) && [ -n "$name" ]; then
    export ZELLIJ_SESSION_NAME="$name"
  fi
  return $status
}
case ";${PROMPT_COMMAND:-};" in
  *";__zellij_reaper_here;"*) ;;
  *) PROMPT_COMMAND="__zellij_reaper_here${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
esac
`,
	"zsh": `# zellij-reaper: keep ZELLIJ_SESSION_NAME at the name this pane's session has now.
__zellij_reaper_here() {
  local name
  if [[ -n ${ZELLIJ-} ]] && name=$(command zellij-reaper here 2>/dev/null) && [[ -n $name ]]; then
    export ZELLIJ_SESSION_NAME=$name
  fi
}
autoload -Uz add-zsh-hook
add-zsh-hook precmd __zellij_reaper_here
`,
	"fish": `# zellij-reaper: keep ZELLIJ_SESSION_NAME at the name this pane's session has now.
function __zellij_reaper_here --on-event fish_prompt
    set -q ZELLIJ; or return
    set -l name (command zellij-reaper here 2>/dev/null)
    and test -n "$name"
    and set -gx ZELLIJ_SESSION_NAME $name
end
`,
}

// cmdShellHook builds `shell-hook bash|zsh|fish`.
func cmdShellHook() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-hook bash|zsh|fish",
		Short: "Print a prompt hook that keeps ZELLIJ_SESSION_NAME current",
		Long: `Print a prompt hook that keeps ZELLIJ_SESSION_NAME at the name this pane's session
has now, so zellij commands run in a pane opened before a rename keep working.
Add it to the shell's startup file:

 # ~/.bashrc
 command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook bash)"
 # ~/.zshrc
 command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook zsh)"
 # ~/.config/fish/config.fish
 type -q zellij-reaper; and zellij-reaper shell-hook fish | source

The test before each keeps a shell quiet where zellij-reaper is not on its PATH.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			h, ok := hooks[args[0]]
			if !ok {
				return fmt.Errorf("no hook for %q: bash, zsh or fish", args[0])
			}
			fmt.Fprint(cmd.OutOrStdout(), h)
			return nil
		},
	}
}

// cmdKeepName builds `keep-name NAME...` (on) or `forget-name NAME...` (off).
// With no name, `keep-name` lists the kept names.
func cmdKeepName(on bool) *cobra.Command {
	use, short := "keep-name [name...]", "Keep sessions' names as they are; with no name, list the kept ones"
	long := `Keep the names of these running sessions as they are: no pass renames them
from then on, as r, Tab and Enter do in the dashboard. With no name, list the
kept names. forget-name lets passes rename them again.`
	if !on {
		use, short = "forget-name name...", "Let passes rename these sessions again"
		long = `Take these names off the list of kept names, so passes may rename those
sessions again. A name that is not on the list is said, and left.`
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args: func(cmd *cobra.Command, args []string) error {
			if !on && len(args) == 0 {
				return fmt.Errorf("name a session to forget")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := load()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if on && len(args) == 0 {
				var names []string
				if a.Reaper.Kept != nil {
					names = a.Reaper.Kept.Names()
				}
				// An empty list prints nothing to a script; a person is told.
				if len(names) == 0 && isTerminal(os.Stdout) {
					println(w, ui.Fg(theme().Muted).Render(i18n.T("keep.empty")))
				}
				for _, n := range names {
					fmt.Fprintln(w, n)
				}
				return nil
			}
			for _, name := range args {
				err := reaper.KeepName(cmd.Context(), a.Reaper, name, on, "the command line", reaper.LockWait)
				// From the command line the name may be a typo: no "any
				// more", as rename says it.
				if errors.Is(err, reaper.ErrNoSession) {
					return errors.New(i18n.T("rename.none", name))
				}
				// As unprotect says of a name the protected list never had.
				if errors.Is(err, reaper.ErrNotKept) {
					fmt.Fprintln(w, i18n.T("forget.notkept", name))
					continue
				}
				if errors.Is(err, lock.ErrBusy) {
					return errors.New(i18n.T("toast.busy"))
				}
				if err != nil {
					return err
				}
				if on {
					fmt.Fprintln(w, i18n.T("toast.kept", name))
				} else {
					fmt.Fprintln(w, i18n.T("forget.done", name))
				}
			}
			return nil
		},
	}
}
