package i18n

import (
	"sort"
	"strings"
	"testing"
)

func TestCatalogsHaveTheSameKeys(t *testing.T) {
	enKeys, koKeys := map[string]bool{}, map[string]bool{}
	for _, k := range Keys(English) {
		enKeys[k] = true
	}
	for _, k := range Keys(Korean) {
		koKeys[k] = true
	}
	var missing []string
	for k := range enKeys {
		// Singular forms are English only: Korean has no plural.
		if strings.HasSuffix(k, ".one") {
			continue
		}
		if !koKeys[k] {
			missing = append(missing, "ko lacks "+k)
		}
	}
	for k := range koKeys {
		if !enKeys[k] {
			missing = append(missing, "en lacks "+k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("catalogs differ:\n%s", strings.Join(missing, "\n"))
	}
}

func TestPlaceholdersMatch(t *testing.T) {
	// Each Korean text takes the same arguments as its English one, in order.
	verbs := func(s string) string {
		var out []string
		for i := 0; i < len(s)-1; i++ {
			if s[i] == '%' && s[i+1] != '%' {
				out = append(out, s[i:i+2])
				i++
			}
		}
		return strings.Join(out, " ")
	}
	for k, e := range en {
		if strings.HasSuffix(k, ".one") {
			continue
		}
		if verbs(e) != verbs(ko[k]) {
			t.Errorf("%s: %q vs %q", k, verbs(e), verbs(ko[k]))
		}
	}
}

func TestDetect(t *testing.T) {
	env := func(kv ...string) func(string) (string, bool) {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	cases := []struct {
		env  func(string) (string, bool)
		want Lang
	}{
		{env(), English},
		{env("LANG", "ko_KR.UTF-8"), Korean},
		{env("LANG", "ko_KR.UTF-8", "LC_ALL", "C.UTF-8"), English},
		{env("LANG", "en_US.UTF-8", "LC_MESSAGES", "ko_KR"), Korean},
		{env("LANG", "ko_KR.UTF-8", "ZELLIJ_REAPER_LANG", "en"), English},
		{env("ZELLIJ_REAPER_LANG", "ko"), Korean},
	}
	for i, c := range cases {
		if got := Detect(c.env); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestKoreanParticlesAfterNamesAreFixed(t *testing.T) {
	// A particle right after a session name would need to follow its last
	// sound, which varies; texts put a fixed word in between instead.
	for k, v := range ko {
		for _, p := range []string{"%s를", "%s을", "%s는", "%s은", "%s가", "%s이 ", "%s로"} {
			if strings.Contains(v, p) {
				t.Errorf("%s: %q has %q", k, v, p)
			}
		}
	}
}
