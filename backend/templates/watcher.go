package templates

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// selfWriteWindow is how long after RegisterSelfWrite the watcher treats
// template events as self-generated. It must cover a full logical save (atomic
// temp + rename), which can emit several fsnotify events — the window
// suppresses all of them, not just the first. Mirrors config.ConfigWatcher.
const selfWriteWindow = 500 * time.Millisecond

// reloadDebounce coalesces a burst of fsnotify events into a single reload, so
// an atomic save (temp + rename → multiple events) triggers one onChange
// callback rather than several. Mirrors config.ConfigWatcher's debounce.
const reloadDebounce = 120 * time.Millisecond

// TemplateWatcher observes <vault>/.system/templates/ for external add/modify/
// delete of user templates and invokes onChange (which the App wires to
// ReloadTemplates + a templates:changed event) so the picker stays live without
// a restart — the same hot-reload posture as the config and theme engines.
//
// Self-loop prevention is a local time-window: the App calls RegisterSelfWrite
// before SaveTemplate's atomic write, and the watcher ignores template events
// for the window so Silt's own multi-event save cannot feed back into a reload.
//
// The watcher observes the templatesDir directly when it exists. When it does
// not yet exist (no user templates saved), the watcher observes the .system
// parent so the eventual creation of templates/ is detected and added to the
// watch — mirroring how ConfigWatcher watches the .system parent to survive a
// delete+recreate of config.yaml.
type TemplateWatcher struct {
	templatesDir string
	parentDir    string

	watcher *fsnotify.Watcher

	onChange func() // invoked (from the watcher goroutine) after a settled external change

	selfMu    sync.Mutex
	selfUntil time.Time // suppress reloads until this time after RegisterSelfWrite

	watchingDir bool // whether templatesDir has been added to the fsnotify watch

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewTemplateWatcher creates (but does not start) a watcher for the vault's
// templates directory. onChange is invoked from the watcher goroutine and must
// be safe to call concurrently. The watcher observes the templatesDir if it
// exists, otherwise the .system parent (so creation of templates/ is caught).
func NewTemplateWatcher(templatesDir string, onChange func()) (*TemplateWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	parent := filepath.Dir(templatesDir)
	w := &TemplateWatcher{
		templatesDir: templatesDir,
		parentDir:    parent,
		watcher:      fw,
		onChange:     onChange,
		stopCh:       make(chan struct{}),
	}
	// Observe whichever of templatesDir / parent currently exists. Both may be
	// absent on a fresh vault (no user templates yet, and .system not created
	// until first config save) — in that case the watcher starts idle and the
	// App re-creates it once the vault is initialized.
	watchingDir := false
	if _, statErr := os.Stat(templatesDir); statErr == nil {
		if err := fw.Add(templatesDir); err != nil {
			fw.Close()
			return nil, fmt.Errorf("watch %s: %w", templatesDir, err)
		}
		watchingDir = true
	} else if _, statErr := os.Stat(parent); statErr == nil {
		if err := fw.Add(parent); err != nil {
			fw.Close()
			return nil, fmt.Errorf("watch %s: %w", parent, err)
		}
	}
	w.watchingDir = watchingDir
	return w, nil
}

// Start launches the background event loop.
func (w *TemplateWatcher) Start() {
	w.wg.Add(1)
	go w.loop()
}

// RegisterSelfWrite records that Silt is about to write a template itself, so
// the resulting fsnotify event(s) are treated as self-generated and ignored for
// selfWriteWindow. Must be called immediately before the write. Mirrors
// config.ConfigWatcher.RegisterSelfWrite.
func (w *TemplateWatcher) RegisterSelfWrite() {
	w.selfMu.Lock()
	w.selfUntil = time.Now().Add(selfWriteWindow)
	w.selfMu.Unlock()
}

func (w *TemplateWatcher) isSelfWrite() bool {
	w.selfMu.Lock()
	defer w.selfMu.Unlock()
	return time.Now().Before(w.selfUntil)
}

// Close stops the loop and closes the fsnotify watcher. Safe to call multiple
// times (stopOnce guarantees a single close of stopCh).
func (w *TemplateWatcher) Close() error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	err := w.watcher.Close()
	w.wg.Wait()
	return err
}

// isTemplateFile reports whether an event path is a .md file directly inside
// templatesDir (the only events the watcher reacts to). Path comparison uses
// EqualFold because case-insensitive filesystems (Windows, macOS) can report
// the same path with different casing (e.g. C:\\ vs c:\\).
func (w *TemplateWatcher) isTemplateFile(name string) bool {
	if !strings.EqualFold(filepath.Ext(name), ".md") {
		return false
	}
	return strings.EqualFold(filepath.Clean(filepath.Dir(name)), filepath.Clean(w.templatesDir))
}

func (w *TemplateWatcher) loop() {
	defer w.wg.Done()
	var debounce <-chan time.Time
	for {
		select {
		case <-w.stopCh:
			return
		case ev, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// Detect the creation of the templates directory itself (seen via
			// the parent watch) and add it to the fsnotify watcher so future
			// file-level events are observed.
			if !w.watchingDir && strings.EqualFold(filepath.Clean(ev.Name), filepath.Clean(w.templatesDir)) {
				if ev.Op&(fsnotify.Create) != 0 {
					if err := w.watcher.Add(w.templatesDir); err == nil {
						w.watchingDir = true
					}
				}
			}
			// Only react to .md files directly inside templatesDir.
			if !w.isTemplateFile(ev.Name) {
				continue
			}
			// Ignore events Silt just produced itself (atomic temp+rename can
			// emit several; the window suppresses all of them).
			if w.isSelfWrite() {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			debounce = time.After(reloadDebounce)
		case <-debounce:
			debounce = nil
			if w.onChange != nil {
				w.onChange()
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			// Log so a persistent fsnotify failure (e.g. inotify watch limit
			// reached on Linux) is diagnosable rather than silently disabling
			// hot-reload. The watcher is still best-effort — a failing watcher
			// just means the picker needs a manual refresh.
			if err != nil {
				log.Printf("templates: watcher error: %v", err)
			}
		}
	}
}
