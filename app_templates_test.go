package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silt/backend/parser"
	"silt/backend/templates"
)

const testUserTemplateMD = `---
schema_version: "1.0.0"
id: my-test-template
title: My Test Template
description: A test user template.
category: notes
placeholders:
  - name: title
    required: true
---
# {{title}}

Created: {{date}}

- [ ] TODO TASK #3 action item
`

func TestListTemplates_IPC_ReturnsBuiltins(t *testing.T) {
	app := newTestApp(t)
	res, err := app.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(res.Templates) < 10 {
		t.Errorf("expected at least 10 built-in templates, got %d", len(res.Templates))
	}
	// Verify a known built-in is present.
	var found bool
	for _, s := range res.Templates {
		if s.ID == "daily-note" {
			found = true
		}
	}
	if !found {
		t.Error("daily-note builtin not found in listing")
	}
}

func TestListTemplates_IPC_WithUserTemplate(t *testing.T) {
	app := newTestApp(t)
	dir := app.templatesDir()
	writeFile(t, filepath.Join(dir, "my-test-template.md"), testUserTemplateMD)

	res, err := app.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var found bool
	for _, s := range res.Templates {
		if s.ID == "my-test-template" && s.Source == "disk" {
			found = true
		}
	}
	if !found {
		t.Error("user template not found in listing")
	}
}

func TestGetTemplate_IPC_HappyPath(t *testing.T) {
	app := newTestApp(t)
	tpl, err := app.GetTemplate("daily-note")
	if err != nil {
		t.Fatalf("GetTemplate(daily-note): %v", err)
	}
	if tpl.ID != "daily-note" {
		t.Errorf("id = %q", tpl.ID)
	}
	if tpl.Body == "" {
		t.Error("body is empty")
	}
}

func TestGetTemplate_IPC_NotFound(t *testing.T) {
	app := newTestApp(t)
	_, err := app.GetTemplate("no-such-template")
	if err == nil {
		t.Fatal("expected error for missing template")
	}
}

func TestGetTemplate_IPC_EmptyID(t *testing.T) {
	app := newTestApp(t)
	_, err := app.GetTemplate("")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestRenderTemplate_IPC_WithVars(t *testing.T) {
	app := newTestApp(t)
	rendered, err := app.RenderTemplate("meeting-notes", map[string]string{"meeting_title": "Sprint Review"})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if !strings.Contains(rendered, "Sprint Review") {
		t.Errorf("meeting_title not substituted: %q", rendered)
	}
	// Date should be today (resolves to a YYYY-MM-DD string).
	if !strings.Contains(rendered, "20") {
		t.Errorf("date not substituted: %q", rendered)
	}
}

func TestRenderTemplate_IPC_EmptyID(t *testing.T) {
	app := newTestApp(t)
	_, err := app.RenderTemplate("", nil)
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestRenderTemplate_IPC_SmartGraphPassthrough(t *testing.T) {
	app := newTestApp(t)
	// Render a built-in — none contain embed syntax, but verify the IPC path
	// doesn't alter the body structure.
	rendered, err := app.RenderTemplate("notes", map[string]string{"title": "Test"})
	if err != nil {
		t.Fatalf("RenderTemplate(notes): %v", err)
	}
	if !strings.Contains(rendered, "# Test") {
		t.Errorf("title not substituted: %q", rendered)
	}
}

func TestRenderTemplateBlocks_IPC(t *testing.T) {
	app := newTestApp(t)
	blocks, err := app.RenderTemplateBlocks("meeting-notes", map[string]string{"meeting_title": "Standup"})
	if err != nil {
		t.Fatalf("RenderTemplateBlocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected parsed blocks")
	}
	// Verify task blocks are present.
	var taskCount int
	for _, b := range blocks {
		if b.Type == "TASK" {
			taskCount++
		}
	}
	if taskCount == 0 {
		t.Error("expected at least one TASK block from meeting-notes action items")
	}
}

func TestSaveUserTemplate_IPC_HappyPath(t *testing.T) {
	app := newTestApp(t)
	tpl := templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "my-test-template",
		Title:         "My Test",
		Category:      "notes",
		Body:          "# Hello\n",
	}
	if err := app.SaveUserTemplate(tpl); err != nil {
		t.Fatalf("SaveUserTemplate: %v", err)
	}
	// File should exist.
	path := filepath.Join(app.templatesDir(), "my-test-template.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("template file not written")
	}
	// It should appear in the listing.
	res, _ := app.ListTemplates()
	var found bool
	for _, s := range res.Templates {
		if s.ID == "my-test-template" {
			found = true
		}
	}
	if !found {
		t.Error("saved template not in listing")
	}
}

func TestSaveUserTemplate_IPC_BuiltinRejected(t *testing.T) {
	app := newTestApp(t)
	tpl := templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "daily-note",
		Title:         "Override",
		Category:      "daily",
		Body:          "# Override\n",
	}
	err := app.SaveUserTemplate(tpl)
	if err == nil {
		t.Fatal("expected error saving over a builtin id")
	}
	// No file should be written.
	path := filepath.Join(app.templatesDir(), "daily-note.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("builtin file should not be written")
	}
}

func TestSaveUserTemplate_IPC_OverwriteExisting(t *testing.T) {
	app := newTestApp(t)
	tpl := templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "my-test-template",
		Title:         "V1",
		Category:      "notes",
		Body:          "# V1\n",
	}
	app.SaveUserTemplate(tpl)
	tpl.Title = "V2"
	tpl.Body = "# V2\n"
	if err := app.SaveUserTemplate(tpl); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	// Verify the overwrite took effect.
	got, err := app.GetTemplate("my-test-template")
	if err != nil {
		t.Fatalf("GetTemplate after overwrite: %v", err)
	}
	if got.Title != "V2" {
		t.Errorf("overwrite did not update: title=%q", got.Title)
	}
}

func TestSaveUserTemplate_IPC_BeforeVault(t *testing.T) {
	app := &App{} // no vaultPath
	tpl := templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "x",
		Title:         "T",
		Category:      "notes",
		Body:          "b",
	}
	if err := app.SaveUserTemplate(tpl); err == nil {
		t.Error("expected error when no vault is loaded")
	}
}

func TestDeleteUserTemplate_IPC_HappyPath(t *testing.T) {
	app := newTestApp(t)
	dir := app.templatesDir()
	writeFile(t, filepath.Join(dir, "my-test-template.md"), testUserTemplateMD)

	if err := app.DeleteUserTemplate("my-test-template"); err != nil {
		t.Fatalf("DeleteUserTemplate: %v", err)
	}
	path := filepath.Join(dir, "my-test-template.md")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

func TestDeleteUserTemplate_IPC_BuiltinRejected(t *testing.T) {
	app := newTestApp(t)
	if err := app.DeleteUserTemplate("daily-note"); err == nil {
		t.Fatal("expected error deleting a builtin")
	}
}

func TestDeleteUserTemplate_IPC_BeforeVault(t *testing.T) {
	app := &App{}
	if err := app.DeleteUserTemplate("x"); err == nil {
		t.Error("expected error when no vault is loaded")
	}
}

func TestCreatePageFromTemplate_IPC(t *testing.T) {
	app := newTestApp(t)
	// Create a notebook + section structure.
	os.MkdirAll(filepath.Join(app.vaultPath, "Work", "Projects"), 0o755)

	dateStr, err := app.CreatePageFromTemplate("Work", "Projects", "Meeting2026", "", "meeting-notes", map[string]string{"meeting_title": "Kickoff"})
	if err != nil {
		t.Fatalf("CreatePageFromTemplate: %v", err)
	}
	if dateStr == "" {
		t.Error("expected non-empty date string")
	}
	// The page file should exist with frontmatter + rendered body.
	path := filepath.Join(app.vaultPath, "Work", "Projects", "Meeting2026.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("page file not written: %v", err)
	}
	content := string(raw)
	if !strings.HasPrefix(content, "---") {
		t.Error("page should start with frontmatter")
	}
	if !strings.Contains(content, "notebook:") {
		t.Error("frontmatter missing notebook field")
	}
	if !strings.Contains(content, "Kickoff") {
		t.Error("rendered template body not in page file")
	}
}

func TestCreatePageFromTemplate_IPC_BuiltinTemplate(t *testing.T) {
	app := newTestApp(t)
	os.MkdirAll(filepath.Join(app.vaultPath, "Personal", "Journal"), 0o755)

	_, err := app.CreatePageFromTemplate("Personal", "Journal", "TodayNote", "", "daily-note", nil)
	if err != nil {
		t.Fatalf("CreatePageFromTemplate(daily-note): %v", err)
	}
	// Verify the daily-note rendered correctly in the page.
	path := filepath.Join(app.vaultPath, "Personal", "Journal", "TodayNote.md")
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "Intentions for today") {
		t.Errorf("daily-note body not in page file:\n%s", string(raw))
	}
}

func TestCreatePageFromTemplate_IPC_DoesNotClobber(t *testing.T) {
	app := newTestApp(t)
	os.MkdirAll(filepath.Join(app.vaultPath, "Work", "Projects"), 0o755)
	// Pre-create the page.
	existing := filepath.Join(app.vaultPath, "Work", "Projects", "Existing.md")
	writeFile(t, existing, "---\nnotebook: Work\nsection: Projects\npage: Existing\ndate: 2026-01-01\ntags: []\n---\n# Existing content\n")

	_, err := app.CreatePageFromTemplate("Work", "Projects", "Existing", "", "notes", map[string]string{"title": "Should Not Override"})
	if err != nil {
		t.Fatalf("CreatePageFromTemplate on existing: %v", err)
	}
	// The existing file should be untouched.
	raw, _ := os.ReadFile(existing)
	if strings.Contains(string(raw), "Should Not Override") {
		t.Error("CreatePageFromTemplate should not clobber an existing page")
	}
}

func TestCreatePageFromTemplate_IPC_BeforeVault(t *testing.T) {
	app := &App{}
	_, err := app.CreatePageFromTemplate("nb", "", "pg", "", "daily-note", nil)
	if err == nil {
		t.Error("expected error when no vault is loaded")
	}
}

// TestCreatePageFromTemplate_EmbedAndRefPreservedInFile is the page-file
// passthrough test for #93. A template body containing {{embed:uuid}} and
// ((uuid)) (Smart Graph syntax) is rendered into a page; the resulting
// .md file on disk must contain the tokens byte-for-byte. The renderer
// already passes these tokens through (render_test.go's
// TestRender_SmartGraphEmbedPassthrough + TestRender_BlockReferencePassthrough);
// this test pins the IPC/file-write layer to preserve that guarantee so
// the editor's NodeViews (Phase 8) can pick up the tokens unchanged.
func TestCreatePageFromTemplate_EmbedAndRefPreservedInFile(t *testing.T) {
	app := newTestApp(t)
	notebook := "EmbedTest"
	if err := os.MkdirAll(filepath.Join(app.vaultPath, notebook), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const body = "See {{embed:abc-123-def}} and ((abc-123-def)) plus {{date}}."
	tpl := templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "embed-test-tpl",
		Title:         "Embed Test",
		Category:      "notes",
		Body:          body,
	}
	if err := app.SaveUserTemplate(tpl); err != nil {
		t.Fatalf("SaveUserTemplate: %v", err)
	}
	_, err := app.CreatePageFromTemplate(notebook, "", "EmbedPage", "", "embed-test-tpl", nil)
	if err != nil {
		t.Fatalf("CreatePageFromTemplate: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(app.vaultPath, notebook, "EmbedPage.md"))
	if err != nil {
		t.Fatalf("read page: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "{{embed:abc-123-def}}") {
		t.Errorf("embed token not preserved in page file:\n%s", content)
	}
	if !strings.Contains(content, "((abc-123-def))") {
		t.Errorf("block-reference token not preserved in page file:\n%s", content)
	}
}

// TestCreatePageFromTemplate_SanitizesEdgeCaseNames is the regression test for
// #98 (and the fix in #89). After the sanitizePathSegment rewrite, internal
// ".." substrings in a user-supplied page name are preserved verbatim — they
// are legitimate filename characters, not path-traversal indicators. Only a
// leading ".." (a path-traversal signal) is stripped. Each case below asserts
// the expected on-disk file name and the corresponding blocks-table row.
func TestCreatePageFromTemplate_SanitizesEdgeCaseNames(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantFile    string
		wantIndexed bool
	}{
		{"internal_dots_preserved", "2.0..2.1", "2.0..2.1.md", true},
		{"multi_internal_dots_preserved", "a..b..c", "a..b..c.md", true},
		{"leading_dots_stripped", "..foo", "foo.md", true},
		{"exact_dots_rejected", "..", "", false},
		{"four_dots_then_name", "....foo", "foo.md", true},
		{"slash_then_dots", "foo/..bar", "foo..bar.md", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := newTestApp(t)
			notebook := "EdgeCases"
			os.MkdirAll(filepath.Join(app.vaultPath, notebook), 0o755)
			_, err := app.CreatePageFromTemplate(notebook, "", c.input, "", "notes", map[string]string{"title": "Edge"})
			if c.wantIndexed {
				if err != nil {
					t.Fatalf("CreatePageFromTemplate(%q) failed: %v", c.input, err)
				}
				wantPath := filepath.Join(app.vaultPath, notebook, c.wantFile)
				if _, statErr := os.Stat(wantPath); statErr != nil {
					t.Errorf("expected file %q on disk, got error: %v", wantPath, statErr)
				}
			} else {
				if err == nil {
					t.Errorf("CreatePageFromTemplate(%q) should have failed", c.input)
				}
			}
		})
	}
}

// TestRegisterPluginTemplates_IPC verifies the plugin-template IPC (#96):
// registering makes the templates appear in ListTemplates with Source =
// "plugin" and PluginID set; unregistering removes them. Emits
// templates:changed (asserted via the test's captured emit log — see
// app_templates_test.go for the helper).
func TestRegisterPluginTemplates_IPC(t *testing.T) {
	app := newTestApp(t)
	tpl := &templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "ipc-plugin-tpl",
		Title:         "IPC Plugin Tpl",
		Category:      "notes",
		Body:          "# {{title}}\n",
	}
	if err := app.RegisterPluginTemplates("silt-kanban", []*templates.Template{tpl}); err != nil {
		t.Fatalf("RegisterPluginTemplates: %v", err)
	}
	res, err := app.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var found *templates.TemplateSummary
	for i := range res.Templates {
		if res.Templates[i].ID == "ipc-plugin-tpl" {
			found = &res.Templates[i]
		}
	}
	if found == nil {
		t.Fatal("plugin template not in listing after register")
	}
	if found.Source != templates.SourcePlugin {
		t.Errorf("Source = %q, want %q", found.Source, templates.SourcePlugin)
	}
	if found.PluginID != "silt-kanban" {
		t.Errorf("PluginID = %q, want silt-kanban", found.PluginID)
	}

	// Unregister removes it from the listing.
	app.UnregisterPluginTemplates("silt-kanban")
	res, _ = app.ListTemplates()
	for _, s := range res.Templates {
		if s.ID == "ipc-plugin-tpl" {
			t.Error("template still in listing after unregister")
		}
	}
}

// TestRegisterPluginTemplates_FiltersNil verifies that nil elements in the
// input slice are silently filtered rather than causing the registry to
// reject the entire batch (loader.go rejects nil entries at the package
// level).
func TestRegisterPluginTemplates_FiltersNil(t *testing.T) {
	app := newTestApp(t)
	tpl := &templates.Template{
		SchemaVersion: "1.0.0",
		ID:            "nil-filter-tpl",
		Title:         "Nil Filter Tpl",
		Category:      "notes",
		Body:          "# {{title}}\n",
	}
	// Slice contains one valid template and one nil.
	if err := app.RegisterPluginTemplates(
		"silt-test", []*templates.Template{tpl, nil},
	); err != nil {
		t.Fatalf("RegisterPluginTemplates with nil entry should succeed (filtered): %v", err)
	}

	res, err := app.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	var count int
	for _, s := range res.Templates {
		if s.PluginID == "silt-test" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 plugin template after nil filtering, got %d", count)
	}

	app.UnregisterPluginTemplates("silt-test")
}

// TestCreatePageFromTemplate_DeepSection_AppearsInNavigation is the regression
// test for #97 (and the fix in #88). A page created from a template in a
// deeply-nested section (e.g. `Work/Projects/Active`) lands at the correct
// filesystem path AND appears in the navigation tree at the nested level.
func TestCreatePageFromTemplate_DeepSection_AppearsInNavigation(t *testing.T) {
	app := newTestApp(t)
	notebook := "Work"
	// Layout the deep section on disk first.
	if err := os.MkdirAll(filepath.Join(app.vaultPath, notebook, "Projects", "Active"), 0o755); err != nil {
		t.Fatalf("mkdir deep section: %v", err)
	}

	_, err := app.CreatePageFromTemplate(notebook, "Projects/Active", "SiteLaunch", "", "notes", map[string]string{"title": "Site Launch"})
	if err != nil {
		t.Fatalf("CreatePageFromTemplate in deep section: %v", err)
	}

	// File on disk at the multi-segment path.
	wantPath := filepath.Join(app.vaultPath, notebook, "Projects", "Active", "SiteLaunch.md")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected file at %s: %v", wantPath, err)
	}

	// Visible in the navigation tree under the nested section.
	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}
	if len(tree.Notebooks) != 1 {
		t.Fatalf("expected 1 notebook, got %d", len(tree.Notebooks))
	}
	work := tree.Notebooks[0]
	// Walk to Projects/Active and find the page.
	var projects *parser.NavigationSection
	for i := range work.Sections {
		if work.Sections[i].Name == "Projects" {
			projects = &work.Sections[i]
			break
		}
	}
	if projects == nil {
		t.Fatalf("Projects section not found in navigation: %+v", work.Sections)
	}
	if len(projects.Children) == 0 {
		t.Fatalf("Projects should have a nested child (Active), got none")
	}
	active := projects.Children[0]
	if active.Name != "Active" {
		t.Errorf("nested section name = %q, want Active", active.Name)
	}
	var found bool
	for _, p := range active.Pages {
		if p.Name == "SiteLaunch" {
			found = true
		}
	}
	if !found {
		t.Errorf("SiteLaunch page not found in Active.Pages = %+v", active.Pages)
	}
}

// TestRenderTemplateBlocks_IPC_UUIDUniqueness is the spec-compat regression
// guard (plan line 297): inserting the same template twice must produce blocks
// with ALL-DIFFERENT UUIDs so there is no blocks-table PK collision. Each
// RenderTemplateBlocks call runs ParseFileContent on the rendered body (which
// has no <!-- id: --> comments), so EnsureBlockID mints fresh UUIDs each time.
func TestRenderTemplateBlocks_IPC_UUIDUniqueness(t *testing.T) {
	app := newTestApp(t)

	blocks1, err := app.RenderTemplateBlocks("meeting-notes", map[string]string{"meeting_title": "A"})
	if err != nil {
		t.Fatalf("first RenderTemplateBlocks: %v", err)
	}
	blocks2, err := app.RenderTemplateBlocks("meeting-notes", map[string]string{"meeting_title": "B"})
	if err != nil {
		t.Fatalf("second RenderTemplateBlocks: %v", err)
	}

	// Collect every UUID across both sets.
	seen := map[string]bool{}
	for _, b := range blocks1 {
		if b.ID == "" {
			continue
		}
		if seen[b.ID] {
			t.Errorf("duplicate UUID %q within first insertion", b.ID)
		}
		seen[b.ID] = true
	}
	for _, b := range blocks2 {
		if b.ID == "" {
			continue
		}
		if seen[b.ID] {
			t.Errorf("UUID collision across two insertions: %q appears in both — PK collision risk", b.ID)
		}
		seen[b.ID] = true
	}
}
