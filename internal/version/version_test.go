package version

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.1.0", "v0.1.1", true},
		{"v0.2.0", "v0.1.9", false},
		{"v0.1.10", "v0.1.9", false},
		{"dev", "v0.0.1", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "garbage", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.cur, c.latest, got)
		}
	}
}
