package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// selfWriteWindow is how long after RegisterSelfWrite the watcher treats
// config.yaml events as self-generated. It must cover a full logical save
// (atomic temp + rename, or a truncate + write), which can emit SEVERAL
// fsnotify events for config.yaml — the window suppresses all of them, not
// just the first (a one-shot suppression leaks the 2nd+ event as a spurious
// reload).
const selfWriteWindow = 500 * time.Millisecond

// ConfigWatcher hot-reloads <vault>/.system/config.yaml. Self-loop prevention
// is a local time-window: SaveSystemConfig calls RegisterSelfWrite() before
// its atomic save, and the watcher ignores every config.yaml event until the
// window elapses, so Silt's own multi-event write cannot feed back into
// itself. External edits (e.g. editing config.yaml in a text editor) re-parse
// and invoke onChange without a restart, exactly as SPECS.md §9.2 requires.
type ConfigWatcher struct {
	vaultPath string
	path      string

	watcher *fsnotify.Watcher

	onChange func(SystemConfig) // invoked with the re-parsed config on success
	onError  func(error)        // invoked when a reload fails to parse

	selfMu    sync.Mutex
	selfUntil time.Time // suppress reloads until this time after RegisterSelfWrite

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewConfigWatcher creates (but does not start) a watcher for the vault's
// config.yaml. onChange/onError are invoked from the watcher goroutine and
// must be safe to call concurrently. The watcher observes the .system parent
// directory (not the file alone) so delete-then-recreate of config.yaml is
// still observed.
func NewConfigWatcher(vaultPath string, onChange func(SystemConfig), onError func(error)) (*ConfigWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}
	path := ConfigPath(vaultPath)
	watchDir := filepath.Dir(path)
	// The parent (.system) is created by vault scaffolding; if it is genuinely
	// missing there is nothing to watch and hot-reload is disabled.
	if _, statErr := os.Stat(watchDir); statErr == nil {
		if err := fw.Add(watchDir); err != nil {
			fw.Close()
			return nil, fmt.Errorf("watch %s: %w", watchDir, err)
		}
	}

	return &ConfigWatcher{
		vaultPath: vaultPath,
		path:      path,
		watcher:   fw,
		onChange:  onChange,
		onError:   onError,
		stopCh:    make(chan struct{}),
	}, nil
}

// Start launches the background event loop.
func (cw *ConfigWatcher) Start() {
	cw.wg.Add(1)
	go cw.loop()
}

// RegisterSelfWrite records that Silt is about to write config.yaml itself, so
// the resulting fsnotify event(s) are treated as self-generated and ignored
// for selfWriteWindow. Must be called immediately before the write.
func (cw *ConfigWatcher) RegisterSelfWrite() {
	cw.selfMu.Lock()
	cw.selfUntil = time.Now().Add(selfWriteWindow)
	cw.selfMu.Unlock()
}

// isSelfWrite reports whether we are still inside a self-write suppression
// window opened by RegisterSelfWrite.
func (cw *ConfigWatcher) isSelfWrite() bool {
	cw.selfMu.Lock()
	defer cw.selfMu.Unlock()
	return time.Now().Before(cw.selfUntil)
}

// Close stops the loop and closes the fsnotify watcher. Safe to call multiple
// times.
func (cw *ConfigWatcher) Close() error {
	cw.stopOnce.Do(func() { close(cw.stopCh) })
	err := cw.watcher.Close()
	cw.wg.Wait()
	return err
}

func (cw *ConfigWatcher) loop() {
	defer cw.wg.Done()
	for {
		select {
		case <-cw.stopCh:
			return
		case ev, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			// Only react to our config file; the .system dir has other entries.
			// Compare by base name so path-representation differences (e.g.
			// macOS /var vs /private/var symlink resolution in temp dirs) don't
			// cause events to be missed. Only direct children of .system are
			// reported (fsnotify is non-recursive), so this is unambiguous.
			if filepath.Base(ev.Name) != "config.yaml" {
				continue
			}
			// Ignore every event Silt just produced itself. A single logical
			// save emits multiple fsnotify events for config.yaml (atomic
			// temp+rename, or truncate+write); the self-write window suppresses
			// all of them so our own save can't feed back into a reload.
			if cw.isSelfWrite() {
				continue
			}
			// A rename/remove is expected during atomic save (temp + rename);
			// the subsequent create/write carries the real reload. Recreate
			// (delete + create externally) is caught here too.
			if ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				cw.reload()
			}
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			if cw.onError != nil {
				cw.onError(err)
			}
		}
	}
}

// reload re-reads and re-parses config.yaml, dispatching to onChange or onError.
func (cw *ConfigWatcher) reload() {
	cfg, err := Load(cw.vaultPath)
	if err != nil {
		if cw.onError != nil {
			cw.onError(fmt.Errorf("config reload failed: %w", err))
		}
		return
	}
	if cw.onChange != nil {
		cw.onChange(cfg)
	}
}
