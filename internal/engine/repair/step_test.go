package repair

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/kanggu12/zellij-reaper/internal/testutil"
)

// A pid above the kernel's limit (4194304): even a test gone wrong, with the
// real signal function in place of the fake, would reach no process.
const nobody = 5_000_042

func TestTheSameProcessIsTheOneThatStartedThen(t *testing.T) {
	fp := testutil.NewFakeProc(t)
	fp.Uptime(time.Hour)
	fp.Add(testutil.Proc{PID: nobody, PPID: 1, Comm: "zellij", Started: 10 * time.Minute})
	same := Inputs{ProcRoot: fp.Root}.sameProcess(nobody)
	if !same() {
		t.Fatal("the process it was asked about is not the same")
	}
	// The server ended and another process got its pid: started later.
	fp.Add(testutil.Proc{PID: nobody, PPID: 1, Comm: "bash", Started: 50 * time.Minute})
	if same() {
		t.Error("another process with the pid is taken for the same")
	}
	// No process has the pid at all.
	if err := os.RemoveAll(filepath.Join(fp.Root, strconv.Itoa(nobody))); err != nil {
		t.Fatal(err)
	}
	if same() {
		t.Error("a pid nobody has is taken for the same process")
	}
}

func TestStopSaysWhatCameOfIt(t *testing.T) {
	for _, c := range []struct {
		name string
		// term and kill are what SIGTERM and SIGKILL answer; goes says
		// whether the process is gone once SIGTERM was sent.
		term, kill error
		goes       bool
		// reused: once SIGTERM was sent, another process has the pid.
		reused bool
		// wait is how long the process gets to go.
		wait time.Duration
		// what and err are the step's; sent the signals, in order.
		what string
		err  error
		sent []syscall.Signal
	}{
		{name: "gone on SIGTERM", goes: true, wait: time.Minute,
			what: "stopped orphan server", sent: []syscall.Signal{syscall.SIGTERM}},
		{name: "SIGTERM refused", term: syscall.EPERM, wait: time.Minute,
			what: "could not stop orphan server", err: syscall.EPERM, sent: []syscall.Signal{syscall.SIGTERM}},
		{name: "still there after the wait",
			what: "stopped orphan server (SIGKILL)", sent: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
		{name: "SIGKILL refused", kill: syscall.EPERM,
			what: "could not stop orphan server (SIGKILL)", err: syscall.EPERM, sent: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
		// It ended, and its pid went to another process of the user before
		// the next look: that process gets no SIGKILL.
		{name: "its pid taken by another process", reused: true,
			what: "stopped orphan server", sent: []syscall.Signal{syscall.SIGTERM}},
		// It ended after its last look and before SIGKILL: SIGTERM stopped it,
		// and a step saying it could not be stopped would be wrong.
		{name: "gone just before SIGKILL", kill: syscall.ESRCH,
			what: "stopped orphan server", sent: []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var sent []syscall.Signal
			gone, termed := false, false
			st := stop(func(pid int, sig syscall.Signal) error {
				if pid != nobody {
					t.Fatalf("signal %d sent to pid %d", sig, pid)
				}
				switch sig {
				// Signal 0 asks whether the process is there.
				case 0:
					if gone {
						return syscall.ESRCH
					}
					return nil
				case syscall.SIGTERM:
					sent = append(sent, sig)
					gone, termed = c.goes && c.term == nil, true
					return c.term
				}
				sent = append(sent, sig)
				return c.kill
			}, nobody, c.wait, func() bool { return !(c.reused && termed) })
			if st.Target != "pid 5000042" || st.What != c.what || !errors.Is(st.Err, c.err) || (c.err == nil && st.Err != nil) {
				t.Errorf("step %+v, want %q with error %v", st, c.what, c.err)
			}
			if !slices.Equal(sent, c.sent) {
				t.Errorf("sent %v, want %v", sent, c.sent)
			}
		})
	}
}

func TestAStepsLineForTheLog(t *testing.T) {
	for _, c := range []struct {
		step Step
		want string
	}{
		// A session is the line's subject, as in every other line about one.
		{Step{Target: "scratch", What: "deleted with zellij"},
			"scratch :: recover: deleted with zellij"},
		// A server is a pid, with what is known of its session beside it.
		{Step{Target: "pid 4242", Session: Orphan{Name: "notes", FirstName: "quiet-mouse"}.said(), What: "stopped orphan server"},
			"pid 4242 (name: notes) :: recover: stopped orphan server"},
		{Step{Target: "pid 4242", Session: Orphan{FirstName: "quiet-mouse"}.said(), What: "kept orphan server: its first name is protected"},
			"pid 4242 (first name: quiet-mouse) :: recover: kept orphan server: its first name is protected"},
		{Step{Target: "pid 4242", Session: Orphan{}.said(), What: "stopped orphan server (SIGKILL)"},
			"pid 4242 (name unknown) :: recover: stopped orphan server (SIGKILL)"},
		// Why a step failed follows what it says.
		{Step{Target: "scratch", What: "zellij could not delete it", Err: errors.New("exit status 1")},
			"scratch :: recover: zellij could not delete it: exit status 1"},
		// A step about all the servers has no session to name.
		{Step{Target: "servers", What: "not stopped: every server looked orphaned, which points at the socket table"},
			"servers :: recover: not stopped: every server looked orphaned, which points at the socket table"},
	} {
		if got := c.step.Line(); got != c.want {
			t.Errorf("line %q, want %q", got, c.want)
		}
	}
}
