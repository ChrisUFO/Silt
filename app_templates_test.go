package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
