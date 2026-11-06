//go:build darwin

// Command probe prints, on macOS, when each zellij server started and when
// each socket file in its directory was made, for the platforms workflow: the
// gap between the two is what matches a renamed session to its server there.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kanggu12/zellij-reaper/internal/machine/procfs"
	"github.com/kanggu12/zellij-reaper/internal/machine/zellij"
)

func main() {
	t, err := procfs.ReadZellij(procfs.Native)
	if err != nil {
		fmt.Println("read:", err)
		os.Exit(1)
	}
	servers := t.Servers(procfs.CurrentUID())
	dirs := map[string]bool{}
	for _, s := range servers {
		fmt.Printf("server pid %d started %s socket %s\n", s.PID, s.Started.Format("15:04:05.000000"), s.Socket)
		dirs[filepath.Dir(s.Socket)] = true
	}
	for d := range dirs {
		for _, name := range zellij.Sockets(d) {
			born, _ := procfs.Born(filepath.Join(d, name))
			fmt.Printf("socket %s born %s", name, born.Format("15:04:05.000000"))
			for _, s := range servers {
				fmt.Printf("  pid %d: %+.3fs", s.PID, born.Sub(s.Started).Seconds())
			}
			owner, ok := procfs.FileOwner(filepath.Join(d, name), servers)
			fmt.Printf("  owner %d %v\n", owner.PID, ok)
		}
	}
}
