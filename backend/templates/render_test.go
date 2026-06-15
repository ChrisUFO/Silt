package templates

import (
	"strings"
	"testing"
	"time"
)

var frozenTime = time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)

func TestRender_DefaultPlaceholders(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "defaults-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "date={{date}} time={{time}} iso={{iso_date}} day={{weekday}}",
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if !strings.Contains(out, "date=2026-06-15") {
		t.Errorf("date not substituted: %q", out)
	}
	if !strings.Contains(out, "time=09:30") {
		t.Errorf("time not substituted: %q", out)
	}
	if !strings.Contains(out, "iso=2026-06-15T09:30:00Z") {
		t.Errorf("iso_date not substituted: %q", out)
	}
	if !strings.Contains(out, "day=Monday") {
		t.Errorf("weekday not substituted: %q", out)
	}
}

func TestRender_UserVars(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "vars-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "# {{title}} on {{date}}",
	}
	out, _ := Render(tpl, map[string]string{"title": "Sprint Planning"}, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if !strings.Contains(out, "# Sprint Planning") {
		t.Errorf("user var not substituted: %q", out)
	}
	if !strings.Contains(out, "on 2026-06-15") {
		t.Errorf("default not substituted alongside user var: %q", out)
	}
}

func TestRender_VarsOverrideDefault(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "override",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{date}}",
	}
	// A caller var named "date" overrides the built-in default.
	out, _ := Render(tpl, map[string]string{"date": "2020-01-01"}, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if !strings.Contains(out, "2020-01-01") {
		t.Errorf("var should override default: %q", out)
	}
	if strings.Contains(out, "2026-06-15") {
		t.Errorf("default should be overridden: %q", out)
	}
}

func TestRender_DeclaredPlaceholderDefault(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "default-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "team={{team}}",
		Placeholders:  []Placeholder{{Name: "team", Default: "Platform"}},
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if len(warnings) != 0 {
		t.Errorf("declared placeholder default should not warn: %v", warnings)
	}
	if !strings.Contains(out, "team=Platform") {
		t.Errorf("placeholder default not used: %q", out)
	}
}

func TestRender_DeclaredPlaceholderNoValue_LeavesLiteral(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "no-val",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{meeting_title}}",
		Placeholders:  []Placeholder{{Name: "meeting_title", Required: true}},
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	// Declared but no value: leave literal, no warning (it's recognized).
	if len(warnings) != 0 {
		t.Errorf("declared-but-unprovided should not warn: %v", warnings)
	}
	if !strings.Contains(out, "{{meeting_title}}") {
		t.Errorf("unprovided placeholder should stay literal: %q", out)
	}
}

func TestRender_UnknownPlaceholder_Warns(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "unknown",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{totally_unknown}} and {{date}}",
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "totally_unknown") {
		t.Errorf("warning should name the unknown placeholder: %q", warnings[0])
	}
	// The unknown token stays literal; the known one resolves.
	if !strings.Contains(out, "{{totally_unknown}}") {
		t.Errorf("unknown placeholder should stay literal: %q", out)
	}
	if !strings.Contains(out, "2026-06-15") {
		t.Errorf("known placeholder should resolve: %q", out)
	}
}

func TestRender_UnknownPlaceholderWarnedOnce(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "repeat",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{unknown}} {{unknown}} {{unknown}}",
	}
	_, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning (deduplicated), got %d: %v", len(warnings), warnings)
	}
}

func TestRender_SmartGraphEmbedPassthrough(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "embed-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "See {{embed:abc-123-def}} and {{date}}",
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if !strings.Contains(out, "{{embed:abc-123-def}}") {
		t.Errorf("embed syntax was altered: %q", out)
	}
	if !strings.Contains(out, "2026-06-15") {
		t.Errorf("date should still resolve: %q", out)
	}
	if len(warnings) != 0 {
		t.Errorf("embed passthrough should not warn: %v", warnings)
	}
}

func TestRender_BlockReferencePassthrough(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "ref-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "Link: ((abc-123-def)) and {{date}}",
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if !strings.Contains(out, "((abc-123-def))") {
		t.Errorf("block reference syntax was altered: %q", out)
	}
	if len(warnings) != 0 {
		t.Errorf("block reference passthrough should not warn: %v", warnings)
	}
}

func TestRender_EmptyVars(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "empty-vars",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{date}}",
	}
	out, _ := Render(tpl, map[string]string{}, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if !strings.Contains(out, "2026-06-15") {
		t.Errorf("date should resolve with empty vars: %q", out)
	}
}

func TestRender_NilTemplate(t *testing.T) {
	out, warnings := Render(nil, nil, RenderOptions{})
	if out != "" {
		t.Errorf("Render(nil) should return empty string, got %q", out)
	}
	if warnings != nil {
		t.Errorf("Render(nil) should return nil warnings, got %v", warnings)
	}
}

func TestRender_TimezoneOption(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "tz-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{time}}",
	}
	// 09:30 UTC → 05:30 in UTC-4 (America/New_York in EDT, but use a fixed
	// offset to avoid DST complexity).
	ny, _ := time.LoadLocation("America/New_York")
	out, _ := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: ny})
	// June → EDT (UTC-4), so 09:30Z → 05:30 EDT.
	if !strings.Contains(out, "05:30") {
		t.Errorf("timezone-adjusted time: expected 05:30 (EDT), got %q", out)
	}
}

func TestRender_DefaultTimeNow(t *testing.T) {
	// Zero-value RenderOptions should use time.Now() — verify it produces a
	// valid date string (not empty and not the zero value).
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "now-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{date}}",
	}
	out, _ := Render(tpl, nil, RenderOptions{})
	if out == "" || strings.Contains(out, "{{date}}") {
		t.Errorf("default Now should resolve date: %q", out)
	}
}

func TestRender_WhitespaceInToken(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "ws-test",
		Title:         "Test",
		Category:      "notes",
		Body:          "{{ date }} and {{time}}",
	}
	out, warnings := Render(tpl, nil, RenderOptions{Now: frozenTime, Timezone: time.UTC})
	if len(warnings) != 0 {
		t.Errorf("whitespace-padded tokens should resolve without warning: %v", warnings)
	}
	if !strings.Contains(out, "2026-06-15") {
		t.Errorf("{{ date }} should resolve: %q", out)
	}
}
