package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"notes-sharp/backend/parser"
)

type AppSettings struct {
	VaultPath string `json:"vault_path"`
}

func GetSettingsPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "notes-sharp", "settings.json"), nil
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
	// 1. Create folders
	folders := []string{
		filepath.Join(vaultPath, ".system"),
		filepath.Join(vaultPath, ".system", "themes"),
		filepath.Join(vaultPath, ".system", "plugins"),
		filepath.Join(vaultPath, "Work"),
		filepath.Join(vaultPath, "Work", "Journal"),
	}

	for _, folder := range folders {
		if err := os.MkdirAll(folder, 0755); err != nil {
			return fmt.Errorf("failed to create vault folder %s: %w", folder, err)
		}
	}

	// 2. Scaffold config.yaml
	configYAML := `# notes# Global System Settings Configuration

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
    - "notes-sharp-agenda"
    - "notes-sharp-calendar"
    - "notes-sharp-kanban"
  disabled: []
  plugin_settings:
    notes-sharp-kanban:
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

	// 4. Scaffold welcome daily note
	todayStr := time.Now().Format("2006-01-02")
	welcomeNote := `---
notebook: Work
section: Journal
date: %s
tags: [welcome, tutorial]
---
# Welcome to notes# <!-- id: 5ec16086-7cb4-49c8-bf50-c831b79f82de -->

notes# is an uncompromised, local-first note-taking and task-lifecycle platform. <!-- id: a78d8a0c-51de-46fa-9fe3-c64efb4d1c16 -->

## Quick Start <!-- id: d5b4c102-482f-410a-b108-a578ee1a221f -->
- [x] DONE TASK [Chris]#3 Successfully initialize notes# vault <!-- id: b64987dc-e33a-4467-9252-78d12a9e328e -->
- [ ] TODO TASK [Chris]#1 Explore the notes# interface <!-- id: f437b7dc-d33a-4f67-8252-78d12a9e3290 -->
- [ ] TODO TASK [Chris]#2 Try typing a new task using the /todo slash menu <!-- id: c537b7dc-c33a-4f67-7252-78d12a9e329f -->
`
	dailyFilePath := filepath.Join(vaultPath, "Work", "Journal", todayStr+".md")
	if _, err := os.Stat(dailyFilePath); os.IsNotExist(err) {
		err = os.WriteFile(dailyFilePath, []byte(fmt.Sprintf(welcomeNote, todayStr)), 0644)
		if err != nil {
			return fmt.Errorf("failed to create welcome note file: %w", err)
		}
	}

	return nil
}
