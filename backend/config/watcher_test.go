package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigWatcher_ExternalWrite_TriggersReload(t *testing.T) {
	tmp := t.TempDir()
	// Seed a config so there is something to edit.
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	changed := make(chan SystemConfig, 4)
	errs := make(chan error, 4)
	cw, err := NewConfigWatcher(tmp, func(c SystemConfig) { changed <- c }, func(e error) { errs <- e })
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()

	// External edit: change tab width and write directly (simulating another
	// editor). Small delay so the watcher is definitely listening.
	time.Sleep(150 * time.Millisecond)
	cfg := Defaults()
	cfg.Editor.TabIndentSpaces = 7
	if err := os.WriteFile(ConfigPath(tmp), mustMarshal(t, cfg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case got := <-changed:
		if got.Editor.TabIndentSpaces != 7 {
			t.Errorf("reloaded config should reflect external edit; got tab=%d", got.Editor.TabIndentSpaces)
		}
	case e := <-errs:
		t.Fatalf("expected onChange, got onError: %v", e)
	case <-time.After(3 * time.Second):
		t.Fatalf("external write did not trigger reload")
	}
}

func TestConfigWatcher_SelfWrite_IsIgnored(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	changed := make(chan SystemConfig, 4)
	cw, err := NewConfigWatcher(tmp, func(c SystemConfig) { changed <- c }, nil)
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()

	time.Sleep(150 * time.Millisecond)
	// Simulate Silt's own save: register then write directly.
	cw.RegisterSelfWrite()
	cfg := Defaults()
	cfg.Editor.FontSizePx = 99
	if err := os.WriteFile(ConfigPath(tmp), mustMarshal(t, cfg), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Give the watcher a chance to (not) react.
	select {
	case <-changed:
		t.Fatalf("self-write should be ignored, but onChange fired")
	case <-time.After(700 * time.Millisecond):
		// expected: no reload within the cooldown window
	}
}

func TestConfigWatcher_MalformedWrite_TriggersError(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	errs := make(chan error, 4)
	cw, err := NewConfigWatcher(tmp, nil, func(e error) { errs <- e })
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()

	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(ConfigPath(tmp), []byte("editor:\n  font_family: [unterminated\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	select {
	case <-errs:
		// expected
	case <-time.After(3 * time.Second):
		t.Fatalf("malformed write should trigger onError")
	}
}

func TestConfigWatcher_CloseIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	cw, err := NewConfigWatcher(tmp, nil, nil)
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	cw.Start()
	if err := cw.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := cw.Close(); err != nil { // must not panic
		t.Fatalf("second Close: %v", err)
	}
}

func TestConfigWatcher_MissingSystemDir_NoError(t *testing.T) {
	// A fresh temp dir has no .system/. The watcher must construct without
	// error (hot-reload is simply inert until the dir exists).
	tmp := t.TempDir()
	cw, err := NewConfigWatcher(tmp, nil, nil)
	if err != nil {
		t.Fatalf("NewConfigWatcher on missing .system should not error: %v", err)
	}
	cw.Start()
	if err := cw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// mustMarshal writes a real config via Save and re-reads the bytes, so the
// on-disk encoding is exactly what Load expects (avoids re-deriving YAML).
func mustMarshal(t *testing.T, cfg SystemConfig) []byte {
	t.Helper()
	tmp := t.TempDir()
	if err := Save(tmp, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tmp, ".system", "config.yaml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

// TestConfigWatcher_ExternalAtomicRename probes whether replacing config.yaml
// via temp-file + rename (how most editors and Silt's own Save do atomic
// writes) actually triggers a reload. On Linux, rename-over-existing emits
// IN_MOVED_TO (fsnotify.Rename), so a Create|Write-only mask would miss it.
func TestConfigWatcher_ExternalAtomicRename(t *testing.T) {
	tmp := t.TempDir()
	if err := Save(tmp, Defaults()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	changed := make(chan SystemConfig, 4)
	errs := make(chan error, 4)
	cw, err := NewConfigWatcher(tmp, func(c SystemConfig) { changed <- c }, func(e error) { errs <- e })
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()

	time.Sleep(150 * time.Millisecond)
	// External atomic edit: write a sibling temp file in .system, then rename
	// it over config.yaml. No RegisterSelfWrite (this is an external editor).
	cfg := Defaults()
	cfg.Editor.TabIndentSpaces = 6
	tmpFile := filepath.Join(tmp, ".system", ".editor-cfg.tmp")
	if err := os.WriteFile(tmpFile, mustMarshal(t, cfg), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Rename(tmpFile, ConfigPath(tmp)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	select {
	case got := <-changed:
		if got.Editor.TabIndentSpaces != 6 {
			t.Errorf("atomic rename should reload with new value; got tab=%d", got.Editor.TabIndentSpaces)
		}
	case e := <-errs:
		t.Fatalf("expected onChange for atomic rename, got onError: %v", e)
	case <-time.After(3 * time.Second):
		t.Fatalf("atomic rename-over of config.yaml did NOT trigger reload (Create|Write mask misses fsnotify.Rename)")
	}
}
