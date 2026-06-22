package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"silt/backend/config"
	"silt/backend/parser"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// CreateNotebook creates a top-level notebook folder under the vault root.
func (a *App) CreateNotebook(name string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	safeName := sanitizePathSegment(name)
	if safeName == "" {
		return fmt.Errorf("notebook name is required")
	}
	nbPath := filepath.Join(a.vaultPath, safeName)
	if !isPathWithinRoot(nbPath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(nbPath); err == nil {
		return fmt.Errorf("notebook %q already exists", safeName)
	}
	if a.nameCollidesWithLink(safeName, "") {
		return fmt.Errorf("a linked notebook named %q already exists; unlink or rename it first", safeName)
	}
	if err := os.MkdirAll(nbPath, 0755); err != nil {
		return fmt.Errorf("failed to create notebook: %w", err)
	}
	return nil
}

// OpenNotebook registers an existing notebook folder. The folder must live
// inside the vault root (the index is rebuilt from a single watched root);
// external notebooks are rejected explicitly rather than silently linked.
// Returns the notebook name (the folder's base name).
func (a *App) OpenNotebook(folderPath string) (string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return "", fmt.Errorf("invalid folder path: %w", err)
	}
	if !isPathWithinRoot(absPath, a.vaultPath) {
		return "", fmt.Errorf("notebooks must live inside the Silt vault")
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("folder not found: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("selected path is not a folder")
	}
	// The notebook is a top-level child of the vault root.
	rel, err := filepath.Rel(a.vaultPath, absPath)
	if err != nil {
		return "", err
	}
	relClean := filepath.ToSlash(rel)
	parts := strings.Split(relClean, "/")
	if len(parts) != 1 {
		return "", fmt.Errorf("a notebook must be a top-level folder in the vault (got %q)", relClean)
	}
	name := parts[0]
	if a.nameCollidesWithLink(name, "") {
		return "", fmt.Errorf("a linked notebook named %q already exists; unlink or rename it first", name)
	}
	return name, nil
}

// PickNotebookFolder opens the native folder picker and registers the chosen
// folder as a notebook. Returns the notebook name, or empty string if the user
// cancelled. Keeping the dialog on the Go side matches InitializeVault and
// avoids depending on frontend runtime dialog bindings.
func (a *App) PickNotebookFolder() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	selectedPath, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Open Notebook Folder",
	})
	if err != nil {
		return "", fmt.Errorf("failed to open folder picker: %w", err)
	}
	if selectedPath == "" {
		return "", nil // user cancelled
	}
	return a.OpenNotebook(selectedPath)
}

// --- Linked / external notebooks (#100) -------------------------------------
//
// A linked notebook is a folder OUTSIDE the vault (e.g. a synced SharePoint
// mount) registered into the vault so it can be browsed/searched/edited in
// place. Its markdown is NEVER copied into the vault — the external folder
// remains the source of truth. The link registry (config.yaml
// `linked_notebooks:`) is vault-scoped; the index rows carry source =
// 'linked:<id>' so same-named notebooks across roots cannot collide.

// LinkNotebook registers an external folder as a linked notebook: validates it,
// assigns a stable id, rejects collisions (with vault notebooks or existing
// links), persists the registry, watches the root, and indexes its tree. The
// external files (and any co-located <root>/.system/) are never modified.
func (a *App) LinkNotebook(folderPath string) (config.LinkedNotebook, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return config.LinkedNotebook{}, fmt.Errorf("vault not loaded")
	}
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return config.LinkedNotebook{}, fmt.Errorf("invalid folder path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return config.LinkedNotebook{}, fmt.Errorf("folder not found: %w", err)
	}
	if !info.IsDir() {
		return config.LinkedNotebook{}, fmt.Errorf("selected path is not a folder")
	}
	// A linked root must live OUTSIDE the vault (otherwise it's just an
	// in-vault notebook — use OpenNotebook). Refusing the vault prevents a
	// double-index (vault + linked) of the same tree.
	if isPathWithinRoot(absPath, a.vaultPath) {
		return config.LinkedNotebook{}, fmt.Errorf("that folder is already inside the vault — open it as a notebook instead of linking")
	}
	// Likewise refuse an ANCESTOR of the vault: the watcher would observe the
	// vault itself as part of the linked root and double-index it (#100).
	if isPathWithinRoot(a.vaultPath, absPath) {
		return config.LinkedNotebook{}, fmt.Errorf("cannot link a folder that contains the vault")
	}
	displayName := sanitizePathSegment(filepath.Base(absPath))
	if displayName == "" {
		return config.LinkedNotebook{}, fmt.Errorf("invalid folder name")
	}
	id := "linked-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
	ln := config.LinkedNotebook{ID: id, RootPath: filepath.Clean(absPath), DisplayName: displayName}

	// Reject display-name collisions: a vault notebook or an existing link with
	// the same name would be ambiguous in the sidebar and in (notebook, ...)
	// lookups (source disambiguates the index, but the UX must stay clear).
	if err := a.rejectLinkCollision(ln); err != nil {
		return config.LinkedNotebook{}, err
	}

	// Persist the registry atomically under configMu (self-write suppressed so
	// the watcher doesn't bounce it back as an external edit). configMu is held
	// across config.Save: cfg would otherwise share the LinkedNotebooks backing
	// array with a.cfg, so a concurrent Link/Unlink mutating the slice during
	// the YAML marshal would be a data race. Mirrors UpdatePluginSetting (#120).
	a.configMu.Lock()
	// Re-validate the uniqueness invariant under the WRITE lock: rejectLink
	// Collision ran with only an RLock and then released, so two concurrent
	// LinkNotebook calls for same-basename folders could both pass it and
	// double-register. nameCollidesWithLink is the authority under the lock.
	if existing, dup := a.linkByRecordLocked(ln); dup {
		a.configMu.Unlock()
		return config.LinkedNotebook{}, fmt.Errorf("a linked notebook with %q already exists", existing.DisplayName)
	}
	a.cfg.LinkedNotebooks = append(a.cfg.LinkedNotebooks, ln)
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	saveErr := config.Save(a.vaultPath, a.cfg)
	a.configMu.Unlock()
	if saveErr != nil {
		return config.LinkedNotebook{}, fmt.Errorf("failed to persist link registry: %w", saveErr)
	}

	// Watch the root so external edits re-index, then index the tree. Errors
	// here don't unwind the link — the notebook stays registered (the user can
	// re-link or the watcher picks it up later); we surface them as a return.
	if a.watcher != nil {
		_ = a.watcher.AddWatchRoot(ln.RootPath, ln.Source(), ln.DisplayName)
	}
	if _, idxErr := a.indexLinkedTree(ln); idxErr != nil {
		log.Printf("LinkNotebook(%s): indexTree failed: %v (link registered; will retry on next change)", ln.DisplayName, idxErr)
	}
	return ln, nil
}

// UnlinkNotebook removes a linked notebook from the registry, stops watching
// it, and drops its local index rows. The external files are left completely
// untouched (safe default). Idempotent.
func (a *App) UnlinkNotebook(id string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	// Mutate the registry AND persist under configMu so a concurrent
	// Link/Unlink or config.Save can't race the LinkedNotebooks slice. A fresh
	// `kept` slice is allocated (not a.cfg.LinkedNotebooks[:0]) so we never
	// overwrite the backing array a concurrent reader may be marshalling.
	a.configMu.Lock()
	removed := false
	var kept []config.LinkedNotebook
	var rootPath string
	for _, ln := range a.cfg.LinkedNotebooks {
		if ln.ID == id {
			removed = true
			rootPath = ln.RootPath
			continue
		}
		kept = append(kept, ln)
	}
	var saveErr error
	if removed {
		a.cfg.LinkedNotebooks = kept
		if a.configWatcher != nil {
			a.configWatcher.RegisterSelfWrite()
		}
		saveErr = config.Save(a.vaultPath, a.cfg)
	}
	a.configMu.Unlock()
	if saveErr != nil {
		return fmt.Errorf("failed to persist link registry: %w", saveErr)
	}
	if !removed {
		return nil // idempotent: unknown id is a no-op
	}

	// Drop the co-located config cache entry for this source (#133);
	// a re-link of the same root will re-populate it lazily. Done AFTER
	// releasing configMu so the dedicated linkedConfigsMu is the only lock
	// held (no nested locking).
	a.invalidateLinkedConfig("linked:" + id)

	if a.watcher != nil && rootPath != "" {
		a.watcher.RemoveWatchRoot(rootPath)
	}
	// Drop the local index rows for this source. The files table rows (keyed by
	// absolute path) are pruned by PruneStaleFiles on the next startup scan;
	// dropping them eagerly here would race the watcher's Remove events.
	a.coordinator.WithDBWrite(func() {
		_ = a.db.ClearSourceBlocks("linked:" + id)
	})
	return nil
}

// PickLinkedNotebook opens the native folder picker and links the chosen
// external folder. Returns the linked notebook, or a zero value (no error) when
// the user cancels.
func (a *App) PickLinkedNotebook() (config.LinkedNotebook, error) {
	if a.ctx == nil {
		return config.LinkedNotebook{}, fmt.Errorf("application context not ready")
	}
	selectedPath, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Link External Notebook Folder",
	})
	if err != nil {
		return config.LinkedNotebook{}, fmt.Errorf("failed to open folder picker: %w", err)
	}
	if selectedPath == "" {
		return config.LinkedNotebook{}, nil // user cancelled
	}
	return a.LinkNotebook(selectedPath)
}

// rejectLinkCollision fails loud if the linked notebook's display name collides
// with an in-vault notebook folder or an already-registered link.
func (a *App) rejectLinkCollision(ln config.LinkedNotebook) error {
	// Existing links.
	a.configMu.RLock()
	for _, existing := range a.cfg.LinkedNotebooks {
		if existing.ID == ln.ID || existing.RootPath == ln.RootPath || existing.DisplayName == ln.DisplayName {
			a.configMu.RUnlock()
			return fmt.Errorf("a linked notebook with this name/path is already registered")
		}
	}
	a.configMu.RUnlock()
	// Vault notebooks (top-level dirs, excluding dot/system).
	entries, err := os.ReadDir(a.vaultPath)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				if e.Name() == ln.DisplayName {
					return fmt.Errorf("a vault notebook named %q already exists; choose a different folder", ln.DisplayName)
				}
			}
		}
	}
	return nil
}

// nameCollidesWithLink reports whether a display name is taken by a registered
// linked notebook other than excludeID (used when renaming a link in place).
// This enforces the GLOBAL name-uniqueness invariant from the VAULT side
// (CreateNotebook / OpenNotebook / RenameNotebook) that resolveSourceByName
// depends on: names must be unique across vault + linked so the name alone maps
// to one source. Without it, a vault notebook sharing a linked name makes
// resolveSourceByName route every notebook-scoped op (incl. DeletePage →
// os.Remove in place) to the external root — silent misrouting + data loss.
func (a *App) nameCollidesWithLink(name, excludeID string) bool {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	for _, ln := range a.cfg.LinkedNotebooks {
		if ln.ID != excludeID && ln.DisplayName == name {
			return true
		}
	}
	return false
}

// linkByRecordLocked reports whether a LinkedNotebook with the same ID,
// RootPath, or DisplayName is already registered. The caller MUST hold
// configMu (read or write). Used to re-validate under the LinkNotebook write
// lock (rejectLinkCollision ran RLock-then-release, so a concurrent link could
// race it).
func (a *App) linkByRecordLocked(ln config.LinkedNotebook) (config.LinkedNotebook, bool) {
	for _, existing := range a.cfg.LinkedNotebooks {
		if existing.ID == ln.ID || existing.RootPath == ln.RootPath || existing.DisplayName == ln.DisplayName {
			return existing, true
		}
	}
	return config.LinkedNotebook{}, false
}

// linkedConfigFor returns the linked notebook's co-located config.yaml
// (<linkedRoot>/.system/config.yaml, #133), mtime-cached. If the on-disk
// mtime is unchanged since the last load, the cached parsed config is
// returned; otherwise the file is re-read and the cache is updated. Thread-
// safe via linkedConfigsMu (a dedicated mutex, NOT configMu) so concurrent
// callers resolving different linked notebooks cannot trigger a
// concurrent-map-write panic. A missing co-located file yields
// config.Defaults() with no error (the normal case — the vault-scoped
// config.yaml is the baseline). An unparseable file yields a real error so
// the user can fix it; the cache is not populated with garbage on error.
//
// The PLAN (Phase 5) called for pre-populating the cache in
// initializeVaultServices; the implementation uses lazy population instead
// (the cache fills on the first GetPluginSettingsForNotebook call for each
// source). This avoids blocking startup on N co-located-config reads for N
// linked notebooks and is functionally equivalent: the mtime check on every
// call guarantees freshness, and a cache miss is a single stat + read.
func (a *App) linkedConfigFor(ln config.LinkedNotebook) (config.SystemConfig, error) {
	source := ln.Source()
	path := config.LinkedConfigPath(ln.RootPath)

	// Stat OUTSIDE the lock — the mtime is the cache key, and stat is fast
	// even on a network mount (no file content read). Holding linkedConfigsMu
	// during stat would serialize concurrent cache-miss resolutions for
	// different linked notebooks (#133 review).
	st, statErr := os.Stat(path)
	var mtime time.Time
	fileExists := false
	if statErr == nil {
		mtime = st.ModTime()
		fileExists = true
	} else if !os.IsNotExist(statErr) {
		return config.Defaults(), fmt.Errorf("stat linked config: %w", statErr)
	}

	// Cache check under lock (no I/O — quick map lookup).
	a.linkedConfigsMu.Lock()
	if a.linkedConfigs == nil {
		a.linkedConfigs = make(map[string]linkedConfigEntry)
	}
	if cached, ok := a.linkedConfigs[source]; ok {
		// Hit conditions: file still missing (zero mtime cached) or
		// mtime unchanged.
		if (!fileExists && cached.mtime.IsZero()) || (fileExists && cached.mtime.Equal(mtime)) {
			a.linkedConfigsMu.Unlock()
			return cached.cfg, nil
		}
	}
	a.linkedConfigsMu.Unlock()

	// Cache miss: load OUTSIDE the lock (disk read + YAML parse). Two
	// concurrent goroutines may both miss and both load — that is fine;
	// last writer wins and the data converges (identical or next-access
	// refresh). The lock is only held for the map mutation.
	cfg, err := config.LoadLinked(ln.RootPath)
	if err != nil {
		return config.Defaults(), err
	}

	// Update cache under lock.
	a.linkedConfigsMu.Lock()
	a.linkedConfigs[source] = linkedConfigEntry{cfg: cfg, mtime: mtime}
	a.linkedConfigsMu.Unlock()

	return cfg, nil
}

// invalidateLinkedConfig drops the cached co-located config for a source so
// the next read re-loads from disk. Called by the watcher hook on an external
// edit of <linkedRoot>/.system/config.yaml and by UnlinkNotebook. Thread-safe
// via linkedConfigsMu.
func (a *App) invalidateLinkedConfig(source string) {
	a.linkedConfigsMu.Lock()
	defer a.linkedConfigsMu.Unlock()
	if a.linkedConfigs == nil {
		return
	}
	delete(a.linkedConfigs, source)
}

// onLinkedConfigChange is the watcher hook for external edits to a linked
// notebook's co-located <root>/.system/config.yaml (#133). It drops the
// cached parsed config for the source (so the next GetPluginSettingsForNotebook
// call re-reads from disk) and emits a linked-config:changed Wails event so
// the frontend can refresh any per-active-notebook settings it derived from
// the old config. Called from the watcher goroutine.
func (a *App) onLinkedConfigChange(source string) {
	a.invalidateLinkedConfig(source)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "linked-config:changed", source)
	}
}

// indexLinkedTree walks a linked root's markdown and indexes it under the
// linked source in a SINGLE batched transaction (#134). The notebook name is
// the link's DisplayName (the root IS one notebook); sections/pages are
// derived from the path relative to the root. Returns the number of files
// indexed.
//
// Batched (was per-file): the previous implementation called IndexFileBlocks
// (which begins/commits its own transaction) plus MarkFileIndexed for every
// file, producing N transactions for N files. On a large synced mount (the
// headline #100 workload) that was WAL-checkpoint thrash and slow first-link
// UX. The batched path threads `source` through IndexScanResults (the same
// function the vault startup scan uses) and does the files-table
// (MarkFileIndexed) pass after the index commit, preserving linked warm
// restart. Per-file read/parse errors are surfaced in the skipped list
// (IndexScanResults collects them) instead of logged inline.
func (a *App) indexLinkedTree(ln config.LinkedNotebook) (int, error) {
	files, warnings, err := parser.WalkMarkdown(ln.RootPath)
	for _, w := range warnings {
		log.Printf("LinkNotebook(%s): %s", ln.DisplayName, w)
	}
	if err != nil {
		return 0, fmt.Errorf("walk linked root: %w", err)
	}
	source := ln.Source()

	// Build the per-file ScanResult set in one pass. Read/parse errors are
	// recorded on the result (Err) so IndexScanReports reports them in the
	// skipped list rather than aborting the whole batch — same visibility
	// as the per-file path, one transaction instead of N.
	results := make([]parser.ScanResult, 0, len(files))
	for _, file := range files {
		rel, relErr := filepath.Rel(ln.RootPath, file)
		if relErr != nil {
			results = append(results, parser.ScanResult{
				Path: file,
				Err:  fmt.Errorf("resolve relative path: %w", relErr),
			})
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		pageName := parts[len(parts)-1]
		if strings.HasSuffix(strings.ToLower(pageName), ".md") {
			pageName = pageName[:len(pageName)-3]
		}
		section := ""
		if len(parts) > 1 {
			section = strings.Join(parts[:len(parts)-1], "/")
		}

		st, statErr := os.Stat(file)
		contentBytes, readErr := os.ReadFile(file)
		if readErr != nil {
			log.Printf("LinkNotebook(%s): read %s failed: %v", ln.DisplayName, file, readErr)
			results = append(results, parser.ScanResult{Path: file, Err: readErr})
			continue
		}
		// Force the linked notebook's display name: an external file's
		// frontmatter may declare a different `notebook:`, which would
		// make the row miss ListNavigation's DisplayName filter. The
		// linked root IS this one notebook (#100).
		blocks, meta, _, _, perr := parser.ParseFileContent(string(contentBytes), ln.DisplayName, section, pageName, fileOrDefaultDate(file), a.spacesPerTab)
		if perr != nil {
			log.Printf("LinkNotebook(%s): parse %s failed: %v", ln.DisplayName, file, perr)
			results = append(results, parser.ScanResult{Path: file, Err: perr})
			continue
		}
		res := parser.ScanResult{
			Path:     file,
			Notebook: ln.DisplayName,
			Section:  section,
			Page:     pageName,
			Source:   source,
			Blocks:   blocks,
			Tags:     meta.Tags,
			Warnings: meta.Warnings,
		}
		if statErr == nil {
			res.MTime = st.ModTime()
			res.Size = st.Size()
		}
		results = append(results, res)
	}

	var (
		indexedCount int
		skipped      []string
		idxErr       error
	)
	a.coordinator.WithDBWrite(func() {
		indexedCount, skipped, idxErr = a.db.IndexScanResults(results)
	})
	if idxErr != nil {
		return indexedCount, fmt.Errorf("index linked tree: %w", idxErr)
	}
	for _, s := range skipped {
		log.Printf("LinkNotebook(%s): skipped %s", ln.DisplayName, s)
	}

	// Post-commit files-table pass: record mtime+size for each successfully
	// indexed file so a warm restart skips re-parsing it. A file is
	// considered indexed iff IndexScanResults counted it (Err == nil &&
	// Notebook != ""). Mirrors the vault startup scan's MarkFileIndexed loop,
	// but batched: a single transaction inside WithDBWrite, so N files cost
	// one commit (not N auto-committed statements) and the coordinator keeps
	// serializing writes against concurrent IPC. Unbatched, this defeated
	// #134's purpose on large linked mounts (WAL-checkpoint thrash) and raced
	// other writers.
	a.coordinator.WithDBWrite(func() {
		tx, err := a.db.SQLDB().Begin()
		if err != nil {
			log.Printf("LinkNotebook(%s): begin files-tx failed: %v", ln.DisplayName, err)
			return
		}
		defer tx.Rollback()
		for _, res := range results {
			if res.Err != nil || res.Notebook == "" {
				continue
			}
			if res.MTime.IsZero() {
				// No stat → can't record a skip key; leave it to be re-parsed
				// next time rather than risk a false "unchanged".
				continue
			}
			if err := a.db.MarkFileIndexed(tx, res.Path, res.MTime.UnixNano(), res.Size); err != nil {
				log.Printf("LinkNotebook(%s): MarkFileIndexed(%s): %v", ln.DisplayName, res.Path, err)
			}
		}
		if err := tx.Commit(); err != nil {
			log.Printf("LinkNotebook(%s): files-tx commit failed: %v", ln.DisplayName, err)
		}
	})
	return indexedCount, nil
}

// CreateSection creates a section folder inside a notebook. A section groups
// pages; it has no content of its own.
func (a *App) CreateSection(notebook, section string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	if safeNotebook == "" || safeSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, a.resolveSourceByName(safeNotebook))
	if err != nil {
		return err
	}
	secPath := filepath.Join(notebookDir, safeSection)
	if !isPathWithinRoot(secPath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if err := os.MkdirAll(secPath, 0755); err != nil {
		return fmt.Errorf("failed to create section: %w", err)
	}
	return nil
}

// CreatePage scaffolds the first daily note inside
// <vault>/<notebook>/[<section>/]<page>/ and indexes it, returning the date
// used. Section may be empty, in which case the page lives directly under the
// notebook. This is the streaming unit shown in the timeline editor.
func (a *App) CreatePage(notebook, section, page, dateStr string) (string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return "", fmt.Errorf("notebook and page names are required (section is optional)")
	}
	safeDate := sanitizePathSegment(dateStr)
	if safeDate == "" {
		safeDate = time.Now().Format("2006-01-02")
	}

	// Resolve the notebook's root from its source (#100): vault →
	// <vault>/<notebook>, linked → the linked root. Page IS a file at
	// <root>/[<section>/]<page>.md.
	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return "", err
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return "", fmt.Errorf("path escapes notebook root")
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directory: %w", err)
	}

	if _, err := os.Stat(filePath); err == nil {
		return safeDate, nil // already exists
	}

	// Create an empty page — just frontmatter, no scaffold blocks. The user
	// starts with a blank editor; the page's date lives in the frontmatter
	// metadata, not as a visible content block.
	scaffoldFrontmatter := fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n",
		strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeDate))

	a.wg.Add(1)
	defer a.wg.Done()

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		a.tracker.RegisterWrite(filePath)
		if err := parser.WriteFileAtomic(filePath, []byte(scaffoldFrontmatter)); err != nil {
			writeErr = err
			return
		}

		blocks, meta, _, _, err := parser.ParseFileContent(scaffoldFrontmatter, safeNotebook, safeSection, safePage, safeDate, a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(source, meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("CreatePage: IndexFileBlocks failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
			}
		}
	})

	if writeErr != nil {
		return "", fmt.Errorf("failed to write scaffolded page note: %w", writeErr)
	}

	return safeDate, nil
}

// SaveFileBlocks writes the updated list of blocks back to the page file.
// With the per-day file model removed, a page is a single file. Each block
// carries its own file_date. The notebook's source is resolved server-side
// from its (globally-unique) name (#100).
// writePageFileLocked reads the existing file content, renders the new block
// list through the single serializer (preserving unmanaged lines), writes
// atomically, and re-indexes in SQLite. The caller MUST already hold
// LockFileWrite for filePath — this method does NOT acquire the per-file lock
// (it would deadlock against a re-entrant LockFileWrite on the same path).
// Extracted from SaveFileBlocks so the cross-page source-removal path in
// applyBlocksOps can do an atomic read-parse-filter-write under a single
// LockFileWrite scope (#104 TOCTOU fix).
func (a *App) writePageFileLocked(filePath, source, notebook, section, page string, blocks []parser.ParsedBlock) error {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing file: %w", err)
	}

	frontmatter, body := parser.SplitFrontmatter(string(contentBytes))

	if frontmatter == "" {
		today := time.Now().Format("2006-01-02")
		frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(notebook), strconv.Quote(section), strconv.Quote(page), strconv.Quote(today))
		body = string(contentBytes)
	}

	newContent := parser.RenderFileContent(blocks, body, frontmatter, a.spacesPerTab)

	a.tracker.RegisterWrite(filePath)

	if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
		return err
	}

	parsedBlocks, meta, _, _, err := parser.ParseFileContent(newContent, notebook, section, page, fileOrDefaultDate(filePath), a.spacesPerTab)
	if err == nil {
		var idxErr error
		a.coordinator.WithDBWrite(func() {
			idxErr = a.db.IndexFileBlocks(source, meta.Notebook, meta.Section, meta.Page, parsedBlocks, meta.Tags, meta.Warnings...)
		})
		if idxErr != nil {
			log.Printf("writePageFileLocked: IndexFileBlocks failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
		}
	}
	return nil
}

func (a *App) SaveFileBlocks(notebook, section, page string, blocks []parser.ParsedBlock) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid path metadata")
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return fmt.Errorf("resolve notebook dir: %w", err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	// Extract block IDs for per-block write-intent locking (#64). This
	// serializes the full-page save against any concurrent MutateBlock for
	// the same block, preventing last-writer-wins clobbering.
	blockIDs := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.ID != "" {
			blockIDs = append(blockIDs, b.ID)
		}
	}

	// Fetch the page's current block IDs so that, after the save, we can
	// release the per-block mutex entries for blocks that were dropped or
	// replaced (#122). Block IDs are page-scoped, so any ID present before
	// but absent from the new set no longer exists and will never be mutated
	// again.
	var beforeIDs []string
	a.coordinator.WithDBRead(func() {
		beforeIDs, _ = a.db.BlockIDsForPage(source, safeNotebook, safeSection, safePage)
	})

	var writeErr error
	a.coordinator.LockBlocksWrite(blockIDs, func() {
		a.coordinator.LockFileWrite(filePath, func() {
			writeErr = a.writePageFileLocked(filePath, source, safeNotebook, safeSection, safePage, blocks)
		})
	}) // LockBlocksWrite

	if writeErr != nil {
		return writeErr
	}
	// Release the per-block mutex entries for blocks that were present before
	// but are absent from the saved set — they were deleted/replaced and will
	// never be mutated again. Bounds blockMu growth (#122).
	newIDSet := make(map[string]bool, len(blockIDs))
	for _, id := range blockIDs {
		newIDSet[id] = true
	}
	var removed []string
	for _, id := range beforeIDs {
		if id != "" && !newIDSet[id] {
			removed = append(removed, id)
		}
	}
	a.coordinator.ReleaseBlockMutexes(removed)
	// Notify live embeds/references that the saved blocks changed.
	for _, b := range blocks {
		if b.ID != "" {
			a.emitBlockChanged(b.ID, safeNotebook, safeSection, safePage, b.FileDate)
		}
	}
	return nil
}
