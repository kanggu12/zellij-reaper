package timers

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestTaskTimesAreLocal(t *testing.T) {
	// The status script prints UTC, and every message shows a time as it is:
	// left in UTC, a pass due at 07:35 in Seoul read "11-07 22:35".
	w := Tasks{Run: func(string, ...string) (string, error) {
		return `{"State":"Ready","Last":"","Next":"2026-11-07T22:35:23.0000000Z","Exec":"conhost.exe","Args":"--headless run --quiet","Interval":"PT1H"}`, nil
	}}
	st := w.askStatus(TaskName)
	want := time.Date(2026, 11, 7, 22, 35, 23, 0, time.UTC)
	if !st.Next.Equal(want) || st.Next.Location() != time.Local || !st.Last.IsZero() {
		t.Errorf("next %v in %v, last %v", st.Next, st.Next.Location(), st.Last)
	}
	if !st.Installed || st.Interval != time.Hour || st.ExecStart != "run --quiet" {
		t.Errorf("status %+v", st)
	}
}

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
