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
	Notebooks       NotebooksConfig   `yaml:"notebooks" json:"notebooks"`
	Editor          EditorConfig      `yaml:"editor" json:"editor"`
	Parsing         ParsingConfig     `yaml:"parsing" json:"parsing"`
	Hotkeys         map[string]string `yaml:"hotkeys" json:"hotkeys"`
	Plugins         PluginsConfig     `yaml:"plugins" json:"plugins"`
	UI              UIConfig          `yaml:"ui" json:"ui"`
	LinkedNotebooks []LinkedNotebook  `yaml:"linked_notebooks,omitempty" json:"linked_notebooks,omitempty"`
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
	// ShowWordCount controls the subtle word/char count in the editor status
	// area (#168 Phase 3). Default false — opt-in so we add no chrome by default.
	ShowWordCount *bool `yaml:"show_word_count,omitempty" json:"show_word_count,omitempty"`
	// FocusMode dims all paragraphs except the active one for distraction-free
	// writing (#168 Phase 3). Default false.
	FocusMode *bool `yaml:"focus_mode,omitempty" json:"focus_mode,omitempty"`
	// DefaultViewMode controls whether pages open in "edit" (TipTap WYSIWYG)
	// or "source" (raw markdown) mode (#171). Default "edit".
	DefaultViewMode *string `yaml:"default_view_mode,omitempty" json:"default_view_mode,omitempty"`
}

// ParsingConfig holds the task-parse rules consumed by the AST parser.
type ParsingConfig struct {
	AutoInjectUUID      bool   `yaml:"auto_inject_uuid" json:"auto_inject_uuid"`
	ShorthandRegex      string `yaml:"shorthand_regex" json:"shorthand_regex"`
	DefaultTaskPriority int    `yaml:"default_task_priority" json:"default_task_priority"`
}

// PluginsConfig mirrors the `plugins:` block of config.yaml. PluginSettings is
// an opaque per-plugin map (the plugin manager surfaces it read-only). Grants
// is the v2 SDK capability grant table (#113): pluginID → capability →
// qualifier ("granted" | "notebook" | "vault"). It is the single source of
// truth for per-vault capability permission — vault state, not user-global,
// aligning with #100's trust-scoping principle. A missing entry means "not
// granted" (privileged bindings return a structured CapabilityDeniedError).
type PluginsConfig struct {
	Active         []string                  `yaml:"active" json:"active"`
	Disabled       []string                  `yaml:"disabled" json:"disabled"`
	PluginSettings map[string]any            `yaml:"plugin_settings" json:"plugin_settings"`
	Grants         map[string]map[string]string `yaml:"grants,omitempty" json:"grants,omitempty"`
}

// LinkedNotebook is an external notebook root registered into the vault but
// living outside it (e.g. a synced SharePoint/OneDrive folder). It is edited
// IN PLACE — never copied into the vault — so its existing source of truth and
// sync/conflict semantics are preserved (#100). The link registry
// (config.yaml `linked_notebooks:`) is vault-scoped state alongside the active
// plugin list; the markdown content (and any co-located <root>/.system/) stays
// with the notebook root and is the product.
type LinkedNotebook struct {
	ID          string `yaml:"id" json:"id"`                     // stable id, e.g. "linked-<short>"; source column = "linked:"+ID
	RootPath    string `yaml:"root_path" json:"root_path"`       // absolute path to the external notebook root
	DisplayName string `yaml:"display_name" json:"display_name"` // sidebar label (the notebook "name")
}

// Source returns the `blocks.source` discriminator value for this linked
// notebook ('linked:<id>'), matching what the indexer writes.
func (l LinkedNotebook) Source() string { return "linked:" + l.ID }

// LinkedNotebooksSource is the `blocks.source` value for in-vault notebooks.
const LinkedNotebooksVaultSource = "vault"

// UIConfig holds per-vault UI preferences (sidebar width, custom navigation
// ordering, the open-tab set). Stored in the YAML tier (per-vault) per
// ARCHITECTURE §0 rule #2.
type UIConfig struct {
	SidebarWidth      int      `yaml:"sidebar_width" json:"sidebar_width"`
	NavOrder          NavOrder `yaml:"nav_order,omitempty" json:"nav_order,omitempty"`
	OpenTabs          []TabRef `yaml:"open_tabs,omitempty" json:"open_tabs,omitempty"`
	ActiveTab         *TabRef  `yaml:"active_tab,omitempty" json:"active_tab,omitempty"`
	EnablePreviewTabs *bool    `yaml:"enable_preview_tabs,omitempty" json:"enable_preview_tabs,omitempty"`
	MaxOpenTabs       int      `yaml:"max_open_tabs,omitempty" json:"max_open_tabs,omitempty"`
	// ShowFormatToolbar controls the persistent format toolbar visibility
	// (#168). Default true; users who want outliner-minimal density can hide
	// it from Settings. The bubble, slash commands, hotkeys, and hover menu
	// remain functional when hidden.
	ShowFormatToolbar *bool `yaml:"show_format_toolbar,omitempty" json:"show_format_toolbar,omitempty"`
	// DismissedTips tracks one-time UI tips the user has dismissed (per-vault).
	// Used by the formatting first-run tip (#168). Same persistence tier as
	// sidebar_width.
	DismissedTips []string `yaml:"dismissed_tips,omitempty" json:"dismissed_tips,omitempty"`
	// Formatting holds inline-formatting-related UI toggles (#168 Phase 3, #170).
	Formatting FormattingConfig `yaml:"formatting,omitempty" json:"formatting,omitempty"`
}

// FormattingConfig holds per-vault toggles for inline formatting features.
type FormattingConfig struct {
	// TypographyEnabled controls smart input replacements (-- → —, (c) → ©,
	// straight → curly quotes). Default true; markdown purists can disable (#168).
	TypographyEnabled *bool `yaml:"typography_enabled,omitempty" json:"typography_enabled,omitempty"`
	// ColorEnabled controls the text/background color pickers (#170). Default
	// true; markdown purists can disable to keep files 100% portable. The marks
	// still parse from incoming files when disabled; only the editor's setColor
	// calls become no-ops.
	ColorEnabled *bool `yaml:"color_enabled,omitempty" json:"color_enabled,omitempty"`
}

// TabRef is a persisted reference to an open tab's page (#142). It is the
// YAML-serializable form of a frontend TabEntry — only the locator triple is
// persisted; preview flag, scroll/cursor state, and the like are ephemeral
// (preview tabs are not restored across restarts). The frontend filters to
// pinned tabs before calling SetOpenTabs.
type TabRef struct {
	Notebook string `yaml:"notebook" json:"notebook"`
	Section  string `yaml:"section" json:"section"`
	Page     string `yaml:"page" json:"page"`
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
			ShowWordCount: boolPtr(false),
			FocusMode:     boolPtr(false),
			DefaultViewMode: stringPtr("edit"),
		},
		Parsing: ParsingConfig{
			AutoInjectUUID:      true,
			ShorthandRegex:      `^([ ]|[/]|[x])\s(TODO|DOING|DONE)\sTASK\s(?:\s*\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$`,
			DefaultTaskPriority: 3,
		},
		Hotkeys: map[string]string{
			"open_search":          "Ctrl+P",
			"open_command_palette": "Ctrl+Slash",
			"toggle_sidebar":       "Ctrl+B",
			"cycle_view_layout":    "Alt+Tab",
			"indent_block":         "Tab",
			"unindent_block":       "Shift+Tab",
			"open_template_picker": "Ctrl+Shift+T",
			// Tab strip hotkeys (#142). `tab` and `w` already parse cleanly
			// via the frontend parseHotkey layer (KEY_ALIASES in
			// frontend/src/settings/hotkeys.ts). Each may be remapped or
			// disabled (set to "") from Settings → General.
			"next_tab":  "Ctrl+Tab",
			"prev_tab":  "Ctrl+Shift+Tab",
			"close_tab": "Ctrl+W",
			// Inline formatting hotkeys (#168). Standard editor bindings
			// so muscle memory transfers. Each is overridable per-vault via
			// the deep-merge. The editor's ProseMirror keymaps consume these
			// inside the contenteditable; the global handler skips them when
			// the editor is focused (Ctrl+B resolution).
			"format_bold":       "Ctrl+B",
			"format_italic":     "Ctrl+I",
			"format_underline":  "Ctrl+U",
			"format_strike":     "Ctrl+Shift+X",
			"format_code":       "Ctrl+E",
			"format_link":       "Ctrl+K",
			"format_highlight":  "Ctrl+Shift+H",
			"format_subscript":  "Ctrl+,",
			"format_superscript": "Ctrl+.",
			// Heading level hotkeys (#169). Standard heading-level shortcuts.
			"set_h1":   "Ctrl+Alt+1",
			"set_h2":   "Ctrl+Alt+2",
			"set_h3":   "Ctrl+Alt+3",
			"set_note": "Ctrl+Alt+0",
			"set_task": "Ctrl+Alt+4",
			// Text alignment hotkeys (#173). Standard alignment shortcuts.
			"align_left":    "Ctrl+Shift+L",
			"align_center":  "Ctrl+Shift+E",
			"align_right":   "Ctrl+Shift+R",
			"align_justify": "Ctrl+Shift+J",
			// View mode toggle (#171). Standard source/view toggle binding.
			"toggle_view_mode": "Ctrl+Shift+V",
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
			Grants: map[string]map[string]string{},
		},
		UI: UIConfig{
			SidebarWidth: 256,
			NavOrder: NavOrder{
				Sections: map[string][]string{},
				Pages:    map[string][]string{},
			},
			OpenTabs: []TabRef{},
			// EnablePreviewTabs defaults to true. Stored as a *bool so
			// "unset" is distinguishable from "explicitly false"; the
			// frontend treats nil as true.
			EnablePreviewTabs: boolPtr(true),
			// MaxOpenTabs caps the simultaneously-mounted editor count
			// (#142 §3). 8 is the documented default; on overflow the
			// frontend LRU-evicts least-recently-active preview tabs first,
			// then oldest pinned. 0 (legacy config without the key) is
			// normalized to 8 in normalize().
			MaxOpenTabs: 8,
			// ShowFormatToolbar defaults to true (#168). Stored as *bool so
			// "unset" is distinguishable from "explicitly false"; the frontend
			// treats nil as true.
			ShowFormatToolbar: boolPtr(true),
			DismissedTips:     []string{},
			Formatting: FormattingConfig{
				TypographyEnabled: boolPtr(true),
				ColorEnabled:      boolPtr(true),
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

// LinkedConfigPath returns the absolute path to a linked notebook's
// co-located config.yaml. Per the storage-of-truth model (#133), data
// attached to a notebook travels with the notebook: for a linked (external)
// notebook, per-notebook plugin overrides live at
// `<linkedRoot>/.system/config.yaml`, so an external notebook on SharePoint
// carries its own config with it — not in the vault. Silt treats this file
// as READ-ONLY / user-authored; plugin settings continue to persist to the
// vault-scoped config.yaml via the atomic UpdatePluginSetting path. The
// co-located file is purely an override layer the user authors on the
// external mount.
func LinkedConfigPath(linkedRoot string) string {
	return filepath.Join(linkedRoot, ".system", "config.yaml")
}

// LoadLinked reads a linked notebook's co-located `<linkedRoot>/.system/
// config.yaml` (#133). A missing file is NOT an error: it returns Defaults()
// with a nil error, because a linked notebook without a co-located config is
// the normal case (the vault-scoped config.yaml still provides the baseline).
// A file that exists but fails to parse returns Defaults() with a wrapped
// error — the caller MUST surface this so the user can fix the source rather
// than silently inheriting defaults. Mirrors Load's decode-over-Defaults
// semantics so omitted sections keep their default values.
func LoadLinked(linkedRoot string) (SystemConfig, error) {
	path := LinkedConfigPath(linkedRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return Defaults(), fmt.Errorf("failed to read linked config.yaml: %w", err)
	}
	cfg := Defaults()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Defaults(), fmt.Errorf("failed to parse linked config.yaml: %w", err)
	}
	return normalize(cfg), nil
}

// MergePluginSettings deep-merges two per-plugin settings maps for the
// co-located config override layer (#133). `vault` is the plugin's entry in
// the vault-scoped config.yaml; `linked` is the plugin's entry in the linked
// notebook's co-located config.yaml. The result is a NEW map (vault is not
// mutated) where:
//   - keys present ONLY in vault are preserved;
//   - keys present ONLY in linked are added;
//   - keys present in BOTH are merged: nested `map[string]any` values merge
//     recursively (linked's sub-keys override vault's per-key); scalars and
//     arrays from linked REPLACE vault's.
//
// This mirrors the user expectation that "the notebook's value wins" without
// losing vault defaults the notebook did not override. Both inputs may be nil
// (treated as empty); the result is always non-nil.
func MergePluginSettings(vault, linked map[string]any) map[string]any {
	out := make(map[string]any, len(vault)+len(linked))
	for k, v := range vault {
		out[k] = cloneValue(v)
	}
	for k, lv := range linked {
		if rv, ok := out[k]; ok {
			if rmap, rOK := rv.(map[string]any); rOK {
				if lmap, lOK := lv.(map[string]any); lOK {
					out[k] = mergeMaps(rmap, lmap)
					continue
				}
			}
		}
		out[k] = cloneValue(lv)
	}
	return out
}

// mergeMaps returns a new map that is `a` deep-merged with `b` (b wins per
// key, nested maps recurse). Neither input is mutated.
func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = cloneValue(v)
	}
	for k, bv := range b {
		if av, ok := out[k]; ok {
			if amap, aOK := av.(map[string]any); aOK {
				if bmap, bOK := bv.(map[string]any); bOK {
					out[k] = mergeMaps(amap, bmap)
					continue
				}
			}
		}
		out[k] = cloneValue(bv)
	}
	return out
}

// cloneValue returns a deep copy of a YAML-derived value. Only the types
// yaml.v3 can produce are handled: map[string]any, []any, string, bool, int,
// int64, float64, and nil. Maps and slices are deep-copied so the merge
// never aliases the caller's input; scalars are returned as-is (immutable).
func cloneValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = cloneValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = cloneValue(vv)
		}
		return out
	default:
		return v
	}
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
	if cfg.Plugins.Grants == nil {
		cfg.Plugins.Grants = map[string]map[string]string{}
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
	if cfg.UI.OpenTabs == nil {
		cfg.UI.OpenTabs = []TabRef{}
	}
	// MaxOpenTabs: 0 (legacy config without the key) → 8 (the default).
	// Negative or absurdly-small values also fall back. An upper bound of
	// 32 prevents a user from mounting hundreds of TipTap editors
	// simultaneously and exhausting memory (#142 hardening).
	if cfg.UI.MaxOpenTabs < 1 {
		cfg.UI.MaxOpenTabs = 8
	}
	if cfg.UI.MaxOpenTabs > 32 {
		cfg.UI.MaxOpenTabs = 32
	}
	// EnablePreviewTabs: nil → true. The field is a *bool so "unset" stays
	// distinguishable from "explicitly false" through the Load → normalize
	// path; once normalized, the frontend reads nil as true.
	if cfg.UI.EnablePreviewTabs == nil {
		cfg.UI.EnablePreviewTabs = boolPtr(true)
	}
	// ShowFormatToolbar: nil → true (#168). Same *bool semantics as
	// EnablePreviewTabs.
	if cfg.UI.ShowFormatToolbar == nil {
		cfg.UI.ShowFormatToolbar = boolPtr(true)
	}
	if cfg.UI.DismissedTips == nil {
		cfg.UI.DismissedTips = []string{}
	}
	// TypographyEnabled: nil → true (#168 Phase 3).
	if cfg.UI.Formatting.TypographyEnabled == nil {
		cfg.UI.Formatting.TypographyEnabled = boolPtr(true)
	}
	if cfg.UI.Formatting.ColorEnabled == nil {
		cfg.UI.Formatting.ColorEnabled = boolPtr(true)
	}
	// ShowWordCount: nil → false (#168 Phase 3). Opt-in.
	if cfg.Editor.ShowWordCount == nil {
		cfg.Editor.ShowWordCount = boolPtr(false)
	}
	// FocusMode: nil → false (#168 Phase 3).
	if cfg.Editor.FocusMode == nil {
		cfg.Editor.FocusMode = boolPtr(false)
	}
	// DefaultViewMode: nil → "edit" (#171). Validate to edit/source.
	if cfg.Editor.DefaultViewMode == nil {
		cfg.Editor.DefaultViewMode = stringPtr("edit")
	} else {
		v := strings.TrimSpace(*cfg.Editor.DefaultViewMode)
		if v != "edit" && v != "source" {
			cfg.Editor.DefaultViewMode = stringPtr("edit")
		}
	}
	return cfg
}

// boolPtr is a small helper for the Defaults() block so *bool fields can be
// initialized inline without a temporary variable.
func boolPtr(b bool) *bool { return &b }

// stringPtr is a small helper for the Defaults() block so *string fields can
// be initialized inline without a temporary variable.
func stringPtr(s string) *string { return &s }

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
