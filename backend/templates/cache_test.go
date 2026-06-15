package templates

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCachedGetTemplate_DiskHit(t *testing.T) {
	ResetCacheForTests()
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)

	t1, err := CachedGetTemplate(dir, "my-template")
	if err != nil {
		t.Fatalf("first CachedGetTemplate: %v", err)
	}
	// Second call should hit the cache (same object pointer).
	t2, err := CachedGetTemplate(dir, "my-template")
	if err != nil {
		t.Fatalf("second CachedGetTemplate: %v", err)
	}
	if t1 != t2 {
		t.Error("expected cache hit (same pointer)")
	}
}

func TestCachedGetTemplate_BuiltinFromEmbed(t *testing.T) {
	ResetCacheForTests()
	t1, err := CachedGetTemplate("", "daily-note")
	if err != nil {
		t.Fatalf("CachedGetTemplate(daily-note) pre-vault: %v", err)
	}
	if t1.ID != "daily-note" {
		t.Errorf("got id %q", t1.ID)
	}
}

func TestCachedGetTemplate_NotFound(t *testing.T) {
	ResetCacheForTests()
	_, err := CachedGetTemplate("", "no-such-id")
	if err == nil {
		t.Error("expected error for missing template")
	}
}

func TestCachedGetTemplate_MtimeReload(t *testing.T) {
	ResetCacheForTests()
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)

	t1, _ := CachedGetTemplate(dir, "my-template")

	// Change the file content + touch the mtime.
	updated := strings.Replace(validUserTemplate, "My Template", "Updated Title", 1)
	time.Sleep(10 * time.Millisecond) // ensure mtime differs
	writeTemplate(t, dir, "my-template.md", updated)
	touchFile(t, filepath.Join(dir, "my-template.md"))

	t2, err := CachedGetTemplate(dir, "my-template")
	if err != nil {
		t.Fatalf("post-mtime CachedGetTemplate: %v", err)
	}
	if t2.Title != "Updated Title" {
		t.Errorf("mtime change did not trigger reload: title=%q", t2.Title)
	}
	if t1 == t2 {
		t.Error("expected a fresh parse after mtime change (different pointer)")
	}
}

func TestInvalidateTemplateCache_One(t *testing.T) {
	ResetCacheForTests()
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)

	t1, _ := CachedGetTemplate(dir, "my-template")
	InvalidateTemplateCache("my-template")
	t2, _ := CachedGetTemplate(dir, "my-template")
	if t1 == t2 {
		t.Error("expected a fresh parse after InvalidateTemplateCache")
	}
}

func TestInvalidateTemplateCache_All(t *testing.T) {
	ResetCacheForTests()
	dir := t.TempDir()
	writeTemplate(t, dir, "my-template.md", validUserTemplate)

	t1, _ := CachedGetTemplate(dir, "my-template")
	InvalidateTemplateCache() // no args → flush all
	t2, _ := CachedGetTemplate(dir, "my-template")
	if t1 == t2 {
		t.Error("expected a fresh parse after full cache flush")
	}
}

func TestCachedGetTemplate_InvalidFileReturnsError(t *testing.T) {
	ResetCacheForTests()
	dir := t.TempDir()
	// A file that exists but fails validation (empty body).
	writeTemplate(t, dir, "bad.md", "---\nschema_version: \"1.0.0\"\nid: bad\ntitle: Bad\ncategory: notes\n---\n")
	_, err := CachedGetTemplate(dir, "bad")
	if err == nil {
		t.Error("expected error for invalid on-disk template")
	}
}
