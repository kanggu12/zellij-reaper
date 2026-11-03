// Command sleeper stands in for a long-running program in the demo: copied
// under a name (npm, claude), it shows up with that name and runs until it is
// stopped, whatever its arguments.
package main

import "time"

func main() { time.Sleep(1000 * time.Hour) }
