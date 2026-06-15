package templates

import (
	"strings"
	"testing"
	"time"
)

// TestSmoke_LoadAndRender is the Phase-1 smoke test: the embedded set loads,
// and rendering daily-note with a frozen time substitutes the default
// placeholders and leaves smart-graph syntax untouched. Comprehensive loader /
// validator / renderer / watcher tests land in Phase 6 (#58).
func TestSmoke_LoadAndRender(t *testing.T) {
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one embedded template, got none")
	}
	var daily *Template
	for _, t := range all {
		if t.ID == "daily-note" {
			daily = t
		}
	}
	if daily == nil {
		t.Fatalf("daily-note not in embedded set: %v", ids(all))
	}

	// Frozen reference time so the assertion is deterministic.
	frozen := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)
	rendered, warnings := Render(daily, nil, RenderOptions{Now: frozen, Timezone: time.UTC})
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings rendering daily-note, got %v", warnings)
	}
	if !strings.Contains(rendered, "2026-06-15") {
		t.Errorf("rendered body missing the date substitution: %q", rendered)
	}
	if !strings.Contains(rendered, "Monday") {
		t.Errorf("rendered body missing the weekday substitution (2026-06-15 is a Monday): %q", rendered)
	}
	// Task grammar participation: the rendered TODO TASK line must survive so
	// ParseFileContent recognises it downstream (spec-compat gate, SPECS §4).
	if !strings.Contains(rendered, "- [ ] TODO TASK") {
		t.Errorf("rendered body lost the TODO TASK shorthand: %q", rendered)
	}
}

// TestSmoke_SmartGraphPassthrough pins the spec-compatibility guarantee
// (SPECS §5.2): smart-graph syntax passes through the renderer byte-for-byte.
// {{embed:uuid}} has a colon (outside the placeholder grammar) and ((uuid))
// uses different delimiters, so neither is ever treated as a placeholder.
func TestSmoke_SmartGraphPassthrough(t *testing.T) {
	body := "See {{embed:abc-123-def}} and also ((abc-123-def)) plus {{date}}."
	tpl := &Template{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "passthrough-test",
		Title:         "Passthrough Test",
		Category:      "notes",
		Body:          body,
	}
	frozen := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)
	rendered, warnings := Render(tpl, nil, RenderOptions{Now: frozen, Timezone: time.UTC})
	if !strings.Contains(rendered, "{{embed:abc-123-def}}") {
		t.Errorf("embed syntax was altered: %q", rendered)
	}
	if !strings.Contains(rendered, "((abc-123-def))") {
		t.Errorf("block-reference syntax was altered: %q", rendered)
	}
	if !strings.Contains(rendered, "2026-06-15") {
		t.Errorf("date placeholder was not substituted: %q", rendered)
	}
	// The embed/reference tokens are NOT unknown placeholders (they don't match
	// the grammar at all), so they produce no warning.
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for smart-graph passthrough, got %v", warnings)
	}
}

// TestEmbeddedTemplates_NoDuplicateIDs is the Phase-1 compile-time unique-id
// guard: no two embedded templates share an id (which would be an authoring
// bug). The exact-roster assertion (the full 10 ids) lands in Phase 2.
func TestEmbeddedTemplates_NoDuplicateIDs(t *testing.T) {
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	seen := map[string]bool{}
	for _, tpl := range all {
		if seen[tpl.ID] {
			t.Fatalf("duplicate embedded template id %q", tpl.ID)
		}
		seen[tpl.ID] = true
	}
}

// TestBuiltinIDs_MatchesFilenames guards the filename==id convention that
// BuiltinIDs (the write-path read-only guard) relies on. Each embedded .md
// must be <id>.md and the parsed id must equal the filename stem.
func TestBuiltinIDs_MatchesFilenames(t *testing.T) {
	ids, err := BuiltinIDs()
	if err != nil {
		t.Fatalf("BuiltinIDs: %v", err)
	}
	all, err := EmbeddedTemplates()
	if err != nil {
		t.Fatalf("EmbeddedTemplates: %v", err)
	}
	if len(ids) != len(all) {
		t.Fatalf("BuiltinIDs count %d != EmbeddedTemplates count %d", len(ids), len(all))
	}
	for _, tpl := range all {
		if !ids[tpl.ID] {
			t.Errorf("embedded template id %q not in BuiltinIDs (filename convention broken)", tpl.ID)
		}
	}
}

func ids(ts []*Template) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
