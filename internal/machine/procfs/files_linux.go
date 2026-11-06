package procfs

import (
	"os"
	"syscall"
	"time"
)

// LastIO is the later of a terminal's last write (mtime) and last read
// (atime): output and input both mean someone or something used the pane.
func LastIO(tty string) time.Time {
	info, err := os.Stat(tty)
	if err != nil {
		return time.Time{}
	}
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if a := time.Unix(st.Atim.Sec, st.Atim.Nsec); a.After(t) {
			t = a
		}
	}
	return t
}
