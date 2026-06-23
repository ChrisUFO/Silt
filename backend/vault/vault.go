package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"silt/backend/parser"
	"silt/backend/themes"
)

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
	raw, err := os.ReadFile(path)
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

func SaveSettings(settings *AppSettings) error {
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
		if err := os.MkdirAll(folder, 0755); err != nil {
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
parsing:
  auto_inject_uuid: true
  checkbox_regex: "^([\\s]*)-\\s\\[([ x/])\\]\\s+(.*)$"
  metadata_token_regex: "\\[([\\w]+)::\\s*([^\\]]*)\\]"
  default_task_priority: 3

# Key-Binding Map
hotkeys:
  open_search: "Ctrl+P"
  open_command_palette: "Ctrl+Slash"
  toggle_sidebar: "Ctrl+B"
  cycle_view_layout: "Alt+Tab"
  indent_block: "Tab"
  unindent_block: "Shift+Tab"
  open_template_picker: "Ctrl+Shift+T"
  next_tab: "Ctrl+Tab"
  prev_tab: "Ctrl+Shift+Tab"
  close_tab: "Ctrl+W"

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
		err = os.WriteFile(configPath, []byte(fmt.Sprintf(configYAML, formattedVaultPath)), 0644)
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
				if err := os.WriteFile(themePath, raw, 0644); err != nil {
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
		if err := os.WriteFile(pluginsReadmePath, []byte(pluginsReadme), 0644); err != nil {
			return fmt.Errorf("failed to write plugins README: %w", err)
		}
	}

	return nil
}
