package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"silt/backend/vault"
)

// TestApp_ExportVault_StreamsActiveVault verifies ExportVault reads the active
// vault path under the read lock, excludes the reproducible index, and produces
// a readable .silt-vault archive carrying the indexed page.
func TestApp_ExportVault_StreamsActiveVault(t *testing.T) {
	app, src := newMoveTestApp(t)

	dest := filepath.Join(t.TempDir(), "backup.silt-vault")
	res, err := app.ExportVault(dest)
	if err != nil {
		t.Fatalf("ExportVault: %v", err)
	}
	if res.FilesArchived == 0 {
		t.Error("FilesArchived should be > 0")
	}
	if res.PageFileCount == 0 {
		t.Error("PageFileCount should be > 0 (Work/Inbox.md was indexed)")
	}
	if !res.SkippedIndex {
		t.Error("SkippedIndex should be true (active vault had an index.sqlite)")
	}

	// The active vault is untouched (read-only export).
	if app.vaultPath != src {
		t.Errorf("app.vaultPath = %q, want %q (export must not switch vault)", app.vaultPath, src)
	}

	// The archive is a readable ZIP that contains the indexed page + the
	// manifest, and excludes the index artifacts.
	zr, err := zip.OpenReader(dest)
	if err != nil {
		t.Fatalf("zip.OpenReader: %v", err)
	}
	defer zr.Close()
	sawPage := false
	sawManifest := false
	for _, f := range zr.File {
		if f.Name == "Work/Inbox.md" {
			sawPage = true
		}
		if f.Name == vault.ArchiveManifestPath {
			sawManifest = true
		}
		if vault.IsIndexArtifactName(f.Name) {
			t.Errorf("index artifact present in archive: %q", f.Name)
		}
	}
	if !sawPage {
		t.Error("Work/Inbox.md missing from archive")
	}
	if !sawManifest {
		t.Error("manifest.json missing from archive")
	}
}

// TestApp_ExportVault_NoVaultOpen guards the no-vault-open precondition.
func TestApp_ExportVault_NoVaultOpen(t *testing.T) {
	app := &App{spacesPerTab: 4} // no vault initialized
	if _, err := app.ExportVault("/tmp/whatever.silt-vault"); err == nil {
		t.Fatal("expected error when no vault is open, got nil")
	}
}

// TestApp_PickVaultExportPath_NoContext guards the no-context precondition
// (ctx is nil in tests; the binding surfaces this as an actionable error
// rather than a nil panic).
func TestApp_PickVaultExportPath_NoContext(t *testing.T) {
	app := &App{spacesPerTab: 4}
	if _, err := app.PickVaultExportPath("x.silt-vault"); err == nil {
		t.Fatal("expected error when app context is nil, got nil")
	}
}

// TestApp_ExportVault_EmptyVaultSucceeds guards the freshly-scaffolded-vault
// edge: a vault with no notebooks still produces a valid archive (config.yaml +
// themes/templates/plugins/.system contents, zero pages).
func TestApp_ExportVault_EmptyVaultSucceeds(t *testing.T) {
	settingsDir := t.TempDir()
	t.Setenv("APPDATA", settingsDir)
	t.Setenv("XDG_CONFIG_HOME", settingsDir)

	src := t.TempDir()
	if err := vault.ScaffoldVault(src); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	if err := vault.SaveSettings(&vault.AppSettings{
		VaultPath:   src,
		ActiveTheme: "cyber_forest",
		ThemeMode:   "dark",
	}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	app := &App{spacesPerTab: 4}
	if err := app.initializeVaultServices(src); err != nil {
		t.Fatalf("initializeVaultServices: %v", err)
	}
	t.Cleanup(func() { _ = app.CloseVault() })

	dest := filepath.Join(t.TempDir(), "empty.silt-vault")
	res, err := app.ExportVault(dest)
	if err != nil {
		t.Fatalf("ExportVault on empty vault: %v", err)
	}
	if res.FilesArchived == 0 {
		t.Error("FilesArchived should be > 0 (.system/ scaffolding was archived)")
	}
	if res.PageFileCount != 0 {
		t.Errorf("PageFileCount = %d, want 0 (no notebooks)", res.PageFileCount)
	}
	if _, statErr := os.Stat(dest); statErr != nil {
		t.Errorf("archive not created: %v", statErr)
	}
}
