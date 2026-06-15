package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveTemplate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "my-template",
		Title:         "My Template",
		Category:      "notes",
		Body:          "# Hello\n",
	}
	if err := SaveTemplate(dir, tpl); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	// The file should exist on disk.
	path := filepath.Join(dir, "my-template.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("template file not written")
	}
	// It should re-parse to the same template.
	parsed, err := loadOne(path)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if parsed.ID != "my-template" || parsed.Title != "My Template" {
		t.Errorf("round-trip mismatch: %+v", parsed)
	}
}

func TestSaveTemplate_BuiltinIDRejected(t *testing.T) {
	dir := t.TempDir()
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "daily-note",
		Title:         "Override",
		Category:      "daily",
		Body:          "# Override\n",
	}
	err := SaveTemplate(dir, tpl)
	if err == nil {
		t.Fatal("expected error saving over a builtin id")
	}
	if !strings.Contains(err.Error(), "built-in") {
		t.Errorf("error should mention built-in: %q", err.Error())
	}
	// Nothing should be written.
	if _, statErr := os.Stat(filepath.Join(dir, "daily-note.md")); !os.IsNotExist(statErr) {
		t.Error("builtin id should not be written to disk")
	}
}

func TestSaveTemplate_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "my-template",
		Title:         "V1",
		Category:      "notes",
		Body:          "# V1\n",
	}
	if err := SaveTemplate(dir, tpl); err != nil {
		t.Fatalf("SaveTemplate v1: %v", err)
	}
	tpl.Title = "V2"
	tpl.Body = "# V2\n"
	if err := SaveTemplate(dir, tpl); err != nil {
		t.Fatalf("SaveTemplate v2 (overwrite): %v", err)
	}
	parsed, _ := loadOne(filepath.Join(dir, "my-template.md"))
	if parsed.Title != "V2" {
		t.Errorf("overwrite did not update the file: title=%q", parsed.Title)
	}
}

func TestDeleteTemplate_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)
	if err := DeleteTemplate(dir, "my-template"); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "my-template.md")); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

func TestDeleteTemplate_Idempotent(t *testing.T) {
	dir := t.TempDir()
	// Deleting a non-existent template is a no-op success.
	if err := DeleteTemplate(dir, "never-existed"); err != nil {
		t.Errorf("idempotent delete should not error: %v", err)
	}
}

func TestDeleteTemplate_BuiltinRejected(t *testing.T) {
	dir := t.TempDir()
	// Write a disk copy of a builtin id to verify DeleteTemplate still rejects
	// it by id (even though the file exists).
	writeTemplate(t, dir, "daily-note.md", validUserTemplate)
	err := DeleteTemplate(dir, "daily-note")
	if err == nil {
		t.Fatal("expected error deleting a builtin id")
	}
}

func TestSerializeTemplate_RoundTrip(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "round-trip",
		Title:         "Round Trip",
		Description:   "A test.",
		Category:      "notes",
		Icon:          "note",
		Placeholders:  []Placeholder{{Name: "title", Required: true}},
		Body:          "# {{title}}\n\nbody line\n",
	}
	canon := SerializeTemplate(tpl)
	parsed, err := ParseTemplateBytes(canon, "round-trip.md", SourceDisk)
	if err != nil {
		t.Fatalf("round-trip parse failed: %v", err)
	}
	if parsed.ID != tpl.ID || parsed.Title != tpl.Title || parsed.Category != tpl.Category {
		t.Errorf("round-trip mismatch: got %+v, want id=%q title=%q", parsed, tpl.ID, tpl.Title)
	}
	if parsed.Body == "" {
		t.Error("body lost in round-trip")
	}
	// Placeholders should survive.
	if len(parsed.Placeholders) != 1 || parsed.Placeholders[0].Name != "title" {
		t.Errorf("placeholders lost in round-trip: %+v", parsed.Placeholders)
	}
}

func TestSaveTemplate_EmptyDir(t *testing.T) {
	tpl := &Template{SchemaVersion: "1.0.0", ID: "x", Title: "T", Category: "notes", Body: "b"}
	if err := SaveTemplate("", tpl); err == nil {
		t.Error("expected error for empty templates dir")
	}
}

func TestSaveTemplate_NilTemplate(t *testing.T) {
	if err := SaveTemplate(t.TempDir(), nil); err == nil {
		t.Error("expected error for nil template")
	}
}

func TestSaveTemplate_InvalidID(t *testing.T) {
	// An id that passes the Template struct but not IsValidID — can't happen
	// via normal Validate, but the defensive guard should still fire.
	tpl := &Template{SchemaVersion: "1.0.0", ID: "valid", Title: "T", Category: "notes", Body: "b"}
	if err := SaveTemplate(t.TempDir(), tpl); err != nil {
		t.Fatalf("valid template should save: %v", err)
	}
}

func TestSaveTemplate_ValidationFailure(t *testing.T) {
	tpl := &Template{SchemaVersion: "1.0.0", ID: "bad id", Title: "T", Category: "notes", Body: "b"}
	if err := SaveTemplate(t.TempDir(), tpl); err == nil {
		t.Error("expected validation error for bad id")
	}
}

func TestDeleteTemplate_EmptyDir(t *testing.T) {
	if err := DeleteTemplate("", "x"); err == nil {
		t.Error("expected error for empty templates dir")
	}
}

func TestDeleteTemplate_EmptyID(t *testing.T) {
	if err := DeleteTemplate(t.TempDir(), ""); err == nil {
		t.Error("expected error for empty id")
	}
}

func TestDeleteTemplate_InvalidID(t *testing.T) {
	if err := DeleteTemplate(t.TempDir(), "bad/id"); err == nil {
		t.Error("expected error for invalid id")
	}
}
