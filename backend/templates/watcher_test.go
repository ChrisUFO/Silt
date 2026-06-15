package templates

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeFileBytes(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeFile(path string) {
	_ = os.Remove(path)
}

// TestTemplateWatcher_AddModifyDelete drives a real fsnotify instance (per
// #58's guidance: "drive a real fsnotify instance in tests rather than
// mocking") and verifies the onChange callback fires on external file changes
// in the templates directory. Uses a buffered channel + generous timeout to
// avoid flakiness on slow CI.
func TestTemplateWatcher_AddModifyDelete(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	// Pre-create the templates dir so the watcher observes it directly (avoids
	// the race where file writes inside templates/ land before the parent-
	// watcher detects and adds the templates/ directory).
	writeTemplate(t, tplDir, ".gitkeep", "")

	var mu sync.Mutex
	var callCount int
	changed := make(chan struct{}, 16)

	w, err := NewTemplateWatcher(tplDir, func() {
		mu.Lock()
		callCount++
		mu.Unlock()
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewTemplateWatcher: %v", err)
	}
	w.Start()
	defer w.Close()

	// Give the watcher a moment to settle after Start.
	time.Sleep(100 * time.Millisecond)

	// Add a file.
	waitFor(t, changed, "add file", func() {
		writeTemplate(t, tplDir, "test.md", validUserTemplate)
	})

	// Modify the file.
	waitFor(t, changed, "modify file", func() {
		path := filepath.Join(tplDir, "test.md")
		if err := writeFileBytes(path, validUserTemplate+"\nextra\n"); err != nil {
			t.Fatalf("modify: %v", err)
		}
	})

	// Delete the file.
	waitFor(t, changed, "delete file", func() {
		path := filepath.Join(tplDir, "test.md")
		removeFile(path)
	})

	mu.Lock()
	if callCount < 3 {
		t.Errorf("expected at least 3 onChange calls (add/modify/delete), got %d", callCount)
	}
	mu.Unlock()
}

func TestTemplateWatcher_SelfWriteSuppressed(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	writeTemplate(t, tplDir, "test.md", validUserTemplate)

	changed := make(chan struct{}, 8)
	w, err := NewTemplateWatcher(tplDir, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewTemplateWatcher: %v", err)
	}
	w.Start()
	defer w.Close()

	// Arm the self-write window, then write. The event should be suppressed.
	w.RegisterSelfWrite()
	path := filepath.Join(tplDir, "test.md")
	if err := writeFileBytes(path, validUserTemplate+"\nself\n"); err != nil {
		t.Fatalf("self-write: %v", err)
	}

	// Wait a bit to confirm NO callback fires for the self-write.
	select {
	case <-changed:
		t.Error("self-write should be suppressed, but callback fired")
	case <-time.After(700 * time.Millisecond):
		// Good — no callback within the window + buffer.
	}
}

func TestTemplateWatcher_ParentWatchPath(t *testing.T) {
	// When templates/ doesn't exist, the watcher observes the parent. It
	// should still function once templates/ is created.
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")

	changed := make(chan struct{}, 8)
	w, err := NewTemplateWatcher(tplDir, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewTemplateWatcher (parent path): %v", err)
	}
	w.Start()
	defer w.Close()

	// Give the parent watcher time to register.
	time.Sleep(100 * time.Millisecond)

	// Create the templates dir + add a file. The parent watcher sees the
	// templates/ dir creation, adds it, then sees the file.
	os.MkdirAll(tplDir, 0o755)
	time.Sleep(200 * time.Millisecond) // let the parent → templates/ Add land
	waitFor(t, changed, "add file after parent-created templates dir", func() {
		writeTemplate(t, tplDir, "test.md", validUserTemplate)
	})
}

func TestTemplateWatcher_IgnoresNonMD(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	writeTemplate(t, tplDir, ".gitkeep", "")

	changed := make(chan struct{}, 8)
	w, err := NewTemplateWatcher(tplDir, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewTemplateWatcher: %v", err)
	}
	w.Start()
	defer w.Close()
	time.Sleep(100 * time.Millisecond)

	// A .txt file should NOT trigger the callback.
	writeTemplate(t, tplDir, "readme.txt", "not a template")
	select {
	case <-changed:
		t.Error("non-.md file should not trigger callback")
	case <-time.After(500 * time.Millisecond):
		// Good — no callback for .txt.
	}
}

func TestTemplateWatcher_Close(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "templates")
	w, err := NewTemplateWatcher(tplDir, func() {})
	if err != nil {
		t.Fatalf("NewTemplateWatcher: %v", err)
	}
	w.Start()
	// Close should not hang or panic.
	if err := w.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Double-close is safe (stopOnce guards).
	_ = w.Close()
}

// waitFor performs an action that triggers a file-system change, then waits
// up to 2s for the watcher's onChange callback to fire.
func waitFor(t *testing.T, ch <-chan struct{}, label string, action func()) {
	t.Helper()
	action()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not fire onChange for: %s", label)
	}
}
