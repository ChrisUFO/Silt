package main

import (
	"os"
	"path/filepath"
	"testing"

	"silt/backend/themes"
	"silt/backend/vault"
)

// TestLaunchBackgroundColour_TracksActiveCustom (#73): the pre-CSS flash
// color must track the active theme. A custom theme with a non-default
// bg.void used to flash the embedded default's color until the runtime
// injector caught up; the in-process cache short-circuits that gap.
func TestLaunchBackgroundColour_TracksActiveCustom(t *testing.T) {
	configDirOverride(t)

	// Stage a vault + a custom theme whose dark.bg.void is a distinctive
	// color we can match against.
	vaultPath := t.TempDir()
	if err := vault.ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	custom := `{
  "schema_version": "1.0.0",
  "id": "warm-test",
  "name": "Warm Test",
  "author": "Tester",
  "description": "warm custom theme for #73",
  "modes": {
    "dark": {
      "bg": {"void":"#102030","surface":"#162840","panel":"#1c3050","hover":"#223860","active":"#284070"},
      "border": {"muted":"#223860","zinc":"#2c4470","active":"#3a5580","focus":"#4870a0"},
      "text": {"primary":"#eef0f4","muted":"#88909c","disabled":"#565c66"},
      "accent": {
        "primary": {"start":"#f59e0b","end":"#b45309","glow":"rgba(245,158,11,0.15)"},
        "secondary": {"start":"#a855f7","end":"#7e22ce","glow":"rgba(168,85,247,0.12)"}
      },
       "status": {"warn":"#fbbf24","danger":"#f43f5e","success":"#22c55e"}
     },
     "light": {
       "bg": {"void":"#fff7e6","surface":"#ffffff","panel":"#fff0d2","hover":"#ffe4b0","active":"#ffd58a"},
       "border": {"muted":"#ffe4b0","zinc":"#ffd58a","active":"#b88a3a","focus":"#8a6420"},
       "text": {"primary":"#2a1f10","muted":"#6e5a36","disabled":"#a08a64"},
       "accent": {
         "primary": {"start":"#b45309","end":"#92400e","glow":"rgba(180,83,9,0.10)"},
         "secondary": {"start":"#7e22ce","end":"#6b21a8","glow":"rgba(126,34,206,0.08)"}
       },
       "status": {"warn":"#d97706","danger":"#be123c","success":"#16a34a"}
    }
  }
}`
	if err := os.WriteFile(filepath.Join(vaultPath, ".system", "themes", "warm-test.json"), []byte(custom), 0o644); err != nil {
		t.Fatalf("write custom: %v", err)
	}
	settings := vault.AppSettings{
		VaultPath:   vaultPath,
		ActiveTheme: "warm-test",
		ThemeMode:   "dark",
	}
	if err := vault.SaveSettings(&settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	themes.InvalidateThemeCache()

	c := launchBackgroundColour()
	if c == nil {
		t.Fatal("expected non-nil RGBA")
	}
	// warm-test's dark.bg.void is #102030 → R=16, G=32, B=48
	if c.R != 16 || c.G != 32 || c.B != 48 {
		t.Errorf("expected #102030 (16,32,48), got (%d,%d,%d)", c.R, c.G, c.B)
	}
}

// TestLaunchBackgroundColour_DefaultWhenNoSettings: pre-vault / fresh
// install → embedded default's bg.void (the shipped first-paint color).
func TestLaunchBackgroundColour_DefaultWhenNoSettings(t *testing.T) {
	configDirOverride(t)
	themes.InvalidateThemeCache()

	c := launchBackgroundColour()
	if c == nil {
		t.Fatal("expected non-nil RGBA")
	}
	dt, err := themes.ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	r, g, b, ok := themes.HexToRGB(dt.BGVoid("dark"))
	if !ok {
		t.Fatal("embedded default dark bg.void unparseable")
	}
	if c.R != r || c.G != g || c.B != b {
		t.Errorf("expected default bg.void (%d,%d,%d), got (%d,%d,%d)", r, g, b, c.R, c.G, c.B)
	}
}

// TestLaunchBackgroundColour_InvalidActiveIDFallsBack: a stale id (file
// gone) returns the embedded default rather than failing the launch.
func TestLaunchBackgroundColour_InvalidActiveIDFallsBack(t *testing.T) {
	configDirOverride(t)
	vaultPath := t.TempDir()
	if err := vault.ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	settings := vault.AppSettings{
		VaultPath:   vaultPath,
		ActiveTheme: "ghost-theme-no-such-file",
		ThemeMode:   "dark",
	}
	if err := vault.SaveSettings(&settings); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	themes.InvalidateThemeCache()

	c := launchBackgroundColour()
	if c == nil {
		t.Fatal("expected non-nil RGBA")
	}
	dt, err := themes.ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	r, g, b, ok := themes.HexToRGB(dt.BGVoid("dark"))
	if !ok {
		t.Fatal("embedded default dark bg.void unparseable")
	}
	if c.R != r || c.G != g || c.B != b {
		t.Errorf("expected default fallback (%d,%d,%d), got (%d,%d,%d)", r, g, b, c.R, c.G, c.B)
	}
}
