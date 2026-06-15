package templates

import (
	"strings"
	"testing"
	"time"
)

// snapshotFrozenTime is the deterministic reference time used to render every
// built-in template for snapshot pinning. Changing it requires regenerating
// the expected strings below. The values are intentionally not derived from
// the template bodies at runtime — the test asserts specific, human-verified
// substrings so an accidental placeholder-format change (e.g. date layout) is
// caught loudly.
var snapshotFrozenTime = time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)

// expectedSnaps maps each built-in id to substrings its rendered output must
// contain at the frozen time. This is a lightweight snapshot (substring checks
// rather than full golden files) — robust against whitespace/formatting churn
// while still catching the drift that matters: placeholder resolution, task
// grammar survival, and date/weekday formatting.
var expectedSnaps = map[string][]string{
	"notes":         {"2026-06-15", "09:30", "# "},
	"meeting-notes": {"2026-06-15", "09:30", "Monday", "TODO TASK"},
	"standup-notes": {"2026-06-15", "Monday", "Standup"},
	"daily-note":    {"2026-06-15", "Monday", "TODO TASK"},
	"project-brief": {"2026-06-15", "TODO TASK"},
	"one-on-one":    {"2026-06-15", "Monday", "TODO TASK"},
	"weekly-review": {"2026-06-15", "TODO TASK"},
	"decision-log":  {"2026-06-15", "ADR-0001"},
	"reading-notes": {"2026-06-15"},
	"retrospective": {"2026-06-15", "TODO TASK"},
}

func TestSnapshot_AllBuiltinsRender(t *testing.T) {
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	for _, tpl := range all {
		t.Run(tpl.ID, func(t *testing.T) {
			// Render with the frozen time + empty vars (declared placeholders
			// stay literal — the snapshot captures this intentionally).
			rendered, warnings := Render(tpl, nil, RenderOptions{Now: snapshotFrozenTime, Timezone: time.UTC})
			// Built-in templates should have NO unknown-placeholder warnings
			// (every token is either a default or a declared placeholder).
			if len(warnings) != 0 {
				t.Errorf("built-in %q rendered with warnings: %v", tpl.ID, warnings)
			}
			expected := expectedSnaps[tpl.ID]
			for _, want := range expected {
				if !strings.Contains(rendered, want) {
					t.Errorf("built-in %q rendered output missing %q\n--- rendered ---\n%s", tpl.ID, want, rendered)
				}
			}
			// No template body should exceed ~80 rendered lines (the #56 AC).
			lineCount := strings.Count(rendered, "\n") + 1
			if lineCount > 80 {
				t.Errorf("built-in %q rendered to %d lines (max ~80)", tpl.ID, lineCount)
			}
		})
	}
}

// TestSnapshot_DateFormatStability pins the date/time/iso_date/weekday formats
// so an accidental format change (e.g. switching to a different date layout)
// is caught. If this test needs to change, the format change is intentional
// and the snapshot expectations above should be updated in lockstep.
func TestSnapshot_DateFormatStability(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "format-pin",
		Title:         "T",
		Category:      "notes",
		Body:          "D[{{date}}]T[{{time}}]I[{{iso_date}}]W[{{weekday}}]",
	}
	out, _ := Render(tpl, nil, RenderOptions{Now: snapshotFrozenTime, Timezone: time.UTC})
	expected := "D[2026-06-15]T[09:30]I[2026-06-15T09:30:00Z]W[Monday]"
	if out != expected {
		t.Errorf("date/time format drift:\n  got:  %q\n  want: %q", out, expected)
	}
}
