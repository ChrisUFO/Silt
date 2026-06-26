package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"silt/backend/themes"
)

// jsonQuote wraps a string as a JSON string literal (with escaping) so a test
// can embed a platform-specific filesystem path into a settings.json template
// without string-concatenation quoting bugs.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

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
	// Use a platform-absolute path so the F20 absolute-path validation passes.
	legacyPath := filepath.Join(dir, "old-vault")
	legacy := []byte(`{"vault_path":` + jsonQuote(legacyPath) + `}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings legacy: %v", err)
	}
	if loaded.VaultPath != legacyPath {
		t.Errorf("legacy vault_path lost: got %q, want %q", loaded.VaultPath, legacyPath)
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
	absVault := filepath.Join(dir, "v")
	bad := []byte(`{"vault_path":` + jsonQuote(absVault) + `,"active_theme":"terra_noir","theme_mode":"neon"}`)
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
	if err := SaveSettings(&AppSettings{VaultPath: absVault, ActiveTheme: "x", ThemeMode: "neon"}); err != nil {
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

// =========================================================================
// F20: settings.json integrity (#251)
// =========================================================================

// writeSettingsRaw writes a raw settings.json + optional fingerprint to the
// overridden config dir, so each F20 test can stage an exact on-disk state.
func writeSettingsRaw(t *testing.T, settingsJSON string, fingerprint string, writeFP bool) {
	t.Helper()
	path, err := GetSettingsPath()
	if err != nil {
		t.Skipf("cannot determine config path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(settingsJSON), 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	if writeFP {
		fpPath := path + ".fingerprint"
		if err := os.WriteFile(fpPath, []byte(fingerprint), 0o644); err != nil {
			t.Fatalf("write fingerprint: %v", err)
		}
	}
}

func TestLoadSettings_RejectsUnknownTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	writeSettingsRaw(t, `{"vault_path":`+jsonQuote(filepath.Join(dir, "v"))+`,"evil":"x"}`, "", false)

	_, err := LoadSettings()
	if err == nil {
		t.Fatal("LoadSettings must reject an unknown top-level key")
	}
	if !strings.Contains(err.Error(), "evil") {
		t.Errorf("error %q should name the offending key", err)
	}
}

func TestLoadSettings_RejectsRelativeVaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	writeSettingsRaw(t, `{"vault_path":"relative/path"}`, "", false)

	_, err := LoadSettings()
	if err == nil {
		t.Fatal("LoadSettings must reject a relative vault_path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error %q should explain the path must be absolute", err)
	}
}

func TestLoadSettings_FingerprintMismatchTriggersError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Write settings with a STALE fingerprint — simulates a co-tenant
	// editing settings.json without updating the fingerprint.
	writeSettingsRaw(t,
		`{"vault_path":`+jsonQuote(filepath.Join(dir, "vault"))+`}`,
		"stale-fingerprint-that-does-not-match",
		true,
	)

	settings, err := LoadSettings()
	if !errors.Is(err, ErrSettingsFingerprintMismatch) {
		t.Fatalf("expected ErrSettingsFingerprintMismatch, got %v", err)
	}
	// Settings are still returned (valid JSON + valid schema) so the app
	// remains usable while the user decides.
	if settings == nil {
		t.Fatal("settings must be returned alongside the sentinel so the app stays usable")
	}
	if settings.VaultPath != filepath.Join(dir, "vault") {
		t.Errorf("vault_path not loaded: got %q", settings.VaultPath)
	}
	// The fingerprint must NOT have been updated by LoadSettings — only
	// SaveSettings or ConfirmSettingsChange updates it.
	fpPath, _ := settingsFingerprintPath()
	fpAfter, _ := os.ReadFile(fpPath)
	if string(fpAfter) != "stale-fingerprint-that-does-not-match" {
		t.Errorf("LoadSettings must not update the fingerprint on mismatch; got %q", string(fpAfter))
	}
}

func TestLoadSettings_FirstLaunchWritesFingerprint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	// settings.json exists but NO fingerprint file — first launch after upgrade.
	writeSettingsRaw(t, `{"vault_path":`+jsonQuote(filepath.Join(dir, "vault"))+`}`, "", false)

	settings, err := LoadSettings()
	if err != nil {
		t.Fatalf("first-launch LoadSettings: %v", err)
	}
	// A fingerprint should now exist matching the loaded values.
	fpPath, _ := settingsFingerprintPath()
	fpData, err := os.ReadFile(fpPath)
	if err != nil {
		t.Fatalf("fingerprint should have been written on first launch: %v", err)
	}
	expected := computeSettingsFingerprint(settings)
	if string(fpData) != expected {
		t.Errorf("fingerprint mismatch: got %q, want %q", string(fpData), expected)
	}
	// Second call should succeed without error (fingerprint now matches).
	if _, err := LoadSettings(); err != nil {
		t.Errorf("second LoadSettings after first-launch seeding: %v", err)
	}
}

func TestSaveSettings_UpdatesFingerprint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	// First save establishes the baseline (writes settings + fingerprint).
	vault1 := filepath.Join(dir, "vault1")
	if err := SaveSettings(&AppSettings{VaultPath: vault1, ActiveTheme: "x", ThemeMode: "dark"}); err != nil {
		t.Fatalf("first SaveSettings: %v", err)
	}

	// A legitimate vault switch (Silt's own trusted write).
	vault2 := filepath.Join(dir, "vault2")
	if err := SaveSettings(&AppSettings{VaultPath: vault2, ActiveTheme: "x", ThemeMode: "dark"}); err != nil {
		t.Fatalf("second SaveSettings: %v", err)
	}

	// The next LoadSettings must NOT prompt — Silt's own SaveSettings updated
	// the fingerprint alongside the new vault_path.
	_, err := LoadSettings()
	if err != nil {
		t.Errorf("LoadSettings after SaveSettings should not error (fingerprint updated): %v", err)
	}
}

func TestSaveSettings_Fingerprint0600Perms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := SaveSettings(&AppSettings{VaultPath: filepath.Join(dir, "v"), ActiveTheme: "x", ThemeMode: "dark"}); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	fpPath, _ := settingsFingerprintPath()
	info, err := os.Stat(fpPath)
	if err != nil {
		t.Fatalf("stat fingerprint: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("fingerprint perm = %o, want 0o600", info.Mode().Perm())
	}
	// The settings file itself should also be 0o600 (WriteFileAtomic default).
	settingsPath, _ := GetSettingsPath()
	sInfo, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if sInfo.Mode().Perm() != 0o600 {
		t.Errorf("settings perm = %o, want 0o600", sInfo.Mode().Perm())
	}
}

// TestScaffoldVault_RestrictiveFilePermissions pins the F7 hardening: the
// scaffolded config.yaml / theme files / plugins README are 0o600, and the
// .system/ + themes/ + plugins/ folders are 0o700 (co-tenant cannot read).
func TestScaffoldVault_RestrictiveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	vault := t.TempDir()
	if err := ScaffoldVault(vault); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	for _, c := range []struct{ name string; want os.FileMode }{
		{".system", 0o700},
		{".system/themes", 0o700},
		{".system/plugins", 0o700},
		{".system/config.yaml", 0o600},
		{".system/plugins/README.md", 0o600},
	} {
		info, err := os.Stat(filepath.Join(vault, c.name))
		if err != nil {
			t.Fatalf("stat %s: %v", c.name, err)
		}
		if got := info.Mode().Perm(); got != c.want {
			t.Errorf("%s perm = %o, want %o", c.name, got, c.want)
		}
	}
	entries, err := os.ReadDir(filepath.Join(vault, ".system", "themes"))
	if err != nil {
		t.Fatalf("read themes dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected scaffolded theme files")
	}
	tInfo, err := os.Stat(filepath.Join(vault, ".system", "themes", entries[0].Name()))
	if err != nil {
		t.Fatalf("stat theme file: %v", err)
	}
	if got := tInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("theme file %s perm = %o, want 0o600", entries[0].Name(), got)
	}
}

// TestExportVaultTree_ArchiveFile0600Perms pins F7: the exported .silt-vault
// archive is written 0o600 (manifest + digests travel with it).
func TestExportVaultTree_ArchiveFile0600Perms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	dest := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, dest, "MyVault", "0.1.25-test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("archive perm = %o, want 0o600", got)
	}
}

// TestImportVaultTree_RestrictiveFilePermissions pins F7: extracted files are
// 0o600 and the extraction-created directories 0o700.
func TestImportVaultTree_RestrictiveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "exp.silt-vault")
	if _, err := ExportVaultTree(root, archive, "MyVault", "0.1.25-test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	dest := t.TempDir()
	if _, err := ImportVaultTree(archive, dest, nil); err != nil {
		t.Fatalf("ImportVaultTree: %v", err)
	}
	pageInfo, err := os.Stat(filepath.Join(dest, "Work", "Inbox.md"))
	if err != nil {
		t.Fatalf("stat extracted page: %v", err)
	}
	if got := pageInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("extracted file perm = %o, want 0o600", got)
	}
	dirInfo, err := os.Stat(filepath.Join(dest, "Work"))
	if err != nil {
		t.Fatalf("stat extracted dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("extracted dir perm = %o, want 0o700", got)
	}
}

// TestLoadSettings_RejectsOversize pins F12: an oversized settings.json is
// rejected at read time without unbounded allocation ahead of the strict decode.
func TestLoadSettings_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := GetSettingsPath()
	if err != nil {
		t.Skipf("cannot determine config path on this platform: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, maxSettingsJSONBytes+1), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = LoadSettings()
	if err == nil {
		t.Fatal("expected oversize settings.json to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds the") {
		t.Errorf("error %q must mention the byte cap", err.Error())
	}
}

// TestUpdateSettings_ConcurrentWritersNoLostUpdate is the regression test for
// the settings.json read-modify-write race. Two goroutines each append a
// distinct marker to TrustedPublishers many times via UpdateSettings. Without
// the settingsWriteMu serialization, the racy Load→Modify→Save would let the
// later writer clobber the earlier (lost appends); with it, every append lands.
// Run under -race to also catch any lock misuse.
func TestUpdateSettings_ConcurrentWritersNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	const perWriter = 40
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			marker := string(rune('A' + w)) // "A" or "B"
			for i := 0; i < perWriter; i++ {
				label := marker + "-" + strconv.Itoa(i)
				if _, err := UpdateSettings(func(s *AppSettings) {
					s.TrustedPublishers = append(s.TrustedPublishers, label)
				}); err != nil {
					t.Errorf("UpdateSettings: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	want := 2 * perWriter
	if got := len(loaded.TrustedPublishers); got != want {
		t.Errorf("TrustedPublishers count = %d, want %d (a write was lost — the RMW race is back)", got, want)
	}
	// Both writers must be represented.
	haveA, haveB := false, false
	for _, p := range loaded.TrustedPublishers {
		if strings.HasPrefix(p, "A-") {
			haveA = true
		}
		if strings.HasPrefix(p, "B-") {
			haveB = true
		}
	}
	if !haveA || !haveB {
		t.Errorf("expected both writers represented; haveA=%v haveB=%v", haveA, haveB)
	}
}

// TestUpdateSettings_PreservesOtherFields confirms a targeted UpdateSettings
// modify does not clobber fields another writer changed (the core invariant).
func TestUpdateSettings_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Seed with a theme + a trusted publisher.
	off := false
	if _, err := UpdateSettings(func(s *AppSettings) {
		s.ActiveTheme = "terra_noir"
		s.ThemeMode = "light"
		s.TrustedPublishers = []string{"founder"}
		s.AutoCheckUpdates = &off
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A second, independent modify (toggle auto-check on) must NOT wipe the
	// theme or the trusted publisher.
	on := true
	if _, err := UpdateSettings(func(s *AppSettings) {
		s.AutoCheckUpdates = &on
	}); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if loaded.ActiveTheme != "terra_noir" || loaded.ThemeMode != "light" {
		t.Errorf("theme clobbered by toggle: %+v", loaded)
	}
	if len(loaded.TrustedPublishers) != 1 || loaded.TrustedPublishers[0] != "founder" {
		t.Errorf("trusted publishers clobbered by toggle: %v", loaded.TrustedPublishers)
	}
	if loaded.AutoCheckUpdates == nil || *loaded.AutoCheckUpdates != true {
		t.Errorf("toggle did not land: %v", loaded.AutoCheckUpdates)
	}
}

