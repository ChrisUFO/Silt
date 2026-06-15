package templates

import (
	"strings"
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

// TestEmbeddedTemplates_ActionItemsAreTasks verifies the templates that declare
// action items render them as recognized TASK blocks (the Kanban/Agenda/Calendar
// integration point). A TODO TASK line must parse to a TASK block.
func TestEmbeddedTemplates_ActionItemsAreTasks(t *testing.T) {
	tpl, err := GetTemplate("", "meeting-notes")
	if err != nil {
		t.Fatalf("GetTemplate(meeting-notes): %v", err)
	}
	rendered, _ := Render(tpl, map[string]string{"meeting_title": "Sprint Planning"}, RenderOptions{
		Now: time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC),
	})
	if !strings.Contains(rendered, "Sprint Planning") {
		t.Errorf("meeting_title var not substituted: %q", rendered)
	}
	blocks, _, _, _, err := parser.ParseFileContent(
		"---\nnotebook: nb\nsection: \npage: pg\ndate: 2026-06-15\ntags: []\n---\n"+rendered,
		"nb", "", "pg", "2026-06-15", 4,
	)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	var tasks int
	for _, b := range blocks {
		if b.Type == parser.BlockTask {
			tasks++
		}
	}
	if tasks == 0 {
		t.Errorf("meeting-notes rendered %d TASK blocks; expected action items to parse as tasks", tasks)
	}
}
