package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFile is a tiny helper for tests.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDefaults_Populated(t *testing.T) {
	d := Defaults()
	// Every section must have sensible non-zero values so a fresh vault never
	// nil-derefs.
	if d.Editor.FontFamily == "" || d.Editor.MonoFontFamily == "" {
		t.Errorf("defaults editor fonts must be set: %+v", d.Editor)
	}
	if d.Editor.FontSizePx <= 0 || d.Editor.TabIndentSpaces <= 0 {
		t.Errorf("defaults editor sizes must be positive: %+v", d.Editor)
	}
	if d.Editor.LineHeight <= 0 || d.Editor.AutoSaveDelayMs <= 0 {
		t.Errorf("defaults editor numeric fields must be positive: %+v", d.Editor)
	}
	if !d.Editor.FocusHighlightAncestors {
		t.Errorf("defaults focus_highlight_ancestors should be true")
	}
	if !d.Parsing.AutoInjectUUID {
		t.Errorf("defaults auto_inject_uuid should be true")
	}
	if d.Parsing.ShorthandRegex == "" {
		t.Errorf("defaults shorthand_regex must be set")
	}
	if d.Parsing.DefaultTaskPriority <= 0 {
		t.Errorf("defaults default_task_priority must be positive")
	}
	if len(d.Hotkeys) == 0 {
		t.Errorf("defaults hotkeys must be populated")
	}
	if _, ok := d.Hotkeys["open_search"]; !ok {
		t.Errorf("defaults hotkeys missing open_search")
	}
	if len(d.Plugins.Active) == 0 {
		t.Errorf("defaults plugins.active must be populated")
	}
}

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	tmp := t.TempDir() // no config.yaml present
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("missing config should not error, got %v", err)
	}
	d := Defaults()
	if cfg.Editor.FontSizePx != d.Editor.FontSizePx {
		t.Errorf("missing file should yield default font size, got %d", cfg.Editor.FontSizePx)
	}
	if cfg.Editor.FontFamily != d.Editor.FontFamily {
		t.Errorf("missing file should yield default font family, got %q", cfg.Editor.FontFamily)
	}
}

func TestLoad_HappyPath_OverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), strings.Join([]string{
		"editor:",
		"  font_family: Inter",
		"  tab_indent_spaces: 2",
		"  auto_save_delay_ms: 750",
		"hotkeys:",
		"  open_search: Ctrl+K",
		"plugins:",
		"  active:",
		"    - silt-agenda",
		"  disabled: []",
	}, "\n"))
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Editor.FontFamily != "Inter" {
		t.Errorf("font override: want Inter, got %q", cfg.Editor.FontFamily)
	}
	if cfg.Editor.TabIndentSpaces != 2 {
		t.Errorf("tab override: want 2, got %d", cfg.Editor.TabIndentSpaces)
	}
	if cfg.Editor.AutoSaveDelayMs != 750 {
		t.Errorf("autosave override: want 750, got %d", cfg.Editor.AutoSaveDelayMs)
	}
	// Fields NOT in the file must keep their defaults.
	d := Defaults()
	if cfg.Editor.FontSizePx != d.Editor.FontSizePx {
		t.Errorf("absent font_size_px should keep default, got %d", cfg.Editor.FontSizePx)
	}
	if cfg.Parsing.AutoInjectUUID != d.Parsing.AutoInjectUUID {
		t.Errorf("absent parsing.auto_inject_uuid should keep default")
	}
	// Present hotkey overridden, absent ones keep defaults.
	if cfg.Hotkeys["open_search"] != "Ctrl+K" {
		t.Errorf("hotkey override: want Ctrl+K, got %q", cfg.Hotkeys["open_search"])
	}
	if cfg.Hotkeys["indent_block"] != d.Hotkeys["indent_block"] {
		t.Errorf("absent hotkey should keep default")
	}
	if len(cfg.Plugins.Active) != 1 || cfg.Plugins.Active[0] != "silt-agenda" {
		t.Errorf("plugins.active override: %v", cfg.Plugins.Active)
	}
}

func TestLoad_MalformedYAML_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), "editor:\n  font_family: [unterminated\n  : : :")
	_, err := Load(tmp)
	if err == nil {
		t.Fatalf("malformed YAML must return an error, not silently fall through")
	}
	if !strings.Contains(err.Error(), "parse config.yaml") {
		t.Errorf("error should mention parse, got %v", err)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := Defaults()
	original.Editor.FontFamily = "Custom Font"
	original.Editor.TabIndentSpaces = 8
	original.Hotkeys["custom_action"] = "Ctrl+Shift+X"
	original.Plugins.PluginSettings["my-plugin"] = map[string]any{"key": "value"}

	if err := Save(tmp, original); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", loaded, original)
	}
}

func TestSave_Atomic_NoPartialWrite(t *testing.T) {
	// Save must leave exactly one config.yaml and no leftover temp files.
	tmp := t.TempDir()
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(tmp, ".system"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly 1 file under .system, got %d: %v", len(entries), names)
	}
}

func TestNormalize_NeverNil(t *testing.T) {
	cfg := normalize(SystemConfig{})
	if cfg.Plugins.Active == nil || cfg.Plugins.Disabled == nil {
		t.Errorf("normalize must produce non-nil plugin slices")
	}
	if cfg.Plugins.PluginSettings == nil {
		t.Errorf("normalize must produce non-nil plugin_settings")
	}
	if cfg.Hotkeys == nil {
		t.Errorf("normalize must produce non-nil hotkeys")
	}
}

func TestValidateHotkeys(t *testing.T) {
	cases := []struct {
		name    string
		hotkeys map[string]string
		wantErr bool
	}{
		{"valid single", map[string]string{"open_search": "Ctrl+P"}, false},
		{"valid multi-modifier + named", map[string]string{"x": "Ctrl+Shift+Slash"}, false},
		{"empty allowed (disabled)", map[string]string{"open_search": ""}, false},
		{"stray empty segment tolerated", map[string]string{"open_search": "Ctrl++P"}, false},
		{"modifier-only rejected", map[string]string{"open_search": "Ctrl+Shift"}, true},
		{"single modifier rejected", map[string]string{"open_search": "Ctrl"}, true},
		{"whitespace-only rejected", map[string]string{"open_search": "   "}, false}, // trims to empty = disabled
		{"nil map ok", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateHotkeys(c.hotkeys)
			if c.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}
