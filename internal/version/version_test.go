package version

import "testing"

func TestShortNamesAReleaseAndOtherwiseTheCommit(t *testing.T) {
	for _, c := range []struct{ v, c, want string }{
		{"v0.9.0", "e820a93", "v0.9.0"},
		{"v1.2.3-rc.1", "", "v1.2.3-rc.1"},
		{"v0.9.1-0.20261108003831-1839aebf23d4", "1839aeb", "dev 1839aeb"},
		{"v0.9.1-0.20261108003831-1839aebf23d4+dirty", "1839aeb", "dev 1839aeb"},
		{"v0.0.0-20261108003831-1839aebf23d4", "1839aeb", "dev 1839aeb"},
		{"v0.10.0-rc.1.0.20261108003831-1839aebf23d4", "1839aeb", "dev 1839aeb"},
		{"dev", "1839aeb", "dev 1839aeb"},
		{"dev", "", "dev"},
	} {
		if got := short(c.v, c.c); got != c.want {
			t.Errorf("short(%q, %q) = %q, want %q", c.v, c.c, got, c.want)
		}
	}
}
