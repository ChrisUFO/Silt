package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"silt/backend/parser"
	"silt/backend/safeio"
	"silt/backend/themes"
)

// settingsWriteMu serializes every settings.json write. LoadSettings/SaveSettings
// are a read-modify-write: without serialization, two concurrent writers (e.g.
// a theme switch racing an update-check toggle, or a vault move racing a
// trusted-publisher add) both load the same state, both modify, and the later
// save silently clobbers the earlier one's field. The mutex makes every write
// atomic w.r.t. other writes. Readers (LoadSettings) stay lock-free — atomic
// file I/O already guarantees they see either the old or new file in full.
//
// It is safe against deadlock with App-level locks (configMu, vaultMu) because
// the load/save path does only file I/O and never acquires an App lock.
var settingsWriteMu sync.Mutex

// maxSettingsJSONBytes bounds settings.json before it is parsed. A hostile
// co-tenant file cannot drive unbounded allocation ahead of the strict
// json.Decode (audit F12).
const maxSettingsJSONBytes int64 = 64 << 10 // 64 KB

// AppSettings is the user-global Silt settings, persisted at
// <UserConfigDir>/silt/settings.json. It is written atomically via
// parser.WriteFileAtomic so a crash never leaves a half-written file.
//
// Storage scope decision: theme selection is USER-GLOBAL (it follows the
// user's OS profile, not the vault). A vault-scoped override via
// .system/config.yaml is a documented future option, not implemented today.
type AppSettings struct {
	VaultPath string `json:"vault_path"`

	// ActiveTheme is the id of the theme to apply. When empty or unset it
	// defaults to the bundled primary theme id (themes.DefaultThemeID). The
	// theme loader (#45) validates this against available themes and falls
	// back to the default when the id is missing/invalid.
	ActiveTheme string `json:"active_theme"`

	// ThemeMode selects which mode of the active theme to render.
	// Valid values: "dark", "light", "system" (honor prefers-color-scheme).
	// An empty or unrecognized value normalizes to "dark", which matches the
	// shipped dark-first behavior.
	ThemeMode string `json:"theme_mode"`

	// TrustedPublishers is the user-global list of plugin publishers whose
	// signed plugins install without a warning (#111 distribution v2). Empty
	// means all unsigned plugins install with a warning prompt.
	TrustedPublishers []string `json:"trusted_publishers,omitempty"`

	// AutoCheckUpdates gates the startup update check (#312). It is a pointer
	// so an absent field in an older settings.json resolves to the default-on
	// behavior (nil → true) via AutoCheckUpdatesEnabled(); a user who turns it
	// off persists an explicit false. User-global, pre-vault state: the check
	// fires before any vault is open, so it cannot live in vault config.yaml.
	AutoCheckUpdates *bool `json:"auto_check_updates,omitempty"`

	// LastUpdateCheck records the last automatic/manual update check as an
	// RFC3339 timestamp (#312). Empty means never checked. It is a string (not
	// time.Time) because encoding/json's omitempty does not fire for a zero
	// time.Time struct, which would persist a junk "0001-01-01..." value.
	// Reproductible? No — but it is user-global app state, not vault content,
	// so it is outside the §0 rule-4 reproducibility contract that governs
	// SQLite. settings.json is its own (non-SQLite) source of truth.
	LastUpdateCheck string `json:"last_update_check,omitempty"`
}

// AutoCheckUpdatesEnabled reports whether the startup update check should run,
// resolving the pointer's default-on semantics (nil/absent → true). The single
// read path so callers never repeat the nil check.
func (s AppSettings) AutoCheckUpdatesEnabled() bool {
	if s.AutoCheckUpdates == nil {
		return true
	}
	return *s.AutoCheckUpdates
}

// ValidThemeMode reports whether mode is a recognized ThemeMode value.
func ValidThemeMode(mode string) bool {
	switch mode {
	case "dark", "light", "system":
		return true
	}
	return false
}

// withDefaults returns a copy of s with zero-valued theme fields filled in
// with their defaults. It keeps LoadSettings the single place that applies
// defaults so every caller (fresh settings, old settings.json, explicit
// accessors) observes the same baseline.
func (s AppSettings) withDefaults() AppSettings {
	if s.ActiveTheme == "" {
		s.ActiveTheme = themes.DefaultThemeID
	}
	if !ValidThemeMode(s.ThemeMode) {
		s.ThemeMode = "dark"
	}
	// Normalize the update toggle so a written settings.json is self-
	// describing (explicit true/false rather than null/absent on save).
	// LoadSettings returns the same resolved value to every caller.
	if s.AutoCheckUpdates == nil {
		t := true
		s.AutoCheckUpdates = &t
	}
	return s
}

func GetSettingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "silt", "settings.json"), nil
}

func LoadSettings() (*AppSettings, error) {
	path, err := GetSettingsPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// No settings file yet (first run): return defaults rather than a
		// zero struct so callers see a valid ActiveTheme/ThemeMode.
		out := AppSettings{}.withDefaults()
		// Seed the fingerprint for the defaults so the next launch (which
		// WILL write a real settings.json) has a baseline to compare against.
		// A missing fingerprint on a later launch is treated as first-run
		// migration (written silently), so this is belt-and-suspenders.
		return &out, nil
	}
	raw, err := safeio.ReadFileMax(path, maxSettingsJSONBytes)
	if err != nil {
		return nil, err
	}
	// Schema-strict decode: DisallowUnknownFields rejects a co-tenant's
	// field-injection attack (F20) — a settings.json with an unrecognized
	// top-level key fails loudly rather than being silently ignored. The
	// AppSettings struct is the schema; any key not modeled here is rejected.
	var settings AppSettings
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&settings); err != nil {
		return nil, fmt.Errorf("invalid settings.json: %w", err)
	}
	// vault_path must be absolute (or empty for a first-run file). A relative
	// path like "../../etc" or "evil" would resolve against the process CWD
	// and could redirect the vault to an attacker-chosen location (F20).
	// On Windows, filepath.IsAbs requires a drive letter or UNC prefix; on
	// POSIX, a leading slash. An empty path is the documented first-run state.
	if settings.VaultPath != "" && !filepath.IsAbs(settings.VaultPath) {
		return nil, fmt.Errorf("settings.json vault_path %q is not an absolute path", settings.VaultPath)
	}
	// Backward compatibility: an older settings.json written before theme
	// fields existed unmarshals with zero values, which withDefaults()
	// normalizes to the dark default theme. This also normalizes any
	// unrecognized ThemeMode to "dark".
	settings = settings.withDefaults()

	// F20 fingerprint tripwire: compare the trust-anchor fields against the
	// stored fingerprint. See fingerprint.go for the full rationale.
	currentFP := computeSettingsFingerprint(&settings)
	storedFP, hasFP, fpErr := readSettingsFingerprint()
	if fpErr != nil {
		return nil, fmt.Errorf("settings fingerprint read failed: %w", fpErr)
	}
	if !hasFP {
		// First launch after upgrade (or fresh install with a settings.json
		// but no fingerprint yet): seed the fingerprint silently. Silt is
		// making the first observation, not the user — no prompt.
		if wErr := writeSettingsFingerprint(&settings); wErr != nil {
			// Non-fatal: log-worthy but the settings are still usable. The
			// next launch retries the write.
			return &settings, nil
		}
		return &settings, nil
	}
	if storedFP != currentFP {
		// Mismatch: the trust-anchor fields changed since the last launch.
		// Return the settings (they are valid — usable) PLUS the sentinel so
		// the App startup can surface a confirmation dialog. The fingerprint
		// is NOT updated here; only ConfirmSettingsChange or SaveSettings
		// (Silt's own trusted write) updates it.
		return &settings, ErrSettingsFingerprintMismatch
	}
	return &settings, nil
}

// SaveSettings atomically persists settings (including the F20 fingerprint
// refresh). It acquires settingsWriteMu so it serializes against other writers
// (UpdateSettings and other SaveSettings calls); blind saves that don't need a
// read first (initial seed, rollback restore) can call it directly.
func SaveSettings(settings *AppSettings) error {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	return saveSettingsLocked(settings)
}

// saveSettingsLocked is SaveSettings without the mutex; the caller MUST hold
// settingsWriteMu (UpdateSettings uses it to keep its Load→Modify→Save under a
// single lock acquisition).
func saveSettingsLocked(settings *AppSettings) error {
	if settings == nil {
		return fmt.Errorf("settings must not be nil")
	}
	// Persist canonical values so the on-disk file is self-describing and a
	// later read never observes an empty/invalid theme field.
	normalized := settings.withDefaults()
	path, err := GetSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(&normalized, "", "  ")
	if err != nil {
		return err
	}
	// Use the same atomic write protocol as note files: write to a sibling
	// temp file, fsync, and rename. This guarantees the settings.json on
	// disk is either the previous version or the new one in full, never a
	// half-written file truncated by power loss.
	if err := parser.WriteFileAtomic(path, bytes); err != nil {
		return err
	}
	// F20: Silt's own write is trusted, so update the fingerprint to match
	// the just-saved trust-anchor values. This means a legitimate
	// user-initiated vault switch (SaveSettings with a new vault_path) does
	// NOT trip the mismatch wire on the next launch — only external
	// (non-Silt) edits to settings.json do. WriteFileAtomic produces 0o600
	// perms (os.CreateTemp default), satisfying the F7 perm-pairing rule.
	return writeSettingsFingerprint(&normalized)
}

// UpdateSettings runs a transactional read-modify-write of settings.json: it
// loads the current settings under settingsWriteMu, applies fn (which mutates
// the settings in place), and saves — all before releasing the lock, so no
// concurrent writer can interleave and clobber fn's change. Callers that only
// need a blind save (initial seed, rollback restore) should use SaveSettings
// directly. A fingerprint-mismatch on load is tolerated (the settings are still
// returned and usable); any other load error aborts the transaction.
//
// Returns the persisted settings (post-fn, post-withDefaults) so callers that
// need the canonicalized result don't have to re-load.
func UpdateSettings(fn func(*AppSettings)) (*AppSettings, error) {
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	settings, err := LoadSettings()
	if err != nil && !errors.Is(err, ErrSettingsFingerprintMismatch) {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	if settings == nil {
		settings = &AppSettings{}
	}
	fn(settings)
	if err := saveSettingsLocked(settings); err != nil {
		return nil, fmt.Errorf("save settings: %w", err)
	}
	return settings, nil
}

func ScaffoldVault(vaultPath string) error {
	// ScaffoldVault is intentionally idempotent: every file/folder create
	// is guarded by an os.Stat existence check. Re-running it on the
	// same vault path is safe and will leave custom user content
	// (e.g. their own themes, plugins, or notes) untouched.
	//
	// Silt starts blank: no default notebook or page is created. The user
	// opens or creates their first notebook from the sidebar selector.
	// 1. Create folders
	folders := []string{
		filepath.Join(vaultPath, ".system"),
		filepath.Join(vaultPath, ".system", "themes"),
		filepath.Join(vaultPath, ".system", "plugins"),
	}

	for _, folder := range folders {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return fmt.Errorf("failed to create vault folder %s: %w", folder, err)
		}
	}

	// 2. Scaffold config.yaml
	configYAML := `# Silt Global System Settings Configuration

# Spatial Mapping
notebooks:
  path: "%s"
  default_active: "Work"

# Editor Tuning
editor:
  font_family: "Plus Jakarta Sans"
  mono_font_family: "JetBrains Mono"
  font_size_px: 14
  line_height: 1.6
  tab_indent_spaces: 4
  auto_save_delay_ms: 500
  focus_highlight_ancestors: true

# Task Parse Rules
# The task checkbox/metadata regexes are fixed in the binary (parser
# package) and intentionally not exposed here — a user-supplied regex on a
# synced vault is a parser-DoS vector (audit F11).
parsing:
  auto_inject_uuid: true
  default_task_priority: 3

# Plugin Registry
plugins:
  active:
    - "silt-agenda"
    - "silt-calendar"
    - "silt-kanban"
  disabled: []
  plugin_settings:
    silt-kanban:
      default_col: "TODO"
      columns: ["TODO", "DOING", "DONE"]

# UI Preferences (per-vault)
ui:
  sidebar_width: 256
  enable_preview_tabs: true
  max_open_tabs: 8
`
	configPath := filepath.Join(vaultPath, ".system", "config.yaml")
	// Format config with absolute vault path (with forward slashes for cross platform consistency)
	formattedVaultPath := filepath.ToSlash(vaultPath)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		err = os.WriteFile(configPath, []byte(fmt.Sprintf(configYAML, formattedVaultPath)), 0o600)
		if err != nil {
			return fmt.Errorf("failed to write config.yaml: %w", err)
		}
	}

	// 3. Scaffold the first-class themes. The canonical content lives in
	// backend/themes/themes and is embedded in the binary; writing every
	// embedded file from that single source of truth keeps the scaffolded
	// copies identical to the runtime fallback set. Each write is guarded
	// by an existence check so a user who edits (or deletes) one is never
	// overwritten — only missing files are created.
	themeFiles, err := themes.EmbeddedThemeFiles()
	if err != nil {
		return fmt.Errorf("failed to read embedded first-class themes: %w", err)
	}
	for fileName, raw := range themeFiles {
		themePath := filepath.Join(vaultPath, ".system", "themes", fileName)
		if _, err := os.Stat(themePath); err != nil {
			if os.IsNotExist(err) {
				if err := os.WriteFile(themePath, raw, 0o600); err != nil {
					return fmt.Errorf("failed to write theme %s: %w", fileName, err)
				}
				continue
			}
			// Anything other than "not exist" (permission denied,
			// I/O error, …) is a real fault that the user should
			// see — silently skipping would leave a half-scaffolded
			// themes dir with no surfaceable cause.
			return fmt.Errorf("failed to stat theme %s: %w", fileName, err)
		}
	}

	// 4. Plugins folder README (documents the on-disk plugin layout).
	pluginsReadme := `# Silt Plugins

Plugins live one-per-folder here, e.g.:

    .system/plugins/<plugin-id>/index.js

Enable a plugin by adding its id to .system/config.yaml under plugins.active.
Third-party plugins can also be installed from a .silt-plugin archive via the
in-app plugin manager.

See docs/PLUGIN_DEVELOPMENT.md for the full SDK reference.
`
	pluginsReadmePath := filepath.Join(vaultPath, ".system", "plugins", "README.md")
	if _, err := os.Stat(pluginsReadmePath); os.IsNotExist(err) {
		if err := os.WriteFile(pluginsReadmePath, []byte(pluginsReadme), 0o600); err != nil {
			return fmt.Errorf("failed to write plugins README: %w", err)
		}
	}

	return nil
}
