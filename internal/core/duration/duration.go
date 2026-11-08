// Package duration parses and prints the lengths of time used in the settings
// ("90m", "12h", "3d", "1d12h") and in what the tool shows ("2h 14m").
//
// time.ParseDuration has no unit longer than an hour, and thresholds for idle
// sessions are naturally written in days, so this package adds "d" (24h) and
// "w" (7d) on top of Go's own units.
package duration

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// units maps every accepted suffix to its length. Longer suffixes that share a
// first letter with a shorter one ("ms" and "m") are matched first by Parse.
var units = map[string]time.Duration{
	"ms": time.Millisecond,
	"s":  time.Second,
	"m":  time.Minute,
	"h":  time.Hour,
	"d":  24 * time.Hour,
	"w":  7 * 24 * time.Hour,
}

// Parse reads a length of time such as "45m", "12h", "3d" or "1d12h".
// A bare "0" means zero (used to switch an age check off). Negative values are
// rejected because a threshold in the past has no meaning here.
func Parse(s string) (time.Duration, error) {
	// Whitespace around the value is a typo, not a different value.
	s = strings.TrimSpace(s)
	// An empty value would silently become zero and disable a safety check.
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	// "0" with no unit is the one unitless value that is unambiguous.
	if s == "0" {
		return 0, nil
	}
	// A leading minus sign is refused outright instead of parsed.
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("duration %q is negative", s)
	}
	var total time.Duration
	rest := s
	for rest != "" {
		// Read the number part: digits with at most one decimal point.
		i := 0
		for i < len(rest) && (rest[i] == '.' || (rest[i] >= '0' && rest[i] <= '9')) {
			i++
		}
		// A unit with no number in front ("h") is malformed.
		if i == 0 {
			return 0, fmt.Errorf("duration %q: expected a number at %q", s, rest)
		}
		num, err := strconv.ParseFloat(rest[:i], 64)
		if err != nil {
			return 0, fmt.Errorf("duration %q: %v", s, err)
		}
		rest = rest[i:]
		// Read the unit: "ms" before "m" so "500ms" is not read as minutes.
		unit := ""
		for _, u := range []string{"ms", "s", "m", "h", "d", "w"} {
			if strings.HasPrefix(rest, u) {
				unit = u
				break
			}
		}
		// A number with no unit ("90") is ambiguous: minutes or hours?
		if unit == "" {
			return 0, fmt.Errorf("duration %q: missing unit (use s, m, h, d or w)", s)
		}
		rest = rest[len(unit):]
		// Past what a Duration holds, the conversion wraps around: a huge
		// wait would come out as zero, which switches the wait off.
		part := num * float64(units[unit])
		if part >= float64(math.MaxInt64)-float64(total) {
			return 0, fmt.Errorf("duration %q is too long", s)
		}
		total += time.Duration(part)
	}
	return total, nil
}

// MustParse is Parse for values written in the source code, which are known to
// be valid; it panics on a typo so tests catch it at once.
func MustParse(s string) time.Duration {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// Format prints a length of time for people, with the two largest units that
// matter: "45s", "12m", "2h 14m", "3d 2h". Seconds are dropped once the value
// is a minute or more, because nothing here is decided by the second.
func Format(d time.Duration) string {
	// Clock skew can make an age slightly negative; show it as "0s".
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && minutes > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// Setting prints a duration back in the compact form the settings file uses
// ("1h", "3d", "1d12h"), so `config show` echoes values the way they are written.
func Setting(d time.Duration) string {
	// Zero is written as a bare "0", which Parse accepts.
	if d == 0 {
		return "0"
	}
	var b strings.Builder
	// Walk from the largest unit down, writing each one that is present.
	for _, u := range []string{"w", "d", "h", "m", "s"} {
		n := d / units[u]
		if n > 0 {
			fmt.Fprintf(&b, "%d%s", n, u)
			d -= n * units[u]
		}
	}
	// Sub-second remainders only appear in tests; keep them exact.
	if d > 0 {
		fmt.Fprintf(&b, "%dms", d/time.Millisecond)
	}
	return b.String()
}
