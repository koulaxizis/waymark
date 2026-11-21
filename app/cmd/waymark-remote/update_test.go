package main

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		tag, cur string
		want     bool
	}{
		{"v0.2.0", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},
		{"v0.1.1", "0.1.0", true},
		{"v0.1.0", "0.2.0", false},
		{"v1.0.0", "0.9.9", true},
		{"v0.1.0", "0.1.0-dev", true},
		{"v0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if got := newer(c.tag, c.cur); got != c.want {
			t.Errorf("newer(%q,%q)=%v", c.tag, c.cur, got)
		}
	}
}
