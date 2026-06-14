package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"silt/backend/core"
	"silt/backend/db"
	"silt/backend/monitor"
	"silt/backend/parser"
	"silt/backend/plugins"
	"silt/backend/themes"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"
)

const (
	maxTimelineLimit     = 200
	defaultTimelineLimit = 30
)

var updateLineIDRegex = regexp.MustCompile(`<!-- id: ([a-f0-9\-]{36}) -->`)

// errBlockBeingEdited is returned by MutateBlock when the target file is
// focus-locked (a user is editing it in another view). Callers retry rather
// than silently overwriting the in-flight edit.
var errBlockBeingEdited = fmt.Errorf("block is being edited in another view")

//go:embed VERSION
var versionBytes []byte

// appVersion is the current Silt version, embedded at build time from the
// VERSION file. Used for plugin minSiltVersion enforcement.
var appVersion = strings.TrimSpace(string(versionBytes))

type App struct {
	ctx          context.Context
	db           *db.DatabaseManager
	coordinator  *core.ExecutionCoordinator
	watcher      *monitor.DirectoryWatcher
	tracker      *monitor.WriteTracker
	vaultPath    string
	spacesPerTab int
	wg           sync.WaitGroup

	// pluginRODB is a lazy read-only handle to the in-memory index, used
	// exclusively by PluginRawQuery so a plugin can never mutate the index
	// or schema even if a prefix check or comment-stripping is bypassed.
	pluginRODBMu sync.Mutex
	pluginRODB   *sql.DB
}

func NewApp() *App {
	return &App{
		spacesPerTab: 4,
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	settings, err := vault.LoadSettings()
	if err != nil {
		// The settings file exists on disk but is unreadable or
		// malformed. Don't silently fall through to "no vault" — the
		// user has a vault setup, something is just broken.
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "vault:init-error",
				fmt.Sprintf("failed to load settings.json: %v", err))
		}
		return
	}
	if settings.VaultPath != "" {
		if _, statErr := os.Stat(settings.VaultPath); statErr == nil {
			if initErr := a.initializeVaultServices(settings.VaultPath); initErr != nil {
				if a.ctx != nil {
					runtime.EventsEmit(a.ctx, "vault:init-error", initErr.Error())
				}
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	// Wait for any in-flight Wails-bound calls (UpdateBlockState,
	// QueryTasks, FetchSectionTimeline) to complete before tearing
	// down the DB, tracker, and watcher. Without this a fast window
	// close could race an in-progress file write.
	a.wg.Wait()

	if a.watcher != nil {
		_ = a.watcher.Close()
	}
	if a.tracker != nil {
		a.tracker.Stop()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
}

func (a *App) initializeVaultServices(vaultPath string) error {
	// Persistent on-disk WAL index at <vault>/.system/index.sqlite. Survives
	// restarts so a warm launch re-indexes only changed files (#29). Markdown
	// remains the source of truth; deleting the 3 index files forces a clean
	// full rebuild. The .system dir is created by ScaffoldVault.
	systemDir := filepath.Join(vaultPath, ".system")
	if err := os.MkdirAll(systemDir, 0755); err != nil {
		return fmt.Errorf("failed to ensure .system dir: %w", err)
	}
	indexPath := filepath.Join(systemDir, "index.sqlite")
	dbMgr, err := db.NewDatabaseManager(indexPath)
	if err != nil {
		return fmt.Errorf("failed to start database: %w", err)
	}

	coord := core.NewExecutionCoordinator(dbMgr.SQLDB())
	tracker := monitor.NewWriteTracker()

	results, err := parser.ScanWorkspace(vaultPath, a.spacesPerTab)
	if err != nil {
		_ = dbMgr.Close()
		return fmt.Errorf("failed to scan workspace: %w", err)
	}

	// Incremental re-index: keep only files whose mtime+size differ from the
	// last recorded index (or that were never indexed). On a cold start (no
	// index file yet) every file is "changed" and gets a full index. Pruning
	// stale `files` rows for paths no longer on disk handles deletes/renames.
	var changed []parser.ScanResult
	var seenPaths []string
	for _, res := range results {
		seenPaths = append(seenPaths, res.Path)
		if res.Err != nil || res.Notebook == "" {
			// Unreadable or unresolvable files are forwarded to the indexer so
			// they appear in the skipped list; they do not get a files row.
			changed = append(changed, res)
			continue
		}
		unchanged, uerr := dbMgr.IsFileUnchanged(res.Path, res.MTime.UnixNano(), res.Size)
		if uerr != nil {
			log.Printf("initializeVaultServices: IsFileUnchanged(%s): %v", res.Path, uerr)
			changed = append(changed, res)
			continue
		}
		if unchanged {
			continue
		}
		changed = append(changed, res)
	}

	indexedCount, skipped, err := dbMgr.IndexScanResults(changed)
	if err != nil {
		_ = dbMgr.Close()
		return fmt.Errorf("failed to index scan results: %w", err)
	}

	// Record the freshly-indexed files' stats and prune paths that vanished
	// since the last run (rename/delete). Only files that were actually
	// indexed (valid metadata, no scan error) get a files row — a file that
	// failed to parse shouldn't be marked "unchanged" next time.
	var allWarnings []string
	for _, res := range changed {
		if res.Err != nil {
			allWarnings = append(allWarnings, fmt.Sprintf("%s: %v", res.Path, res.Err))
			continue
		}
		if res.Notebook == "" {
			for _, w := range res.Warnings {
				allWarnings = append(allWarnings, fmt.Sprintf("%s: %s", res.Path, w))
			}
			if len(res.Warnings) == 0 {
				allWarnings = append(allWarnings, fmt.Sprintf("%s: missing notebook/section/page", res.Path))
			}
			continue
		}
		if res.MTime.IsZero() {
			// No stat → can't record a skip key; leave it to be re-parsed
			// next time rather than risk a false "unchanged".
			continue
		}
		if err := dbMgr.MarkFileIndexed(nil, res.Path, res.MTime.UnixNano(), res.Size); err != nil {
			log.Printf("initializeVaultServices: MarkFileIndexed(%s): %v", res.Path, err)
		}
	}
	pruned, pruneErr := dbMgr.PruneStaleFiles(seenPaths)
	if pruneErr != nil {
		log.Printf("initializeVaultServices: PruneStaleFiles: %v", pruneErr)
	}
	for _, p := range pruned {
		allWarnings = append(allWarnings, fmt.Sprintf("%s: removed from index (file no longer exists)", p))
	}

	// Merge the indexer's per-file skip list into the warning stream.
	allWarnings = append(allWarnings, skipped...)

	if indexedCount > 0 {
		// A checkpoint after the bulk insert keeps the WAL bounded for the
		// session. No-op on in-memory.
		if err := dbMgr.Checkpoint(); err != nil {
			log.Printf("initializeVaultServices: post-index checkpoint: %v", err)
		}
	}
	if len(allWarnings) > 0 && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:init-warnings", allWarnings)
	}

	watcher, err := monitor.NewDirectoryWatcher(vaultPath, dbMgr, tracker, coord, a.spacesPerTab)
	if err != nil {
		_ = dbMgr.Close()
		return fmt.Errorf("failed to start watcher: %w", err)
	}
	if err := watcher.Start(); err != nil {
		_ = watcher.Close()
		_ = dbMgr.Close()
		return fmt.Errorf("failed to execute watcher start: %w", err)
	}

	a.db = dbMgr
	a.coordinator = coord
	a.tracker = tracker
	a.watcher = watcher
	a.vaultPath = vaultPath

	// Report any paths the watcher could not subscribe to (fsnotify
	// limits, permissions, etc.) so the UI can inform the user.
	if failed := watcher.FailedPaths(); len(failed) > 0 && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:watch-coverage", failed)
	}

	return nil
}

// IsVaultInitialized returns whether a workspace vault has been configured and loaded.
func (a *App) IsVaultInitialized() bool {
	return a.vaultPath != "" && a.db != nil
}

// InitializeVault prompts the user for a folder, sets it up, and loads the services.
func (a *App) InitializeVault() (bool, error) {
	selectedPath, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Silt Vault Directory",
	})
	if err != nil {
		return false, fmt.Errorf("failed to select vault folder: %w", err)
	}

	if selectedPath == "" {
		return false, nil // Cancelled
	}

	if err := vault.ScaffoldVault(selectedPath); err != nil {
		return false, fmt.Errorf("failed to scaffold vault: %w", err)
	}

	settings := &vault.AppSettings{
		VaultPath: selectedPath,
	}
	if err := vault.SaveSettings(settings); err != nil {
		return false, fmt.Errorf("failed to save settings: %w", err)
	}

	if err := a.initializeVaultServices(selectedPath); err != nil {
		return false, fmt.Errorf("failed to boot vault services: %w", err)
	}

	return true, nil
}

// FetchPageTimeline returns blocks grouped by days for the streaming Page
// (notebook/section/page), paged for scroll virtualization.
func (a *App) FetchPageTimeline(notebook, section, page string, offset int, limit int) ([]parser.DayGroup, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	// Clamp server-side so a frontend bug sending limit=1_000_000 cannot
	// materialize an arbitrarily large in-memory slice.
	if limit <= 0 || limit > maxTimelineLimit {
		limit = maxTimelineLimit
	}
	if offset < 0 {
		offset = 0
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []parser.DayGroup
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.FetchTimelineDays(notebook, section, page, limit, offset)
	})

	return res, err
}

// UpdateBlockState changes task status and updates the file and cache.
//
// To avoid TOCTOU races between the DB read and the file write, we look up the
// block's UUID, file metadata, and the lock by file path, then re-locate the
// target line inside the file write lock by scanning for the UUID comment. The
// UUID is the source of truth for the target line, not the cached line number.
func (a *App) UpdateBlockState(blockID string, newState string) error {
	// Guard against a meaningless no-op that the frontend might interpret
	// as an error. The only valid task status values are TODO, DOING, DONE.
	switch newState {
	case "TODO", "DOING", "DONE":
	default:
		return fmt.Errorf("invalid target status: %s (valid: TODO, DOING, DONE)", newState)
	}

	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var notebook, section, page, fileDate, blockType string
	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow("SELECT notebook, section, page, file_date, type FROM blocks WHERE id = ?", blockID)
		return row.Scan(&notebook, &section, &page, &fileDate, &blockType)
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}

	if blockType != string(parser.BlockTask) {
		return fmt.Errorf("block %s is not a task", blockID)
	}

	// Defense-in-depth against path traversal: notebook/section/page originate
	// from user-editable YAML frontmatter and date is a filename. Section may
	// be empty (a page living directly under its notebook).
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	safeFileDate := sanitizePathSegment(fileDate)
	if safeNotebook == "" || safePage == "" || safeFileDate == "" {
		return fmt.Errorf("invalid file metadata for block %s: notebook=%q section=%q page=%q date=%q", blockID, notebook, section, page, fileDate)
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeFileDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("resolved file path %q escapes vault %q", filePath, a.vaultPath)
	}

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			writeErr = err
			return
		}

		// Parse the whole file, flip the target task's status in the parsed
		// slice, then re-render through the single serializer. This keeps
		// UpdateBlockState on the same write path as every other writer
		// (one on-disk format definition) and preserves unmanaged lines via
		// the original body.
		parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, safeFileDate, a.spacesPerTab)
		if parseErr != nil {
			writeErr = fmt.Errorf("failed to parse file for state update: %w", parseErr)
			return
		}
		found := false
		for i := range parsedBlocks {
			if parsedBlocks[i].ID == blockID {
				if parsedBlocks[i].Type != parser.BlockTask {
					writeErr = fmt.Errorf("block %s is not a task", blockID)
					return
				}
				parsedBlocks[i].Status = newState
				found = true
				break
			}
		}
		if !found {
			writeErr = fmt.Errorf("block %s not found in file %s", blockID, filePath)
			return
		}

		frontmatter, body := splitFrontmatter(string(contentBytes))
		if frontmatter == "" {
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeFileDate))
			body = string(contentBytes)
		}

		newContent := parser.RenderFileContent(parsedBlocks, body, frontmatter, a.spacesPerTab)

		a.tracker.RegisterWrite(filePath)

		if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
			writeErr = err
			return
		}

		// Re-parse with the sanitized metadata so the re-indexed row
		// uses the same cleaned values that went into the file path.
		blocks, remeta, _, _, err := parser.ParseFileContent(newContent, meta.Notebook, meta.Section, meta.Page, meta.Date, a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, blocks, remeta.Tags, remeta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("UpdateBlockState: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
			}
		}
	})

	if writeErr != nil {
		return writeErr
	}
	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, safeFileDate)
	return nil
}

// QueryTasks retrieves indexed items matching the active filters.
func (a *App) QueryTasks(filter parser.TaskQueryFilter) ([]parser.TaskResult, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []parser.TaskResult
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.QueryTasksWithFilters(filter)
	})

	return res, err
}

// emitBlockChanged broadcasts a block:changed event so live embeds/references
// refresh whenever a block is mutated through any code path.
func (a *App) emitBlockChanged(id, notebook, section, page, fileDate string) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "block:changed", parser.BlockChangedEvent{
		ID: id, Notebook: notebook, Section: section, Page: page, FileDate: fileDate,
	})
}

// --- Theme engine IPC (#45) -----------------------------------------------

// ActiveThemeResult is the IPC payload returned by GetActiveTheme /
// ApplyTheme. It carries the active theme id/name, the STORED mode
// (dark|light|system), the effective token map for the first paint, both
// dark/light maps so the frontend can resolve "system" locally without a
// second round-trip, and the resolved bg.void for the native webview
// background.
type ActiveThemeResult struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Mode        string            `json:"mode"`         // stored: dark|light|system
	Tokens      map[string]string `json:"tokens"`       // effective (first-paint) map
	DarkTokens  map[string]string `json:"dark_tokens"`  // always present
	LightTokens map[string]string `json:"light_tokens"` // always present
	BGVoid      string            `json:"bg_void"`      // effective bg.void for webview
}

// effectiveMode resolves a stored ThemeMode to a concrete dark/light for the
// first paint. "system" is resolved to "dark" here as the shipped default;
// the frontend re-resolves "system" via prefers-color-scheme using both
// token maps, so the backend never needs to query the OS.
func effectiveMode(mode string) string {
	if mode == "light" {
		return "light"
	}
	return "dark" // dark + system + unknown → dark first paint
}

// buildThemeResult assembles the IPC payload from a parsed theme + stored mode.
func buildThemeResult(t *themes.Theme, mode string) ActiveThemeResult {
	em := effectiveMode(mode)
	return ActiveThemeResult{
		ID:          t.ID,
		Name:        t.Name,
		Mode:        mode,
		Tokens:      t.Flatten(em),
		DarkTokens:  t.Flatten("dark"),
		LightTokens: t.Flatten("light"),
		BGVoid:      t.BGVoid(em),
	}
}

// themesDir returns <vault>/.system/themes, or "" before a vault is open.
func (a *App) themesDir() string {
	if a.vaultPath == "" {
		return ""
	}
	return filepath.Join(a.vaultPath, ".system", "themes")
}

// ListThemes enumerates available themes (on-disk + the embedded default)
// and any per-file load errors. Works before a vault is open (returns just
// the embedded default).
func (a *App) ListThemes() (*themes.ListThemesResult, error) {
	return themes.ListThemes(a.themesDir())
}

// GetActiveTheme reads AppSettings, resolves the active theme (falling back
// to the embedded default when the id is missing/invalid), and returns the
// token maps for injection. Always succeeds with the default theme on a
// fresh/empty vault so the app can render on first paint.
func (a *App) GetActiveTheme() (ActiveThemeResult, error) {
	settings, err := vault.LoadSettings()
	if err != nil {
		// Settings exist but are unreadable — surface it rather than
		// masking with the default (matches the startup() policy).
		return ActiveThemeResult{}, fmt.Errorf("failed to load settings: %w", err)
	}
	t, err := themes.ResolveActive(a.themesDir(), settings.ActiveTheme, settings.ThemeMode)
	if err != nil {
		return ActiveThemeResult{}, err
	}
	return buildThemeResult(t, settings.ThemeMode), nil
}

// ApplyTheme selects a theme and mode, persists it to settings, and returns
// the new token maps. Both id and mode are validated: an unknown id or an
// invalid mode returns a structured error and is NOT persisted.
func (a *App) ApplyTheme(id, mode string) (ActiveThemeResult, error) {
	if !vault.ValidThemeMode(mode) {
		return ActiveThemeResult{}, fmt.Errorf("invalid mode %q (valid: dark, light, system)", mode)
	}
	// Confirm the requested id is selectable (on-disk or the embedded
	// default). A typo or stale id errors here rather than silently
	// snapping to the default.
	listing, err := themes.ListThemes(a.themesDir())
	if err != nil {
		return ActiveThemeResult{}, fmt.Errorf("failed to enumerate themes: %w", err)
	}
	if id != themes.DefaultThemeID {
		known := false
		for _, ti := range listing.Themes {
			if ti.ID == id {
				known = true
				break
			}
		}
		if !known {
			return ActiveThemeResult{}, fmt.Errorf("theme %q is not available", id)
		}
	}

	t, err := themes.ResolveActive(a.themesDir(), id, mode)
	if err != nil {
		return ActiveThemeResult{}, err
	}

	// Persist the selection atomically. Use the actually-resolved theme id
	// (t.ID) rather than the requested id: if ResolveActive fell back to the
	// embedded default (e.g. the file vanished between the ListThemes check
	// and resolution), settings stays consistent with what is rendered.
	settings, err := vault.LoadSettings()
	if err != nil {
		return ActiveThemeResult{}, fmt.Errorf("failed to load settings: %w", err)
	}
	settings.ActiveTheme = t.ID
	settings.ThemeMode = mode
	if err := vault.SaveSettings(settings); err != nil {
		return ActiveThemeResult{}, fmt.Errorf("failed to persist theme selection: %w", err)
	}

	res := buildThemeResult(t, mode)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "theme:changed", map[string]string{
			"id": t.ID, "mode": mode,
		})
	}
	return res, nil
}


// ResolveBlockReference looks up a ((uuid)) reference, returning its content
// and location for hover previews and scroll-to-source navigation. Missing
// UUIDs return Exists=false (no error) so the UI can render a broken-link chip.
func (a *App) ResolveBlockReference(blockID string) (parser.BlockReference, error) {
	ref := parser.BlockReference{ID: blockID}
	if a.db == nil {
		return ref, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow(
			"SELECT type, raw_content, clean_content, notebook, section, page, file_date, line_number FROM blocks WHERE id = ?",
			blockID,
		)
		var bType, raw, clean, notebook, section, page, fileDate string
		var ln int
		if err := row.Scan(&bType, &raw, &clean, &notebook, &section, &page, &fileDate, &ln); err != nil {
			return nil // not found → Exists stays false
		}
		ref.Exists = true
		ref.Type = bType
		ref.RawText = raw
		ref.CleanText = clean
		ref.Notebook = notebook
		ref.Section = section
		ref.Page = page
		ref.FileDate = fileDate
		ref.LineNumber = ln
		return nil
	})
	return ref, err
}

// MutateBlock rewrites the body text of a block (identified by UUID) in its
// source file, preserving the leading task/header/bullet syntax and the
// trailing <!-- id --> comment. It re-indexes the file and emits block:changed
// so live embeds/references stay in sync.
func (a *App) MutateBlock(blockID, newText string) error {
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	// Block text is single-line; collapse any newlines to spaces.
	cleanText := strings.ReplaceAll(newText, "\n", " ")

	a.wg.Add(1)
	defer a.wg.Done()

	var notebook, section, page, fileDate string
	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow("SELECT notebook, section, page, file_date FROM blocks WHERE id = ?", blockID)
		return row.Scan(&notebook, &section, &page, &fileDate)
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	safeFileDate := sanitizePathSegment(fileDate)
	if safeNotebook == "" || safePage == "" || safeFileDate == "" {
		return fmt.Errorf("invalid file metadata for block %s", blockID)
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeFileDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("resolved file path %q escapes vault %q", filePath, a.vaultPath)
	}

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		// Don't clobber a block the user is actively editing in another view
		// (the timeline editor holds a focus lock on the file while focused).
		// Refuse rather than silently overwrite; callers (e.g. EmbedPortal)
		// retry once the editor releases the lock.
		if a.watcher != nil && a.watcher.IsFocusLocked(filePath) {
			writeErr = errBlockBeingEdited
			return
		}
		contentBytes, err := os.ReadFile(filePath)
		if err != nil {
			writeErr = err
			return
		}

		// Parse the whole file, mutate the target block in the slice, then
		// re-render through the single serializer (RenderFileContent). This
		// preserves unmanaged lines (code fences, prose) via the original
		// body and keeps MutateBlock on the same write path as every other
		// writer, so there is one on-disk format definition.
		parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, safeFileDate, a.spacesPerTab)
		if parseErr != nil {
			writeErr = fmt.Errorf("failed to parse file for mutation: %w", parseErr)
			return
		}
		found := false
		for i := range parsedBlocks {
			if parsedBlocks[i].ID == blockID {
				parsedBlocks[i].CleanText = cleanText
				found = true
				break
			}
		}
		if !found {
			writeErr = fmt.Errorf("block %s not found in file %s", blockID, filePath)
			return
		}

		frontmatter, body := splitFrontmatter(string(contentBytes))
		if frontmatter == "" {
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeFileDate))
		}

		newContent := parser.RenderFileContent(parsedBlocks, body, frontmatter, a.spacesPerTab)

		a.tracker.RegisterWrite(filePath)
		if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
			writeErr = err
			return
		}

		// Re-parse the rendered output and reindex so the cache reflects the
		// canonical on-disk state (RenderFileContent may have normalized the
		// mutated line's format).
		reblocks, remeta, _, _, err := parser.ParseFileContent(newContent, meta.Notebook, meta.Section, meta.Page, meta.Date, a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, reblocks, remeta.Tags, remeta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("MutateBlock: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
			}
		}
	})
	if writeErr != nil {
		return writeErr
	}

	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, safeFileDate)
	return nil
}

// findLineByBlockID returns the 0-based index of the line in `lines` whose
// trailing `<!-- id: UUID -->` comment matches blockID, or -1 if no such line
// exists.
func findLineByBlockID(lines []string, blockID string) int {
	for i, line := range lines {
		matches := updateLineIDRegex.FindStringSubmatch(line)
		if len(matches) >= 2 && matches[1] == blockID {
			return i
		}
	}
	return -1
}

// sanitizePathSegment strips path-traversal characters from a single path
// component: directory separators, NUL, and `..` sequences. The intent is to
// safely fold untrusted frontmatter strings into file paths.
func sanitizePathSegment(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return -1
		}
		return r
	}, s)
	for strings.Contains(cleaned, "..") {
		cleaned = strings.ReplaceAll(cleaned, "..", "")
	}
	if cleaned == "." {
		cleaned = ""
	}
	return strings.TrimSpace(cleaned)
}

// splitFrontmatter separates a leading YAML frontmatter block (--- ... ---)
// from the body. It returns the frontmatter exactly as it appears in content
// (including the trailing newline after the closing ---), and the body with
// the frontmatter stripped. If content has no frontmatter, frontmatter is ""
// and body is the full content. Callers pair this with parser.RenderFileContent
// so every writer extracts frontmatter the same way.
func splitFrontmatter(content string) (frontmatter, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			fm := strings.Join(lines[:i+1], "\n") + "\n"
			body := strings.Join(lines[i+1:], "\n")
			return fm, body
		}
	}
	// Opening --- with no closing ---: treat the whole thing as body so we
	// don't silently drop user content.
	return "", content
}

// isPathWithinVault reports whether target is the same as or a descendant of
// vaultRoot. Both paths are cleaned and made absolute before comparison so
// that `..` segments in the joined path are resolved before the check.
func isPathWithinVault(target, vaultRoot string) bool {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(vaultRoot)
	if err != nil {
		return false
	}
	absTarget = filepath.Clean(absTarget)
	absRoot = filepath.Clean(absRoot)
	if absTarget == absRoot {
		return true
	}
	prefix := absRoot + string(os.PathSeparator)
	return strings.HasPrefix(absTarget, prefix)
}

// ListNavigation returns the Notebook > Section > Page tree for the sidebar.
//
// The directory structure on disk is the single source of truth. Disambiguation
// is by what a folder directly contains:
//   - A depth-1 folder under a Notebook that contains .md files is a
//     section-less PAGE (section = "").
//   - A depth-1 folder under a Notebook that does NOT contain .md files is a
//     SECTION (shown even when empty, so a freshly created section appears).
//   - Pages within a section are the .md-containing folders nested beneath it.
//
// Block counts are merged from the index for per-page badges.
func (a *App) ListNavigation() (parser.NavigationTree, error) {
	if a.vaultPath == "" {
		return parser.NavigationTree{}, fmt.Errorf("vault not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	// 1. Block counts per (notebook, section, page) from the index.
	type nspKey struct{ n, s, p string }
	counts := map[nspKey]int{}
	if a.db != nil {
		a.coordinator.WithDBRead(func() {
			rows, err := a.db.SQLDB().Query("SELECT notebook, section, page, COUNT(*) FROM blocks GROUP BY notebook, section, page")
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var n, s, p string
				var c int
				if err := rows.Scan(&n, &s, &p, &c); err == nil {
					counts[nspKey{n, s, p}] = c
				}
			}
		})
	}

	tree := parser.NavigationTree{Notebooks: []parser.NavigationNotebook{}}
	nbEntries, err := os.ReadDir(a.vaultPath)
	if err != nil {
		return tree, fmt.Errorf("failed to read vault: %w", err)
	}

	for _, nbE := range nbEntries {
		nbName := nbE.Name()
		if !nbE.IsDir() || strings.HasPrefix(nbName, ".") {
			continue // skip .system and hidden dirs
		}
		nbPath := filepath.Join(a.vaultPath, nbName)

		// sections: name -> pages (name "" holds section-less pages).
		secPages := map[string][]parser.NavigationPage{}

		depth1, err := os.ReadDir(nbPath)
		if err != nil {
			continue
		}
		for _, d1 := range depth1 {
			if !d1.IsDir() || strings.HasPrefix(d1.Name(), ".") {
				continue
			}
			d1Path := filepath.Join(nbPath, d1.Name())
			if dirHasMarkdown(d1Path) {
				// Section-less page: lives directly under the notebook.
				secPages[""] = append(secPages[""], parser.NavigationPage{
					Name:  d1.Name(),
					Count: counts[nspKey{nbName, "", d1.Name()}],
				})
				continue
			}
			// Section (possibly empty): register it even before it has pages
			// so a freshly created section appears in the navigator.
			if _, ok := secPages[d1.Name()]; !ok {
				secPages[d1.Name()] = []parser.NavigationPage{}
			}
			// Pages within the section: .md-containing folders nested beneath it.
			subs, err := os.ReadDir(d1Path)
			if err != nil {
				continue
			}
			for _, d2 := range subs {
				if !d2.IsDir() || strings.HasPrefix(d2.Name(), ".") {
					continue
				}
				d2Path := filepath.Join(d1Path, d2.Name())
				if dirHasMarkdown(d2Path) {
					secPages[d1.Name()] = append(secPages[d1.Name()], parser.NavigationPage{
						Name:  d2.Name(),
						Count: counts[nspKey{nbName, d1.Name(), d2.Name()}],
					})
				}
			}
		}

		nn := parser.NavigationNotebook{Name: nbName, Sections: []parser.NavigationSection{}}
		secNames := make([]string, 0, len(secPages))
		for s := range secPages {
			secNames = append(secNames, s)
		}
		// Surface the section-less group ("") last under a friendly label.
		sortStrings(secNames)
		moveStringToEnd(secNames, "")
		for _, s := range secNames {
			pages := secPages[s]
			sortNavPages(pages)
			nn.Sections = append(nn.Sections, parser.NavigationSection{Name: s, Pages: pages})
		}
		tree.Notebooks = append(tree.Notebooks, nn)
	}
	return tree, nil
}

// dirHasMarkdown reports whether a directory directly contains a .md file.
func dirHasMarkdown(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			return true
		}
	}
	return false
}

func moveStringToEnd(s []string, v string) {
	idx := -1
	for i, x := range s {
		if x == v {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	copy(s[idx:], s[idx+1:])
	s[len(s)-1] = v
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func sortNavPages(p []parser.NavigationPage) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j-1].Name > p[j].Name; j-- {
			p[j-1], p[j] = p[j], p[j-1]
		}
	}
}

// QueryTagHierarchy returns the hierarchical tag tree for the Tags Explorer.
func (a *App) QueryTagHierarchy() ([]parser.TagNode, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var res []parser.TagNode
	var err error
	a.coordinator.WithDBRead(func() { res, err = a.db.QueryTagHierarchy() })
	return res, err
}

// QueryBlocksByTag returns blocks tagged at or beneath tagPath (prefix match).
func (a *App) QueryBlocksByTag(tagPath string) ([]parser.TaskResult, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var res []parser.TaskResult
	var err error
	a.coordinator.WithDBRead(func() { res, err = a.db.QueryBlocksByTag(tagPath) })
	return res, err
}

// SearchBlocks fuzzy searches blocks and headings matching the query.
func (a *App) SearchBlocks(query string) ([]parser.TaskResult, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []parser.TaskResult
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.SearchBlocks(query)
	})

	return res, err
}

// AcquireFocusLock registers a focus lock on a file to ignore fsnotify updates.
func (a *App) AcquireFocusLock(notebook, section, page, fileDate string) error {
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	safeFileDate := sanitizePathSegment(fileDate)
	if safeNotebook == "" || safePage == "" || safeFileDate == "" {
		return fmt.Errorf("invalid path metadata")
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeFileDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	a.watcher.LockFocus(filePath)
	return nil
}

// ReleaseFocusLock removes a focus lock from a file.
func (a *App) ReleaseFocusLock(notebook, section, page, fileDate string) error {
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	safeFileDate := sanitizePathSegment(fileDate)
	if safeNotebook == "" || safePage == "" || safeFileDate == "" {
		return fmt.Errorf("invalid path metadata")
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeFileDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	a.watcher.UnlockFocus(filePath)
	return nil
}

// CreateNotebook creates a top-level notebook folder under the vault root.
// Silt starts blank; the user creates or opens notebooks from the sidebar.
func (a *App) CreateNotebook(name string) error {
	safeName := sanitizePathSegment(name)
	if safeName == "" {
		return fmt.Errorf("notebook name is required")
	}
	nbPath := filepath.Join(a.vaultPath, safeName)
	if !isPathWithinVault(nbPath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(nbPath); err == nil {
		return fmt.Errorf("notebook %q already exists", safeName)
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
	absPath, err := filepath.Abs(folderPath)
	if err != nil {
		return "", fmt.Errorf("invalid folder path: %w", err)
	}
	if !isPathWithinVault(absPath, a.vaultPath) {
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
	return parts[0], nil
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

// CreateSection creates a section folder inside a notebook. A section groups
// pages; it has no content of its own.
func (a *App) CreateSection(notebook, section string) error {
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	if safeNotebook == "" || safeSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}
	secPath := filepath.Join(a.vaultPath, safeNotebook, safeSection)
	if !isPathWithinVault(secPath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
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

	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return "", fmt.Errorf("path escapes vault")
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create page directory: %w", err)
	}

	if _, err := os.Stat(filePath); err == nil {
		return safeDate, nil // already exists
	}

	formattedDate := safeDate
	if t, err := time.Parse("2006-01-02", safeDate); err == nil {
		formattedDate = t.Format("Monday, January 2, 2006")
	}

	// Build the scaffold through the single serializer so even the very
	// first write to a new page uses the canonical on-disk format (no inline
	// fmt.Sprintf parallel serializer that could drift from the parser).
	// RenderFileContent assigns the UUIDs, so the blocks start ID-less.
	scaffoldBlocks := []parser.ParsedBlock{
		{Type: parser.BlockHeader, Depth: 1, CleanText: formattedDate},
		{Type: parser.BlockTask, Status: "TODO", Owner: "Chris", Priority: 3,
			CleanText: "Start writing in " + safePage},
	}
	scaffoldFrontmatter := fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n",
		strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeDate))
	scaffoldContent := parser.RenderFileContent(scaffoldBlocks, "", scaffoldFrontmatter, a.spacesPerTab)

	a.wg.Add(1)
	defer a.wg.Done()

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		a.tracker.RegisterWrite(filePath)
		if err := parser.WriteFileAtomic(filePath, []byte(scaffoldContent)); err != nil {
			writeErr = err
			return
		}

		blocks, meta, _, _, err := parser.ParseFileContent(scaffoldContent, safeNotebook, safeSection, safePage, safeDate, a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, blocks, meta.Tags, meta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("CreatePage: IndexFileBlocks failed for %s/%s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, meta.Date, idxErr)
			}
		}
	})

	if writeErr != nil {
		return "", fmt.Errorf("failed to write scaffolded page note: %w", writeErr)
	}

	return safeDate, nil
}

// SaveFileBlocks writes the updated list of blocks back to the note file.
func (a *App) SaveFileBlocks(notebook, section, page, fileDate string, blocks []parser.ParsedBlock) error {
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	safeFileDate := sanitizePathSegment(fileDate)
	if safeNotebook == "" || safePage == "" || safeFileDate == "" {
		return fmt.Errorf("invalid path metadata")
	}

	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage, safeFileDate+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		contentBytes, err := os.ReadFile(filePath)
		if err != nil && !os.IsNotExist(err) {
			writeErr = fmt.Errorf("failed to read existing file: %w", err)
			return
		}

		// Split frontmatter from body. The body (frontmatter stripped) is
		// handed to RenderFileContent so it can preserve unmanaged lines
		// (code fences, blanks, prose) in their relative position to the
		// managed blocks. The frontmatter is emitted verbatim; if the file
		// had none, synthesize the default so the note stays self-describing.
		frontmatter, body := splitFrontmatter(string(contentBytes))

		if frontmatter == "" {
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeFileDate))
			body = string(contentBytes)
		}

		// RenderFileContent is the single serializer: it assigns any missing
		// block IDs, weaves preserved unmanaged lines around the managed
		// blocks, and emits the canonical per-block format.
		newContent := parser.RenderFileContent(blocks, body, frontmatter, a.spacesPerTab)

		a.tracker.RegisterWrite(filePath)

		if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
			writeErr = err
			return
		}

		parsedBlocks, meta, _, _, err := parser.ParseFileContent(newContent, safeNotebook, safeSection, safePage, safeFileDate, a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, parsedBlocks, meta.Tags, meta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("SaveFileBlocks: IndexFileBlocks failed for %s/%s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, meta.Date, idxErr)
			}
		}
	})

	if writeErr != nil {
		return writeErr
	}
	// Notify live embeds/references that the saved blocks changed.
	for _, b := range blocks {
		if b.ID != "" {
			a.emitBlockChanged(b.ID, safeNotebook, safeSection, safePage, safeFileDate)
		}
	}
	return nil
}

// maxPluginQueryRows caps the number of rows returned by PluginRawQuery so a
// plugin can't exhaust frontend memory with an unbounded SELECT.
const maxPluginQueryRows = 5000

// --- Plugin SDK bindings -------------------------------------------------

// openPluginRODB lazily opens a read-only handle to the same on-disk index
// (or the in-memory shared cache before a vault is open) for use by
// PluginRawQuery. The handle is capped at one connection to match the main
// DB's pool size. query_only=ON causes SQLite to reject any write at the
// engine level — the primary guarantee that plugins can't mutate the index.
// On success the handle is cached; on failure it is NOT cached — the next
// call retries — so a transient startup error doesn't permanently break
// plugin queries. On a vault switch (CloseVault) the cached handle is closed
// and the next call re-opens against the new vault's index.
func (a *App) openPluginRODB() (*sql.DB, error) {
	a.pluginRODBMu.Lock()
	defer a.pluginRODBMu.Unlock()
	if a.pluginRODB != nil {
		return a.pluginRODB, nil
	}
	dsn := "file::memory:?cache=shared"
	if a.db != nil && a.db.IsOnDisk() {
		dsn = a.db.Path()
	}
	ro, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open read-only plugin DB: %w", err)
	}
	ro.SetMaxOpenConns(1)
	if _, err := ro.Exec("PRAGMA query_only = ON"); err != nil {
		ro.Close()
		return nil, fmt.Errorf("failed to enable query_only on plugin DB: %w", err)
	}
	a.pluginRODB = ro
	return ro, nil
}

// stripSQLComments removes leading SQL line ("--") and block ("/* ... */")
// comments and surrounding whitespace. The result is then checked against
// the SELECT/WITH prefix list. This is intentionally narrow: a real SQL
// parser would be a heavier dependency for a defense-in-depth check, and the
// connection-level read-only mode is the primary guarantee.
func stripSQLComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "--") {
			if idx := strings.IndexByte(s, '\n'); idx >= 0 {
				s = s[idx+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if idx := strings.Index(s, "*/"); idx >= 0 {
				s = s[idx+2:]
				continue
			}
			return ""
		}
		return s
	}
}

// PluginRawQuery runs a read-only SQL query against the in-memory index.
// Only SELECT / WITH statements are permitted; anything else is rejected so a
// plugin can never mutate the index or schema through this hook. The query
// is also executed against a connection with `PRAGMA query_only = ON`, which
// makes the engine reject any write attempt (including stacked queries like
// `SELECT 1; DROP TABLE blocks;`) regardless of how the prefix check is
// bypassed. Results are returned as a slice of column→value maps.
func (a *App) PluginRawQuery(sqlText string, params []any) ([]map[string]any, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}
	trimmed := stripSQLComments(sqlText)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return nil, fmt.Errorf("PluginRawQuery permits only SELECT/WITH statements")
	}

	roDB, err := a.openPluginRODB()
	if err != nil {
		return nil, err
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var out []map[string]any
	err = a.coordinator.WithDBReadResult(func() error {
		rows, err := roDB.Query(trimmed, params...)
		if err != nil {
			return err
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			row := make(map[string]any, len(cols))
			for i, c := range cols {
				row[c] = values[i]
			}
			out = append(out, row)
			// Cap the result set so a malicious plugin can't exhaust memory
			// with SELECT * FROM blocks on a large vault.
			if len(out) >= maxPluginQueryRows {
				break
			}
		}
		return rows.Err()
	})
	return out, err
}

// PluginMutateBlock wraps MutateBlock for the plugin SDK, returning success.
func (a *App) PluginMutateBlock(blockID, newText string) (bool, error) {
	if err := a.MutateBlock(blockID, newText); err != nil {
		return false, err
	}
	return true, nil
}

// PluginUpdateBlockState wraps UpdateBlockState for the plugin SDK.
func (a *App) PluginUpdateBlockState(blockID, status string) (bool, error) {
	if err := a.UpdateBlockState(blockID, status); err != nil {
		return false, err
	}
	return true, nil
}

// pluginConfigSchema mirrors the relevant fields of .system/config.yaml.
type pluginConfigSchema struct {
	Plugins struct {
		Active   []string              `yaml:"active"`
		Disabled []string              `yaml:"disabled"`
		Settings map[string]any        `yaml:"plugin_settings"`
	} `yaml:"plugins"`
}

// GetPluginRegistry parses the `plugins:` block of .system/config.yaml.
func (a *App) GetPluginRegistry() (parser.PluginRegistry, error) {
	registry := parser.PluginRegistry{Active: []string{}, Disabled: []string{}}
	if a.vaultPath == "" {
		return registry, fmt.Errorf("vault not loaded")
	}
	configPath := filepath.Join(a.vaultPath, ".system", "config.yaml")
	bytes, err := os.ReadFile(configPath)
	if err != nil {
		// No config file → empty registry (no plugins active).
		return registry, nil
	}
	var schema pluginConfigSchema
	if err := yaml.Unmarshal(bytes, &schema); err != nil {
		return registry, fmt.Errorf("failed to parse config.yaml: %w", err)
	}
	registry.Active = schema.Plugins.Active
	registry.Disabled = schema.Plugins.Disabled
	registry.Settings = schema.Plugins.Settings
	if registry.Active == nil {
		registry.Active = []string{}
	}
	if registry.Disabled == nil {
		registry.Disabled = []string{}
	}
	return registry, nil
}

// ListPlugins enumerates plugin folders under .system/plugins/, surfacing
// manifest name/version and the disabled sentinel for the manager UI.
func (a *App) ListPlugins() ([]parser.PluginInfo, error) {
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	pluginsDir := filepath.Join(a.vaultPath, ".system", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []parser.PluginInfo{}, nil
		}
		return nil, fmt.Errorf("failed to read plugins dir: %w", err)
	}
	var infos []parser.PluginInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dir := filepath.Join(pluginsDir, name)
		info := parser.PluginInfo{ID: name, Disabled: plugins.IsDisabled(dir)}
		if manifestBytes, err := os.ReadFile(filepath.Join(dir, "plugin.json")); err == nil {
			info.HasManifest = true
			var m parser.PluginManifest
			if json.Unmarshal(manifestBytes, &m) == nil {
				info.Name = m.Name
				info.Version = m.Version
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "index.js")); err == nil {
			info.HasIndex = true
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// ReadPluginSource returns the ESM source of a plugin's index.js for the
// dynamic loader.
func (a *App) ReadPluginSource(pluginID string) (string, error) {
	safeID := sanitizePathSegment(pluginID)
	if safeID == "" {
		return "", fmt.Errorf("invalid plugin id")
	}
	srcPath := filepath.Join(a.vaultPath, ".system", "plugins", safeID, "index.js")
	if !isPathWithinVault(srcPath, a.vaultPath) {
		return "", fmt.Errorf("path escapes vault")
	}
	bytes, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("failed to read plugin source: %w", err)
	}
	return string(bytes), nil
}

// --- Plugin install / uninstall (.silt-plugin) ---------------------------

// ValidatePluginArchive validates a .silt-plugin file without installing it,
// returning its manifest and any non-fatal warnings.
func (a *App) ValidatePluginArchive(archivePath string) (parser.PluginManifest, []string, error) {
	manifest, warnings, err := plugins.Validate(archivePath)
	if err != nil {
		return parser.PluginManifest{}, warnings, err
	}
	if verr := enforceMinVersion(manifest.MinSiltVersion); verr != nil {
		return parser.PluginManifest{}, warnings, verr
	}
	return manifestToParser(manifest), warnings, nil
}

// PickPluginArchive opens the native file picker (filtered to .silt-plugin)
// and returns the chosen path, or empty string if cancelled.
func (a *App) PickPluginArchive() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	selected, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select a .silt-plugin package",
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Plugin (*.silt-plugin)", Pattern: "*.silt-plugin"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file picker: %w", err)
	}
	return selected, nil
}

// InstallPlugin installs a .silt-plugin archive into .system/plugins/<id>/,
// emits plugins:changed so the loader re-runs, and returns the manifest.
func (a *App) InstallPlugin(archivePath string) (parser.PluginManifest, error) {
	if a.vaultPath == "" {
		return parser.PluginManifest{}, fmt.Errorf("vault not loaded")
	}
	manifest, err := plugins.Install(a.vaultPath, archivePath)
	if err != nil {
		return parser.PluginManifest{}, err
	}
	if verr := enforceMinVersion(manifest.MinSiltVersion); verr != nil {
		// Roll back the install since the version requirement isn't met.
		_ = plugins.Uninstall(a.vaultPath, manifest.ID)
		return parser.PluginManifest{}, verr
	}
	a.emitPluginsChanged()
	return manifestToParser(manifest), nil
}

// UninstallPlugin removes a plugin folder and emits plugins:changed.
func (a *App) UninstallPlugin(pluginID string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if err := plugins.Uninstall(a.vaultPath, pluginID); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

// EnablePlugin / DisablePlugin toggle a per-plugin ".disabled" sentinel
// (the loader skips disabled plugins), then emit plugins:changed.
func (a *App) EnablePlugin(pluginID string) error {
	if err := plugins.SetDisabled(a.vaultPath, pluginID, false); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

func (a *App) DisablePlugin(pluginID string) error {
	if err := plugins.SetDisabled(a.vaultPath, pluginID, true); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

func (a *App) emitPluginsChanged() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "plugins:changed", struct{}{})
}

// enforceMinVersion rejects a plugin whose minSiltVersion exceeds the current
// app version (semver-style segment-by-segment comparison).
func enforceMinVersion(minSiltVersion string) error {
	if minSiltVersion == "" {
		return nil
	}
	if versionLessThan(appVersion, minSiltVersion) {
		return fmt.Errorf("plugin requires Silt %s or later (current: %s)", minSiltVersion, appVersion)
	}
	return nil
}

func versionLessThan(a, b string) bool {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		ai, _ := strconv.Atoi(ap[i])
		bi, _ := strconv.Atoi(bp[i])
		if ai < bi {
			return true
		}
		if ai > bi {
			return false
		}
	}
	return len(ap) < len(bp) // shorter = older if all segments equal so far
}

func manifestToParser(m plugins.Manifest) parser.PluginManifest {
	return parser.PluginManifest{
		ID:             m.ID,
		Name:           m.Name,
		Version:        m.Version,
		Author:         m.Author,
		Description:    m.Description,
		Icon:           m.Icon,
		Main:           m.Main,
		MinSiltVersion: m.MinSiltVersion,
	}
}
