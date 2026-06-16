package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silt/backend/themes"
)

func TestLoadSettings_CorruptJSON(t *testing.T) {
	// settings.json exists but contains unparseable content. LoadSettings
	// must return an error, not silently return AppSettings{}.

	// Override UserConfigDir by setting APPDATA (Windows) or
	// XDG_CONFIG_HOME (Linux/macOS).
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := GetSettingsPath()
	if err != nil {
		t.Skipf("cannot determine config path on this platform: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{not valid json!!!!}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err = LoadSettings()
	if err == nil {
		t.Fatalf("expected error for corrupt settings.json, got nil")
	}
}

func TestSaveSettings_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	original := &AppSettings{
		VaultPath:   filepath.Join(dir, "my-vault"),
		ActiveTheme: "terra_noir",
		ThemeMode:   "light",
	}
	if err := SaveSettings(original); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if loaded.VaultPath != original.VaultPath {
		t.Errorf("round-trip mismatch vault_path: got %q, want %q", loaded.VaultPath, original.VaultPath)
	}
	if loaded.ActiveTheme != "terra_noir" {
		t.Errorf("round-trip mismatch active_theme: got %q, want %q", loaded.ActiveTheme, "terra_noir")
	}
	if loaded.ThemeMode != "light" {
		t.Errorf("round-trip mismatch theme_mode: got %q, want %q", loaded.ThemeMode, "light")
	}
}

// TestLoadSettings_BackwardCompat covers an older settings.json written
// before the theme fields existed. It must load with the default theme/mode
// rather than crashing or returning empty values.
func TestLoadSettings_BackwardCompat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := GetSettingsPath()
	if err != nil {
		t.Skipf("cannot determine config path on this platform: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-theme-era settings.json: only vault_path.
	legacy := []byte(`{"vault_path":"/old/vault"}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings legacy: %v", err)
	}
	if loaded.VaultPath != "/old/vault" {
		t.Errorf("legacy vault_path lost: got %q", loaded.VaultPath)
	}
	if loaded.ActiveTheme != themes.DefaultThemeID {
		t.Errorf("legacy active_theme should default to %q, got %q", themes.DefaultThemeID, loaded.ActiveTheme)
	}
	if loaded.ThemeMode != "dark" {
		t.Errorf("legacy theme_mode should default to dark, got %q", loaded.ThemeMode)
	}
}

// TestLoadSettings_FirstRunDefaults covers the no-settings-file case (fresh
// install): LoadSettings returns valid defaults instead of a zero struct.
func TestLoadSettings_FirstRunDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings first run: %v", err)
	}
	if loaded.ActiveTheme != themes.DefaultThemeID {
		t.Errorf("first-run active_theme should be %q, got %q", themes.DefaultThemeID, loaded.ActiveTheme)
	}
	if loaded.ThemeMode != "dark" {
		t.Errorf("first-run theme_mode should be dark, got %q", loaded.ThemeMode)
	}
}

// TestThemeModeNormalization ensures an unrecognized ThemeMode persisted to
// disk (or passed by a caller) normalizes to "dark" on load and save.
func TestThemeModeNormalization(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := GetSettingsPath()
	if err != nil {
		t.Skipf("cannot determine config path on this platform: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := []byte(`{"vault_path":"/v","active_theme":"terra_noir","theme_mode":"neon"}`)
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if loaded.ThemeMode != "dark" {
		t.Errorf("invalid theme_mode should normalize to dark, got %q", loaded.ThemeMode)
	}

	// Saving a struct with an invalid mode persists the normalized value.
	if err := SaveSettings(&AppSettings{VaultPath: "/v", ActiveTheme: "x", ThemeMode: "neon"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	raw, _ := os.ReadFile(path)
	var onDisk struct {
		ThemeMode string `json:"theme_mode"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if onDisk.ThemeMode != "dark" {
		t.Errorf("saved theme_mode should be normalized to dark, got %q", onDisk.ThemeMode)
	}
}

func TestValidThemeMode(t *testing.T) {
	for _, m := range []string{"dark", "light", "system"} {
		if !ValidThemeMode(m) {
			t.Errorf("ValidThemeMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "neon", "DARK", "dark "} {
		if ValidThemeMode(m) {
			t.Errorf("ValidThemeMode(%q) = true, want false", m)
		}
	}
}

// TestScaffoldVault_WritesAllFirstClassThemes: a fresh scaffold writes every
// embedded first-class theme file into <vault>/.system/themes/ (the default +
// the four Sprint 8 palettes), so the full first-party set is editable on disk.
func TestScaffoldVault_WritesAllFirstClassThemes(t *testing.T) {
	vaultPath := t.TempDir()
	if err := ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	files, err := themes.EmbeddedThemeFiles()
	if err != nil {
		t.Fatalf("EmbeddedThemeFiles: %v", err)
	}
	for fn := range files {
		p := filepath.Join(vaultPath, ".system", "themes", fn)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected scaffolded theme %s: %v", fn, err)
		}
	}
}

// TestScaffoldVault_ThemesIdempotent: re-running ScaffoldVault never
// overwrites a user's existing theme file (the existence guard). A hand-edited
// sentinel on disk must survive a second scaffold.
func TestScaffoldVault_ThemesIdempotent(t *testing.T) {
	vaultPath := t.TempDir()
	if err := ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault first run: %v", err)
	}
	// Mutate the on-disk default with a sentinel and re-scaffold.
	defaultPath := filepath.Join(vaultPath, ".system", "themes", "cyber_forest.json")
	const sentinel = "// user-edit sentinel"
	if err := os.WriteFile(defaultPath, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault second run: %v", err)
	}
	got, err := os.ReadFile(defaultPath)
	if err != nil {
		t.Fatalf("read after re-scaffold: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("ScaffoldVault overwrote an existing user theme; expected sentinel to survive")
	}
}

// TestScaffoldVault_ThemeStatErrorPropagates: a stat failure on a
// scaffolded theme that is not "not exist" (e.g. permission denied on
// the themes directory) must surface to the caller rather than being
// silently swallowed. The user has no other way to know the themes
// dir is in a broken state otherwise.
func TestScaffoldVault_ThemeStatErrorPropagates(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits are bypassed for root; stat always succeeds")
	}
	// os.Chmod on Windows only flips the read-only bit; the POSIX mode bits
	// the test relies on (0o000 = no perms) are not honoured, so the stat
	// never fails and the regression we're guarding cannot be exercised.
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows; chmod 0o000 does not revoke stat access")
	}
	vaultPath := t.TempDir()
	// Pre-scaffold so .system/themes exists with real files, then
	// revoke all perms on the themes dir so the loop's stat fails
	// with EACCES (a non-IsNotExist error).
	if err := ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault setup: %v", err)
	}
	themesDir := filepath.Join(vaultPath, ".system", "themes")
	if err := os.Chmod(themesDir, 0o000); err != nil {
		t.Fatalf("chmod themes dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(themesDir, 0o755) })

	err := ScaffoldVault(vaultPath)
	if err == nil {
		t.Fatal("ScaffoldVault: expected error for unreadable themes dir, got nil")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("ScaffoldVault: not-exist should be ignored, got %v", err)
	}
	if !strings.Contains(err.Error(), "failed to stat theme") {
		t.Errorf("ScaffoldVault: error %q should wrap the stat failure", err)
	}
}

