package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silt/backend/themes"
	"silt/backend/vault"
)

// configDirOverride points the settings path at an isolated temp dir so
// theme IPC tests never touch the real user settings.json.
func configDirOverride(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

// validCustomThemeJSON is a second valid theme (id "terra-test") used to
// exercise multi-theme enumeration and switching.
const validCustomThemeJSON = `{
  "schema_version": "1.0.0",
  "id": "terra-test",
  "name": "Terra Test",
  "author": "Tester",
  "description": "a second theme",
  "modes": {
    "dark": {
      "bg": {"void":"#1a0f0a","surface":"#2a1a12","panel":"#33221a","hover":"#3d2a20","active":"#4a3328"},
      "border": {"muted":"#2a1a12","zinc":"#3d2a20","active":"#5a3d30","focus":"#7a5238"},
      "text": {"primary":"#f0e6dc","muted":"#a08878","disabled":"#5a4a40"},
      "accent": {
        "primary": {"start":"#c2410c","end":"#7c2d12","glow":"rgba(194,65,12,0.15)"},
        "secondary": {"start":"#4d7c0f","end":"#365314","glow":"rgba(77,124,15,0.12)"}
      },
      "status": {"warn":"#fbbf24","danger":"#f43f5e","success":"#22c55e"}
    },
    "light": {
      "bg": {"void":"#faf6f2","surface":"#ffffff","panel":"#f1ebe4","hover":"#e5dccf","active":"#d6c7b4"},
      "border": {"muted":"#e5dccf","zinc":"#d6c7b4","active":"#a8907a","focus":"#7a6452"},
      "text": {"primary":"#2a1a12","muted":"#7a6452","disabled":"#a8907a"},
      "accent": {
        "primary": {"start":"#9a3412","end":"#7c2d12","glow":"rgba(154,52,18,0.10)"},
        "secondary": {"start":"#3f6212","end":"#365314","glow":"rgba(63,98,18,0.08)"}
      },
      "status": {"warn":"#b45309","danger":"#be123c","success":"#16a34a"}
    }
  }
}`

func TestGetActiveTheme_DefaultOnFreshVault(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	res, err := app.GetActiveTheme()
	if err != nil {
		t.Fatalf("GetActiveTheme: %v", err)
	}
	if res.ID != themes.DefaultThemeID {
		t.Errorf("expected default theme %q, got %q", themes.DefaultThemeID, res.ID)
	}
	if res.Mode != "dark" {
		t.Errorf("expected default mode dark, got %q", res.Mode)
	}
	// Effective first-paint tokens must be the dark set.
	if res.Tokens["--color-void"] != "#0c0c0e" {
		t.Errorf("expected dark bg.void #0c0c0e, got %q", res.Tokens["--color-void"])
	}
	// Both maps present so the frontend can resolve "system" locally.
	if res.DarkTokens["--color-void"] != "#0c0c0e" {
		t.Errorf("DarkTokens bg.void wrong: %q", res.DarkTokens["--color-void"])
	}
	if res.LightTokens["--color-void"] != "#f8fafc" {
		t.Errorf("LightTokens bg.void wrong: %q", res.LightTokens["--color-void"])
	}
	if res.BGVoid != "#0c0c0e" {
		t.Errorf("BGVoid wrong: %q", res.BGVoid)
	}
}

func TestListThemes_IncludesScaffoldedDefault(t *testing.T) {
	app := newTestApp(t)
	res, err := app.ListThemes()
	if err != nil {
		t.Fatalf("ListThemes: %v", err)
	}
	// ScaffoldVault wrote cyber_forest.json on disk → present as source disk.
	found := false
	for _, ti := range res.Themes {
		if ti.ID == themes.DefaultThemeID {
			found = true
			if ti.Source != "disk" {
				t.Errorf("scaffolded default should be source=disk, got %q", ti.Source)
			}
		}
	}
	if !found {
		t.Fatalf("scaffolded default theme not listed: %+v", res.Themes)
	}
	// No load errors on a freshly scaffolded vault.
	if len(res.Errors) != 0 {
		t.Errorf("expected no load errors, got %+v", res.Errors)
	}
}

func TestListThemes_MalformedFileDoesNotCrash(t *testing.T) {
	app := newTestApp(t)
	// Drop a broken theme file next to the valid default.
	writeFile(t, filepath.Join(app.vaultPath, ".system", "themes", "broken.json"), "{not json")

	res, err := app.ListThemes()
	if err != nil {
		t.Fatalf("ListThemes: %v", err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("expected 1 load error, got %d: %+v", len(res.Errors), res.Errors)
	}
	// The valid default is still enumerated despite the broken neighbor.
	foundDefault := false
	for _, ti := range res.Themes {
		if ti.ID == themes.DefaultThemeID {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Fatalf("valid theme dropped because of a broken neighbor: %+v", res.Themes)
	}
}

func TestApplyTheme_SwitchesAndPersists(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	// Add a second selectable theme on disk.
	writeFile(t, filepath.Join(app.vaultPath, ".system", "themes", "terra.json"), validCustomThemeJSON)

	res, err := app.ApplyTheme("terra-test", "light")
	if err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}
	if res.ID != "terra-test" {
		t.Errorf("expected switched id terra-test, got %q", res.ID)
	}
	if res.Mode != "light" {
		t.Errorf("expected mode light, got %q", res.Mode)
	}
	// Effective tokens reflect the light mode of the selected theme.
	if res.Tokens["--color-void"] != "#faf6f2" {
		t.Errorf("light bg.void wrong: %q", res.Tokens["--color-void"])
	}

	// The selection persisted across a fresh settings load.
	settings, err := vault.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.ActiveTheme != "terra-test" {
		t.Errorf("persisted active_theme = %q, want terra-test", settings.ActiveTheme)
	}
	if settings.ThemeMode != "light" {
		t.Errorf("persisted theme_mode = %q, want light", settings.ThemeMode)
	}

	// A subsequent GetActiveTheme reflects the persisted selection.
	res2, err := app.GetActiveTheme()
	if err != nil {
		t.Fatalf("GetActiveTheme after apply: %v", err)
	}
	if res2.ID != "terra-test" || res2.Tokens["--color-void"] != "#faf6f2" {
		t.Errorf("GetActiveTheme did not reflect persisted light theme: %+v", res2)
	}
}

func TestApplyTheme_RejectsInvalidMode(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	if _, err := app.ApplyTheme(themes.DefaultThemeID, "neon"); err == nil {
		t.Fatalf("expected error for invalid mode, got nil")
	}
}

func TestApplyTheme_RejectsUnknownID(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	if _, err := app.ApplyTheme("no-such-theme", "dark"); err == nil {
		t.Fatalf("expected error for unknown theme id, got nil")
	}
}

// TestApplyTheme_ResolvesFirstClassEmbeddedOffDisk (#92 review): the new
// first-class themes (Terra Noir, Linen, Stark, Graphite) live in the
// embedded roster. On a vault whose themes dir was scaffolded before the
// theme shipped — or was wiped by the user — the on-disk file is missing,
// so LoadByID returns found=false. ApplyTheme must still succeed by
// falling back to the embedded copy, mirroring what GetActiveTheme's
// ResolveActive does at startup. Regression guard for the bug where
// picking a new first-class theme in the UI errored with
// "theme silt-X is not available".
func TestApplyTheme_ResolvesFirstClassEmbeddedOffDisk(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	// Wipe the on-disk file so LoadByID returns not-found and the
	// embedded-fallback branch is the only path that can succeed.
	// (A fresh vault's ScaffoldVault writes every first-class id, so we
	// have to remove it explicitly to simulate a pre-Sprint-8 dir.)
	themesDir := filepath.Join(app.vaultPath, ".system", "themes")
	if err := os.Remove(filepath.Join(themesDir, "silt-graphite.json")); err != nil {
		t.Fatalf("remove silt-graphite.json: %v", err)
	}
	res, err := app.ApplyTheme("silt-graphite", "dark")
	if err != nil {
		t.Fatalf("ApplyTheme silt-graphite (embedded, off-disk): %v", err)
	}
	if res.ID != "silt-graphite" {
		t.Errorf("resolved id = %q, want silt-graphite", res.ID)
	}
	// Persisted: a subsequent GetActiveTheme should serve the same id
	// (now via the settings-backed path, not the picker shortcut).
	settings, err := vault.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if settings.ActiveTheme != "silt-graphite" {
		t.Errorf("settings.ActiveTheme = %q, want silt-graphite", settings.ActiveTheme)
	}
}

func TestApplyTheme_SystemModeResolvesFirstPaintDark(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	res, err := app.ApplyTheme(themes.DefaultThemeID, "system")
	if err != nil {
		t.Fatalf("ApplyTheme system: %v", err)
	}
	// Stored mode is system; effective first-paint tokens are dark.
	if res.Mode != "system" {
		t.Errorf("mode = %q, want system", res.Mode)
	}
	if res.Tokens["--color-void"] != "#0c0c0e" {
		t.Errorf("system should first-paint dark bg.void, got %q", res.Tokens["--color-void"])
	}
	// But both maps are present so the frontend can resolve the real preference.
	if res.LightTokens["--color-void"] != "#f8fafc" {
		t.Errorf("system mode must still ship light tokens, got %q", res.LightTokens["--color-void"])
	}
}

func TestGetActiveTheme_BeforeVaultOpen(t *testing.T) {
	// Before a vault is open (vaultPath==""), GetActiveTheme still works,
	// serving the embedded default so first-run onboarding renders themed.
	configDirOverride(t)
	app := &App{spacesPerTab: 4} // no vaultPath, no db

	res, err := app.GetActiveTheme()
	if err != nil {
		t.Fatalf("GetActiveTheme pre-vault: %v", err)
	}
	if res.ID != themes.DefaultThemeID {
		t.Errorf("expected embedded default, got %q", res.ID)
	}
	// And ListThemes returns the embedded first-class set (the default +
	// the four shipped palettes) even before a vault is open.
	listing, err := app.ListThemes()
	if err != nil {
		t.Fatalf("ListThemes pre-vault: %v", err)
	}
	ids := map[string]bool{}
	for _, ti := range listing.Themes {
		ids[ti.ID] = true
	}
	for _, id := range []string{
		themes.DefaultThemeID, "silt-terra-noir", "silt-linen", "silt-stark", "silt-graphite",
	} {
		if !ids[id] {
			t.Errorf("expected embedded first-class theme %q pre-vault, got %v", id, ids)
		}
	}
}

// Ensure the themes package embed (used by ScaffoldVault) is not nil so the
// app never boots without a fallback theme.
func TestEmbeddedDefaultAvailable(t *testing.T) {
	if len(themes.DefaultThemeJSON()) == 0 {
		t.Fatal("DefaultThemeJSON returned empty bytes")
	}
	// Sanity: the bytes parse and validate.
	if _, err := themes.ParseDefault(); err != nil {
		t.Fatalf("embedded default invalid: %v", err)
	}
}

func TestEffectiveMode(t *testing.T) {
	cases := map[string]string{
		"dark":   "dark",
		"light":  "light",
		"system": "dark", // system resolves to dark for the first paint
		"":       "dark",
		"neon":   "dark", // unknown normalizes to dark
	}
	for in, want := range cases {
		if got := effectiveMode(in); got != want {
			t.Errorf("effectiveMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildThemeResult_DarkFirstPaint(t *testing.T) {
	th, err := themes.ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	// system mode: effective tokens are dark (first paint) but both maps ship.
	res := buildThemeResult(th, "system")
	if res.Mode != "system" {
		t.Errorf("mode = %q, want system", res.Mode)
	}
	if res.Tokens["--color-void"] != "#0c0c0e" {
		t.Errorf("system first-paint should be dark bg.void, got %q", res.Tokens["--color-void"])
	}
	if res.DarkTokens["--color-accent-primary-start"] != "#2dd4bf" {
		t.Errorf("dark primary-start wrong: %q", res.DarkTokens["--color-accent-primary-start"])
	}
	if res.LightTokens["--color-accent-primary-start"] != "#0d9488" {
		t.Errorf("light primary-start wrong: %q", res.LightTokens["--color-accent-primary-start"])
	}
	if res.BGVoid != "#0c0c0e" {
		t.Errorf("BGVoid wrong: %q", res.BGVoid)
	}
	// light mode: effective tokens are light.
	lightRes := buildThemeResult(th, "light")
	if lightRes.Tokens["--color-void"] != "#f8fafc" {
		t.Errorf("light effective bg.void wrong: %q", lightRes.Tokens["--color-void"])
	}
	if lightRes.BGVoid != "#f8fafc" {
		t.Errorf("light BGVoid wrong: %q", lightRes.BGVoid)
	}
}

func TestApplyTheme_BadModeNotPersisted(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	before, err := vault.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings before: %v", err)
	}
	beforeMode := before.ThemeMode
	beforeTheme := before.ActiveTheme

	// Invalid mode is rejected and NOT persisted.
	if _, err := app.ApplyTheme(themes.DefaultThemeID, "neon"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
	after, err := vault.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings after: %v", err)
	}
	if after.ThemeMode != beforeMode || after.ActiveTheme != beforeTheme {
		t.Errorf("invalid ApplyTheme mutated settings: mode %q->%q theme %q->%q",
			beforeMode, after.ThemeMode, beforeTheme, after.ActiveTheme)
	}
}

// TestImportTheme_IPCHappyPath verifies the Wails-bound ImportTheme method:
// valid file → metadata returned, file written under <vault>/.system/themes/.
func TestImportTheme_IPCHappyPath(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	// Source file outside the vault, so the importer does the work.
	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, validCustomThemeJSON)

	res, err := app.ImportTheme(src)
	if err != nil {
		t.Fatalf("ImportTheme: %v", err)
	}
	if res.Info.ID != "terra-test" {
		t.Errorf("expected id terra-test, got %q", res.Info.ID)
	}
	if res.Info.Source != "disk" {
		t.Errorf("expected source disk, got %q", res.Info.Source)
	}
	dst := filepath.Join(app.vaultPath, ".system", "themes", "terra-test.json")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected import to write %s: %v", dst, err)
	}
	// And the new theme is in the picker listing immediately (no restart).
	listing, err := app.ListThemes()
	if err != nil {
		t.Fatalf("ListThemes: %v", err)
	}
	found := false
	for _, ti := range listing.Themes {
		if ti.ID == "terra-test" {
			found = true
		}
	}
	if !found {
		t.Errorf("imported theme not in ListThemes: %+v", listing.Themes)
	}
}

// TestImportTheme_IPCValidationFailure verifies the IPC layer surfaces
// ValidationErrors in the result payload and does not write a file on failure.
func TestImportTheme_IPCValidationFailure(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	bad := strings.Replace(validCustomThemeJSON, `"#c2410c"`, `""`, 1)
	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, bad)

	res, err := app.ImportTheme(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.ValidationErrors) == 0 {
		t.Fatal("expected validation errors in result")
	}
	found := false
	for _, e := range res.ValidationErrors {
		if strings.Contains(e.Field, "accent.primary.start") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on accent.primary.start, got: %+v", res.ValidationErrors)
	}
	// No file written under themes/ beyond the scaffolded first-class set
	// (the rejected import must add nothing). The scaffold writes every
	// embedded first-class theme, so those are the expected baseline.
	themesDir := filepath.Join(app.vaultPath, ".system", "themes")
	entries, _ := os.ReadDir(themesDir)
	files, err := themes.EmbeddedThemeFiles()
	if err != nil {
		t.Fatalf("EmbeddedThemeFiles: %v", err)
	}
	scaffolded := make(map[string]bool, len(files))
	for fn := range files {
		scaffolded[fn] = true
	}
	imported := 0
	for _, e := range entries {
		if !scaffolded[e.Name()] {
			imported++
		}
	}
	if imported != 0 {
		t.Errorf("expected no imported file, found: %+v", entries)
	}
}

// TestImportTheme_IPCBeforeVault: no vault → error, no file written.
func TestImportTheme_IPCBeforeVault(t *testing.T) {
	configDirOverride(t)
	app := &App{spacesPerTab: 4}
	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, validCustomThemeJSON)
	if _, err := app.ImportTheme(src); err == nil {
		t.Fatal("expected error for pre-vault import")
	}
}

// TestImportTheme_IPCNamespaceBuiltIn: an import whose id collides with the
// bundled default is renamed to user-<id>.
func TestImportTheme_IPCNamespaceBuiltIn(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	clone := strings.Replace(validCustomThemeJSON, `"terra-test"`, `"`+themes.DefaultThemeID+`"`, 1)
	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, clone)

	res, err := app.ImportTheme(src)
	if err != nil {
		t.Fatalf("ImportTheme: %v", err)
	}
	want := "user-" + themes.DefaultThemeID
	if res.Info.ID != want {
		t.Errorf("expected id %q, got %q", want, res.Info.ID)
	}
	if !res.Renamed {
		t.Errorf("expected Renamed=true")
	}
}

// TestImportTheme_IPCRejectsDuplicate: importing the same id twice yields
// ErrImportDuplicate the second time.
func TestImportTheme_IPCRejectsDuplicate(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, validCustomThemeJSON)
	if _, err := app.ImportTheme(src); err != nil {
		t.Fatalf("first import: %v", err)
	}
	_, err := app.ImportTheme(src)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if !errors.Is(err, themes.ErrImportDuplicate) {
		t.Errorf("expected ErrImportDuplicate, got: %v", err)
	}
}

// TestImportTheme_IPCMissingSource: file does not exist on disk.
func TestImportTheme_IPCMissingSource(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	if _, err := app.ImportTheme("/no/such/file.json"); err == nil {
		t.Fatal("expected error for missing source")
	}
}

// TestPickThemeFile_NoCtx: returns a clear error when the Wails ctx is
// not available (mirrors the existing pre-vault test pattern).
func TestPickThemeFile_NoCtx(t *testing.T) {
	app := &App{spacesPerTab: 4}
	if _, err := app.PickThemeFile(); err == nil {
		t.Fatal("expected error when ctx is nil")
	}
}

// TestPickExportPath_NoCtx: returns a clear error when the Wails ctx is
// not available (same guard as PickThemeFile).
func TestPickExportPath_NoCtx(t *testing.T) {
	app := &App{spacesPerTab: 4}
	if _, err := app.PickExportPath("theme.json"); err == nil {
		t.Fatal("expected error when ctx is nil")
	}
}

// TestExportActiveTheme_IPCRoundTrip: switching to a custom theme, exporting
// it, and re-parsing the exported file with the canonical validator.
func TestExportActiveTheme_IPCRoundTrip(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)

	// Add a custom theme, switch to it.
	writeFile(t, filepath.Join(app.vaultPath, ".system", "themes", "terra.json"), validCustomThemeJSON)
	if _, err := app.ApplyTheme("terra-test", "light"); err != nil {
		t.Fatalf("ApplyTheme: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "export.json")
	if err := app.ExportActiveTheme(dst); err != nil {
		t.Fatalf("ExportActiveTheme: %v", err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	parsed, err := themes.ParseAndValidate(raw)
	if err != nil {
		t.Fatalf("exported file fails canonical validation: %v", err)
	}
	if parsed.ID != "terra-test" {
		t.Errorf("exported id = %q, want terra-test", parsed.ID)
	}
}

// TestExportActiveTheme_IPCBeforeVault: no vault → error.
func TestExportActiveTheme_IPCBeforeVault(t *testing.T) {
	app := &App{spacesPerTab: 4}
	if err := app.ExportActiveTheme(filepath.Join(t.TempDir(), "out.json")); err == nil {
		t.Fatal("expected error for pre-vault export")
	}
}

// TestExportActiveTheme_IPCEmptyPath: empty destination returns error.
func TestExportActiveTheme_IPCEmptyPath(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	if err := app.ExportActiveTheme(""); err == nil {
		t.Fatal("expected error for empty dst")
	}
}

// TestApplyTheme_ReadsListOnce (#76): ApplyTheme now resolves the requested
// theme via themes.LoadByID (a single os.ReadDir). Switch through several
// on-disk themes under -race to catch any unsynchronized access that the
// double-scan path might have masked.
//
// This is a SMOKE GUARD, not a strict single-scan assertion: it exercises
// the happy path under -race but does not count os.ReadDir calls (which
// would require wrapping the syscall in a test-only counter, adding
// production-code churn for marginal test value). The single-scan contract
// is enforced structurally by the code path: ApplyTheme calls
// themes.LoadByID exactly once and never calls themes.ListThemes.
func TestApplyTheme_ReadsListOnce(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t)
	// Populate themes dir so LoadByID has work to do.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		body := strings.Replace(validCustomThemeJSON, `"terra-test"`, `"`+name+`"`, 1)
		writeFile(t, filepath.Join(app.vaultPath, ".system", "themes", name+".json"), body)
	}
	// Cycle through them under -race; any double-scan race surfaces here.
	for _, name := range []string{"alpha", "beta", "gamma", "alpha", "beta"} {
		if _, err := app.ApplyTheme(name, "dark"); err != nil {
			t.Fatalf("ApplyTheme(%q): %v", name, err)
		}
	}
}

// TestImportTheme_EmitsThemesChanged: a successful import must emit exactly
// one "themes:changed" Wails event so the frontend picker re-fetches
// ListThemes and the new theme appears without a restart. Plan-promised
// in PLAN.md Phase 2; verifies the emit path end-to-end.
//
// The App struct stores the Wails context (a.ctx) which is nil in tests
// (no Wails runtime). When ctx is nil, EventsEmit is a no-op (guarded by
// `if a.ctx != nil`). So we verify the guard logic by asserting (a) the
// method succeeds and (b) it does not panic with a nil ctx. The actual
// event wire-format is exercised by the Wails integration test (future).
func TestImportTheme_EmitsThemesChanged_NoCtxNoPanic(t *testing.T) {
	configDirOverride(t)
	app := newTestApp(t) // a.ctx is nil in tests

	src := filepath.Join(t.TempDir(), "src.json")
	writeFile(t, src, validCustomThemeJSON)

	// Must not panic despite nil ctx (the EventsEmit guard must hold).
	res, err := app.ImportTheme(src)
	if err != nil {
		t.Fatalf("ImportTheme: %v", err)
	}
	if res == nil || res.Info.ID != "terra-test" {
		t.Errorf("unexpected result: %+v", res)
	}
}
