package main

import (
	"os"
	"path/filepath"
	"testing"

	"silt/backend/vault"
)

// TestApp_ImportVault_OpensExtractedVault is the full pipeline: an active
// vault with one indexed page is exported, the archive is imported into a fresh
// empty folder, and the app then serves the round-tripped content from the new
// path — block identity preserved, fresh index built (not copied).
func TestApp_ImportVault_OpensExtractedVault(t *testing.T) {
	app, src := newMoveTestApp(t)
	taskID := "22222222-2222-2222-2222-222222222222"

	// Sanity: the source vault has the indexed task.
	blocksBefore, err := app.FetchPageBlocks("Work", "", "Inbox")
	if err != nil {
		t.Fatalf("pre-export FetchPageBlocks: %v", err)
	}
	if !blockPresent(blocksBefore, taskID) {
		t.Fatalf("pre-export: task %s missing", taskID)
	}

	// 1. Export the active vault to an archive.
	archive := filepath.Join(t.TempDir(), "roundtrip.silt-vault")
	if _, err := app.ExportVault(archive); err != nil {
		t.Fatalf("ExportVault: %v", err)
	}

	// 2. Import into a fresh empty folder + open it.
	dest := filepath.Join(t.TempDir(), "imported")
	if _, err := app.ImportVault(archive, dest); err != nil {
		t.Fatalf("ImportVault: %v", err)
	}

	// 3. The app now serves from dest.
	if app.vaultPath != dest {
		t.Errorf("app.vaultPath = %q, want %q", app.vaultPath, dest)
	}
	if !app.IsVaultInitialized() {
		t.Error("IsVaultInitialized should be true after import")
	}

	// settings.json now points at dest (SwitchVault persisted it).
	s, err := vault.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.VaultPath != dest {
		t.Errorf("settings vault_path = %q, want %q", s.VaultPath, dest)
	}

	// 4. The round-tripped block is served from the new location by id.
	blocksAfter, err := app.FetchPageBlocks("Work", "", "Inbox")
	if err != nil {
		t.Fatalf("post-import FetchPageBlocks: %v", err)
	}
	if !blockPresent(blocksAfter, taskID) {
		t.Errorf("post-import: task %s missing from %v", taskID, blocksAfter)
	}

	// 5. A fresh index was built at dest (never carried in the archive).
	if _, err := os.Stat(filepath.Join(dest, ".system", "index.sqlite")); err != nil {
		t.Errorf("dest should have a freshly-built index.sqlite: %v", err)
	}

	// 6. The source folder is untouched (import is non-destructive; the source
	//    stays exactly where it was on disk, only the app's active pointer moved).
	if _, err := os.Stat(filepath.Join(src, "Work", "Inbox.md")); err != nil {
		t.Errorf("source vault content should be untouched: %v", err)
	}
}

// TestApp_ImportVault_PropagatesValidationError surfaces a bad-archive error
// without switching the active vault.
func TestApp_ImportVault_PropagatesValidationError(t *testing.T) {
	app, src := newMoveTestApp(t)

	// A plain ZIP with no manifest is not a .silt-vault archive.
	notArchive := filepath.Join(t.TempDir(), "not-a-vault.silt-vault")
	writeFile(t, notArchive, "PK\x03\x04not actually a zip")
	dest := filepath.Join(t.TempDir(), "dest")

	_, err := app.ImportVault(notArchive, dest)
	if err == nil {
		t.Fatal("expected error for a non-archive, got nil")
	}
	// Active vault is untouched.
	if app.vaultPath != src {
		t.Errorf("app.vaultPath = %q, want %q (failed import must not switch)", app.vaultPath, src)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("destDir must not be created on a failed import")
	}
}

// TestApp_PickVaultArchive_NoContext guards the no-context precondition.
func TestApp_PickVaultArchive_NoContext(t *testing.T) {
	app := &App{spacesPerTab: 4}
	if _, err := app.PickVaultArchive(); err == nil {
		t.Fatal("expected error when app context is nil, got nil")
	}
}
