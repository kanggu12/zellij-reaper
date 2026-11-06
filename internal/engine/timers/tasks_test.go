package timers

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestATaskScriptEndsInExitZero(t *testing.T) {
	// PowerShell exits 1 when the last command failed, even with its error
	// silenced: an install whose last line removes a names task that is not
	// there failed for that alone.
	var script string
	w := Tasks{Run: func(name string, args ...string) (string, error) {
		b, err := base64.StdEncoding.DecodeString(args[len(args)-1])
		if err != nil {
			t.Fatal(err)
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		script = string(utf16.Decode(u))
		return "", nil
	}}
	if _, err := w.ps("Unregister-ScheduledTask -TaskName 'x' -ErrorAction SilentlyContinue"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(script), "exit 0") {
		t.Errorf("script ends:\n%s", script)
	}
	if !strings.HasPrefix(script, "$ErrorActionPreference = 'Stop'") {
		t.Errorf("script starts:\n%s", script)
	}
}
