// Command zellij-reaper reaps idle and exited zellij sessions, safely.
package main

import (
	"os"

	"github.com/kanggu12/zellij-reaper/internal/front/cli"
)

func main() { os.Exit(cli.Execute()) }
