package procfs

import "testing"

func TestAWindowsProgramShowsByItsName(t *testing.T) {
	p := &Proc{Comm: "ping", Argv: []string{`C:\Windows\system32\ping.EXE`, "-n", "900", "127.0.0.1"}}
	if got := p.CommandLine(); got != "ping -n 900 127.0.0.1" {
		t.Errorf("command line %q", got)
	}
	if got := p.Name(); got != "ping" {
		t.Errorf("name %q", got)
	}
	// A Unix command line stays as it was typed.
	q := &Proc{Comm: "python3", Argv: []string{"/usr/bin/python3", "app.py"}}
	if got := q.CommandLine(); got != "/usr/bin/python3 app.py" {
		t.Errorf("unix command line %q", got)
	}
}
