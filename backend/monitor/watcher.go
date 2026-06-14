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

// DefaultFocusLeaseTTL is how long a focus lease stays valid without a refresh.
// Picked well above any reasonable editor pause (typing/thinking) and the
// auto-save debounce (500ms) so a normally-focused editor never lets its lease
// lapse, while an editor that crashed / unmounted without releasing self-heals
// within a minute (#38).
const DefaultFocusLeaseTTL = 60 * time.Second

type DirectoryWatcher struct {
	watcher      *fsnotify.Watcher
	vaultPath    string
	dm           *db.DatabaseManager
	tracker      *WriteTracker
	coordinator  *core.ExecutionCoordinator
	spacesPerTab int
	closeChan    chan struct{}

	failedMu    sync.Mutex
	failedPaths []string

	// Focus suppression uses TTL leases instead of a plain boolean (#38). The
	// Svelte editor acquires on focus, refreshes via a heartbeat while focused,
	// and releases on blur. If the component unmounts without releasing (route
	// change, crash, hot-reload) the lease expires and the background sweeper
	// drops it, so fsnotify suppression self-heals instead of leaking forever.
	focusMu     sync.RWMutex
	focusLeases map[string]time.Time // path -> lease expiry
	focusTTL    time.Duration
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
		focusLeases:  make(map[string]time.Time),
		focusTTL:     DefaultFocusLeaseTTL,
	}, nil
}

// LockFocus acquires (or re-acquires) a focus lease for path, suppressing
// fsnotify-driven reindexes while the editor is focused on the file.
func (dw *DirectoryWatcher) LockFocus(path string) {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	if dw.focusLeases == nil {
		dw.focusLeases = make(map[string]time.Time)
	}
	dw.focusLeases[filepath.Clean(path)] = time.Now().Add(dw.focusTTL)
}

// RefreshFocus extends an existing lease. Called by the Svelte editor's
// heartbeat while it stays focused, and on save. A no-op if there is no lease
// OR the lease already expired (an expired-but-not-yet-reaped entry is treated
// as gone, so a late heartbeat can't resurrect suppression — the editor must
// re-Acquire). This matches IsFocusLocked's expiry semantics.
func (dw *DirectoryWatcher) RefreshFocus(path string) {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	if dw.focusLeases == nil {
		return
	}
	key := filepath.Clean(path)
	expiry, ok := dw.focusLeases[key]
	if !ok {
		return
	}
	if !time.Now().Before(expiry) {
		// Expired: reap it now rather than refresh, so the editor can't
		// silently hold suppression past a crash/unmount.
		delete(dw.focusLeases, key)
		return
	}
	dw.focusLeases[key] = time.Now().Add(dw.focusTTL)
}

func (dw *DirectoryWatcher) UnlockFocus(path string) {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	if dw.focusLeases != nil {
		delete(dw.focusLeases, filepath.Clean(path))
	}
}

func (dw *DirectoryWatcher) IsFocusLocked(path string) bool {
	dw.focusMu.RLock()
	defer dw.focusMu.RUnlock()
	if dw.focusLeases == nil {
		return false
	}
	expiry, ok := dw.focusLeases[filepath.Clean(path)]
	if !ok {
		return false
	}
	// An expired lease reads as unlocked; the sweeper reaps it shortly. This
	// keeps IsFocusLocked correct even between sweeper ticks.
	return time.Now().Before(expiry)
}

// ReleaseAllFocus clears every outstanding focus lease. Called on shutdown so
// a clean exit can't strand a file under suppression, and on CloseVault.
func (dw *DirectoryWatcher) ReleaseAllFocus() {
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	dw.focusLeases = make(map[string]time.Time)
}

// startLeaseSweeper runs a background goroutine that drops expired focus
// leases every ttl/2 so a crashed/unmounted editor self-heals. Stopped by
// closeChan (closed in Close).
func (dw *DirectoryWatcher) startLeaseSweeper() {
	interval := dw.focusTTL / 2
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				dw.sweepExpiredLeases()
			case <-dw.closeChan:
				return
			}
		}
	}()
}

func (dw *DirectoryWatcher) sweepExpiredLeases() {
	now := time.Now()
	dw.focusMu.Lock()
	defer dw.focusMu.Unlock()
	for path, expiry := range dw.focusLeases {
		if !now.Before(expiry) {
			delete(dw.focusLeases, path)
		}
	}
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
		} else if d.Type()&fs.ModeSymlink != 0 {
			// Explicit symlink skip: WalkDir already does not follow them, but
			// we short-circuit here so a symlinked directory is never added to
			// the watch set (#32 — matches the scanner's WalkMarkdown).
			return nil
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
	dw.startLeaseSweeper()
	return nil
}

// resolveFileMetadata derives (notebook, section, page, date) for a markdown
// file from its path relative to the vault root, mirroring the scanner:
// notebook = top folder, page = the folder containing the file, section =
// the path between them ("" when the page is directly under the notebook).
// Files too shallow to resolve return empty notebook/section/page so callers
// can skip them rather than indexing under empty strings.
func (dw *DirectoryWatcher) resolveFileMetadata(path string) (notebook, section, page, dateStr string) {
	relPath, err := filepath.Rel(dw.vaultPath, path)
	if err != nil {
		return "", "", "", time.Now().Format("2006-01-02")
	}

	relPathClean := filepath.ToSlash(relPath)
	parts := strings.Split(relPathClean, "/")
	filename := parts[len(parts)-1]
	ancestors := parts[:len(parts)-1]

	if len(ancestors) >= 2 {
		notebook = ancestors[0]
		page = ancestors[len(ancestors)-1]
		if len(ancestors) > 2 {
			section = strings.Join(ancestors[1:len(ancestors)-1], "/")
		}
	} else {
		notebook, section, page = "", "", ""
	}

	dateStr = ""
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

	return notebook, section, page, dateStr
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
			// Evict the per-file IO mutex so ioMu doesn't grow linearly with
			// the cumulative set of distinct paths ever touched (#30). Safe
			// against an in-flight LockFileWrite via the generation check.
			dw.coordinator.ReleaseFileMutex(path)
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

		notebook, section, page, dateStr := dw.resolveFileMetadata(path)
		// Skip files that do not map to a notebook/section/page (e.g. living
		// too shallow in the vault). They are surfaced as init-warnings on
		// the full scan; here we just ignore them.
		if notebook == "" {
			return
		}
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return
		}

		blocks, meta, newContent, modified, err := parser.ParseFileContent(string(contentBytes), notebook, section, page, dateStr, dw.spacesPerTab)
		if err != nil {
			return
		}

		if modified {
			dw.tracker.RegisterWrite(path)
			_ = parser.WriteFileAtomic(path, []byte(newContent))
		}

		dw.coordinator.WithDBWrite(func() {
			if err := dw.dm.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, blocks, meta.Tags, meta.Warnings...); err != nil {
				log.Printf("reindexFile: IndexFileBlocks failed for %s: %v", path, err)
				return
			}
			// Keep the files table warm during the session: a successful
			// reindex records the file's current mtime/size so the next
			// *startup* scan can skip it (#29).
			if st, err := os.Stat(path); err == nil {
				if err := dw.dm.MarkFileIndexed(nil, path, st.ModTime().UnixNano(), st.Size()); err != nil {
					log.Printf("reindexFile: MarkFileIndexed failed for %s: %v", path, err)
				}
			}
		})
	})
}

func (dw *DirectoryWatcher) clearIndexForFile(path string) {
	notebook, section, page, dateStr := dw.resolveFileMetadata(path)
	if notebook == "" {
		return
	}
	// Serialize the DB deletion through the coordinator, matching reindexFile
	// and all other DB-touching paths. Without this, a concurrent file event
	// can race an in-flight query and produce database-locked errors.
	dw.coordinator.WithDBWrite(func() {
		_ = dw.dm.ClearFileBlocks(nil, notebook, section, page, dateStr)
		// Drop the files row so a future startup scan doesn't think the
		// deleted/renamed file is still "unchanged" and skip re-indexing the
		// new occupant of that path.
		_ = dw.dm.ForgetFile(path)
	})
}
