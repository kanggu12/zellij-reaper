package procfs

import "time"

// procRoot is where Linux keeps its process table.
const procRoot = "/proc"

// readNative reads the machine's /proc.
func readNative(sessions bool) (*Table, error) {
	if sessions {
		return readDirSessions(procRoot)
	}
	return readDirAll(procRoot)
}

// processNative reads one process from the machine's /proc.
func processNative(pid int) (*Proc, bool) { return readProc(procRoot, pid) }

// envNative reads a variable from a process's environ in the machine's /proc.
func envNative(pid int, key string) (string, bool) { return envDir(procRoot, pid, key) }

// youngestNative is YoungestServer on the machine's /proc.
func youngestNative(uid int) (time.Duration, bool) { return youngestDir(procRoot, uid) }

// readZellijNative reads the machine's /proc the way a pass does.
func readZellijNative() (*Table, error) { return readDirSessions(procRoot) }

// fileOwner has nothing to read from a socket file on Linux: the kernel's
// socket table ties a socket file to its server there.
func fileOwner(path string, servers []Server) (Server, bool) {
	_, _ = path, servers
	return Server{}, false
}

// nativeRoot is the directory a table reads from: /proc for this machine.
func nativeRoot(root string) string {
	if root == Native {
		return procRoot
	}
	return root
}
