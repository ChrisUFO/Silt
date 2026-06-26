package semver

import "testing"

func TestLessThan(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"1.0.0", "1.0.0", false},
		{"0.1.0", "1.0.0", true},
		{"0.10.0", "0.9.0", false},
		{"1.0", "1.0.0", true},   // shorter = older when shared segments equal
		{"1.0.0", "1.0", false},
	}
	for _, c := range cases {
		if got := LessThan(c.a, c.b); got != c.want {
			t.Errorf("LessThan(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
