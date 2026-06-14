package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"silt/backend/parser"
)

type AppSettings struct {
	VaultPath string `json:"vault_path"`
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
		return &AppSettings{}, nil
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var settings AppSettings
	if err := json.Unmarshal(bytes, &settings); err != nil {
		return nil, err
	}
	return &settings, nil
}

func SaveSettings(settings *AppSettings) error {
	path, err := GetSettingsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	// Use the same atomic write protocol as note files: write to a sibling
	// temp file, fsync, and rename. This guarantees the settings.json on
	// disk is either the previous version or the new one in full, never a
	// half-written file truncated by power loss.
	return parser.WriteFileAtomic(path, bytes)
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
  shorthand_regex: "^([ ]|[/]|[x])\\s(TODO|DOING|DONE)\\sTASK\\s(?:\\s*\\[([^\\]]*)\\])?(?:\\(([^)]*)\\))?(?:#(\\d+))?\\s(.*)$"
  default_task_priority: 3

# Key-Binding Map
hotkeys:
  open_search: "Ctrl+P"
  open_command_palette: "Ctrl+Slash"
  cycle_view_layout: "Alt+Tab"
  indent_block: "Tab"
  unindent_block: "Shift+Tab"

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

	// 3. Scaffold default theme (cyber_forest.json)
	cyberForestJSON := `{
  "name": "Cyber Forest",
  "author": "System Designer",
  "colors": {
    "bg-void": "#080b09",
    "bg-surface": "#0d1310",
    "bg-panel": "#121b16",
    "bg-hover": "#1a2620",
    "border-zinc": "#22332a",
    "border-active": "#3d5c4b",
    "text-primary": "#e2ebd5",
    "text-muted": "#6a8274",
    "color-teal-start": "#10b981",
    "color-teal-end": "#059669",
    "color-indigo-start": "#4ade80",
    "color-indigo-end": "#22c55e"
  }
}
`
	themePath := filepath.Join(vaultPath, ".system", "themes", "cyber_forest.json")
	if _, err := os.Stat(themePath); os.IsNotExist(err) {
		err = os.WriteFile(themePath, []byte(cyberForestJSON), 0644)
		if err != nil {
			return fmt.Errorf("failed to write cyber_forest.json theme: %w", err)
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
