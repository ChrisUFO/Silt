package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"silt/backend/config"
	"silt/backend/core"
	"silt/backend/db"
	"silt/backend/monitor"
	"silt/backend/parser"
	"silt/backend/plugins"
	"silt/backend/templates"
	"silt/backend/themes"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2/pkg/runtime"
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

	// cfg is the parsed .system/config.yaml, the single source of truth for
	// non-vault-path settings. configMu guards it; it is replaced wholesale on
	// reload (never mutated in place) so a struct read under RLock is a safe
	// snapshot even though its map/slice fields share references.
	cfg           config.SystemConfig
	configMu      sync.RWMutex
	configWatcher *config.ConfigWatcher
	// configLoadErr holds the initial config.yaml load error, if any. The
	// startup load runs before the frontend subscribes to config:error, so
	// that event is typically lost; GetConfigLoadError surfaces this one-shot.
	configLoadErr error

	// templateWatcher hot-reloads <vault>/.system/templates/ so the picker
	// stays live when a user adds/edits/deletes a custom template externally.
	// Started in initializeVaultServices, stopped in teardownVaultServices
	// (mirrors configWatcher).
	templateWatcher *templates.TemplateWatcher

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
	// Share the exact teardown path with CloseVault so both nil every
	// service field. Nilling here matters: if a "change vault" IPC lands
	// during OS-driven close (race), CloseVault's nothing-to-close guard
	// sees the nil'd fields and becomes a no-op instead of double-closing
	// already-closed handles.
	a.teardownVaultServices()
}

// teardownVaultServices closes and nils every vault-scoped service in the
// reverse order of initializeVaultServices. Shared by shutdown (app exit)
// and CloseVault (workspace switch) so the two paths can't drift. Safe to
// call when services are already nil (each close is guarded).
func (a *App) teardownVaultServices() {
	if a.watcher != nil {
		// Drop every focus lease before tearing the watcher down so a clean
		// exit can't strand a file under fsnotify suppression (#38).
		a.watcher.ReleaseAllFocus()
		_ = a.watcher.Close()
		a.watcher = nil
	}
	if a.templateWatcher != nil {
		_ = a.templateWatcher.Close()
		a.templateWatcher = nil
	}
	if a.configWatcher != nil {
		_ = a.configWatcher.Close()
		a.configWatcher = nil
	}
	if a.tracker != nil {
		a.tracker.Stop()
		a.tracker = nil
	}
	// Close the read-only plugin handle too (it points at the closing index).
	a.pluginRODBMu.Lock()
	if a.pluginRODB != nil {
		_ = a.pluginRODB.Close()
		a.pluginRODB = nil
	}
	a.pluginRODBMu.Unlock()
	if a.db != nil {
		// Close runs PRAGMA wal_checkpoint(TRUNCATE) so the WAL is merged
		// into the main index file on a clean close (#29).
		_ = a.db.Close()
		a.db = nil
	}
	a.coordinator = nil
	a.vaultPath = ""
}

// CloseVault tears down the active vault's services in the reverse order of
// initializeVaultServices (via the shared teardownVaultServices helper).
// After it returns, IsVaultInitialized is false so the UI re-shows the
// onboarding screen. It does NOT clear the saved settings.json path — the
// user can re-open the same vault via InitializeVault / a new selection.
// Idempotent: safe to call when no vault is open. Waits on any in-flight
// Wails-bound calls (a.wg) so a close can't race an in-progress write.
func (a *App) CloseVault() error {
	a.wg.Add(1)
	defer a.wg.Done()

	if a.vaultPath == "" && a.db == nil {
		return nil // nothing to close
	}
	a.teardownVaultServices()
	return nil
}

func (a *App) initializeVaultServices(vaultPath string) error {
	// Load system config first: its editor.tab_indent_spaces drives
	// ScanWorkspace and every subsequent parse, so it must be applied before
	// the initial index is built. A missing/invalid config is non-fatal —
	// defaults keep the vault usable — but a parse error is surfaced.
	cfg, cfgErr := config.Load(vaultPath)
	if cfgErr != nil && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "config:error", cfgErr.Error())
	}
	a.applyConfigLocked(cfg) // sets a.cfg + a.spacesPerTab before scanning
	// The config:error event above fires before the frontend mounts and
	// subscribes, so it is typically lost. Stash the error for
	// GetConfigLoadError() to surface on the frontend's initial loadConfig().
	a.configMu.Lock()
	a.configLoadErr = cfgErr
	a.configMu.Unlock()

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

	// Migrate old per-day file model: <page>/<date>.md → <page>.md.
	// Runs before the scan so the indexer sees the new model. Idempotent.
	migrationWarnings := migratePerDayFiles(vaultPath, a.spacesPerTab)

	results, walkWarnings, err := parser.ScanWorkspace(vaultPath, a.spacesPerTab)
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

	// indexedCount = files that passed metadata validation and were actually
	// written to the index (NOT len(changed); errored/unresolvable files in
	// `changed` are reported in `skipped` and excluded from this count). Used
	// below to decide whether a post-index WAL checkpoint is worth running.
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
	// Surface walk-level warnings (symlink skips, permission errors) from #32.
	allWarnings = append(allWarnings, walkWarnings...)
	allWarnings = append(allWarnings, migrationWarnings...)

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

	// Start hot-reload of .system/config.yaml. External edits re-parse and
	// emit config:changed without a restart (SPECS.md §9.2). Silt's own
	// SaveSystemWrite is ignored via the watcher's self-loop tracker.
	if a.ctx != nil {
		cw, wErr := config.NewConfigWatcher(vaultPath,
			func(reloaded config.SystemConfig) { a.applyConfig(reloaded) },
			func(e error) { runtime.EventsEmit(a.ctx, "config:error", e.Error()) })
		if wErr != nil {
			log.Printf("config watcher disabled: %v", wErr)
		} else {
			cw.Start()
			a.configWatcher = cw
		}
	}

	// Start hot-reload of .system/templates/ so the picker stays live when a
	// user adds/edits/deletes a custom template externally (the same posture
	// as the config and theme watchers). The onChange callback invalidates the
	// cache and emits templates:changed; the frontend store re-lists.
	if a.ctx != nil {
		tw, tErr := templates.NewTemplateWatcher(a.templatesDir(), func() {
			templates.InvalidateTemplateCache()
			runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
		})
		if tErr != nil {
			log.Printf("template watcher disabled: %v", tErr)
		} else {
			tw.Start()
			a.templateWatcher = tw
		}
	}

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

// GetAppVersion returns the Silt version (embedded from the VERSION file at
// build time). Surfaced for the About tab and plugin minSiltVersion checks.
func (a *App) GetAppVersion() string {
	return appVersion
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

// FetchPageBlocks returns a flat list of all blocks for a page, ordered by
// line_number. With the per-day file model removed, a page is a single file;
// each block carries its own file_date. This replaces FetchPageTimeline for
// the editor surface (which renders one TipTap editor per page, not per day).
func (a *App) FetchPageBlocks(notebook, section, page string) ([]parser.ParsedBlock, error) {
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []parser.ParsedBlock
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.FetchPageBlocks(notebook, section, page)
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

	var notebook, section, page, blockType string
	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow("SELECT notebook, section, page, type FROM blocks WHERE id = ?", blockID)
		return row.Scan(&notebook, &section, &page, &blockType)
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}

	if blockType != string(parser.BlockTask) {
		return fmt.Errorf("block %s is not a task", blockID)
	}

	// Defense-in-depth against path traversal: notebook/section/page originate
	// from user-editable YAML frontmatter. Section may be empty (a page living
	// directly under its notebook).
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid file metadata for block %s: notebook=%q section=%q page=%q", blockID, notebook, section, page)
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
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
		// Use the file's modification time as the default date for blocks
		// whose comment lacks a @ date suffix — matches the scanner's behavior.
		// Using time.Now() here would silently shift old blocks' dates to today.
		fileDate := fileOrDefaultDate(filePath)
		parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, fileDate, a.spacesPerTab)
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
			today := time.Now().Format("2006-01-02")
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(today))
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
				idxErr = a.db.IndexFileBlocks(remeta.Notebook, remeta.Section, remeta.Page, blocks, remeta.Tags, remeta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("UpdateBlockState: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
			}
		}
	})

	if writeErr != nil {
		return writeErr
	}
	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, "")
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
//
// The on-disk theme scan happens exactly once (per #76): themes.LoadByID
// reads the themesDir and returns the parsed theme in a single pass. The
// previous implementation called ListThemes (reads + parses every file)
// followed by ResolveActive (reads the directory a second time to find the
// same theme), so every switch did two directory scans + 2N parses.
func (a *App) ApplyTheme(id, mode string) (ActiveThemeResult, error) {
	if !vault.ValidThemeMode(mode) {
		return ActiveThemeResult{}, fmt.Errorf("invalid mode %q (valid: dark, light, system)", mode)
	}
	// Resolve the requested theme in one pass. The embedded default is
	// always available; any other id must live on disk. A typo or stale id
	// errors here rather than silently snapping to the default.
	var (
		t   *themes.Theme
		err error
	)
	if id == themes.DefaultThemeID {
		t, err = themes.ParseDefault()
		if err != nil {
			return ActiveThemeResult{}, err
		}
	} else {
		var found bool
		t, found, err = themes.LoadByID(a.themesDir(), id)
		if err != nil {
			return ActiveThemeResult{}, fmt.Errorf("failed to look up theme %q: %w", id, err)
		}
		if !found {
			return ActiveThemeResult{}, fmt.Errorf("theme %q is not available", id)
		}
	}

	// Persist the selection atomically. Use the actually-resolved theme id
	// (t.ID) rather than the requested id: if the caller requested the
	// embedded default and the file vanished mid-request, settings stays
	// consistent with what is rendered.
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
	log.Printf("themes: ApplyTheme(id=%q mode=%q) → resolved %q", id, mode, t.ID)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "theme:changed", map[string]string{
			"id": t.ID, "mode": mode,
		})
	}
	return res, nil
}

// PickThemeFile opens the native file picker (filtered to *.json) and
// returns the chosen path. The empty string means the user cancelled. The
// frontend feeds the returned path to ImportTheme — the backend does all
// validation and writing, so the frontend never touches the filesystem
// directly.
func (a *App) PickThemeFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	selected, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select a theme JSON",
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Theme (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file picker: %w", err)
	}
	return selected, nil
}

// ImportTheme validates a theme JSON at srcPath, namespaces its id to
// avoid collisions with built-ins / already-imported themes, and writes
// it atomically to <vault>/.system/themes/. The shared validator
// (themes.ParseAndValidate) is the same call the loader uses, so a
// successfully imported theme is the exact same object ListThemes will
// enumerate on the next picker refresh.
//
// On success the Wails-bound event "themes:changed" is emitted so any
// subscribed frontend (the picker, future command palette, etc.)
// re-fetches the listing immediately. The active theme is NOT changed:
// a fresh import is unselected until the user picks it.
//
// The in-process theme cache (#73) is invalidated so a launch-time
// background-color resolution that runs after the import will pick up
// the new file instead of a stale parse.
func (a *App) ImportTheme(srcPath string) (*themes.ImportResult, error) {
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	res, err := themes.ImportThemeFromPath(a.themesDir(), srcPath)
	if err != nil {
		log.Printf("themes: ImportTheme(%q) failed: %v", filepath.Base(srcPath), err)
		return nil, err
	}
	if len(res.ValidationErrors) > 0 {
		log.Printf("themes: ImportTheme(%q) rejected: %d validation error(s)", filepath.Base(srcPath), len(res.ValidationErrors))
		return res, nil
	}
	log.Printf("themes: ImportTheme(%q) → imported as %q (renamed=%v)", filepath.Base(srcPath), res.Info.ID, res.Renamed)
	themes.InvalidateThemeCache(res.Info.ID)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "themes:changed", struct{}{})
	}
	return res, nil
}

// PickExportPath opens the native save-file dialog (filtered to *.json)
// and returns the chosen path. The empty string means the user
// cancelled. The frontend feeds the returned path to ExportActiveTheme.
// defaultFilename is offered as the initial file name (e.g.
// "<theme-id>.json"); pass "" to let the OS pick a default.
func (a *App) PickExportPath(defaultFilename string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:            "Export active theme",
		DefaultFilename:  defaultFilename,
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Theme (*.json)", Pattern: "*.json"},
		},
	})
}

// ExportActiveTheme writes the currently active theme verbatim to
// dstPath as JSON, so the user can round-trip edit it (and re-import).
// The active id is read from AppSettings; the embedded default ships
// even when the on-disk copy is missing.
func (a *App) ExportActiveTheme(dstPath string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	settings, err := vault.LoadSettings()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	return themes.ExportThemeToPath(a.themesDir(), settings.ActiveTheme, dstPath)
}

// templatesDir returns the on-disk user-template directory, mirroring themesDir.
// Returns "" when no vault is open (the embedded set is still served).
func (a *App) templatesDir() string {
	if a.vaultPath == "" {
		return ""
	}
	return filepath.Join(a.vaultPath, ".system", "templates")
}

// ListTemplates enumerates available templates (on-disk user templates + the
// embedded first-class set, deduped with on-disk winning) and any per-file
// load errors. Works before a vault is open (returns just the embedded set,
// mirroring ListThemes).
func (a *App) ListTemplates() (*templates.ListTemplatesResult, error) {
	a.wg.Add(1)
	defer a.wg.Done()
	return templates.ListTemplates(a.templatesDir())
}

// GetTemplate resolves a single template by id (on-disk then embedded) and
// returns the full Template including Body. Used by the picker to render a
// live preview + drive the placeholder form. Returns a user-facing error when
// the id is on neither tier.
func (a *App) GetTemplate(id string) (templates.Template, error) {
	a.wg.Add(1)
	defer a.wg.Done()
	if id == "" {
		return templates.Template{}, fmt.Errorf("template id is required")
	}
	t, err := templates.CachedGetTemplate(a.templatesDir(), id)
	if err != nil {
		return templates.Template{}, err
	}
	return *t, nil
}

// RenderTemplate renders the template with the given id, substituting the four
// default placeholders (date/time/iso_date/weekday from the current local time)
// plus any caller-supplied vars. Smart-graph syntax ({{embed:uuid}}, ((uuid)))
// passes through untouched. Non-fatal warnings (unknown placeholders) are
// logged, not returned — Wails exposes only the first non-error return value,
// and the picker preview intentionally ignores forward-compat warnings.
func (a *App) RenderTemplate(id string, vars map[string]string) (string, error) {
	a.wg.Add(1)
	defer a.wg.Done()
	if id == "" {
		return "", fmt.Errorf("template id is required")
	}
	t, err := templates.CachedGetTemplate(a.templatesDir(), id)
	if err != nil {
		return "", err
	}
	rendered, warnings := templates.Render(t, vars, templates.RenderOptions{})
	for _, w := range warnings {
		log.Printf("templates: RenderTemplate(%q) warning: %s", id, w)
	}
	return rendered, nil
}

// SaveUserTemplate validates t, rejects any builtin:// id (read-only), and
// writes the canonical form atomically to <vault>/.system/templates/<id>.md.
// The template watcher's self-write window is armed so the resulting fsnotify
// events do not trigger a redundant reload. Emits templates:changed so the
// picker re-lists immediately. Mirrors App.ImportTheme.
func (a *App) SaveUserTemplate(t templates.Template) error {
	a.wg.Add(1)
	defer a.wg.Done()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if a.templateWatcher != nil {
		a.templateWatcher.RegisterSelfWrite()
	}
	if a.tracker != nil {
		a.tracker.RegisterWrite(filepath.Join(a.templatesDir(), t.ID+".md"))
	}
	if err := templates.SaveTemplate(a.templatesDir(), &t); err != nil {
		log.Printf("templates: SaveUserTemplate(%q) failed: %v", t.ID, err)
		return err
	}
	templates.InvalidateTemplateCache(t.ID)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
	}
	log.Printf("templates: SaveUserTemplate → saved %q", t.ID)
	return nil
}

// DeleteUserTemplate removes the on-disk user template with the given id.
// Builtin ids are rejected (read-only). Emits templates:changed. Idempotent
// (deleting an already-deleted template is a no-op success).
func (a *App) DeleteUserTemplate(id string) error {
	a.wg.Add(1)
	defer a.wg.Done()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if a.templateWatcher != nil {
		a.templateWatcher.RegisterSelfWrite()
	}
	if a.tracker != nil {
		a.tracker.RegisterWrite(filepath.Join(a.templatesDir(), id+".md"))
	}
	if err := templates.DeleteTemplate(a.templatesDir(), id); err != nil {
		log.Printf("templates: DeleteUserTemplate(%q) failed: %v", id, err)
		return err
	}
	templates.InvalidateTemplateCache(id)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
	}
	log.Printf("templates: DeleteUserTemplate → removed %q", id)
	return nil
}

// ReloadTemplates forces a re-scan of the templates directory + cache flush.
// Used by the template watcher's onChange callback (external edit detected) and
// available as a manual refresh affordance. Emits templates:changed.
func (a *App) ReloadTemplates() error {
	a.wg.Add(1)
	defer a.wg.Done()
	templates.InvalidateTemplateCache()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
	}
	return nil
}

// CreatePageFromTemplate creates a new page pre-filled with a rendered
// template's body. It composes with the existing CreatePage write path: render
// the template, prepend the standard frontmatter (SPECS §3.3), write atomically
// (temp + rename, SPECS §7.1) under the file-write lock + self-write tracker,
// and index the resulting blocks via ParseFileContent so task/embed/tag
// pipelines pick them up immediately. Returns the resolved date string.
func (a *App) CreatePageFromTemplate(notebook, section, page, dateStr, templateID string, vars map[string]string) (string, error) {
	if a.vaultPath == "" || a.db == nil {
		return "", fmt.Errorf("vault not loaded")
	}
	if templateID == "" {
		return "", fmt.Errorf("template id is required")
	}
	t, err := templates.CachedGetTemplate(a.templatesDir(), templateID)
	if err != nil {
		return "", err
	}
	rendered, warnings := templates.Render(t, vars, templates.RenderOptions{})
	for _, w := range warnings {
		log.Printf("templates: CreatePageFromTemplate(%q) warning: %s", templateID, w)
	}

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

	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return "", fmt.Errorf("path escapes vault")
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directory: %w", err)
	}
	if _, err := os.Stat(filePath); err == nil {
		return safeDate, nil // already exists — don't clobber
	}

	scaffoldFrontmatter := fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n",
		strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(safeDate))
	content := scaffoldFrontmatter + rendered

	a.wg.Add(1)
	defer a.wg.Done()

	var writeErr error
	a.coordinator.LockFileWrite(filePath, func() {
		a.tracker.RegisterWrite(filePath)
		if err := parser.WriteFileAtomic(filePath, []byte(content)); err != nil {
			writeErr = err
			return
		}
		blocks, meta, _, _, perr := parser.ParseFileContent(content, safeNotebook, safeSection, safePage, safeDate, a.spacesPerTab)
		if perr == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("CreatePageFromTemplate: IndexFileBlocks failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
			}
		}
	})
	if writeErr != nil {
		return "", fmt.Errorf("failed to write templated page: %w", writeErr)
	}
	return safeDate, nil
}

// RenderTemplateBlocks renders the template with the given id and parses the
// rendered Markdown into ParsedBlocks for the "insert at cursor" flow. Each
// call produces blocks with fresh UUIDs (the rendered body has no <!-- id: -->
// comments, so EnsureBlockID mints new ones), so inserting the same template
// twice never collides in the blocks-table PK. The frontend converts the
// returned blocks via blocksToDoc() → editor.commands.insertContent; the
// UniqueBlockIds extension also guards against any residual collision.
func (a *App) RenderTemplateBlocks(id string, vars map[string]string) ([]parser.ParsedBlock, error) {
	a.wg.Add(1)
	defer a.wg.Done()
	if id == "" {
		return nil, fmt.Errorf("template id is required")
	}
	t, err := templates.CachedGetTemplate(a.templatesDir(), id)
	if err != nil {
		return nil, err
	}
	rendered, warnings := templates.Render(t, vars, templates.RenderOptions{})
	for _, w := range warnings {
		log.Printf("templates: RenderTemplateBlocks(%q) warning: %s", id, w)
	}
	spaces := a.spacesPerTab
	if spaces <= 0 {
		spaces = 4
	}
	blocks, _, _, _, perr := parser.ParseFileContent(rendered, "", "", "", "", spaces)
	if perr != nil {
		return nil, fmt.Errorf("failed to parse rendered template %q: %w", id, perr)
	}
	return blocks, nil
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

	var notebook, section, page string
	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow("SELECT notebook, section, page FROM blocks WHERE id = ?", blockID)
		return row.Scan(&notebook, &section, &page)
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid file metadata for block %s", blockID)
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
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
		// Use the file's modification time as the default date for blocks
		// whose comment lacks a @ date suffix — matches the scanner's behavior.
		fileDate := fileOrDefaultDate(filePath)
		parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, fileDate, a.spacesPerTab)
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
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(time.Now().Format("2006-01-02")))
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
				idxErr = a.db.IndexFileBlocks(remeta.Notebook, remeta.Section, remeta.Page, reblocks, remeta.Tags, remeta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("MutateBlock: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
			}
		}
	})
	if writeErr != nil {
		return writeErr
	}

	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, "")
	return nil
}

// fileOrDefaultDate returns the file's modification date (YYYY-MM-DD), falling
// back to today if the stat fails. Used consistently by SaveFileBlocks,
// MutateBlock, and UpdateBlockState as the defaultDate passed to
// ParseFileContent — ensures old blocks without a @ date suffix inherit the
// file's actual mtime rather than silently shifting to today.
func fileOrDefaultDate(filePath string) string {
	if fi, err := os.Stat(filePath); err == nil {
		return fi.ModTime().Format("2006-01-02")
	}
	return time.Now().Format("2006-01-02")
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
			name := d1.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !d1.IsDir() {
				if !strings.EqualFold(filepath.Ext(name), ".md") {
					continue
				}
				pageName := strings.TrimSuffix(name, filepath.Ext(name))
				secPages[""] = append(secPages[""], parser.NavigationPage{
					Name:  pageName,
					Count: counts[nspKey{nbName, "", pageName}],
				})
				continue
			}
			d1Path := filepath.Join(nbPath, name)
			if _, ok := secPages[name]; !ok {
				secPages[name] = []parser.NavigationPage{}
			}
			subs, err := os.ReadDir(d1Path)
			if err != nil {
				continue
			}
			for _, d2 := range subs {
				if d2.IsDir() || strings.HasPrefix(d2.Name(), ".") {
					continue
				}
				if !strings.EqualFold(filepath.Ext(d2.Name()), ".md") {
					continue
				}
				pageName := strings.TrimSuffix(d2.Name(), filepath.Ext(d2.Name()))
				secPages[name] = append(secPages[name], parser.NavigationPage{
					Name:  pageName,
					Count: counts[nspKey{nbName, name, pageName}],
				})
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

// SearchBlocks fuzzy searches blocks and headings matching the query. Returns
// the first page (offset 0, limit 50) of FTS5-ranked results for backwards
// compatibility with the original binding; the Svelte search modal that needs
// pagination/snippets calls SearchBlocksPaged instead.
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

// SearchBlocksPaged runs the FTS5 search and returns a ranked, paginated
// envelope with highlighted snippets, the total match count, and a HasMore
// flag. offset/limit control the page (defaults applied by the caller).
func (a *App) SearchBlocksPaged(query string, offset, limit int) (parser.SearchResult, error) {
	if a.db == nil {
		return parser.SearchResult{}, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res parser.SearchResult
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.SearchBlocksPaged(query, offset, limit)
	})

	return res, err
}

// AcquireFocusLock registers a focus lock on a page file to ignore fsnotify updates.
func (a *App) AcquireFocusLock(notebook, section, page string) error {
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid path metadata")
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	a.watcher.LockFocus(filePath)
	return nil
}

// ReleaseFocusLock removes a focus lock from a page file.
func (a *App) ReleaseFocusLock(notebook, section, page string) error {
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid path metadata")
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	a.watcher.UnlockFocus(filePath)
	return nil
}

// RefreshFocusLock extends an existing focus lease for a page file. Called by the
// Svelte editor's heartbeat while it stays focused (#38); a no-op if the
// lease already expired (the editor must re-acquire).
func (a *App) RefreshFocusLock(notebook, section, page string) error {
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid path metadata")
	}
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	a.watcher.RefreshFocus(filePath)
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

	// New file model: a page IS a file at <vault>/<notebook>/[<section>/]<page>.md
	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
	if !isPathWithinVault(filePath, a.vaultPath) {
		return "", fmt.Errorf("path escapes vault")
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
				idxErr = a.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
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
// With the per-day file model removed, a page is a single file at
// <vault>/<notebook>/<section>/<page>.md. Each block carries its own file_date.
func (a *App) SaveFileBlocks(notebook, section, page string, blocks []parser.ParsedBlock) error {
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid path metadata")
	}

	filePath := filepath.Join(a.vaultPath, safeNotebook, safeSection, safePage+".md")
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
			today := time.Now().Format("2006-01-02")
			frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(today))
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

		parsedBlocks, meta, _, _, err := parser.ParseFileContent(newContent, safeNotebook, safeSection, safePage, fileOrDefaultDate(filePath), a.spacesPerTab)
		if err == nil {
			var idxErr error
			a.coordinator.WithDBWrite(func() {
				idxErr = a.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, parsedBlocks, meta.Tags, meta.Warnings...)
			})
			if idxErr != nil {
				log.Printf("SaveFileBlocks: IndexFileBlocks failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
			}
		}
	})

	if writeErr != nil {
		return writeErr
	}
	// Notify live embeds/references that the saved blocks changed.
	for _, b := range blocks {
		if b.ID != "" {
			a.emitBlockChanged(b.ID, safeNotebook, safeSection, safePage, b.FileDate)
		}
	}
	return nil
}

// maxPluginQueryRows caps the number of rows returned by PluginRawQuery so a
// plugin can't exhaust frontend memory with an unbounded SELECT.
const maxPluginQueryRows = 5000

// migratePerDayFiles converts old-model per-day files (<page>/<date>.md) into
// the new single-file-per-page model (<page>.md). For each directory that
// contains files matching YYYY-MM-DD.md:
//  1. Read all date files sorted by filename (= by date).
//  2. Parse each file's blocks, tagging each block with the original file's date.
//  3. Concatenate into a single document and render to <parent>/<dirname>.md.
//  4. Remove the old directory.
//
// Idempotent: if no per-date directories exist, this is a no-op. If the target
// <page>.md already exists, that directory is skipped (user may have migrated
// manually). Returns non-fatal warnings for the caller to surface.
func migratePerDayFiles(vaultPath string, spacesPerTab int) []string {
	var warnings []string

	rootAbs, err := filepath.Abs(vaultPath)
	if err != nil {
		return []string{fmt.Sprintf("migration: cannot resolve vault path: %v", err)}
	}

	// Collect directories that contain date-named .md files.
	type pageDir struct {
		dir      string
		dateFiles []string // sorted filenames
	}
	var pageDirs []pageDir

	_ = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("migration: cannot traverse %q: %v", path, walkErr))
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == rootAbs {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return nil
		}
		segments := strings.Split(filepath.ToSlash(rel), "/")
		if len(segments) < 2 {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		var dates []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if parser.DateFileRegex.MatchString(e.Name()) {
				dates = append(dates, e.Name())
			}
		}
		if len(dates) > 0 {
			sort.Strings(dates)
			pageDirs = append(pageDirs, pageDir{dir: path, dateFiles: dates})
		}
		return nil
	})

	for _, pd := range pageDirs {
		pageName := filepath.Base(pd.dir)
		targetPath := filepath.Join(filepath.Dir(pd.dir), pageName+".md")

		// Skip if the target already exists (user may have migrated).
		if _, err := os.Stat(targetPath); err == nil {
			warnings = append(warnings, fmt.Sprintf("migration: skipped %q — target %q already exists", pd.dir, targetPath))
			continue
		}

		var allBlocks []parser.ParsedBlock
		var frontmatter string
		for _, dateFile := range pd.dateFiles {
			dateFilePath := filepath.Join(pd.dir, dateFile)
			contentBytes, err := os.ReadFile(dateFilePath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("migration: cannot read %q: %v", dateFilePath, err))
				continue
			}
			dateStr := parser.DateFileRegex.FindStringSubmatch(dateFile)[1]
			notebook, section := "", ""
			relParts := strings.Split(strings.Trim(filepath.ToSlash(strings.TrimPrefix(pd.dir, rootAbs)), "/"), "/")
			if len(relParts) >= 1 {
				notebook = relParts[0]
				if len(relParts) > 2 {
					section = strings.Join(relParts[1:len(relParts)-1], "/")
				}
			}

			blocks, _, _, _, parseErr := parser.ParseFileContent(string(contentBytes), notebook, section, pageName, dateStr, spacesPerTab)
			if parseErr != nil {
				warnings = append(warnings, fmt.Sprintf("migration: parse error in %q: %v", dateFilePath, parseErr))
				continue
			}

			// Stamp each block with the original file's date.
			for i := range blocks {
				if blocks[i].FileDate == "" {
					blocks[i].FileDate = dateStr
				}
			}

			if frontmatter == "" {
				fm, _ := splitFrontmatter(string(contentBytes))
				if fm != "" {
					frontmatter = fm
				} else {
					frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n",
						strconv.Quote(notebook), strconv.Quote(section), strconv.Quote(pageName), strconv.Quote(dateStr))
				}
			}

			allBlocks = append(allBlocks, blocks...)
		}

		if len(allBlocks) == 0 {
			warnings = append(warnings, fmt.Sprintf("migration: no blocks found in %q, skipping", pd.dir))
			continue
		}

		// Render the merged content and write the new page file.
		mergedContent := parser.RenderFileContent(allBlocks, "", frontmatter, spacesPerTab)
		if err := parser.WriteFileAtomic(targetPath, []byte(mergedContent)); err != nil {
			warnings = append(warnings, fmt.Sprintf("migration: cannot write %q: %v", targetPath, err))
			continue
		}

		// Verify the merged file parses correctly before destroying the
		// originals. A partial/corrupt write must NOT trigger removal.
		verifyBlocks, _, _, _, verifyErr := parser.ParseFileContent(mergedContent, "", "", "", "", spacesPerTab)
		if verifyErr != nil || len(verifyBlocks) != len(allBlocks) {
			warnings = append(warnings, fmt.Sprintf("migration: verification failed for %q (%d/%d blocks) — keeping originals", targetPath, len(verifyBlocks), len(allBlocks)))
			_ = os.Remove(targetPath)
			continue
		}

		// Remove the migrated date files individually (verified safe).
		for _, dateFile := range pd.dateFiles {
			_ = os.Remove(filepath.Join(pd.dir, dateFile))
		}
		// Remove the old directory only if it is now empty.
		if err := os.Remove(pd.dir); err != nil {
			warnings = append(warnings, fmt.Sprintf("migration: wrote %q and removed migrated files, but kept directory %q (may contain other files): %v", targetPath, pd.dir, err))
		}

		warnings = append(warnings, fmt.Sprintf("migration: merged %d date files from %q into %q", len(pd.dateFiles), pd.dir, targetPath))
	}

	return warnings
}

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

// GetPluginRegistry returns the `plugins:` block of .system/config.yaml from
// the in-memory config (the single source of truth maintained by the config
// package + hot-reload watcher), so callers never re-read the file.
func (a *App) GetPluginRegistry() (parser.PluginRegistry, error) {
	registry := parser.PluginRegistry{Active: []string{}, Disabled: []string{}}
	if a.vaultPath == "" {
		return registry, fmt.Errorf("vault not loaded")
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	registry.Active = a.cfg.Plugins.Active
	registry.Disabled = a.cfg.Plugins.Disabled
	registry.Settings = a.cfg.Plugins.PluginSettings
	if registry.Active == nil {
		registry.Active = []string{}
	}
	if registry.Disabled == nil {
		registry.Disabled = []string{}
	}
	if registry.Settings == nil {
		registry.Settings = map[string]any{}
	}
	return registry, nil
}

// GetSystemConfig returns the parsed system config (a value copy under the
// read lock). The map/slice fields are shared references that are only ever
// replaced wholesale under the write lock, so they are safe to read/marshal
// after the lock is released.
func (a *App) GetSystemConfig() (config.SystemConfig, error) {
	if a.vaultPath == "" {
		return config.Defaults(), fmt.Errorf("vault not loaded")
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.cfg, nil
}

// GetConfigLoadError returns the error from the initial config.yaml load (if
// any) and clears it. The startup load runs before the frontend subscribes to
// config:error, so that event can be missed; this binding lets the frontend
// retrieve the one-shot error on its first loadConfig() so a broken config is
// surfaced rather than silently masked by Defaults(). Returns "" when there
// was no error (or it was already retrieved).
func (a *App) GetConfigLoadError() string {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.configLoadErr == nil {
		return ""
	}
	msg := a.configLoadErr.Error()
	a.configLoadErr = nil
	return msg
}

// SaveSystemConfig validates, persists atomically, and applies the new config.
// The self-write is registered first so the hot-reload watcher ignores the
// fsnotify event from our own atomic write.
func (a *App) SaveSystemConfig(cfg config.SystemConfig) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if cfg.Editor.TabIndentSpaces <= 0 {
		return fmt.Errorf("invalid config: editor.tab_indent_spaces must be positive")
	}
	if cfg.Editor.FontSizePx <= 0 {
		return fmt.Errorf("invalid config: editor.font_size_px must be positive")
	}
	if cfg.Editor.LineHeight <= 0 {
		return fmt.Errorf("invalid config: editor.line_height must be positive")
	}
	if cfg.Editor.AutoSaveDelayMs < 0 {
		return fmt.Errorf("invalid config: editor.auto_save_delay_ms must be non-negative")
	}
	if err := config.ValidateHotkeys(cfg.Hotkeys); err != nil {
		return err
	}
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, cfg); err != nil {
		return err
	}
	// Apply live Go-side knobs without emitting config:changed. The frontend
	// store already updates optimistically in saveConfig(); emitting here would
	// race the store's dirty flag and could spuriously flip pendingExternal.
	// External edits still flow through the watcher → applyConfig (with emit).
	a.applyConfigLocked(cfg)
	return nil
}

// applyConfig stores the parsed config under configMu, applies the live
// Go-side knobs (tab indent width), then emits config:changed so the frontend
// refreshes editor settings, hotkeys, and per-plugin settings.
func (a *App) applyConfig(cfg config.SystemConfig) {
	a.applyConfigLocked(cfg)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "config:changed", cfg)
	}
}

// applyConfigLocked updates a.cfg + live knobs under the write lock. Split out
// so initializeVaultServices can set the config (and spacesPerTab) before the
// first scan without emitting an event for a vault the frontend hasn't seen yet.
func (a *App) applyConfigLocked(cfg config.SystemConfig) {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	a.cfg = cfg
	if cfg.Editor.TabIndentSpaces > 0 {
		a.spacesPerTab = cfg.Editor.TabIndentSpaces
	}
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
				info.Author = m.Author
				info.Description = m.Description
				info.Icon = m.Icon
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
// returning its manifest and any non-fatal warnings bundled in a single struct
// (so both cross the Wails IPC boundary together).
func (a *App) ValidatePluginArchive(archivePath string) (parser.PluginValidationResult, error) {
	manifest, warnings, err := plugins.Validate(archivePath)
	if err != nil {
		return parser.PluginValidationResult{Warnings: warnings}, err
	}
	if verr := enforceMinVersion(manifest.MinSiltVersion); verr != nil {
		return parser.PluginValidationResult{Warnings: warnings}, verr
	}
	return parser.PluginValidationResult{
		Manifest: manifestToParser(manifest),
		Warnings: warnings,
	}, nil
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
