// Package config parses and persists Silt's system configuration
// (<vault>/.system/config.yaml). It is the single source of truth for all
// non-vault-path application settings: editor defaults, parsing rules,
// hotkeys, and the plugin registry. The vault path itself still lives in the
// OS-config settings.json (it must be known before any vault can be opened).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// SystemConfig is the parsed contents of <vault>/.system/config.yaml. It
// mirrors the schema documented in SPECS.md §9.1.
type SystemConfig struct {
	Notebooks NotebooksConfig   `yaml:"notebooks" json:"notebooks"`
	Editor    EditorConfig      `yaml:"editor" json:"editor"`
	Parsing   ParsingConfig     `yaml:"parsing" json:"parsing"`
	Hotkeys   map[string]string `yaml:"hotkeys" json:"hotkeys"`
	Plugins   PluginsConfig     `yaml:"plugins" json:"plugins"`
	UI        UIConfig          `yaml:"ui" json:"ui"`
}

// NotebooksConfig holds spatial-mapping defaults.
type NotebooksConfig struct {
	Path          string `yaml:"path" json:"path"`
	DefaultActive string `yaml:"default_active" json:"default_active"`
}

// EditorConfig holds editor rendering and behaviour defaults.
type EditorConfig struct {
	FontFamily              string  `yaml:"font_family" json:"font_family"`
	MonoFontFamily          string  `yaml:"mono_font_family" json:"mono_font_family"`
	FontSizePx              int     `yaml:"font_size_px" json:"font_size_px"`
	LineHeight              float64 `yaml:"line_height" json:"line_height"`
	TabIndentSpaces         int     `yaml:"tab_indent_spaces" json:"tab_indent_spaces"`
	AutoSaveDelayMs         int     `yaml:"auto_save_delay_ms" json:"auto_save_delay_ms"`
	FocusHighlightAncestors bool    `yaml:"focus_highlight_ancestors" json:"focus_highlight_ancestors"`
}

// ParsingConfig holds the task-parse rules consumed by the AST parser.
type ParsingConfig struct {
	AutoInjectUUID      bool   `yaml:"auto_inject_uuid" json:"auto_inject_uuid"`
	ShorthandRegex      string `yaml:"shorthand_regex" json:"shorthand_regex"`
	DefaultTaskPriority int    `yaml:"default_task_priority" json:"default_task_priority"`
}

// PluginsConfig mirrors the `plugins:` block of config.yaml. PluginSettings is
// an opaque per-plugin map (the plugin manager surfaces it read-only).
type PluginsConfig struct {
	Active         []string       `yaml:"active" json:"active"`
	Disabled       []string       `yaml:"disabled" json:"disabled"`
	PluginSettings map[string]any `yaml:"plugin_settings" json:"plugin_settings"`
}

// UIConfig holds per-vault UI preferences (sidebar width, custom navigation
// ordering). Stored in the YAML tier (per-vault) per ARCHITECTURE §0 rule #2.
type UIConfig struct {
	SidebarWidth int      `yaml:"sidebar_width" json:"sidebar_width"`
	NavOrder     NavOrder `yaml:"nav_order,omitempty" json:"nav_order,omitempty"`
}

// NavOrder stores explicit ordering for the sidebar navigator tree. Folders on
// disk have no inherent custom order; this map overrides the default
// alphabetical sort. Keys not present in the map fall back to alphabetical.
type NavOrder struct {
	Notebooks []string            `yaml:"notebooks,omitempty" json:"notebooks,omitempty"`
	Sections  map[string][]string `yaml:"sections,omitempty" json:"sections,omitempty"`
	Pages     map[string][]string `yaml:"pages,omitempty" json:"pages,omitempty"`
}

// hotkeyModifiers are the modifier tokens allowed in a hotkey binding
// (case-insensitive). Everything else in a binding is treated as the key.
var hotkeyModifiers = map[string]bool{
	"ctrl": true, "control": true, "shift": true,
	"alt": true, "option": true, "meta": true,
	"cmd": true, "command": true, "win": true,
}

// ValidateHotkeys rejects bindings that would parse to a null hotkey and
// silently disable the action. An empty binding is allowed (it means
// "intentionally disabled" — matchHotkey never fires — which is also the only
// way to disable a hotkey, since deleting the key would restore the default
// via the YAML merge). A non-empty binding must contain at least one
// non-modifier token, mirroring the frontend parseHotkey's null outcome so the
// two layers agree on what is valid.
func ValidateHotkeys(hotkeys map[string]string) error {
	for action, binding := range hotkeys {
		binding = strings.TrimSpace(binding)
		if binding == "" {
			continue // explicitly disabled
		}
		hasKey := false
		for _, p := range strings.Split(strings.ToLower(binding), "+") {
			t := strings.TrimSpace(p)
			if t == "" {
				continue // tolerate stray empty segments (e.g. "Ctrl++P")
			}
			if !hotkeyModifiers[t] {
				hasKey = true
			}
		}
		if !hasKey {
			return fmt.Errorf("invalid hotkey for %q: %q has no key (only modifiers)", action, binding)
		}
	}
	return nil
}

// ConfigPath returns the absolute path to a vault's config.yaml.
func ConfigPath(vaultPath string) string {
	return filepath.Join(vaultPath, ".system", "config.yaml")
}

// Defaults returns a fully-populated SystemConfig matching the config.yaml
// scaffolded by vault.ScaffoldVault, so a missing/empty field is never a
// nil-deref and "first run" behaves like a fresh scaffold.
func Defaults() SystemConfig {
	return SystemConfig{
		Notebooks: NotebooksConfig{
			DefaultActive: "Work",
		},
		Editor: EditorConfig{
			FontFamily:              "Plus Jakarta Sans",
			MonoFontFamily:          "JetBrains Mono",
			FontSizePx:              14,
			LineHeight:              1.6,
			TabIndentSpaces:         4,
			AutoSaveDelayMs:         500,
			FocusHighlightAncestors: true,
		},
		Parsing: ParsingConfig{
			AutoInjectUUID: true,
			ShorthandRegex: `^([ ]|[/]|[x])\s(TODO|DOING|DONE)\sTASK\s(?:\s*\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$`,
			DefaultTaskPriority: 3,
		},
		Hotkeys: map[string]string{
			"open_search":           "Ctrl+P",
			"open_command_palette":  "Ctrl+Slash",
			"toggle_sidebar":        "Ctrl+B",
			"cycle_view_layout":     "Alt+Tab",
			"indent_block":          "Tab",
			"unindent_block":        "Shift+Tab",
			"open_template_picker":  "Ctrl+Shift+T",
		},
		Plugins: PluginsConfig{
			Active:   []string{"silt-agenda", "silt-calendar", "silt-kanban"},
			Disabled: []string{},
			PluginSettings: map[string]any{
				"silt-kanban": map[string]any{
					"default_col": "TODO",
					"columns":     []any{"TODO", "DOING", "DONE"},
				},
			},
		},
		UI: UIConfig{
			SidebarWidth: 256,
			NavOrder: NavOrder{
				Sections: map[string][]string{},
				Pages:    map[string][]string{},
			},
		},
	}
}

// Load reads <vault>/.system/config.yaml. A missing file is not an error: it
// returns Defaults() so a fresh vault works without an explicit config. A file
// that exists but fails to parse returns an error (do not silently fall
// through to defaults — the user has a config, it is just broken). Fields
// absent from the file keep their default values.
func Load(vaultPath string) (SystemConfig, error) {
	data, err := os.ReadFile(ConfigPath(vaultPath))
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return Defaults(), fmt.Errorf("failed to read config.yaml: %w", err)
	}

	// Decode over the defaults so omitted sections keep their default values
	// rather than being zero-valued.
	//
	// Merge semantics worth knowing: yaml.v3 decodes into the pre-populated
	// struct, so SCALAR fields absent from the file keep their default, while
	// MAP fields (hotkeys, plugin_settings) are MERGED — keys present in the
	// file override defaults, but keys ABSENT from the file are NOT removed.
	// Practically: deleting a default hotkey/plugin-setting entry from
	// config.yaml will silently restore it on the next load. To "remove" a
	// hotkey, set it to an empty string ("") rather than deleting the line.
	// (A zero-value-first unmarshal + custom presence-aware merge would change
	// this, but it is a deliberate behavior change left out of scope here.)
	cfg := Defaults()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Defaults(), fmt.Errorf("failed to parse config.yaml: %w", err)
	}
	cfg = normalize(cfg)
	return cfg, nil
}

// Save atomically writes cfg to <vault>/.system/config.yaml. Atomicity
// (temp file + fsync + rename) guarantees the on-disk file is either the
// previous version or the new one in full, never a half-written file.
func Save(vaultPath string, cfg SystemConfig) error {
	cfg = normalize(cfg)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config.yaml: %w", err)
	}
	return writeFileAtomic(ConfigPath(vaultPath), out, 0o644)
}

// normalize guarantees non-nil slices/maps and a populated hotkeys table so
// downstream consumers (and JSON serialization over the IPC boundary) never
// see null where an empty collection is meant.
func normalize(cfg SystemConfig) SystemConfig {
	if cfg.Plugins.Active == nil {
		cfg.Plugins.Active = []string{}
	}
	if cfg.Plugins.Disabled == nil {
		cfg.Plugins.Disabled = []string{}
	}
	if cfg.Plugins.PluginSettings == nil {
		cfg.Plugins.PluginSettings = map[string]any{}
	}
	if cfg.Hotkeys == nil {
		cfg.Hotkeys = map[string]string{}
	}
	if cfg.UI.NavOrder.Sections == nil {
		cfg.UI.NavOrder.Sections = map[string][]string{}
	}
	if cfg.UI.NavOrder.Pages == nil {
		cfg.UI.NavOrder.Pages = map[string][]string{}
	}
	if cfg.UI.SidebarWidth < 200 {
		cfg.UI.SidebarWidth = 256
	}
	return cfg
}

// writeFileAtomic writes data to a sibling temp file, fsyncs it, then renames
// it over path. Kept local (rather than reusing parser.WriteFileAtomic) so the
// config package stays decoupled from the markdown parser.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // best-effort cleanup on any failure path

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
