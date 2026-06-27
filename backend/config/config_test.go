package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

// TestSave_RestrictiveFilePermissions pins the F7 hardening: config.yaml is
// written 0o600 and its .system/ parent 0o700 so a co-tenant on a multi-user
// host cannot read the plugin grant table / linked-notebook paths / settings.
func TestSave_RestrictiveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	vault := t.TempDir()
	if err := Save(vault, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cfgInfo, err := os.Stat(ConfigPath(vault))
	if err != nil {
		t.Fatalf("stat config.yaml: %v", err)
	}
	if got := cfgInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("config.yaml perm = %o, want 0o600", got)
	}
	sysInfo, err := os.Stat(filepath.Join(vault, ".system"))
	if err != nil {
		t.Fatalf("stat .system: %v", err)
	}
	if got := sysInfo.Mode().Perm(); got != 0o700 {
		t.Errorf(".system perm = %o, want 0o700", got)
	}
}

// TestLoad_RejectsOversizeConfig pins F12: an oversized config.yaml is rejected
// at read time without unbounded allocation ahead of yaml.Unmarshal.
func TestLoad_RejectsOversizeConfig(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, ".system"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(vault), make([]byte, maxConfigYAMLBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(vault)
	if err == nil {
		t.Fatal("expected oversize config.yaml to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds the") {
		t.Errorf("error %q must mention the byte cap", err.Error())
	}
}

// TestLoadLinked_RejectsOversizeConfig pins F12 for the co-located linked
// notebook config.yaml override layer.
func TestLoadLinked_RejectsOversizeConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".system"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LinkedConfigPath(root), make([]byte, maxConfigYAMLBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLinked(root)
	if err == nil {
		t.Fatal("expected oversize linked config.yaml to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds the") {
		t.Errorf("error %q must mention the byte cap", err.Error())
	}
}

// TestLoad_LegacyShorthandRegexIgnored pins F11: the user-editable
// shorthand_regex is removed (it was dead config — the parser uses fixed
// package-level regexes, never this field). A synced vault carrying a
// catastrophic-backtracking regex such as ^(a+)+$ must load cleanly with the
// value silently dropped (yaml.v3 ignores unknown keys), so it can never
// reach the indexer.
func TestLoad_LegacyShorthandRegexIgnored(t *testing.T) {
	vault := t.TempDir()
	hostile := "parsing:\n  auto_inject_uuid: true\n  default_task_priority: 3\n  shorthand_regex: \"^(a+)+$\"\n"
	writeFile(t, ConfigPath(vault), hostile)
	cfg, err := Load(vault)
	if err != nil {
		t.Fatalf("config with legacy shorthand_regex should load: %v", err)
	}
	if want := Defaults().Parsing; cfg.Parsing != want {
		t.Errorf("legacy shorthand_regex should be dropped; Parsing = %+v, want %+v", cfg.Parsing, want)
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

// --- #133: co-located per-notebook config ---

// TestLinkedConfigPath confirms the co-located path lives at
// <linkedRoot>/.system/config.yaml, mirroring the vault config layout so the
// same per-notebook-attached-state contract holds on both roots.
func TestLinkedConfigPath(t *testing.T) {
	got := LinkedConfigPath("/mnt/share/Ext")
	want := filepath.ToSlash(filepath.Join("/mnt/share/Ext", ".system", "config.yaml"))
	if filepath.ToSlash(got) != want {
		t.Errorf("LinkedConfigPath = %q, want %q", got, want)
	}
}

// TestLoadLinked_MissingFileReturnsDefaults verifies the normal case: a
// linked notebook WITHOUT a co-located config.yaml is not an error — the
// vault-scoped config.yaml provides the baseline, and Defaults fills any gap.
func TestLoadLinked_MissingFileReturnsDefaults(t *testing.T) {
	tmp := t.TempDir() // no .system/config.yaml
	cfg, err := LoadLinked(tmp)
	if err != nil {
		t.Fatalf("missing co-located file should not error, got %v", err)
	}
	// Defaults() shape — the default active notebook proves we got the
	// canonical defaults rather than a zero struct.
	if cfg.Notebooks.DefaultActive != Defaults().Notebooks.DefaultActive {
		t.Errorf("expected Defaults(), got DefaultActive=%q", cfg.Notebooks.DefaultActive)
	}
	if cfg.Plugins.PluginSettings == nil {
		t.Error("expected non-nil PluginSettings from Defaults()")
	}
}

// TestLoadLinked_ParsesAndOverrides confirms a present co-located config.yaml
// is parsed and its plugin_settings surface (the merge-with-vault happens at
// the App layer, not here — LoadLinked returns the parsed linked config
// verbatim).
func TestLoadLinked_ParsesAndOverrides(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, LinkedConfigPath(tmp), ""+
		"plugins:\n"+
		"  plugin_settings:\n"+
		"    silt-kanban:\n"+
		"      columns: [Backlog, In Progress, Done]\n"+
		"      theme: dark\n")
	cfg, err := LoadLinked(tmp)
	if err != nil {
		t.Fatalf("LoadLinked: %v", err)
	}
	kanban, ok := cfg.Plugins.PluginSettings["silt-kanban"].(map[string]any)
	if !ok {
		t.Fatalf("expected silt-kanban settings map, got %T", cfg.Plugins.PluginSettings["silt-kanban"])
	}
	if kanban["theme"] != "dark" {
		t.Errorf("theme override: got %v, want dark", kanban["theme"])
	}
	cols, ok := kanban["columns"].([]any)
	if !ok || len(cols) != 3 {
		t.Errorf("columns override: got %v", kanban["columns"])
	}
}

// TestLoadLinked_UnparseableReturnsError locks the fail-loud contract: a
// present-but-broken co-located config must NOT silently fall through to
// Defaults (that would hide a user's broken file from them).
func TestLoadLinked_UnparseableReturnsError(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, LinkedConfigPath(tmp), "plugins:\n  plugin_settings: [unterminated\n  : : :")
	_, err := LoadLinked(tmp)
	if err == nil {
		t.Fatalf("unparseable co-located config must return an error")
	}
	if !strings.Contains(err.Error(), "parse linked config.yaml") {
		t.Errorf("error should mention parse, got %v", err)
	}
}

// TestMergePluginSettings_LinkedOverridesVaultPerKey covers the merge contract:
// linked keys win per-key; nested maps merge recursively; scalars and arrays
// from linked REPLACE vault's; vault-only keys survive; neither input is
// mutated.
func TestMergePluginSettings_LinkedOverridesVaultPerKey(t *testing.T) {
	vault := map[string]any{
		"columns": []any{"TODO", "DOING", "DONE"},
		"filters": map[string]any{
			"owners":     []any{"Alice"},
			"priorities": []any{1, 2},
		},
		"vault_only": "keep",
	}
	linked := map[string]any{
		"columns": []any{"Backlog", "Done"},
		"filters": map[string]any{
			"priorities": []any{3},
			"tags":       []any{"work"},
		},
		"linked_only": "add",
	}

	// Snapshot inputs to prove MergePluginSettings does not mutate them.
	vaultBefore := deepCopy(vault)
	linkedBefore := deepCopy(linked)

	got := MergePluginSettings(vault, linked)

	// Scalar/array: linked replaces vault.
	if cols, ok := got["columns"].([]any); !ok || len(cols) != 2 || cols[0] != "Backlog" {
		t.Errorf("columns: expected linked to replace vault, got %v", got["columns"])
	}
	// Nested map: recursive per-key merge.
	filters, ok := got["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters missing or wrong type: %T", got["filters"])
	}
	// vault-only sub-key preserved.
	if owners, ok := filters["owners"].([]any); !ok || len(owners) != 1 || owners[0] != "Alice" {
		t.Errorf("filters.owners: expected vault preserved, got %v", filters["owners"])
	}
	// linked sub-key replaces vault's (array replacement, same as top-level).
	// reflect.DeepEqual verifies both value AND type (yaml.v3 decodes
	// integer literals as `int`, not int64 — but the type must match exactly).
	if !reflect.DeepEqual(filters["priorities"], []any{3}) {
		t.Errorf("filters.priorities: expected linked to replace vault with [3], got %v", filters["priorities"])
	}
	// linked-only sub-key added.
	if tags, ok := filters["tags"].([]any); !ok || len(tags) != 1 || tags[0] != "work" {
		t.Errorf("filters.tags: expected linked-only addition, got %v", filters["tags"])
	}
	// vault-only top-level key preserved.
	if got["vault_only"] != "keep" {
		t.Errorf("vault_only: expected preserved, got %v", got["vault_only"])
	}
	// linked-only top-level key added.
	if got["linked_only"] != "add" {
		t.Errorf("linked_only: expected added, got %v", got["linked_only"])
	}

	// Inputs not mutated.
	if !reflect.DeepEqual(vault, vaultBefore) {
		t.Errorf("MergePluginSettings mutated vault input:\n before=%v\n after =%v", vaultBefore, vault)
	}
	if !reflect.DeepEqual(linked, linkedBefore) {
		t.Errorf("MergePluginSettings mutated linked input:\n before=%v\n after =%v", linkedBefore, linked)
	}
}

// TestMergePluginSettings_NilInputsAreEmpty confirms both nil inputs are
// tolerated and the result is always a non-nil map.
func TestMergePluginSettings_NilInputsAreEmpty(t *testing.T) {
	got := MergePluginSettings(nil, nil)
	if got == nil {
		t.Fatal("expected non-nil result for nil inputs")
	}
	if len(got) != 0 {
		t.Errorf("expected empty merge of two nils, got %v", got)
	}

	got = MergePluginSettings(map[string]any{"a": 1}, nil)
	if got["a"] != 1 {
		t.Errorf("vault-only merge lost key, got %v", got)
	}
	got = MergePluginSettings(nil, map[string]any{"b": 2})
	if got["b"] != 2 {
		t.Errorf("linked-only merge lost key, got %v", got)
	}
}

// deepCopy is a test-only helper that clones a map[string]any snapshot for
// mutation-comparison. It does not need to handle every YAML type — only the
// types used in the merge tests above.
func deepCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case map[string]any:
			out[k] = deepCopy(x)
		case []any:
			cp := make([]any, len(x))
			copy(cp, x)
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}

// --- #142: open-tab persistence config ---

// TestDefaults_TabsConfig verifies the tab-strip defaults ship in Defaults():
// enable_preview_tabs=true, max_open_tabs=8, next_tab/prev_tab/close_tab
// hotkeys present, and OpenTabs is a non-nil empty slice (not nil) so JSON
// serialization over IPC never yields null.
func TestDefaults_TabsConfig(t *testing.T) {
	d := Defaults()
	if d.UI.EnablePreviewTabs == nil || *d.UI.EnablePreviewTabs != true {
		t.Errorf("defaults enable_preview_tabs should be *true, got %v", d.UI.EnablePreviewTabs)
	}
	if d.UI.MaxOpenTabs != 8 {
		t.Errorf("defaults max_open_tabs should be 8, got %d", d.UI.MaxOpenTabs)
	}
	if d.UI.OpenTabs == nil {
		t.Errorf("defaults open_tabs should be non-nil empty slice, got nil")
	}
	if len(d.UI.OpenTabs) != 0 {
		t.Errorf("defaults open_tabs should be empty, got %v", d.UI.OpenTabs)
	}
	for _, key := range []string{"next_tab", "prev_tab", "close_tab"} {
		if _, ok := d.Hotkeys[key]; !ok {
			t.Errorf("defaults hotkeys missing %q", key)
		}
	}
	if d.Hotkeys["next_tab"] != "Ctrl+Tab" {
		t.Errorf("next_tab default: got %q", d.Hotkeys["next_tab"])
	}
	if d.Hotkeys["prev_tab"] != "Ctrl+Shift+Tab" {
		t.Errorf("prev_tab default: got %q", d.Hotkeys["prev_tab"])
	}
	if d.Hotkeys["close_tab"] != "Ctrl+W" {
		t.Errorf("close_tab default: got %q", d.Hotkeys["close_tab"])
	}
}

// TestOpenTabs_RoundTrip confirms OpenTabs + ActiveTab survive Save → Load
// with byte-for-byte fidelity, including the section-less case (Section == "").
func TestOpenTabs_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := Defaults()
	previewOff := false
	original.UI.EnablePreviewTabs = &previewOff
	original.UI.MaxOpenTabs = 12
	original.UI.OpenTabs = []TabRef{
		{Notebook: "Work", Section: "Projects", Page: "Site"},
		{Notebook: "Work", Section: "", Page: "Top"},
		{Notebook: "Personal", Section: "Journal", Page: "Daily"},
	}
	original.UI.ActiveTab = &TabRef{Notebook: "Work", Section: "Projects", Page: "Site"}

	if err := Save(tmp, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.UI.OpenTabs, original.UI.OpenTabs) {
		t.Errorf("open_tabs round-trip:\n got  %+v\n want %+v", loaded.UI.OpenTabs, original.UI.OpenTabs)
	}
	if loaded.UI.ActiveTab == nil || !reflect.DeepEqual(*loaded.UI.ActiveTab, *original.UI.ActiveTab) {
		t.Errorf("active_tab round-trip:\n got  %+v\n want %+v", loaded.UI.ActiveTab, original.UI.ActiveTab)
	}
	if loaded.UI.EnablePreviewTabs == nil || *loaded.UI.EnablePreviewTabs != false {
		t.Errorf("enable_preview_tabs=false round-trip: got %v", loaded.UI.EnablePreviewTabs)
	}
	if loaded.UI.MaxOpenTabs != 12 {
		t.Errorf("max_open_tabs round-trip: got %d, want 12", loaded.UI.MaxOpenTabs)
	}
}

// TestLoad_LegacyConfigMissingTabFields verifies a config.yaml authored
// before #142 (no ui.open_tabs / enable_preview_tabs / max_open_tabs keys)
// loads cleanly with the new fields filled from Defaults — backward compat.
func TestLoad_LegacyConfigMissingTabFields(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), strings.Join([]string{
		"editor:",
		"  font_family: Inter",
		"ui:",
		"  sidebar_width: 280",
	}, "\n"))
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("legacy config Load: %v", err)
	}
	// The pre-existing ui.sidebar_width override is honored.
	if cfg.UI.SidebarWidth != 280 {
		t.Errorf("sidebar_width override lost: got %d", cfg.UI.SidebarWidth)
	}
	// The new fields default-in cleanly (not zero-value).
	if cfg.UI.OpenTabs == nil || len(cfg.UI.OpenTabs) != 0 {
		t.Errorf("legacy open_tabs should default to empty non-nil slice, got %v", cfg.UI.OpenTabs)
	}
	if cfg.UI.EnablePreviewTabs == nil || *cfg.UI.EnablePreviewTabs != true {
		t.Errorf("legacy enable_preview_tabs should default to *true, got %v", cfg.UI.EnablePreviewTabs)
	}
	if cfg.UI.MaxOpenTabs != 8 {
		t.Errorf("legacy max_open_tabs should default to 8, got %d", cfg.UI.MaxOpenTabs)
	}
	if cfg.UI.ActiveTab != nil {
		t.Errorf("legacy active_tab should default to nil, got %+v", *cfg.UI.ActiveTab)
	}
}

// TestLoad_MalformedOpenTabsEntryNotFatal confirms a malformed open_tabs
// entry does NOT abort the entire config load — yaml.v3 decodes a
// missing-field entry as an empty TabRef, which the App-layer GetOpenTabs
// prunes against ListNavigation. A parse-level error is still raised for
// genuinely broken YAML (covered by TestLoad_MalformedYAML_ReturnsError).
func TestLoad_MalformedOpenTabsEntryNotFatal(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), strings.Join([]string{
		"ui:",
		"  open_tabs:",
		"    - notebook: Work",
		"      section: Projects",
		"      page: Site",
		"    - notebook: Personal",
		"      # page missing — decodes as empty string, pruned later",
	}, "\n"))
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("malformed open_tabs entry should not be fatal, got %v", err)
	}
	if len(cfg.UI.OpenTabs) != 2 {
		t.Fatalf("expected 2 open_tabs entries (1 valid, 1 partial), got %d", len(cfg.UI.OpenTabs))
	}
	// The partial entry decodes with an empty Page; the App-layer
	// GetOpenTabs prunes it against ListNavigation.
	if cfg.UI.OpenTabs[1].Page != "" {
		t.Errorf("partial entry page should be empty string, got %q", cfg.UI.OpenTabs[1].Page)
	}
}

// TestNormalize_MaxOpenTabsClamp confirms MaxOpenTabs of 0 or negative
// (legacy/invalid) is normalized to the default 8, while positive values
// pass through untouched (including 1 and very large values).
func TestNormalize_MaxOpenTabsClamp(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 8},     // legacy missing key → default
		{-1, 8},    // invalid negative → default
		{1, 1},     // minimum valid
		{8, 8},     // the default itself
		{20, 20},   // user-configured large value honored
		{32, 32},   // upper bound
		{33, 32},   // clamped to upper bound
		{1000, 32}, // absurdly large → clamped (#142 hardening)
	}
	for _, c := range cases {
		cfg := normalize(SystemConfig{UI: UIConfig{MaxOpenTabs: c.in}})
		if cfg.UI.MaxOpenTabs != c.want {
			t.Errorf("normalize MaxOpenTabs %d: got %d, want %d", c.in, cfg.UI.MaxOpenTabs, c.want)
		}
	}
}

// TestNormalize_EnablePreviewTabsNilBecomesTrue confirms the *bool field is
// normalized to *true when nil (so the frontend reads a stable default),
// while an explicit false survives the normalize pass unchanged.
func TestNormalize_EnablePreviewTabsNilBecomesTrue(t *testing.T) {
	// nil → *true
	cfg := normalize(SystemConfig{})
	if cfg.UI.EnablePreviewTabs == nil || *cfg.UI.EnablePreviewTabs != true {
		t.Errorf("normalize nil → *true, got %v", cfg.UI.EnablePreviewTabs)
	}
	// explicit false survives
	f := false
	cfg = normalize(SystemConfig{UI: UIConfig{EnablePreviewTabs: &f}})
	if cfg.UI.EnablePreviewTabs == nil || *cfg.UI.EnablePreviewTabs != false {
		t.Errorf("normalize should preserve explicit false, got %v", cfg.UI.EnablePreviewTabs)
	}
}

// TestDefaults_FormattingConfig confirms the #168 formatting config fields and
// hotkeys have correct defaults.
func TestDefaults_FormattingConfig(t *testing.T) {
	d := Defaults()
	if d.UI.ShowFormatToolbar == nil || *d.UI.ShowFormatToolbar != true {
		t.Errorf("defaults show_format_toolbar should be *true, got %v", d.UI.ShowFormatToolbar)
	}
	if d.UI.DismissedTips == nil {
		t.Errorf("defaults dismissed_tips should be non-nil empty slice")
	}
	if len(d.UI.DismissedTips) != 0 {
		t.Errorf("defaults dismissed_tips should be empty, got %v", d.UI.DismissedTips)
	}
	for _, key := range []string{
		"format_bold", "format_italic", "format_underline", "format_strike",
		"format_code", "format_link", "format_highlight",
		"format_subscript", "format_superscript",
	} {
		if _, ok := d.Hotkeys[key]; !ok {
			t.Errorf("defaults hotkeys missing %q", key)
		}
	}
	// Heading level hotkeys (#169).
	for _, key := range []string{"set_h1", "set_h2", "set_h3", "set_note", "set_task"} {
		if _, ok := d.Hotkeys[key]; !ok {
			t.Errorf("defaults hotkeys missing %q", key)
		}
	}
	if d.Hotkeys["format_bold"] != "Ctrl+B" {
		t.Errorf("format_bold default: got %q", d.Hotkeys["format_bold"])
	}
	if d.Hotkeys["format_italic"] != "Ctrl+I" {
		t.Errorf("format_italic default: got %q", d.Hotkeys["format_italic"])
	}
	if d.Hotkeys["format_subscript"] != "Ctrl+," {
		t.Errorf("format_subscript default: got %q", d.Hotkeys["format_subscript"])
	}
	// Alignment (#173) + blockquote (#188) hotkeys.
	for _, key := range []string{
		"align_left", "align_center", "align_right", "align_justify", "toggle_quote",
	} {
		if _, ok := d.Hotkeys[key]; !ok {
			t.Errorf("defaults hotkeys missing %q", key)
		}
	}
	if d.Hotkeys["toggle_quote"] != "Ctrl+Shift+9" {
		t.Errorf("toggle_quote default: got %q", d.Hotkeys["toggle_quote"])
	}
	if d.Hotkeys["toggle_details"] != "Ctrl+Shift+." {
		t.Errorf("toggle_details default: got %q", d.Hotkeys["toggle_details"])
	}
	// Table row/column insert hotkeys (#172).
	for _, key := range []string{
		"table_insert_row_above", "table_insert_row_below",
		"table_insert_col_left", "table_insert_col_right",
	} {
		if _, ok := d.Hotkeys[key]; !ok {
			t.Errorf("defaults hotkeys missing %q", key)
		}
	}
}

// TestFormattingConfig_RoundTrip confirms ShowFormatToolbar + DismissedTips
// survive Save → Load with byte-for-byte fidelity.
func TestFormattingConfig_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := Defaults()
	toolbarOff := false
	original.UI.ShowFormatToolbar = &toolbarOff
	original.UI.DismissedTips = []string{"formatting_tip_v1", "other_tip"}

	if err := Save(tmp, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.UI.ShowFormatToolbar == nil || *loaded.UI.ShowFormatToolbar != false {
		t.Errorf("show_format_toolbar=false round-trip: got %v", loaded.UI.ShowFormatToolbar)
	}
	if !reflect.DeepEqual(loaded.UI.DismissedTips, original.UI.DismissedTips) {
		t.Errorf("dismissed_tips round-trip:\n got  %+v\n want %+v", loaded.UI.DismissedTips, original.UI.DismissedTips)
	}
}

// TestNormalize_ShowFormatToolbarNilBecomesTrue confirms the *bool field is
// normalized to *true when nil (so the frontend reads a stable default).
func TestNormalize_ShowFormatToolbarNilBecomesTrue(t *testing.T) {
	cfg := normalize(SystemConfig{})
	if cfg.UI.ShowFormatToolbar == nil || *cfg.UI.ShowFormatToolbar != true {
		t.Errorf("normalize nil → *true, got %v", cfg.UI.ShowFormatToolbar)
	}
	f := false
	cfg = normalize(SystemConfig{UI: UIConfig{ShowFormatToolbar: &f}})
	if cfg.UI.ShowFormatToolbar == nil || *cfg.UI.ShowFormatToolbar != false {
		t.Errorf("normalize should preserve explicit false, got %v", cfg.UI.ShowFormatToolbar)
	}
	if cfg.UI.DismissedTips == nil {
		t.Errorf("normalize should ensure non-nil dismissed_tips")
	}
}

// TestDefaults_EditorEnhancements confirms the Phase 3 editor enhancement
// config fields have correct defaults.
func TestDefaults_EditorEnhancements(t *testing.T) {
	d := Defaults()
	if d.UI.Formatting.TypographyEnabled == nil || *d.UI.Formatting.TypographyEnabled != true {
		t.Errorf("defaults typography_enabled should be *true, got %v", d.UI.Formatting.TypographyEnabled)
	}
	if d.Editor.ShowWordCount == nil || *d.Editor.ShowWordCount != false {
		t.Errorf("defaults show_word_count should be *false, got %v", d.Editor.ShowWordCount)
	}
	if d.Editor.FocusMode == nil || *d.Editor.FocusMode != false {
		t.Errorf("defaults focus_mode should be *false, got %v", d.Editor.FocusMode)
	}
}

// TestNormalize_EditorEnhancements confirms *bool normalization for the
// Phase 3 fields.
func TestNormalize_EditorEnhancements(t *testing.T) {
	// nil → defaults
	cfg := normalize(SystemConfig{})
	if cfg.UI.Formatting.TypographyEnabled == nil || *cfg.UI.Formatting.TypographyEnabled != true {
		t.Errorf("normalize typography nil → *true, got %v", cfg.UI.Formatting.TypographyEnabled)
	}
	if cfg.Editor.ShowWordCount == nil || *cfg.Editor.ShowWordCount != false {
		t.Errorf("normalize show_word_count nil → *false, got %v", cfg.Editor.ShowWordCount)
	}
	if cfg.Editor.FocusMode == nil || *cfg.Editor.FocusMode != false {
		t.Errorf("normalize focus_mode nil → *false, got %v", cfg.Editor.FocusMode)
	}
	// Explicit values survive
	tv := true
	cfg = normalize(SystemConfig{UI: UIConfig{Formatting: FormattingConfig{TypographyEnabled: &tv}}})
	if cfg.UI.Formatting.TypographyEnabled == nil || *cfg.UI.Formatting.TypographyEnabled != true {
		t.Errorf("normalize should preserve explicit typography true, got %v", cfg.UI.Formatting.TypographyEnabled)
	}
	fv := false
	cfg = normalize(SystemConfig{UI: UIConfig{Formatting: FormattingConfig{TypographyEnabled: &fv}}})
	if cfg.UI.Formatting.TypographyEnabled == nil || *cfg.UI.Formatting.TypographyEnabled != false {
		t.Errorf("normalize should preserve explicit typography false, got %v", cfg.UI.Formatting.TypographyEnabled)
	}
}

// TestDefaults_ShowTabDirtyIndicators confirms the #167 tab dirty indicator
// toggle defaults to *true.
func TestDefaults_ShowTabDirtyIndicators(t *testing.T) {
	d := Defaults()
	if d.UI.ShowTabDirtyIndicators == nil || *d.UI.ShowTabDirtyIndicators != true {
		t.Errorf("defaults show_tab_dirty_indicators should be *true, got %v", d.UI.ShowTabDirtyIndicators)
	}
}

// TestNormalize_ShowTabDirtyIndicatorsNilBecomesTrue confirms nil normalizes
// to *true (legacy config without the key) while explicit false is preserved.
func TestNormalize_ShowTabDirtyIndicatorsNilBecomesTrue(t *testing.T) {
	// nil → *true
	cfg := normalize(SystemConfig{})
	if cfg.UI.ShowTabDirtyIndicators == nil || *cfg.UI.ShowTabDirtyIndicators != true {
		t.Errorf("normalize nil → *true, got %v", cfg.UI.ShowTabDirtyIndicators)
	}
	// explicit false preserved
	f := false
	cfg = normalize(SystemConfig{UI: UIConfig{ShowTabDirtyIndicators: &f}})
	if cfg.UI.ShowTabDirtyIndicators == nil || *cfg.UI.ShowTabDirtyIndicators != false {
		t.Errorf("normalize should preserve explicit false, got %v", cfg.UI.ShowTabDirtyIndicators)
	}
	// explicit true preserved
	tv := true
	cfg = normalize(SystemConfig{UI: UIConfig{ShowTabDirtyIndicators: &tv}})
	if cfg.UI.ShowTabDirtyIndicators == nil || *cfg.UI.ShowTabDirtyIndicators != true {
		t.Errorf("normalize should preserve explicit true, got %v", cfg.UI.ShowTabDirtyIndicators)
	}
}

// TestShowTabDirtyIndicators_RoundTrip confirms YAML round-trip for the
// #167 toggle (true, false, and legacy-missing-key paths).
func TestShowTabDirtyIndicators_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := Defaults()
	off := false
	original.UI.ShowTabDirtyIndicators = &off

	if err := Save(tmp, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.UI.ShowTabDirtyIndicators == nil || *loaded.UI.ShowTabDirtyIndicators != false {
		t.Errorf("show_tab_dirty_indicators=false round-trip: got %v", loaded.UI.ShowTabDirtyIndicators)
	}
}

// TestLoad_LegacyConfigMissingShowTabDirtyIndicators verifies a config.yaml
// authored before #167 (no ui.show_tab_dirty_indicators key) loads cleanly
// with the field defaulted to *true — backward compat.
func TestLoad_LegacyConfigMissingShowTabDirtyIndicators(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), strings.Join([]string{
		"editor:",
		"  font_family: Inter",
		"ui:",
		"  sidebar_width: 280",
	}, "\n"))
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("legacy config Load: %v", err)
	}
	if cfg.UI.ShowTabDirtyIndicators == nil || *cfg.UI.ShowTabDirtyIndicators != true {
		t.Errorf("legacy show_tab_dirty_indicators should default to *true, got %v", cfg.UI.ShowTabDirtyIndicators)
	}
}

// --- #195: per-tab view mode persistence (TabRef.ViewMode) ---

// TestTabRef_ViewMode_RoundTrip confirms a Source-mode tab persists
// view_mode across Save → Load, while an Edit-mode tab stays the zero value
// (the frontend writes the field only when Source, keeping config.yaml lean).
func TestTabRef_ViewMode_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	original := Defaults()
	original.UI.OpenTabs = []TabRef{
		{Notebook: "Work", Section: "Projects", Page: "Site", ViewMode: "source"},
		{Notebook: "Work", Section: "", Page: "Top"}, // Edit (default) → omitted on disk
	}
	original.UI.ActiveTab = &TabRef{Notebook: "Work", Section: "Projects", Page: "Site", ViewMode: "source"}

	if err := Save(tmp, original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.UI.OpenTabs, original.UI.OpenTabs) {
		t.Errorf("open_tabs view_mode round-trip:\n got  %+v\n want %+v", loaded.UI.OpenTabs, original.UI.OpenTabs)
	}
	if loaded.UI.ActiveTab == nil || loaded.UI.ActiveTab.ViewMode != "source" {
		t.Errorf("active_tab view_mode round-trip: got %+v", loaded.UI.ActiveTab)
	}
}

// TestNormalize_TabRefViewModeSanitize confirms normalize collapses any
// non-"source" value (including hand-edited garbage) to "" (the Edit default),
// while "source" survives. This is the storage-side defense: the frontend
// also reads non-"source" as Edit, but normalize keeps config.yaml clean so a
// corrupted entry can't persist a bogus string.
func TestNormalize_TabRefViewModeSanitize(t *testing.T) {
	cfg := normalize(SystemConfig{UI: UIConfig{OpenTabs: []TabRef{
		{Notebook: "A", Page: "p", ViewMode: "source"},
		{Notebook: "B", Page: "p", ViewMode: ""},
		{Notebook: "C", Page: "p", ViewMode: "edit"},
		{Notebook: "D", Page: "p", ViewMode: "garbage"},
	}}})
	got := cfg.UI.OpenTabs
	if got[0].ViewMode != "source" {
		t.Errorf("source should survive, got %q", got[0].ViewMode)
	}
	if got[1].ViewMode != "" {
		t.Errorf("empty should stay empty (Edit default), got %q", got[1].ViewMode)
	}
	if got[2].ViewMode != "" {
		t.Errorf("edit should collapse to empty, got %q", got[2].ViewMode)
	}
	if got[3].ViewMode != "" {
		t.Errorf("garbage should collapse to empty, got %q", got[3].ViewMode)
	}
}

// TestLoad_LegacyOpenTabsMissingViewMode verifies a config.yaml authored
// before #195 (open_tabs entries without view_mode) loads cleanly with each
// entry's ViewMode as the zero value — backward compat.
func TestLoad_LegacyOpenTabsMissingViewMode(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, ConfigPath(tmp), strings.Join([]string{
		"ui:",
		"  open_tabs:",
		"    - notebook: Work",
		"      section: Projects",
		"      page: Site",
	}, "\n"))
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("legacy config Load: %v", err)
	}
	if len(cfg.UI.OpenTabs) != 1 {
		t.Fatalf("expected 1 open_tabs entry, got %d", len(cfg.UI.OpenTabs))
	}
	if cfg.UI.OpenTabs[0].ViewMode != "" {
		t.Errorf("legacy open_tabs entry view_mode should default to empty (Edit), got %q", cfg.UI.OpenTabs[0].ViewMode)
	}
}
