package plugins

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeZip builds a zip archive at dest from a map of path→content.
func writeZip(t *testing.T, dest string, files map[string]string) {
	t.Helper()
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

func manifestJSON(id, name, version string) string {
	b, _ := json.Marshal(map[string]string{
		"id":      id,
		"name":    name,
		"version": version,
		"main":    "index.js",
	})
	return string(b)
}

func TestValidateAndInstall_HappyPath(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins"), 0o755)
	archive := filepath.Join(t.TempDir(), "good.silt-plugin")
	writeZip(t, archive, map[string]string{
		"plugin.json": manifestJSON("my-plugin", "My Plugin", "1.2.0"),
		"index.js":    "export default { manifest: { id: 'my-plugin' } };\n",
	})

	m, warns, err := Validate(archive)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if m.ID != "my-plugin" || m.Name != "My Plugin" || m.Version != "1.2.0" {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}

	installed, err := Install(vault, archive)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if installed.ID != "my-plugin" {
		t.Errorf("expected installed id my-plugin, got %s", installed.ID)
	}
	// Files extracted.
	if _, err := os.Stat(filepath.Join(vault, ".system", "plugins", "my-plugin", "index.js")); err != nil {
		t.Errorf("index.js not installed: %v", err)
	}
}

func TestValidate_RejectsBadArchives(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins"), 0o755)

	tests := []struct {
		name  string
		files map[string]string
	}{
		{"missing manifest", map[string]string{"index.js": "x"}},
		{"bad id uppercase", map[string]string{"plugin.json": manifestJSON("MyPlugin", "x", "1"), "index.js": "x"}},
		{"missing main", map[string]string{"plugin.json": manifestJSON("ok", "x", "1")}},
		{"zip-slip", map[string]string{
			"plugin.json": manifestJSON("slip", "x", "1"),
			"index.js":    "x",
			"../evil.txt": "pwned",
		}},
		{"absolute path", map[string]string{
			"plugin.json":  manifestJSON("abs", "x", "1"),
			"index.js":     "x",
			"/etc/evil":    "pwned",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.silt-plugin")
			writeZip(t, archive, tc.files)
			if _, _, err := Validate(archive); err == nil {
				t.Errorf("expected Validate to reject %s", tc.name)
			}
			if _, err := Install(vault, archive); err == nil {
				t.Errorf("expected Install to reject %s", tc.name)
			}
		})
	}
}

func TestInstall_RefusesDuplicate(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins"), 0o755)
	archive := filepath.Join(t.TempDir(), "dup.silt-plugin")
	writeZip(t, archive, map[string]string{
		"plugin.json": manifestJSON("dup", "Dup", "1.0.0"),
		"index.js":    "x",
	})

	if _, err := Install(vault, archive); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := Install(vault, archive); err == nil {
		t.Errorf("expected duplicate install to be refused")
	}
}

func TestUninstall_RemovesAndRejectsTraversal(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins"), 0o755)
	archive := filepath.Join(t.TempDir(), "u.silt-plugin")
	writeZip(t, archive, map[string]string{
		"plugin.json": manifestJSON("removable", "Removable", "1.0.0"),
		"index.js":    "x",
	})
	if _, err := Install(vault, archive); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := Uninstall(vault, "removable"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vault, ".system", "plugins", "removable")); !os.IsNotExist(err) {
		t.Errorf("expected plugin dir removed")
	}

	if err := Uninstall(vault, "../../escape"); err == nil {
		t.Errorf("expected traversal id rejected")
	}
}

func TestEnableDisable_SentinelToggle(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins", "toggleable"), 0o755)

	if IsDisabled(filepath.Join(vault, ".system", "plugins", "toggleable")) {
		t.Errorf("expected not disabled initially")
	}
	if err := SetDisabled(vault, "toggleable", true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if !IsDisabled(filepath.Join(vault, ".system", "plugins", "toggleable")) {
		t.Errorf("expected disabled after SetDisabled(true)")
	}
	if err := SetDisabled(vault, "toggleable", false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if IsDisabled(filepath.Join(vault, ".system", "plugins", "toggleable")) {
		t.Errorf("expected enabled after SetDisabled(false)")
	}
}

func TestUninstall_RejectsDotSegmentAndTraversal(t *testing.T) {
	vault := t.TempDir()
	_ = os.MkdirAll(filepath.Join(vault, ".system", "plugins"), 0o755)

	// "..." must NOT resolve to "." (which would wipe the entire plugins dir).
	for _, evil := range []string{"...", ".", "", "..", "../escape", "/etc"} {
		if err := Uninstall(vault, evil); err == nil {
			t.Errorf("expected Uninstall(%q) to be rejected", evil)
		}
	}
}

func TestValidate_RejectsCustomMain(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "custom.silt-plugin")
	// manifest with a non-index.js main
	customMain, _ := json.Marshal(map[string]string{
		"id":   "custom",
		"name": "Custom",
		"main": "foo.js",
	})
	writeZip(t, archive, map[string]string{
		"plugin.json": string(customMain),
		"foo.js":      "x",
	})
	if _, _, err := Validate(archive); err == nil {
		t.Errorf("expected Validate to reject a manifest.main other than index.js")
	}
}

func TestValidate_AcceptsEmptyMain(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "ok.silt-plugin")
	writeZip(t, archive, map[string]string{
		"plugin.json": manifestJSON("ok", "Ok", "1.0.0"),
		"index.js":    "x",
	})
	if _, _, err := Validate(archive); err != nil {
		t.Errorf("expected Validate to accept an empty main (defaults to index.js): %v", err)
	}
}
