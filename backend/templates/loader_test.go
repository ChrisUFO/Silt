package templates

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// writeTemplate is a test helper that writes a template .md file to dir.
func writeTemplate(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", dir, err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

const validUserTemplate = `---
schema_version: "1.0.0"
id: my-template
title: My Template
description: A test template.
category: notes
icon: note
placeholders:
  - name: title
    description: The note title.
    required: true
---
# {{title}}

Created: {{date}}

## Notes

- 
`

func TestListTemplates_EmptyDir_BuiltinsOnly(t *testing.T) {
	dir := t.TempDir()
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(res.Templates) != len(builtinRoster) {
		t.Fatalf("expected %d templates (builtins only), got %d", len(builtinRoster), len(res.Templates))
	}
}

func TestListTemplates_MissingDir_BuiltinsOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates on missing dir: %v", err)
	}
	if len(res.Templates) != len(builtinRoster) {
		t.Fatalf("expected %d builtins on missing dir, got %d", len(builtinRoster), len(res.Templates))
	}
}

func TestListTemplates_EmptyStringDir_BuiltinsOnly(t *testing.T) {
	res, err := ListTemplates("")
	if err != nil {
		t.Fatalf("ListTemplates(\"\"): %v", err)
	}
	if len(res.Templates) != len(builtinRoster) {
		t.Fatalf("expected %d builtins on empty dir, got %d", len(builtinRoster), len(res.Templates))
	}
}

func TestListTemplates_UserOnly(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	// The user template + all builtins.
	if len(res.Templates) != len(builtinRoster)+1 {
		t.Fatalf("expected %d (user + builtins), got %d", len(builtinRoster)+1, len(res.Templates))
	}
	var found bool
	for _, s := range res.Templates {
		if s.ID == "my-template" {
			found = true
			if s.Source != SourceDisk {
				t.Errorf("user template source = %q, want %q", s.Source, SourceDisk)
			}
		}
	}
	if !found {
		t.Error("user template not found in listing")
	}
}

func TestListTemplates_MixedDedupOnDiskWins(t *testing.T) {
	dir := t.TempDir()
	// A user template whose id collides with a builtin (daily-note). On-disk
	// should win the dedup.
	userVariant := strings.Replace(validUserTemplate, `id: my-template`, `id: daily-note`, 1)
	userVariant = strings.Replace(userVariant, `title: My Template`, `title: My Daily Override`, 1)
	writeTemplate(t, dir, "daily-note.md", userVariant)

	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var dn *TemplateSummary
	for i := range res.Templates {
		if res.Templates[i].ID == "daily-note" {
			dn = &res.Templates[i]
		}
	}
	if dn == nil {
		t.Fatal("daily-note not in listing")
	}
	if dn.Source != SourceDisk {
		t.Errorf("colliding id source = %q, want %q (on-disk wins)", dn.Source, SourceDisk)
	}
	if dn.Title != "My Daily Override" {
		t.Errorf("colliding id title = %q, want the on-disk override", dn.Title)
	}
}

func TestListTemplates_MalformedFileCollected(t *testing.T) {
	dir := t.TempDir()
	// A template with valid frontmatter but an empty body — fails validation
	// (body is required), so ListTemplates collects it into Errors.
	writeTemplate(t, dir, "broken.md", "---\nschema_version: \"1.0.0\"\nid: broken\ntitle: Broken\ncategory: notes\n---\n")
	writeTemplate(t, dir, "good.md", validUserTemplate)
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Error("expected at least one load error for the malformed file")
	}
	// The good template + builtins should still be present.
	var found bool
	for _, s := range res.Templates {
		if s.ID == "my-template" {
			found = true
		}
	}
	if !found {
		t.Error("good template dropped because of malformed sibling")
	}
}

func TestListTemplates_SortByCategoryThenTitle(t *testing.T) {
	dir := t.TempDir()
	// Two user templates in categories that sort differently from their titles.
	writeTemplate(t, dir, "z-template.md", `---
schema_version: "1.0.0"
id: z-template
title: Zebra
category: aaa
---
body
`)
	writeTemplate(t, dir, "a-template.md", `---
schema_version: "1.0.0"
id: a-template
title: Apple
category: zzz
---
body
`)
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	// Verify the sort: category "aaa" (Zebra) comes before "zzz" (Apple).
	var zebraIdx, appleIdx int = -1, -1
	for i, s := range res.Templates {
		if s.ID == "z-template" {
			zebraIdx = i
		}
		if s.ID == "a-template" {
			appleIdx = i
		}
	}
	if zebraIdx == -1 || appleIdx == -1 {
		t.Fatalf("user templates not found in listing")
	}
	if zebraIdx > appleIdx {
		t.Errorf("expected category 'aaa' (idx %d) before 'zzz' (idx %d)", zebraIdx, appleIdx)
	}
}

func TestListTemplates_UnknownCategoryWarning(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "weird.md", `---
schema_version: "1.0.0"
id: weird
title: Weird
category: exotic-category
---
body
`)
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var found bool
	for _, s := range res.Templates {
		if s.ID == "weird" {
			found = true
		}
	}
	if !found {
		t.Error("unknown-category template should still load (additive)")
	}
	if len(res.Warnings) == 0 {
		t.Error("expected a forward-compat warning for the unknown category")
	}
}

func TestGetTemplate_OnDisk(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)
	tpl, err := GetTemplate(dir, "my-template")
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if tpl.ID != "my-template" {
		t.Errorf("GetTemplate returned id %q", tpl.ID)
	}
	if tpl.Body == "" {
		t.Error("GetTemplate returned empty body")
	}
}

func TestGetTemplate_BuiltinFallback(t *testing.T) {
	tpl, err := GetTemplate("", "daily-note")
	if err != nil {
		t.Fatalf("GetTemplate(daily-note) pre-vault: %v", err)
	}
	if tpl.ID != "daily-note" {
		t.Errorf("GetTemplate returned id %q", tpl.ID)
	}
}

func TestGetTemplate_NotFound(t *testing.T) {
	_, err := GetTemplate("", "no-such-template")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestGetTemplate_ReadDirError(t *testing.T) {
	// Pass a path that exists as a FILE (not a directory) → os.ReadDir fails
	// with an error that is NOT IsNotExist, so it propagates rather than
	// falling through to the embedded lookup.
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(tmpFile, []byte("x"), 0o644)
	_, err := GetTemplate(tmpFile, "any-id")
	if err == nil {
		t.Error("expected error when templatesDir is a file, not a directory")
	}
}

func TestListTemplates_RealIOError(t *testing.T) {
	// POSIX permission/dir-as-file semantics are not enforced on Windows;
	// os.ReadDir on a file path may succeed where it fails on Linux/macOS.
	if runtime.GOOS == "windows" {
		t.Skip("os.ReadDir on a file path does not error on Windows")
	}
	// Same idea: a file path passed as dir → os.ReadDir returns a non-IsNotExist
	// error → ListTemplates returns that error (not just the builtins).
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	os.WriteFile(tmpFile, []byte("x"), 0o644)
	_, err := ListTemplates(tmpFile)
	if err == nil {
		t.Error("expected error when templatesDir is a file")
	}
}

func TestParseTemplateBytes_NoFrontmatter(t *testing.T) {
	// A file with no frontmatter should still parse: id from filename, title
	// from first heading, schema_version defaulted.
	raw := []byte("# Hello World\n\nSome content.\n")
	tpl, err := ParseTemplateBytes(raw, "auto-id.md", SourceDisk)
	if err != nil {
		t.Fatalf("ParseTemplateBytes (no frontmatter): %v", err)
	}
	if tpl.ID != "auto-id" {
		t.Errorf("id = %q, want auto-id (from filename)", tpl.ID)
	}
	if tpl.Title != "Hello World" {
		t.Errorf("title = %q, want Hello World (from first heading)", tpl.Title)
	}
	if tpl.SchemaVersion != SupportedSchemaVersion {
		t.Errorf("schema_version = %q, want default %q", tpl.SchemaVersion, SupportedSchemaVersion)
	}
}

func TestParseTemplateBytes_NoHeading_TitleFromFilename(t *testing.T) {
	// No heading in the body → title falls back to the filename stem.
	raw := []byte("just prose, no heading\n")
	tpl, err := ParseTemplateBytes(raw, "my-note.md", SourceDisk)
	if err != nil {
		t.Fatalf("ParseTemplateBytes: %v", err)
	}
	if tpl.Title != "my-note" {
		t.Errorf("title = %q, want my-note (from filename)", tpl.Title)
	}
}

func TestSplitFrontmatter_NoFrontmatter(t *testing.T) {
	fm, body, hasFM := splitFrontmatter("just body\nno frontmatter")
	if hasFM {
		t.Error("expected hasFM=false for content without frontmatter")
	}
	if fm != "" {
		t.Errorf("expected empty fm, got %q", fm)
	}
	if body != "just body\nno frontmatter" {
		t.Errorf("body mismatch: %q", body)
	}
}

func TestSplitFrontmatter_UnclosedFrontmatter(t *testing.T) {
	// Opening --- with no closing --- should be treated as no frontmatter.
	raw := "---\nthis is not closed\nbody line"
	fm, body, hasFM := splitFrontmatter(raw)
	if hasFM {
		t.Error("expected hasFM=false for unclosed frontmatter")
	}
	_ = fm
	_ = body
}

func TestAsSummary(t *testing.T) {
	tpl := &Template{
		ID:           "test",
		Title:        "Test",
		Category:     "notes",
		Placeholders: []Placeholder{{Name: "x"}},
		Body:         "body content",
		Source:       SourceDisk,
	}
	s := tpl.AsSummary()
	if s.ID != "test" || s.Title != "Test" || s.Category != "notes" || s.Source != SourceDisk {
		t.Errorf("AsSummary lost fields: %+v", s)
	}
	if len(s.Placeholders) != 1 || s.Placeholders[0].Name != "x" {
		t.Errorf("AsSummary lost placeholders: %+v", s.Placeholders)
	}
	// Summary should not carry the Body.
}

func TestIsKnownCategory(t *testing.T) {
	if !IsKnownCategory("notes") {
		t.Error("notes should be known")
	}
	if IsKnownCategory("exotic") {
		t.Error("exotic should not be known")
	}
}

// sortedIDs is a helper for deterministic comparison.
func sortedIDs(items []TemplateSummary) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = s.ID
	}
	sort.Strings(out)
	return out
}

// touchFile sets the mtime to now (for cache tests).
func touchFile(t *testing.T, path string) {
	t.Helper()
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatalf("Chtimes %s: %v", path, err)
	}
}

// --- Plugin template registry (#96) --------------------------------------

func TestRegisterPluginTemplates_HappyPath(t *testing.T) {
	ResetPluginRegistry()
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "plugin-sprint",
		Title:         "Sprint",
		Category:      "projects",
		Body:          "# {{title}}\n",
		Source:        SourcePlugin,
		PluginID:      "silt-kanban",
	}
	if err := RegisterPluginTemplates("silt-kanban", []*Template{tpl}); err != nil {
		t.Fatalf("RegisterPluginTemplates: %v", err)
	}
	summaries := ListPluginTemplates()
	if len(summaries) != 1 {
		t.Fatalf("ListPluginTemplates count = %d, want 1", len(summaries))
	}
	if summaries[0].PluginID != "silt-kanban" {
		t.Errorf("summary PluginID = %q, want silt-kanban", summaries[0].PluginID)
	}
	if summaries[0].Source != SourcePlugin {
		t.Errorf("summary Source = %q, want %q", summaries[0].Source, SourcePlugin)
	}
}

func TestRegisterPluginTemplates_RejectsEmptyPluginID(t *testing.T) {
	ResetPluginRegistry()
	if err := RegisterPluginTemplates("", []*Template{{ID: "x"}}); err == nil {
		t.Fatal("expected error for empty plugin id")
	}
}

func TestRegisterPluginTemplates_RejectsNilSlice(t *testing.T) {
	ResetPluginRegistry()
	if err := RegisterPluginTemplates("p", nil); err == nil {
		t.Fatal("expected error for nil slice")
	}
}

func TestRegisterPluginTemplates_RejectsMismatchedSource(t *testing.T) {
	ResetPluginRegistry()
	err := RegisterPluginTemplates("p", []*Template{{
		SchemaVersion: "1.0.0", ID: "x", Title: "X", Category: "notes", Body: "b",
		Source: SourceBuiltin,
	}})
	if err == nil {
		t.Fatal("expected error for non-plugin source")
	}
}

func TestRegisterPluginTemplates_RejectsMismatchedPluginID(t *testing.T) {
	ResetPluginRegistry()
	err := RegisterPluginTemplates("plugin-a", []*Template{{
		SchemaVersion: "1.0.0", ID: "x", Title: "X", Category: "notes", Body: "b",
		Source: SourcePlugin, PluginID: "plugin-b",
	}})
	if err == nil {
		t.Fatal("expected error for mismatched plugin id")
	}
}

func TestUnregisterPluginTemplates_Idempotent(t *testing.T) {
	ResetPluginRegistry()
	UnregisterPluginTemplates("never-registered")
	UnregisterPluginTemplates("never-registered")
}

func TestGetPluginTemplate_ResolvesURI(t *testing.T) {
	ResetPluginRegistry()
	_ = RegisterPluginTemplates("silt-kanban", []*Template{{
		SchemaVersion: "1.0.0", ID: "sprint", Title: "Sprint",
		Category: "projects", Body: "# {{title}}",
		Source: SourcePlugin, PluginID: "silt-kanban",
	}})
	got, err := GetPluginTemplate("plugin://silt-kanban/sprint")
	if err != nil {
		t.Fatalf("GetPluginTemplate: %v", err)
	}
	if got.ID != "sprint" || got.Title != "Sprint" {
		t.Errorf("resolved template = %+v", got)
	}
}

func TestGetPluginTemplate_NotFound(t *testing.T) {
	ResetPluginRegistry()
	if _, err := GetPluginTemplate("plugin://missing/missing"); err == nil {
		t.Fatal("expected error for unregistered plugin")
	}
}

func TestGetPluginTemplate_InvalidURI(t *testing.T) {
	ResetPluginRegistry()
	if _, err := GetPluginTemplate("not-a-uri"); err == nil {
		t.Fatal("expected error for invalid URI")
	}
	if _, err := GetPluginTemplate("plugin://"); err == nil {
		t.Fatal("expected error for uri missing plugin id")
	}
	if _, err := GetPluginTemplate("plugin://plugin-only/"); err == nil {
		t.Fatal("expected error for uri missing template id")
	}
}

func TestListTemplates_IncludesPluginTemplates(t *testing.T) {
	ResetPluginRegistry()
	dir := t.TempDir()
	_ = RegisterPluginTemplates("silt-kanban", []*Template{{
		SchemaVersion: "1.0.0", ID: "plugin-template", Title: "Plugin Tpl",
		Category: "projects", Body: "# plugin",
		Source: SourcePlugin, PluginID: "silt-kanban",
	}})
	res, err := ListTemplates(dir)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var found bool
	for _, s := range res.Templates {
		if s.ID == "plugin-template" {
			found = true
			if s.PluginID != "silt-kanban" {
				t.Errorf("PluginID = %q", s.PluginID)
			}
		}
	}
	if !found {
		t.Errorf("plugin template not in listing")
	}
}

func TestGetTemplate_PluginURI(t *testing.T) {
	ResetPluginRegistry()
	_ = RegisterPluginTemplates("silt-kanban", []*Template{{
		SchemaVersion: "1.0.0", ID: "x", Title: "X",
		Category: "notes", Body: "b",
		Source: SourcePlugin, PluginID: "silt-kanban",
	}})
	got, err := GetTemplate("", "plugin://silt-kanban/x")
	if err != nil {
		t.Fatalf("GetTemplate via plugin URI: %v", err)
	}
	if got.ID != "x" {
		t.Errorf("id = %q", got.ID)
	}
}

func TestRejectPluginIDInFrontmatter(t *testing.T) {
	if err := rejectPluginIDInFrontmatter([]byte("plugin_id: foo")); err == nil {
		t.Error("expected error for plugin_id: line")
	}
	if err := rejectPluginIDInFrontmatter([]byte("title: ok\nbody: hello")); err != nil {
		t.Errorf("benign frontmatter should pass: %v", err)
	}
}
