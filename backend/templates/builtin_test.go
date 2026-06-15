package templates

import (
	"testing"
	"time"

	"silt/backend/parser"
)

// builtinRoster is the curated first-class set (the 10 templates from #56).
// Adding a built-in means adding its id here AND its .md file; the
// TestEmbeddedTemplates_Roster guard turns a mismatch into a loud failure so
// the two never drift.
var builtinRoster = map[string]bool{
	"notes":         true,
	"meeting-notes": true,
	"standup-notes": true,
	"daily-note":    true,
	"project-brief": true,
	"one-on-one":    true,
	"weekly-review": true,
	"decision-log":  true,
	"reading-notes": true,
	"retrospective": true,
}

// TestEmbeddedTemplates_Roster pins the curated first-class set: every embedded
// template parses + validates, the roster is exactly the 10 known ids, and each
// carries the required identity fields + a non-empty body.
func TestEmbeddedTemplates_Roster(t *testing.T) {
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	if len(all) != len(builtinRoster) {
		t.Fatalf("expected %d embedded templates, got %d (%v)", len(builtinRoster), len(all), ids(all))
	}
	for _, tpl := range all {
		if !builtinRoster[tpl.ID] {
			t.Errorf("unexpected embedded template id %q", tpl.ID)
		}
		if tpl.Source != SourceBuiltin {
			t.Errorf("embedded template %q source = %q, want %q", tpl.ID, tpl.Source, SourceBuiltin)
		}
		if tpl.Title == "" || tpl.Category == "" || tpl.Body == "" {
			t.Errorf("embedded template %q has an empty identity/body field: %+v", tpl.ID, tpl)
		}
		if !IsKnownCategory(tpl.Category) {
			t.Errorf("embedded template %q uses unknown category %q", tpl.ID, tpl.Category)
		}
	}
}

// TestEmbeddedTemplates_RoundTripParseFileContent is the spec-compatibility
// gate (SPECS §4 / §3.3): every rendered built-in must parse cleanly through
// the real AST parser, and the templates that carry TODO TASK action items must
// surface them as recognized TASK blocks (so they flow into Kanban/Agenda/
// Calendar). Rendered with a frozen time so the assertion is deterministic.
func TestEmbeddedTemplates_RoundTripParseFileContent(t *testing.T) {
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	frozen := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)
	for _, tpl := range all {
		rendered, warnings := Render(tpl, nil, RenderOptions{Now: frozen, Timezone: time.UTC})
		for _, w := range warnings {
			t.Errorf("template %q rendered with warning: %s", tpl.ID, w)
		}
		// The parser expects frontmatter in a real page file; templates carry
		// their own frontmatter-shaped body but here we only exercise the body
		// (block) parsing, so wrap minimally.
		blocks, _, _, _, perr := parser.ParseFileContent(
			"---\nnotebook: nb\nsection: \npage: pg\ndate: 2026-06-15\ntags: []\n---\n"+rendered,
			"nb", "", "pg", "2026-06-15", 4,
		)
		if perr != nil {
			t.Errorf("template %q failed ParseFileContent: %v\nrendered:\n%s", tpl.ID, perr, rendered)
			continue
		}
		if len(blocks) == 0 {
			t.Errorf("template %q produced no parsed blocks", tpl.ID)
		}
	}
}

// TestEmbeddedTemplates_AllActionItemsAreTasks verifies EVERY built-in that
// carries TODO TASK lines produces recognized TASK blocks after rendering +
// parsing. This is the comprehensive regression guard for the Kanban/Agenda/
// Calendar contract — the original meeting-notes-only test let the
// weekly-review ordered-list bug slip through.
func TestEmbeddedTemplates_AllActionItemsAreTasks(t *testing.T) {
	// expectedTasks maps template id → expected number of TASK blocks after
	// render + ParseFileContent. Templates without TODO TASK lines expect 0.
	expectedTasks := map[string]int{
		"notes":         0,
		"meeting-notes": 2,
		"standup-notes": 0,
		"daily-note":    1,
		"project-brief": 2,
		"one-on-one":    2,
		"weekly-review": 2,
		"decision-log":  0,
		"reading-notes": 0,
		"retrospective": 2,
	}
	// placeholderVars supplies the required user-declared placeholder for
	// templates that declare one, so no token stays unresolved.
	placeholderVars := map[string]map[string]string{
		"notes":         {"title": "Test Note"},
		"meeting-notes": {"meeting_title": "Test Meeting"},
		"standup-notes": {"project_name": "Test Project"},
		"project-brief": {"project_name": "Test Project"},
		"one-on-one":    {"with": "Test Person"},
		"decision-log":  {"title": "Test Decision"},
		"reading-notes": {"title": "Test Book"},
	}

	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	frozen := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)

	for _, tpl := range all {
		t.Run(tpl.ID, func(t *testing.T) {
			vars := placeholderVars[tpl.ID]
			rendered, warnings := Render(tpl, vars, RenderOptions{Now: frozen, Timezone: time.UTC})
			for _, w := range warnings {
				t.Errorf("template %q rendered with warning: %s", tpl.ID, w)
			}
			blocks, _, _, _, perr := parser.ParseFileContent(
				"---\nnotebook: nb\nsection: \npage: pg\ndate: 2026-06-15\ntags: []\n---\n"+rendered,
				"nb", "", "pg", "2026-06-15", 4,
			)
			if perr != nil {
				t.Fatalf("template %q ParseFileContent: %v", tpl.ID, perr)
			}
			var tasks int
			for _, b := range blocks {
				if b.Type == parser.BlockTask {
					tasks++
				}
			}
			want := expectedTasks[tpl.ID]
			if tasks != want {
				t.Errorf("template %q rendered %d TASK blocks, want %d\n--- rendered ---\n%s", tpl.ID, tasks, want, rendered)
			}
		})
	}
}
