package main

import (
	"testing"

	"silt/backend/config"
)

// TestCustomDictionary_RoundTrip covers the #196 custom-dictionary IPC: get is
// empty initially, add lowercases + persists + returns the list, duplicate adds
// are idempotent, remove works, and empty/whitespace input is rejected. Mirrors
// the atomic config-RMW path the production bindings use.
func TestCustomDictionary_RoundTrip(t *testing.T) {
	app := newTestApp(t)

	// Empty + non-nil to start.
	got, err := app.GetCustomDictionary()
	if err != nil {
		t.Fatalf("GetCustomDictionary: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty initial dictionary, got %v", got)
	}

	// Add lowercases the word.
	res, err := app.AddCustomDictionaryWord("TypeScript")
	if err != nil {
		t.Fatalf("Add TypeScript: %v", err)
	}
	if len(res) != 1 || res[0] != "typescript" {
		t.Errorf("add returned %v, want [typescript]", res)
	}

	// Get reflects the add.
	got, err = app.GetCustomDictionary()
	if err != nil {
		t.Fatalf("GetCustomDictionary: %v", err)
	}
	if len(got) != 1 || got[0] != "typescript" {
		t.Errorf("get after add: %v, want [typescript]", got)
	}

	// Duplicate add is idempotent.
	res, err = app.AddCustomDictionaryWord("typescript")
	if err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	if len(res) != 1 {
		t.Errorf("duplicate add should be idempotent, got %v", res)
	}

	// Add a second word.
	if _, err := app.AddCustomDictionaryWord("OAuth"); err != nil {
		t.Fatalf("Add OAuth: %v", err)
	}
	got, err = app.GetCustomDictionary()
	if err != nil {
		t.Fatalf("GetCustomDictionary: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 words after two adds, got %v", got)
	}

	// Remove (case-insensitive match — normalize lowercased both).
	res, err = app.RemoveCustomDictionaryWord("TYPESCRIPT")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(res) != 1 || res[0] != "oauth" {
		t.Errorf("remove returned %v, want [oauth]", res)
	}

	// Removing a word not present is a no-op (idempotent).
	res, err = app.RemoveCustomDictionaryWord("nonexistent")
	if err != nil {
		t.Fatalf("Remove nonexistent: %v", err)
	}
	if len(res) != 1 {
		t.Errorf("remove of absent word should be idempotent, got %v", res)
	}
}

// TestCustomDictionary_Validation confirms empty/whitespace input is rejected
// rather than producing an empty-dictionary entry.
func TestCustomDictionary_Validation(t *testing.T) {
	app := newTestApp(t)

	for _, bad := range []string{"", "   ", "\t\n"} {
		if _, err := app.AddCustomDictionaryWord(bad); err == nil {
			t.Errorf("AddCustomDictionaryWord(%q) should error", bad)
		}
	}

	// Nothing was added despite the rejected calls.
	got, err := app.GetCustomDictionary()
	if err != nil {
		t.Fatalf("GetCustomDictionary: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("dictionary should still be empty after rejected adds, got %v", got)
	}
}

// TestCustomDictionary_Persists confirms the word survives a fresh config Load
// (the atomic config.Save wrote it to disk, not just the in-memory copy).
func TestCustomDictionary_Persists(t *testing.T) {
	app := newTestApp(t)
	if _, err := app.AddCustomDictionaryWord("docker"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	loaded, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(loaded.Editor.CustomDictionary) != 1 || loaded.Editor.CustomDictionary[0] != "docker" {
		t.Errorf("custom_dictionary did not persist: %v", loaded.Editor.CustomDictionary)
	}
}
