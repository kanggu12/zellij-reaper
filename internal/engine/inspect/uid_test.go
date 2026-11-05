package inspect_test

import "os"

// osUID is the uid the fake processes are created with.
func osUID() int { return os.Getuid() }
