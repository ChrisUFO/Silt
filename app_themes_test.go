package main

import (
	"os"
	"path/filepath"
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
      "status": {"warn":"#fbbf24","danger":"#f43f5e"}
    },
    "light": {
      "bg": {"void":"#faf6f2","surface":"#ffffff","panel":"#f1ebe4","hover":"#e5dccf","active":"#d6c7b4"},
      "border": {"muted":"#e5dccf","zinc":"#d6c7b4","active":"#a8907a","focus":"#7a6452"},
      "text": {"primary":"#2a1a12","muted":"#7a6452","disabled":"#a8907a"},
      "accent": {
        "primary": {"start":"#9a3412","end":"#7c2d12","glow":"rgba(154,52,18,0.10)"},
        "secondary": {"start":"#3f6212","end":"#365314","glow":"rgba(63,98,18,0.08)"}
      },
      "status": {"warn":"#b45309","danger":"#be123c"}
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
	if res.Tokens["--bg-void"] != "#0c0c0e" {
		t.Errorf("expected dark bg.void #0c0c0e, got %q", res.Tokens["--bg-void"])
	}
	// Both maps present so the frontend can resolve "system" locally.
	if res.DarkTokens["--bg-void"] != "#0c0c0e" {
		t.Errorf("DarkTokens bg.void wrong: %q", res.DarkTokens["--bg-void"])
	}
	if res.LightTokens["--bg-void"] != "#f8fafc" {
		t.Errorf("LightTokens bg.void wrong: %q", res.LightTokens["--bg-void"])
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
	if res.Tokens["--bg-void"] != "#faf6f2" {
		t.Errorf("light bg.void wrong: %q", res.Tokens["--bg-void"])
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
	if res2.ID != "terra-test" || res2.Tokens["--bg-void"] != "#faf6f2" {
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
	if res.Tokens["--bg-void"] != "#0c0c0e" {
		t.Errorf("system should first-paint dark bg.void, got %q", res.Tokens["--bg-void"])
	}
	// But both maps are present so the frontend can resolve the real preference.
	if res.LightTokens["--bg-void"] != "#f8fafc" {
		t.Errorf("system mode must still ship light tokens, got %q", res.LightTokens["--bg-void"])
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
	// And ListThemes returns just the embedded default.
	listing, err := app.ListThemes()
	if err != nil {
		t.Fatalf("ListThemes pre-vault: %v", err)
	}
	if len(listing.Themes) != 1 || listing.Themes[0].Source != "default" {
		t.Errorf("expected embedded default only pre-vault, got %+v", listing.Themes)
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
	// Avoid unused-import warnings if the file evolves.
	_ = os.Stat
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
	if res.Tokens["--bg-void"] != "#0c0c0e" {
		t.Errorf("system first-paint should be dark bg.void, got %q", res.Tokens["--bg-void"])
	}
	if res.DarkTokens["--accent-primary-start"] != "#2dd4bf" {
		t.Errorf("dark primary-start wrong: %q", res.DarkTokens["--accent-primary-start"])
	}
	if res.LightTokens["--accent-primary-start"] != "#0d9488" {
		t.Errorf("light primary-start wrong: %q", res.LightTokens["--accent-primary-start"])
	}
	if res.BGVoid != "#0c0c0e" {
		t.Errorf("BGVoid wrong: %q", res.BGVoid)
	}
	// light mode: effective tokens are light.
	lightRes := buildThemeResult(th, "light")
	if lightRes.Tokens["--bg-void"] != "#f8fafc" {
		t.Errorf("light effective bg.void wrong: %q", lightRes.Tokens["--bg-void"])
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
