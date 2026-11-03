package duration

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"0", 0},
		{"45s", 45 * time.Second},
		{"90m", 90 * time.Minute},
		{"12h", 12 * time.Hour},
		{"3d", 72 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"1.5h", 90 * time.Minute},
		{" 2h ", 2 * time.Hour},
		{"500ms", 500 * time.Millisecond},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	// Each of these would otherwise turn into a surprising threshold.
	for _, in := range []string{"", "90", "-1h", "h", "3x", "1h-", "1..5h"} {
		if d, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", in, d)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := map[time.Duration]string{
		-5 * time.Second:                "0s",
		42 * time.Second:                "42s",
		12 * time.Minute:                "12m",
		2*time.Hour + 14*time.Minute:    "2h 14m",
		3 * time.Hour:                   "3h",
		3*24*time.Hour + 2*time.Hour:    "3d 2h",
		5 * 24 * time.Hour:              "5d",
		2*time.Hour + 30*time.Second:    "2h",
		26*time.Hour + 59*time.Minute:   "1d 2h",
		59*time.Minute + 59*time.Second: "59m",
	}
	for in, want := range cases {
		if got := Format(in); got != want {
			t.Errorf("Format(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSettingRoundTrip(t *testing.T) {
	// Setting must print values that Parse reads back unchanged.
	for _, d := range []time.Duration{0, time.Minute, time.Hour, 36 * time.Hour, 3 * 24 * time.Hour, 15 * 24 * time.Hour, 90 * time.Second} {
		s := Setting(d)
		back, err := Parse(s)
		if err != nil || back != d {
			t.Errorf("Setting(%v) = %q, which parses to %v, %v", d, s, back, err)
		}
	}
	if got := Setting(3 * 24 * time.Hour); got != "3d" {
		t.Errorf("Setting(3d) = %q", got)
	}
}
