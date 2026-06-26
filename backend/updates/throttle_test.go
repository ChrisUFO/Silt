package updates

import (
	"testing"
	"time"
)

func TestShouldAutoCheck(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name        string
		last        time.Time
		autoEnabled bool
		want        bool
	}{
		{"auto off → never", now, false, false},
		{"never checked + auto on → yes", time.Time{}, true, true},
		{"checked 1h ago + auto on → no (within 24h)", now.Add(-1 * time.Hour), true, false},
		{"checked 23h ago + auto on → no (within 24h)", now.Add(-23 * time.Hour), true, false},
		{"checked 24h ago + auto on → yes (at threshold)", now.Add(-24 * time.Hour), true, true},
		{"checked 48h ago + auto on → yes (beyond)", now.Add(-48 * time.Hour), true, true},
		{"checked recently + auto off → no", now.Add(-48 * time.Hour), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldAutoCheck(c.last, c.autoEnabled); got != c.want {
				t.Errorf("ShouldAutoCheck(%v, %v) = %v, want %v", c.last, c.autoEnabled, got, c.want)
			}
		})
	}
}
