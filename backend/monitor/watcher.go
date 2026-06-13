package monitor

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"silt/backend/core"
	"silt/backend/db"
	"silt/backend/parser"

	"github.com/fsnotify/fsnotify"
)

type DirectoryWatcher struct {
	watcher      *fsnotify.Watcher
	vaultPath    string
	dm           *db.DatabaseManager
	tracker      *WriteTracker
	coordinator  *core.ExecutionCoordinator
	spacesPerTab int
	closeChan    chan struct{}

	failedMu   sync.Mutex
	failedPaths []string

	focusMu    sync.RWMutex
	focusLocks map[string]bool
}

func NewDirectoryWatcher(vaultPath string, dm *db.DatabaseManager, tracker *WriteTracker, coordinator *core.ExecutionCoordinator, spacesPerTab int) (*DirectoryWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	return &DirectoryWatcher{
		watcher:      watcher,
		vaultPath:    vaultPath,
		dm:           dm,
		tracker:      tracker,
		coordinator:  coordinator,
		spacesPerTab: spacesPerTab,
		closeChan:    make(chan struct{}),
		focusLocks:   make(map[string]bool),
	}, nil
}

func (dw *DirectoryWatcher) LockFocus(path string) {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	if dw.focusLocks == nil {
		dw.focusLocks = make(map[string]bool)
	}
	dw.focusLocks[filepath.Clean(path)] = true
}

func (dw *DirectoryWatcher) UnlockFocus(path string) {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	if dw.focusLocks != nil {
		delete(dw.focusLocks, filepath.Clean(path))
	}
}

func (dw *DirectoryWatcher) IsFocusLocked(path string) bool {
	dw.focusMu.RLock()
	defer dw.focusMu.RUnlock()
	if dw.focusLocks == nil {
		return false
	}
	return dw.focusLocks[filepath.Clean(path)]
}

func (dw *DirectoryWatcher) Close() error {
	close(dw.closeChan)
	return dw.watcher.Close()
}

func (dw *DirectoryWatcher) AddRecursive(path string) error {
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip system and hidden directories
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if err := dw.watcher.Add(p); err != nil {
				dw.failedMu.Lock()
				dw.failedPaths = append(dw.failedPaths, fmt.Sprintf("%s: %v", p, err))
				dw.failedMu.Unlock()
				return fmt.Errorf("failed to add path to watcher %s: %w", p, err)
			}
		}
		return nil
	})
}

// FailedPaths returns a copy of the list of paths that the watcher could
// not subscribe to (fsnotify limits, permissions, removed during
// traversal, etc.). A non-empty slice means these subtrees are not being
// monitored.
func (dw *DirectoryWatcher) FailedPaths() []string {
	dw.failedMu.Lock()
	defer dw.failedMu.Unlock()
	return append([]string(nil), dw.failedPaths...)
}

func (dw *DirectoryWatcher) Start() error {
	if err := dw.AddRecursive(dw.vaultPath); err != nil {
		return err
	}

	go dw.listenLoop()
	return nil
}

func (dw *DirectoryWatcher) resolveFileMetadata(path string) (string, string, string) {
	relPath, err := filepath.Rel(dw.vaultPath, path)
	if err != nil {
		return "General", "General", time.Now().Format("2006-01-02")
	}

	relPathClean := filepath.ToSlash(relPath)
	parts := strings.Split(relPathClean, "/")

	notebook := "General"
	section := "General"
	filename := parts[len(parts)-1]

	if len(parts) >= 3 {
		notebook = parts[0]
		section = strings.Join(parts[1:len(parts)-1], "/")
	} else if len(parts) == 2 {
		notebook = parts[0]
	}

	dateStr := ""
	if matches := parser.DateFileRegex.FindStringSubmatch(filename); len(matches) > 1 {
		dateStr = matches[1]
	} else {
		info, err := os.Stat(path)
		if err == nil {
			dateStr = info.ModTime().Format("2006-01-02")
		} else {
			dateStr = time.Now().Format("2006-01-02")
		}
	}

	return notebook, section, dateStr
}

func (dw *DirectoryWatcher) listenLoop() {
	for {
		select {
		case event, ok := <-dw.watcher.Events:
			if !ok {
				return
			}

			path := filepath.Clean(event.Name)

			// Check if directory
			info, err := os.Stat(path)
			isDir := false
			if err == nil {
				isDir = info.IsDir()
			}

			if isDir {
				// If new directory is created, watch it recursively
				if event.Has(fsnotify.Create) {
					_ = dw.AddRecursive(path)
				}
				continue
			}

			// Only process markdown files
			if strings.ToLower(filepath.Ext(path)) != ".md" {
				continue
			}

			// Ignore events if the file is focus-locked by Svelte editor
			if dw.IsFocusLocked(path) {
				continue
			}

			// Ignore self-generated writes
			if dw.tracker.IsSelfGenerated(path) {
				continue
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				dw.reindexFile(path)
			} else if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				dw.clearIndexForFile(path)
			}

		case err, ok := <-dw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("DirectoryWatcher error: %v", err)

		case <-dw.closeChan:
			return
		}
	}
}

func (dw *DirectoryWatcher) reindexFile(path string) {
	// Serialize the read/parse/write/index sequence against concurrent
	// app-driven file mutations (UpdateBlockState). Without this lock a
	// user-driven checkbox click could land between our initial read and
	// our eventual write, and the watcher's stale write would silently
	// clobber the user's change. The WriteTracker cooldown only covers
	// self-generated writes — it does not protect against genuine
	// external mutations racing the watcher.
	dw.coordinator.LockFileWrite(path, func() {
		if dw.IsFocusLocked(path) {
			return
		}

		notebook, section, dateStr := dw.resolveFileMetadata(path)
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return
		}

		blocks, meta, newContent, modified, err := parser.ParseFileContent(string(contentBytes), notebook, section, dateStr, dw.spacesPerTab)
		if err != nil {
			return
		}

		if modified {
			dw.tracker.RegisterWrite(path)
			_ = parser.WriteFileAtomic(path, []byte(newContent))
		}

		dw.coordinator.WithDBWrite(func() {
			if err := dw.dm.IndexFileBlocks(meta.Notebook, meta.Section, meta.Date, blocks, meta.Tags, meta.Warnings...); err != nil {
				log.Printf("reindexFile: IndexFileBlocks failed for %s: %v", path, err)
			}
		})
	})
}

func (dw *DirectoryWatcher) clearIndexForFile(path string) {
	notebook, section, dateStr := dw.resolveFileMetadata(path)
	_ = dw.dm.ClearFileBlocks(nil, notebook, section, dateStr)
}
