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
	goruntime "runtime"
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

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

	// linkedConfigs is an mtime-aware cache of each linked notebook's
	// co-located <root>/.system/config.yaml (#133). Keyed by source
	// ('linked:<id>'). linkedConfigFor refreshes an entry when the on-disk
	// mtime advances; the watcher invalidates an entry on external edit so
	// the next read re-loads. Guarded by linkedConfigsMu (a dedicated mutex,
	// NOT configMu) so concurrent GetPluginSettingsForNotebook callers can
	// safely read/write the cache without holding configMu's write lock
	// (which would serialize all config access) and without risking a
	// concurrent-map-write panic under configMu.RLock (a read lock cannot
	// protect map writes).
	linkedConfigsMu sync.Mutex
	linkedConfigs   map[string]linkedConfigEntry

	// pluginRODB is a lazy read-only handle to the in-memory index, used
	// exclusively by PluginRawQuery so a plugin can never mutate the index
	// or schema even if a prefix check or comment-stripping is bypassed.
	pluginRODBMu sync.Mutex
	pluginRODB   *sql.DB

	// rateLimiter caps per-plugin PluginFetch RPS so a network-granted plugin
	// cannot hammer external services (#153). Guarded by its own internal
	// mutex; eviction happens on uninstall.
	rateLimiter *pluginRateLimiter

	// vaultMu guards the lifecycle of the vault-scoped service pointers (db,
	// coordinator, watcher, tracker, vaultPath) against concurrent IPC access.
	// Wails dispatches each bound method on its own goroutine, so without this
	// a lifecycle transition (CloseVault / InitializeVault / MoveVault /
	// SwitchVault) could nil out a.db while an in-flight reader
	// (FetchPageBlocks, UpdateBlockState, …) is between its nil check and its
	// use of the pointer — a nil-deref panic (#141 review).
	//   - Lifecycle cutover sections acquire the exclusive Lock().
	//   - Reader IPC handlers acquire RLock() (defer RUnlock()) for the whole
	//     call so the pointer they checked stays valid for its duration.
	//   - Internal lowercase helpers assume the caller already holds the lock
	//     and never acquire it themselves (RLock is not reentrant; nesting it
	//     on the same goroutine would deadlock under writer contention).
	//   - Pure-delegation wrappers (PickNotebookFolder, PickLinkedNotebook,
	//     PluginMutateBlock, PluginUpdateBlockState) take no lock — their
	//     callee does — so the same goroutine never holds RLock twice.
	// Lock ordering: vaultMu is always acquired BEFORE configMu.
	vaultMu sync.RWMutex
}

// linkedConfigEntry is one slot in App.linkedConfigs. mtime is the on-disk
// modification time of the co-located config.yaml at the moment cfg was
// parsed; a later mtime triggers a re-read.
type linkedConfigEntry struct {
	cfg   config.SystemConfig
	mtime time.Time
}

func NewApp() *App {
	return &App{
		spacesPerTab: 4,
		rateLimiter:  newPluginRateLimiter(),
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
	// Emit vault:closing so the frontend plugin loader runs every plugin's
	// onVaultClose/onShutdown hook (#106) before IPC tears down. Best-effort:
	// a nil ctx (headless test) skips the emit.
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:closing", struct{}{})
	}
	// Wait for any in-flight Wails-bound calls (UpdateBlockState,
	// QueryTasks, FetchSectionTimeline) to complete before tearing
	// down the DB, tracker, and watcher. Without this a fast window
	// close could race an in-progress file write.
	a.wg.Wait()
	// Take the write lock for the terminal teardown so any reader that
	// slipped in between wg.Wait() returning and this point can't
	// dereference a service mid-close. (No new handlers arrive after the
	// Wails context is cancelled, but the lock makes the guarantee
	// structural rather than relying on dispatch ordering.)
	a.vaultMu.Lock()
	// Share the exact teardown path with CloseVault so both nil every
	// service field. Nilling here matters: if a "change vault" IPC lands
	// during OS-driven close (race), CloseVault's nothing-to-close guard
	// sees the nil'd fields and becomes a no-op instead of double-closing
	// already-closed handles.
	a.teardownVaultServices()
	a.vaultMu.Unlock()
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
	templates.ResetPluginRegistry()
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

	// Hold the write lock across the teardown so concurrent readers can't
	// dereference a service pointer mid-close. The fast nil-check is also
	// taken under the lock so the "nothing to close" decision can't race a
	// concurrent Initialize.
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
	if a.vaultPath == "" && a.db == nil {
		return nil // nothing to close
	}
	// Emit vault:closing BEFORE teardown so the frontend plugin loader can run
	// every plugin's onVaultClose hook (#106) while IPC is still live. The
	// event is best-effort: if no frontend is mounted (e.g. headless test), the
	// emit is a no-op (a.ctx == nil guard).
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:closing", struct{}{})
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

	// Route co-located per-notebook config edits to the cache invalidator +
	// linked-config:changed event (#133). The handler is called from the
	// watcher goroutine; it only touches configMu + the event emitter.
	watcher.SetLinkedConfigHandler(a.onLinkedConfigChange)

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

	// Seed the in-memory network audit log from the on-disk per-plugin
	// network.log files so entries survive a restart (#157).
	seedNetworkAuditFromDisk(vaultPath)

	// Report any paths the watcher could not subscribe to (fsnotify
	// limits, permissions, etc.) so the UI can inform the user.
	if failed := watcher.FailedPaths(); len(failed) > 0 && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:watch-coverage", failed)
	}

	return nil
}

// IsVaultInitialized returns whether a workspace vault has been configured and loaded.
func (a *App) IsVaultInitialized() bool {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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

	// Persist settings + boot services under the write lock so a concurrent
	// reader/CloseVault sees the transition atomically (no window where
	// settings.json points at the new path but a.db is still the old one).
	a.vaultMu.Lock()
	defer a.vaultMu.Unlock()
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

// PickVaultDestination opens a native folder picker for a vault move/copy
// destination and returns the chosen path ("" on cancel). Shared by Move and
// Copy so the frontend can show its own confirmation modal between the pick
// and the commit, mirroring the delete flows (#141).
func (a *App) PickVaultDestination() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Destination for Silt Vault",
	})
}

// CopyVault duplicates the active vault tree at destPath, EXCLUDING the
// reproducible SQLite index (rebuilt from markdown when the copy is first
// opened). The active vault is untouched: no settings change, no service
// teardown, no event. The copy is a separate workspace the user can switch
// to later. CopyVaultTree validates the destination and verifies every byte
// (size + SHA-256); on failure it cleans up the partial destination.
func (a *App) CopyVault(destPath string) (vault.CopyResult, error) {
	a.wg.Add(1)
	defer a.wg.Done()

	// Snapshot the active vault path under the read lock so the copy reads a
	// stable source even if a lifecycle transition is racing. The copy itself
	// (CopyVaultTree) is long and never touches the service pointers, so it
	// runs without holding the lock.
	a.vaultMu.RLock()
	src := a.vaultPath
	a.vaultMu.RUnlock()
	if src == "" {
		return vault.CopyResult{}, fmt.Errorf("no vault is currently open")
	}
	return vault.CopyVaultTree(src, destPath)
}

// MoveVault relocates the active vault to destPath: copy + verify, then a
// cutover (teardown services → patch dest config.yaml path → persist
// settings.json → reinit at the new path) that reuses the existing
// close/open paths, with a verbatim rollback to the original path if reinit
// fails. The dest config.yaml's notebooks.path is updated to dest so the
// Settings → General "Workspace" row shows the new location. Emits vault:moved
// ({from, to}) so the frontend resets navigation and reloads its stores. If
// removeOld is true the original folder is deleted AFTER a successful
// cutover (non-fatal on failure: RemoveOldErr carries the message).
func (a *App) MoveVault(destPath string, removeOld bool) (vault.MoveVaultResult, error) {
	a.wg.Add(1)
	defer a.wg.Done()

	// Snapshot the active vault path under the read lock so the (long) copy
	// reads a stable source even if a lifecycle transition is racing.
	a.vaultMu.RLock()
	src := a.vaultPath
	a.vaultMu.RUnlock()
	if src == "" {
		return vault.MoveVaultResult{}, fmt.Errorf("no vault is currently open")
	}

	// 1. Copy + verify. On failure the primitive cleans up dest itself; the
	//    active vault and settings are untouched. No lock held — this is the
	//    slow phase and it never touches the service pointers.
	copyRes, err := vault.CopyVaultTree(src, destPath)
	if err != nil {
		return vault.MoveVaultResult{}, fmt.Errorf("move vault: %w", err)
	}
	dest := destPath
	// Snapshot the instant the copy+verify completed. Used before removeOld
	// to detect an external edit (e.g. VS Code) that landed in the source
	// during the cutover — deleting the source then would silently lose it.
	copyDoneAt := time.Now()

	// 2. Update the dest config.yaml notebooks.path so the Settings → General
	//    workspace row reflects the new location (matches ScaffoldVault's
	//    forward-slash convention). Best-effort: a failure here is logged but
	//    does not abort — the vault is fully usable with a stale display path.
	if cfg, cfgErr := config.Load(dest); cfgErr == nil {
		cfg.Notebooks.Path = filepath.ToSlash(dest)
		if err := config.Save(dest, cfg); err != nil {
			log.Printf("MoveVault: could not update dest config.yaml notebooks.path: %v", err)
		}
	}

	// 3. Cutover under the exclusive write lock: no reader can dereference a
	//    service pointer while the db / watcher are being torn down and
	//    rebuilt. The prior-settings snapshot is read UNDER this lock (not
	//    before it) so a concurrent settings.json writer (ApplyTheme) can't
	//    commit a change that the cutover then overwrites with a stale
	//    snapshot. Re-check a.vaultPath hasn't moved (defensive; the UI
	//    serializes lifecycle calls). rollbackMove also runs under this lock
	//    (it does not acquire it itself — RWMutex is not reentrant).
	cutoverErr := func() error {
		a.vaultMu.Lock()
		defer a.vaultMu.Unlock()
		if a.vaultPath != src {
			return fmt.Errorf("vault changed during move (concurrent lifecycle transition)")
		}
		prior, err := vault.LoadSettings()
		if err != nil {
			return fmt.Errorf("move vault: snapshot settings: %w", err)
		}
		a.teardownVaultServices()
		newSettings := *prior
		newSettings.VaultPath = dest
		if err := vault.SaveSettings(&newSettings); err != nil {
			_ = a.rollbackMove(src, prior)
			return fmt.Errorf("move vault: save settings: %w", err)
		}
		if err := a.initializeVaultServices(dest); err != nil {
			if recoverErr := a.rollbackMove(src, prior); recoverErr != nil {
				return fmt.Errorf("move vault: init services at %s failed (%v); rollback to %s also failed (%v)", dest, err, src, recoverErr)
			}
			return fmt.Errorf("move vault: init services at %s failed — rolled back to %s (%v)", dest, src, err)
		}
		return nil
	}()
	if cutoverErr != nil {
		return vault.MoveVaultResult{}, cutoverErr
	}

	result := vault.MoveVaultResult{
		CopyResult: copyRes,
		From:       src,
		To:         dest,
	}

	// 4. Optional old-vault removal (non-fatal: the cutover already
	//    succeeded). First guard against an external edit to the source that
	//    landed after the copy snapshot — if so, keep the old folder in place
	//    rather than delete the user's unsaved-to-the-new-vault change. A
	//    permission/lock failure on the delete itself is also carried on
	//    RemoveOldErr + logged so it is never fully silent.
	if removeOld {
		if modified, mErr := vault.SourceModifiedAfter(src, copyDoneAt); mErr != nil {
			result.RemoveOldErr = fmt.Sprintf("could not verify source unchanged: %v", mErr)
			log.Printf("MoveVault: skip removeOld — source check failed for %s: %v", src, mErr)
		} else if modified {
			result.RemoveOldErr = "original vault was modified during the move; left in place"
			log.Printf("MoveVault: skip removeOld — %s modified after copy snapshot", src)
		} else if err := vault.RemoveOldVault(src); err != nil {
			result.RemoveOldErr = err.Error()
			log.Printf("MoveVault: failed to remove old vault at %s: %v", src, err)
		}
	}

	// 5. Notify the frontend to reset navigation + reload stores. If the
	//    optional old-vault removal didn't happen (source modified during
	//    cutover, or a delete permission/lock error), carry a warning so the
	//    user is told the original folder is still on disk — the move itself
	//    succeeded, so this is non-blocking (surfaced as a toast, not an error
	//    return).
	if a.ctx != nil {
		payload := map[string]string{
			"from": src,
			"to":   dest,
		}
		if result.RemoveOldErr != "" {
			payload["warning"] = "Vault moved, but the original folder could not be removed: " + result.RemoveOldErr
		}
		runtime.EventsEmit(a.ctx, "vault:moved", payload)
	}
	return result, nil
}

// rollbackMove restores the active vault to originalPath after a failed
// cutover: persists the prior settings (verbatim, preserving theme/mode) and
// reinitializes services at the original path. The leftover verified copy is
// intentionally left in place — deleting it during error handling would risk
// data loss. Caller MUST hold vaultMu (it does not acquire it; RWMutex is not
// reentrant). Returns the reinit error, if any.
func (a *App) rollbackMove(originalPath string, prior *vault.AppSettings) error {
	// Never (re)initialize services against an empty path: absClean("") would
	// resolve to the working directory and pollute it with a .system/index.
	if originalPath == "" {
		return nil
	}
	_ = vault.SaveSettings(prior)
	return a.initializeVaultServices(originalPath)
}

// SwitchVault points Silt at an existing vault folder (e.g. one created by
// CopyVault) without a picker or scaffolding: teardown the active vault,
// persist settings.json to the new path, and reinit services there. The path
// must already contain a .system folder (CopyVault/MoveVault both produce
// valid vaults). Emits vault:moved so the frontend resets navigation.
func (a *App) SwitchVault(path string) error {
	a.wg.Add(1)
	defer a.wg.Done()

	if path == "" {
		return fmt.Errorf("empty vault path")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve vault path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(abs, ".system")); err != nil {
		return fmt.Errorf("not a Silt vault (no .system folder): %s", path)
	}

	// Cutover under the exclusive write lock so concurrent readers can't race
	// the teardown/reinit. The prior-settings snapshot is read UNDER this lock
	// (not before it) so a concurrent settings.json writer (ApplyTheme) can't
	// commit a change that the cutover overwrites with a stale snapshot.
	// activePath is captured under the lock (before teardown nils it) for
	// rollback. rollbackMove runs under this same lock.
	switchErr := func() error {
		a.vaultMu.Lock()
		defer a.vaultMu.Unlock()
		activePath := a.vaultPath
		prior, _ := vault.LoadSettings()
		a.teardownVaultServices()

		settings := prior
		if settings == nil {
			settings = &vault.AppSettings{}
		}
		settings.VaultPath = abs
		if err := vault.SaveSettings(settings); err != nil {
			if prior != nil {
				_ = a.rollbackMove(activePath, prior)
			}
			return fmt.Errorf("switch vault: save settings: %w", err)
		}
		if err := a.initializeVaultServices(abs); err != nil {
			if prior != nil {
				_ = a.rollbackMove(activePath, prior)
			}
			return fmt.Errorf("switch vault: init services: %w", err)
		}
		return nil
	}()
	if switchErr != nil {
		return switchErr
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:moved", map[string]string{
			"from": "",
			"to":   abs,
		})
	}
	return nil
}

// PickVaultExportPath opens the native save-file dialog filtered to
// *.silt-vault and returns the chosen path ("" on cancel). The frontend feeds
// the returned path to ExportVault. defaultFilename is offered as the initial
// name (e.g. "<vault-name>.silt-vault"); pass "" to let the OS pick a default.
// Mirrors PickExportPath (theme export) — the same SaveFileDialog surface.
func (a *App) PickVaultExportPath(defaultFilename string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export Silt vault",
		DefaultFilename: defaultFilename,
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Vault (*.silt-vault)", Pattern: "*.silt-vault"},
		},
	})
}

// ExportVault streams the active vault into a portable .silt-vault archive at
// destPath. The archive carries every file under the vault root EXCEPT the
// reproducible SQLite index (.system/index.sqlite* — rebuilt from markdown on
// import, identical to CopyVaultTree/MoveVault). Per-entry + whole-archive
// root SHA-256 digests are written into manifest.json (last) so import can
// detect corruption/tampering before extracting.
//
// The active vault is never touched (read-only): no settings change, no
// service teardown, no event other than vault:archive:progress. Streaming +
// determinate progress: the up-front stat pass gives a file count, and an
// event is emitted per file so the UI renders a progress bar for large vaults.
// Runs without holding vaultMu across the (long) write — the path is snapshotted
// under the read lock, exactly like CopyVault.
func (a *App) ExportVault(destPath string) (vault.ExportResult, error) {
	a.wg.Add(1)
	defer a.wg.Done()

	a.vaultMu.RLock()
	src := a.vaultPath
	a.vaultMu.RUnlock()
	if src == "" {
		return vault.ExportResult{}, fmt.Errorf("no vault is currently open")
	}
	vaultName := filepath.Base(filepath.Clean(src))
	return vault.ExportVaultTree(src, destPath, vaultName, appVersion, func(phase string, current, total int) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "vault:archive:progress", map[string]any{
				"phase":   phase,
				"current": current,
				"total":   total,
			})
		}
	})
}

// PickVaultArchive opens the native open-file dialog filtered to *.silt-vault
// and returns the chosen path ("" on cancel). The frontend feeds the returned
// path to ImportVault. Mirrors PickPluginArchive (the .silt-plugin picker) —
// the same OpenFileDialog surface.
func (a *App) PickVaultArchive() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Import Silt vault",
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Vault (*.silt-vault)", Pattern: "*.silt-vault"},
		},
	})
}

// ImportVault validates a .silt-vault archive and extracts it into destPath
// (an empty local folder the user chose via PickVaultDestination), then opens
// the extracted vault by calling SwitchVault. The validate-before-extract
// posture (manifest parsed, version accepted, every entry zip-slip/absolute/
// size-guarded, whole-archive root digest recomputed) runs BEFORE any file is
// written; extraction streams into a sibling temp dir, verifying each entry's
// SHA-256 during the copy, and the temp dir is atomically renamed into destPath
// only after every entry verifies. A corrupt or hostile archive leaves
// destPath untouched.
//
// On success the backend emits vault:moved (from SwitchVault) so the frontend
// resets navigation and reloads its stores; a fresh SQLite index is rebuilt
// from markdown at destPath (the index is never carried in the archive — §0
// rule 4). Streaming progress is emitted via vault:archive:progress.
func (a *App) ImportVault(archivePath, destPath string) (vault.ImportResult, error) {
	a.wg.Add(1)
	defer a.wg.Done()

	res, err := vault.ImportVaultTree(archivePath, destPath, func(phase string, current, total int) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "vault:archive:progress", map[string]any{
				"phase":   phase,
				"current": current,
				"total":   total,
			})
		}
	})
	if err != nil {
		return vault.ImportResult{}, err
	}
	// Open the extracted vault. SwitchVault rebuilds the index from markdown
	// and emits vault:moved so the frontend resets navigation + stores. It
	// refuses a path without a .system folder, which a verified archive
	// always produced.
	if err := a.SwitchVault(destPath); err != nil {
		return res, fmt.Errorf("archive extracted to %s but could not be opened: %w", destPath, err)
	}
	return res, nil
}

// FetchPageBlocks returns a flat list of all blocks for a page, ordered by
// line_number. A page is a single file; each block carries its own file_date.
// The notebook's source is resolved server-side from its (globally-unique)
// name so a linked notebook sharing a display name with a vault notebook
// returns its own page (#100).
func (a *App) FetchPageBlocks(notebook, section, page string) ([]parser.ParsedBlock, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	source := a.resolveSourceByName(sanitizePathSegment(notebook))
	var res []parser.ParsedBlock
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.FetchPageBlocks(source, notebook, section, page)
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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

	var loc db.BlockLocation
	err := a.coordinator.WithDBReadResult(func() error {
		var e error
		loc, e = a.db.GetBlockLocation(blockID)
		return e
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}
	notebook, section, page, blockType := loc.Notebook, loc.Section, loc.Page, loc.BlockType

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
	// Resolve the notebook content dir from the block's source (#100): vault
	// blocks live under <vault>/<notebook>, linked blocks under their root.
	notebookDir, err := a.resolveNotebookDir(safeNotebook, loc.Source)
	if err != nil {
		return fmt.Errorf("resolve notebook dir for block %s: %w", blockID, err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("resolved file path %q escapes notebook root %q", filePath, notebookDir)
	}

	var writeErr error
	a.coordinator.LockBlockWrite(blockID, func() {
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
					idxErr = a.db.IndexFileBlocks(loc.Source, remeta.Notebook, remeta.Section, remeta.Page, blocks, remeta.Tags, remeta.Warnings...)
				})
				if idxErr != nil {
					log.Printf("UpdateBlockState: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
				}
			}
		})
	}) // LockBlockWrite

	if writeErr != nil {
		return writeErr
	}
	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, "")
	return nil
}

// QueryTasks retrieves indexed items matching the active filters.
func (a *App) QueryTasks(filter parser.TaskQueryFilter) ([]parser.TaskResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	return themes.ListThemes(a.themesDir())
}

// GetActiveTheme reads AppSettings, resolves the active theme (falling back
// to the embedded default when the id is missing/invalid), and returns the
// token maps for injection. Always succeeds with the default theme on a
// fresh/empty vault so the app can render on first paint.
func (a *App) GetActiveTheme() (ActiveThemeResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
			// Not on disk: a first-class id may still be available from the
			// embedded roster (a wiped or pre-Sprint-8 themes dir shouldn't
			// prevent switching to a shipped theme). ResolveActive does the
			// same fallback for the startup path; mirror it here so the
			// picker's "apply" and the launch-time resolve can't disagree
			// on whether a theme is selectable. A genuinely unknown id
			// (e.g. typo) still falls through to the error below.
			if et, ok := themes.ParseEmbeddedByID(id); ok {
				t = et
			} else {
				return ActiveThemeResult{}, fmt.Errorf("theme %q is not available", id)
			}
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
		Title:           "Export active theme",
		DefaultFilename: defaultFilename,
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	a.wg.Add(1)
	defer a.wg.Done()
	return templates.ListTemplates(a.templatesDir())
}

// GetTemplate resolves a single template by id (on-disk then embedded) and
// returns the full Template including Body. Used by the picker to render a
// live preview + drive the placeholder form. Returns a user-facing error when
// the id is on neither tier.
func (a *App) GetTemplate(id string) (templates.Template, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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

// RegisterPluginTemplates adds a plugin's templates to the runtime registry
// (#96). Each template MUST have Source = "plugin" and PluginID = pluginID
// (the registry rejects mismatches). Emits templates:changed so the picker's
// listing refreshes immediately. The plugin tier is in-memory only — no
// disk write, no LockFileWrite, no atomic-rename.
func (a *App) RegisterPluginTemplates(pluginID string, tpls []*templates.Template) error {
	a.wg.Add(1)
	defer a.wg.Done()
	// Set Source and PluginID uniformly on each template (defensive — the
	// registry also validates). Nil elements are filtered out so the
	// registry never receives them (it rejects nil entries).
	var valid []*templates.Template
	for _, t := range tpls {
		if t == nil {
			continue
		}
		t.Source = templates.SourcePlugin
		t.PluginID = pluginID
		valid = append(valid, t)
	}
	if err := templates.RegisterPluginTemplates(pluginID, valid); err != nil {
		log.Printf("templates: RegisterPluginTemplates(%q) failed: %v", pluginID, err)
		return err
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
	}
	log.Printf("templates: RegisterPluginTemplates → %d templates for %q", len(valid), pluginID)
	return nil
}

// UnregisterPluginTemplates removes a plugin's templates from the runtime
// registry. Idempotent. Emits templates:changed.
func (a *App) UnregisterPluginTemplates(pluginID string) {
	a.wg.Add(1)
	defer a.wg.Done()
	templates.UnregisterPluginTemplates(pluginID)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "templates:changed", struct{}{})
	}
	log.Printf("templates: UnregisterPluginTemplates → %q", pluginID)
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	safeSection := sanitizeSectionPath(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return "", fmt.Errorf("notebook and page names are required (section is optional)")
	}
	safeDate := sanitizePathSegment(dateStr)
	if safeDate == "" {
		safeDate = time.Now().Format("2006-01-02")
	}

	// Resolve the notebook root from its source (#100).
	tplSource := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, tplSource)
	if err != nil {
		return "", err
	}
	var filePath string
	if safeSection == "" {
		filePath = filepath.Join(notebookDir, safePage+".md")
	} else {
		filePath = filepath.Join(notebookDir, safeSection, safePage+".md")
	}
	if !isPathWithinRoot(filePath, notebookDir) {
		return "", fmt.Errorf("path escapes notebook root")
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
				idxErr = a.db.IndexFileBlocks(tplSource, meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	// Block text is single-line; collapse any newlines to spaces.
	cleanText := strings.ReplaceAll(newText, "\n", " ")

	a.wg.Add(1)
	defer a.wg.Done()

	var loc db.BlockLocation
	err := a.coordinator.WithDBReadResult(func() error {
		var e error
		loc, e = a.db.GetBlockLocation(blockID)
		return e
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}
	notebook, section, page := loc.Notebook, loc.Section, loc.Page

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid file metadata for block %s", blockID)
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, loc.Source)
	if err != nil {
		return fmt.Errorf("resolve notebook dir for block %s: %w", blockID, err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("resolved file path %q escapes notebook root %q", filePath, notebookDir)
	}

	var writeErr error
	a.coordinator.LockBlockWrite(blockID, func() {
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
					idxErr = a.db.IndexFileBlocks(loc.Source, remeta.Notebook, remeta.Section, remeta.Page, reblocks, remeta.Tags, remeta.Warnings...)
				})
				if idxErr != nil {
					log.Printf("MutateBlock: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
				}
			}
		})
	}) // LockBlockWrite
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

// sanitizePathSegment strips path-traversal indicators from a single path
// component: directory separators, NUL, control chars, and a LEADING `..`
// (or run of leading `..`s) which is the path-traversal signal. Internal `..`
// substrings (e.g. `2.0..2.1`, `a..b..c`) are preserved verbatim — they are
// legitimate filename characters, not traversal (#89). The contract is
// "single segment": `/` and `\` are stripped so the join can never produce
// a multi-segment path.
func sanitizePathSegment(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return -1
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	for strings.HasPrefix(cleaned, "..") {
		cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, ".."))
	}
	if cleaned == "." {
		cleaned = ""
	}
	return cleaned
}

// sanitizeSectionPath sanitizes a multi-segment section path (e.g.
// "Projects/Active"). Each segment is sanitized independently via
// sanitizePathSegment, preserving the `/` separator so deeply-nested
// section paths survive the sanitize pass (#88, #97). An empty input
// (or all-empty segments) returns "".
func sanitizeSectionPath(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if c := sanitizePathSegment(p); c != "" {
			out = append(out, c)
		}
	}
	return strings.Join(out, "/")
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

// isPathWithinRoot reports whether target is the same as or a descendant of
// root. Generalized from the vault-only check for #100: callers pass the
// resolved notebook root (vault root, an in-vault notebook dir, or a linked
// notebook root) so the same traversal guard covers external notebooks.
//
// Both paths are cleaned, made absolute, and resolved through EvalSymlinks
// (mirroring backend/plugins/installer.go:isWithin) so a symlink planted
// inside a notebook that points outside it cannot mask an escape. The
// comparison is case-insensitive on Windows where the filesystem itself is
// case-insensitive. EvalSymlinks errors (e.g. non-existent target during
// construction) fall back to the lexical form.
func isPathWithinRoot(target, root string) bool {
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	absTarget = filepath.Clean(absTarget)
	absRoot = filepath.Clean(absRoot)
	if absTarget == absRoot {
		return true
	}
	prefix := absRoot + string(os.PathSeparator)
	if goruntime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(absTarget), strings.ToLower(prefix))
	}
	return strings.HasPrefix(absTarget, prefix)
}

// resolveNotebookDir returns the content directory for a notebook under the
// given source (#100): the folder whose direct children are the notebook's
// sections and section-less pages. For an in-vault notebook ('vault') that is
// <vaultPath>/<notebookName>; for a linked notebook ('linked:<id>') it is the
// linked root itself (sections/pages live directly under the external root).
// The caller MUST still guard any path built from this dir with
// isPathWithinRoot. Returns an error if the vault is not loaded or a linked
// source references an unregistered id.
func (a *App) resolveNotebookDir(notebookName, source string) (string, error) {
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	if source == "" || source == config.LinkedNotebooksVaultSource {
		return filepath.Join(a.vaultPath, notebookName), nil
	}
	if strings.HasPrefix(source, "linked:") {
		id := strings.TrimPrefix(source, "linked:")
		a.configMu.RLock()
		defer a.configMu.RUnlock()
		for _, ln := range a.cfg.LinkedNotebooks {
			if ln.ID == id {
				return ln.RootPath, nil
			}
		}
		return "", fmt.Errorf("linked notebook %q is not registered", id)
	}
	return "", fmt.Errorf("unknown notebook source %q", source)
}

// nspKey is the source-aware (source, notebook, section, page) lookup key for
// the per-page block count map used by ListNavigation. Source leads so a
// linked notebook sharing a display name with a vault notebook gets its own
// counts (#100).
type nspKey struct{ src, n, s, p string }

// resolveSourceByName returns the blocks.source discriminator for a notebook
// display name: 'linked:<id>' if the name matches a registered linked
// notebook, else 'vault'. Notebook display names are globally unique
// (LinkNotebook rejects collisions), so the name unambiguously resolves the
// source. This lets the notebook-scoped CRUD/focus-lock operations keep their
// IPC signatures source-free while still routing to the correct root (#100),
// avoiding a parallel frontend source-flow.
// resolveSourceByName maps a notebook display name to its index source
// ("vault" or "linked:<id>"). It acquires configMu in read mode for the
// standalone callers below.
func (a *App) resolveSourceByName(notebookName string) string {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.resolveSourceByNameLocked(notebookName)
}

// resolveSourceByNameLocked is the lock-free inner form. The caller MUST hold
// configMu (read or write). Needed so GetPluginSettingsForNotebook — which
// holds configMu in WRITE mode (linkedConfigLocked mutates the cache map) —
// can resolve the source without self-deadlocking on a re-entrant RLock
// (sync.RWMutex blocks RLock while a writer holds the lock).
func (a *App) resolveSourceByNameLocked(notebookName string) string {
	for _, ln := range a.cfg.LinkedNotebooks {
		if ln.DisplayName == notebookName {
			return ln.Source()
		}
	}
	return config.LinkedNotebooksVaultSource
}

// ListNavigation returns the Notebook > Section > Page tree for the sidebar.
//
// The directory structure on disk is the single source of truth. Each
// directory is classified by what it DIRECTLY contains:
//   - A `.md` file directly under a folder is a PAGE belonging to that folder's
//     section (a page belongs to the folder it's in; the folder's own path
//     is the section path, multi-segment joined with `/`).
//   - A sub-directory of a folder is a nested SECTION. We recurse into it to
//     collect its own pages + its own nested sections. Empty sections are
//     preserved so a freshly-created section appears in the sidebar (#88).
//   - A `.md` file directly under a Notebook's root belongs to the section-less
//     group (Name = "").
//
// Block counts are merged from the index for per-page badges. The returned
// tree is a true tree: each section may carry `Children []NavigationSection`
// for arbitrarily-deep nesting.
func (a *App) ListNavigation() (parser.NavigationTree, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return parser.NavigationTree{}, fmt.Errorf("vault not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	// 1. Block counts per (source, notebook, section, page) from the index.
	// Source is part of the key so a linked notebook sharing a display name
	// with a vault notebook gets its own counts (#100).
	counts := map[nspKey]int{}
	if a.db != nil {
		a.coordinator.WithDBRead(func() {
			rows, err := a.db.SQLDB().Query("SELECT COALESCE(source, 'vault'), notebook, section, page, COUNT(*) FROM blocks GROUP BY COALESCE(source, 'vault'), notebook, section, page")
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var src, n, s, p string
				var c int
				if err := rows.Scan(&src, &n, &s, &p, &c); err == nil {
					counts[nspKey{src, n, s, p}] = c
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
			continue
		}
		nbPath := filepath.Join(a.vaultPath, nbName)
		rootPages, childSections := a.walkSections(nbPath, nbName, "", counts)
		var sections []parser.NavigationSection
		// Direct .md files at the notebook root form the section-less
		// group (Name = ""), surfaced first in the sidebar.
		if len(rootPages) > 0 {
			sections = append(sections, parser.NavigationSection{
				Name:  "",
				Pages: rootPages,
			})
		}
		sections = append(sections, childSections...)
		tree.Notebooks = append(tree.Notebooks, parser.NavigationNotebook{
			Name:     nbName,
			Sections: sections,
			Source:   "vault",
		})
	}

	// 2. Linked (external) notebooks (#100). Their sections/pages come from
	// the index counts (the root may be momentarily offline; the last-synced
	// rows still show). Each link is one notebook. Section-less pages ("")
	// surface first, matching the vault ordering above.
	a.configMu.RLock()
	links := append([]config.LinkedNotebook(nil), a.cfg.LinkedNotebooks...)
	a.configMu.RUnlock()
	for _, ln := range links {
		src := ln.Source()
		pagesBySection := map[string][]parser.NavigationPage{}
		for k, c := range counts {
			if k.src == src && k.n == ln.DisplayName {
				pagesBySection[k.s] = append(pagesBySection[k.s], parser.NavigationPage{Name: k.p, Count: c})
			}
		}
		_, statErr := os.Stat(ln.RootPath)

		var sections []parser.NavigationSection
		if pages, ok := pagesBySection[""]; ok {
			sortNavPages(pages)
			sections = append(sections, parser.NavigationSection{Name: "", Pages: pages})
		}
		named := make([]string, 0, len(pagesBySection))
		for s := range pagesBySection {
			if s != "" {
				named = append(named, s)
			}
		}
		sortStrings(named)
		for _, s := range named {
			pages := pagesBySection[s]
			sortNavPages(pages)
			sections = append(sections, parser.NavigationSection{Name: s, Pages: pages})
		}

		tree.Notebooks = append(tree.Notebooks, parser.NavigationNotebook{
			Name:         ln.DisplayName,
			Source:       src,
			RootPath:     ln.RootPath,
			Disconnected: statErr != nil,
			Sections:     sections,
		})
	}

	// Mix vault + linked notebooks alphabetically by name for a unified tree.
	sort.Slice(tree.Notebooks, func(i, j int) bool {
		return tree.Notebooks[i].Name < tree.Notebooks[j].Name
	})
	return normalizeNavTree(tree), nil
}

// normalizeNavTree guarantees no nil slices cross the Wails IPC boundary. A Go
// nil slice serializes to JSON `null`, but the generated TS constructor passes
// `null` through unchanged — the frontend's `.length` reads then crash with
// "Cannot read properties of null", which tears down the reactive update and
// leaves the sidebar blank even though the data is correct (#140). Every
// Sections / Pages / Children slice is normalized to a non-nil empty array.
func normalizeNavTree(tree parser.NavigationTree) parser.NavigationTree {
	if tree.Notebooks == nil {
		tree.Notebooks = []parser.NavigationNotebook{}
	}
	for i := range tree.Notebooks {
		if tree.Notebooks[i].Sections == nil {
			tree.Notebooks[i].Sections = []parser.NavigationSection{}
		}
		for j := range tree.Notebooks[i].Sections {
			tree.Notebooks[i].Sections[j] = normalizeNavSection(tree.Notebooks[i].Sections[j])
		}
	}
	return tree
}

func normalizeNavSection(s parser.NavigationSection) parser.NavigationSection {
	if s.Pages == nil {
		s.Pages = []parser.NavigationPage{}
	}
	if s.Children == nil {
		s.Children = []parser.NavigationSection{}
	}
	for i := range s.Children {
		s.Children[i] = normalizeNavSection(s.Children[i])
	}
	return s
}

// walkSections reads `dirPath` once and returns:
//   - `pages`: the direct .md files in this directory (the "own pages").
//   - `sections`: one NavigationSection per sub-directory, each carrying its
//     own pages and recursively-built children.
//
// `parentSectionID` is the multi-segment section id of `dirPath` itself
// (empty at the notebook root). The caller (ListNavigation) is responsible
// for turning the notebook-root `pages` into the section-less group.
// Sections with no pages and no children are still emitted so freshly-
// created sections appear in the sidebar immediately.
func (a *App) walkSections(
	dirPath, nbName, parentSectionID string,
	counts map[nspKey]int,
) ([]parser.NavigationPage, []parser.NavigationSection) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, nil
	}

	var pages []parser.NavigationPage
	var subDirs []string

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip the attachments/ directory in the sidebar navigator (#101) —
		// it holds binary assets, not pages/sections.
		if e.IsDir() && strings.EqualFold(name, "attachments") {
			continue
		}
		if e.IsDir() {
			subDirs = append(subDirs, name)
			continue
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		pageName := strings.TrimSuffix(name, filepath.Ext(name))
		pages = append(pages, parser.NavigationPage{
			Name:  pageName,
			Count: counts[nspKey{"vault", nbName, parentSectionID, pageName}],
		})
	}
	sortNavPages(pages)
	sortStrings(subDirs)

	sections := []parser.NavigationSection{}

	for _, sd := range subDirs {
		var childID string
		if parentSectionID == "" {
			childID = sd
		} else {
			childID = parentSectionID + "/" + sd
		}
		childPath := filepath.Join(dirPath, sd)
		// Single read: the recursive call returns both the child's own
		// pages and its nested sections, so we never re-read childPath.
		childPages, childSections := a.walkSections(childPath, nbName, childID, counts)
		// Preserve the child even when empty so a freshly-created
		// section shows up in the sidebar.
		sections = append(sections, parser.NavigationSection{
			Name:     sd,
			Path:     childID,
			Pages:    childPages,
			Children: childSections,
		})
	}

	return pages, sections
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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

// focusFilePath resolves the on-disk page file for a focus-lease operation,
// routing to the correct root via the notebook's source (#100). Shared by
// Acquire/Release/RefreshFocusLock so the lease key always matches the file
// the watcher sees — including linked notebooks.
func (a *App) focusFilePath(notebook, section, page string) (string, error) {
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return "", fmt.Errorf("invalid path metadata")
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, a.resolveSourceByName(safeNotebook))
	if err != nil {
		return "", err
	}
	fp := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(fp, notebookDir) {
		return "", fmt.Errorf("path escapes notebook root")
	}
	return fp, nil
}

// AcquireFocusLock registers a focus lock on a page file to ignore fsnotify updates.
func (a *App) AcquireFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.LockFocus(fp)
	return nil
}

// ReleaseFocusLock removes a focus lock from a page file.
func (a *App) ReleaseFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.UnlockFocus(fp)
	return nil
}

// RefreshFocusLock extends an existing focus lease for a page file. Called by the
// Svelte editor's heartbeat while it stays focused (#38); a no-op if the
// lease already expired (the editor must re-acquire).
func (a *App) RefreshFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.RefreshFocus(fp)
	return nil
}

// CreateNotebook creates a top-level notebook folder under the vault root.
// Silt starts blank; the user creates or opens notebooks from the sidebar.
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

	frontmatter, body := splitFrontmatter(string(contentBytes))

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

// --- Rename / Delete lifecycle (#62, #83) ---------------------------------

// trashBase returns the .system/trash directory path.
func (a *App) trashBase() string {
	return filepath.Join(a.vaultPath, ".system", "trash")
}

// moveToTrash moves a file or directory to .system/trash/<timestamp>/<relPath>,
// preserving the relative structure so the user can recover it. Returns the
// trash destination path. The caller MUST guard with isPathWithinRoot.
func (a *App) moveToTrash(source string) (string, error) {
	rel, err := filepath.Rel(a.vaultPath, source)
	if err != nil {
		return "", fmt.Errorf("cannot compute relative path: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(a.trashBase(), ts, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("failed to create trash directory: %w", err)
	}
	if err := os.Rename(source, dest); err != nil {
		return "", fmt.Errorf("failed to move to trash: %w", err)
	}
	return dest, nil
}

// reindexFile reads, parses, and indexes a single .md file at the given path.
// Used by rename operations where the file content changed (frontmatter) or
// the path changed (folder rename). The caller MUST hold the file lock.
func (a *App) reindexFile(filePath, notebook, section, page string) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("reindexFile: failed to read %s: %v", filePath, err)
		return
	}
	content := string(contentBytes)
	blocks, meta, _, _, parseErr := parser.ParseFileContent(
		content, notebook, section, page,
		fileOrDefaultDate(filePath), a.spacesPerTab,
	)
	if parseErr != nil {
		log.Printf("reindexFile: parse failed for %s: %v", filePath, parseErr)
		return
	}
	var idxErr error
	reidxSource := a.resolveSourceByName(notebook)
	a.coordinator.WithDBWrite(func() {
		idxErr = a.db.IndexFileBlocks(reidxSource, meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
	})
	if idxErr != nil {
		log.Printf("reindexFile: index failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
	}
	// Emit block:changed so live embeds/references refresh.
	for _, b := range blocks {
		if b.ID != "" {
			a.emitBlockChanged(b.ID, meta.Notebook, meta.Section, meta.Page, b.FileDate)
		}
	}
}

// updateFrontmatterField rewrites a single YAML key in the frontmatter block.
// It performs a simple line-based replacement of `key: "old"` → `key: "new"`.
// The caller MUST hold the file lock and call tracker.RegisterWrite.
func updateFrontmatterField(content, key, newVal string) string {
	lines := strings.Split(content, "\n")
	inFM := false
	closeIdx := -1
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if !inFM {
				inFM = true
				continue
			}
			closeIdx = i
			break // closing ---
		}
		if inFM {
			prefix := key + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", key, strconv.Quote(newVal))
				found = true
				break
			}
		}
	}
	// If the frontmatter exists but the key was absent, insert it before
	// the closing --- so externally-authored files (Obsidian/VS Code) that
	// lack the key gain it on rename rather than silently no-oping.
	if inFM && !found && closeIdx >= 0 {
		newLine := fmt.Sprintf("%s: %s", key, strconv.Quote(newVal))
		lines = append(lines[:closeIdx], append([]string{newLine}, lines[closeIdx:]...)...)
	}
	return strings.Join(lines, "\n")
}

// RenamePage renames a single page file. Updates the page: frontmatter value,
// moves the file, and re-indexes. Block UUIDs are preserved so references
// and embeds keep resolving (#62, #83).
func (a *App) RenamePage(notebook, section, oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safeOldPage := sanitizePathSegment(oldName)
	safeNewPage := sanitizePathSegment(newName)
	if safeNotebook == "" || safeOldPage == "" || safeNewPage == "" {
		return fmt.Errorf("notebook and page names are required")
	}
	if safeOldPage == safeNewPage {
		return nil
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	oldFile := filepath.Join(notebookDir, safeSection, safeOldPage+".md")
	newFile := filepath.Join(notebookDir, safeSection, safeNewPage+".md")
	if !isPathWithinRoot(oldFile, notebookDir) || !isPathWithinRoot(newFile, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(newFile); err == nil {
		return fmt.Errorf("a page named %q already exists", safeNewPage)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	// Lock the notebook root to prevent interleaving with the scanner.
	nbRoot := notebookDir
	a.coordinator.LockFileWrite(nbRoot, func() {
		// 1. Read the file content before renaming.
		contentBytes, err := os.ReadFile(oldFile)
		if err != nil {
			runErr = err
			return
		}

		// 2. Rename old → new FIRST. If this fails, nothing was modified
		// (clean state). This avoids the stale-frontmatter-at-old-path
		// inconsistency that would occur if we wrote frontmatter first.
		a.tracker.RegisterWrite(oldFile)
		a.tracker.RegisterWrite(newFile)
		if err := os.Rename(oldFile, newFile); err != nil {
			runErr = err
			return
		}

		// 3. Update frontmatter at the new path. If this fails, the file
		// is at the correct new path with stale frontmatter — the scanner
		// will use the path-derived page name, which matches the sidebar.
		content := updateFrontmatterField(string(contentBytes), "page", safeNewPage)
		a.tracker.RegisterWrite(newFile)
		if err := parser.WriteFileAtomic(newFile, []byte(content)); err != nil {
			runErr = err
			return
		}

		// 4. Clear old index entries + re-index at new path.
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, safeOldPage)
		})
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ForgetFile(oldFile)
		})
		a.reindexFile(newFile, safeNotebook, safeSection, safeNewPage)
	})

	return runErr
}

// RenameSection renames a section folder and updates the section: frontmatter
// in every .md file it contains. All affected blocks are re-indexed (#62).
func (a *App) RenameSection(notebook, oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeOldSection := sanitizePathSegment(oldName)
	safeNewSection := sanitizePathSegment(newName)
	if safeNotebook == "" || safeOldSection == "" || safeNewSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}
	if safeOldSection == safeNewSection {
		return nil
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	oldDir := filepath.Join(notebookDir, safeOldSection)
	newDir := filepath.Join(notebookDir, safeNewSection)
	if !isPathWithinRoot(oldDir, notebookDir) || !isPathWithinRoot(newDir, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("a section named %q already exists", safeNewSection)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	nbRoot := notebookDir
	a.coordinator.LockFileWrite(nbRoot, func() {
		// 1. Read all .md files from the old section BEFORE renaming.
		entries, err := os.ReadDir(oldDir)
		if err != nil {
			runErr = err
			return
		}
		type fileContent struct {
			name    string
			content []byte
		}
		var files []fileContent
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			oldPath := filepath.Join(oldDir, entry.Name())
			b, err := os.ReadFile(oldPath)
			if err != nil {
				runErr = fmt.Errorf("RenameSection: read %s: %w", entry.Name(), err)
				return
			}
			files = append(files, fileContent{name: entry.Name(), content: b})
		}

		// 2. Rename the section folder FIRST. If this fails, nothing was
		// modified (clean state — avoids stale frontmatter at old paths).
		a.tracker.RegisterWrite(oldDir)
		a.tracker.RegisterWrite(newDir)
		if err := os.Rename(oldDir, newDir); err != nil {
			runErr = err
			return
		}

		// 3. Update section: frontmatter in each file at the new path.
		// If any write fails, the folder is at the correct new path;
		// the scanner will derive section from the path (which matches
		// the sidebar), and stale frontmatter self-heals on next rename.
		var writeErrs []string
		for _, fc := range files {
			newPath := filepath.Join(newDir, fc.name)
			updated := updateFrontmatterField(string(fc.content), "section", safeNewSection)
			a.tracker.RegisterWrite(newPath)
			if err := parser.WriteFileAtomic(newPath, []byte(updated)); err != nil {
				writeErrs = append(writeErrs, fmt.Sprintf("write %s: %v", fc.name, err))
			}
		}
		if len(writeErrs) > 0 {
			runErr = fmt.Errorf("RenameSection: %d file(s) failed frontmatter update at new path: %s", len(writeErrs), strings.Join(writeErrs, "; "))
			return
		}

		// 4. Clear old index entries + re-index all pages at new paths.
		var pageFiles []string
		for _, fc := range files {
			pageFiles = append(pageFiles, fc.name)
		}
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeOldSection, "")
		})
		for _, pageFile := range pageFiles {
			oldPath := filepath.Join(oldDir, pageFile)
			newPath := filepath.Join(newDir, pageFile)
			pageName := strings.TrimSuffix(pageFile, ".md")
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ForgetFile(oldPath)
			})
			a.reindexFile(newPath, safeNotebook, safeNewSection, pageName)
		}
	})

	return runErr
}

// RenameNotebook renames a notebook folder and updates the notebook: frontmatter
// in every .md file it contains. All affected blocks are re-indexed (#62).
func (a *App) RenameNotebook(oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeOldNotebook := sanitizePathSegment(oldName)
	safeNewNotebook := sanitizePathSegment(newName)
	if safeOldNotebook == "" || safeNewNotebook == "" {
		return fmt.Errorf("notebook names are required")
	}
	if safeOldNotebook == safeNewNotebook {
		return nil
	}

	// A linked notebook's name is its external folder basename + registry
	// identity; renaming it is unlink + re-link, not a folder rename on the
	// external source of truth. Refuse here so the vault-only folder rename
	// below never misroutes (#100).
	if src := a.resolveSourceByName(safeOldNotebook); strings.HasPrefix(src, "linked:") {
		return fmt.Errorf("linked notebooks cannot be renamed in place — unlink and re-link the folder under the new name")
	}

	oldDir := filepath.Join(a.vaultPath, safeOldNotebook)
	newDir := filepath.Join(a.vaultPath, safeNewNotebook)
	if !isPathWithinRoot(oldDir, a.vaultPath) || !isPathWithinRoot(newDir, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("a notebook named %q already exists", safeNewNotebook)
	}
	if a.nameCollidesWithLink(safeNewNotebook, "") {
		return fmt.Errorf("a linked notebook named %q already exists; unlink or rename it first", safeNewNotebook)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	a.coordinator.LockFileWrite(oldDir, func() {
		// 1. Walk all .md files under the old notebook recursively and
		// read their content BEFORE renaming.
		type fileContent struct {
			oldPath string
			relPath string
			content []byte
		}
		var files []fileContent
		_ = filepath.WalkDir(oldDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				b, readErr := os.ReadFile(path)
				if readErr != nil {
					runErr = fmt.Errorf("RenameNotebook: read %s: %w", path, readErr)
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(oldDir, path)
				files = append(files, fileContent{oldPath: path, relPath: rel, content: b})
			}
			return nil
		})
		if runErr != nil {
			return
		}

		// 2. Rename the notebook folder FIRST. If this fails, nothing
		// was modified (clean state).
		a.tracker.RegisterWrite(oldDir)
		a.tracker.RegisterWrite(newDir)
		if err := os.Rename(oldDir, newDir); err != nil {
			runErr = err
			return
		}

		// 3. Update notebook: frontmatter in each file at the new path.
		var writeErrs []string
		for _, fc := range files {
			newMdPath := filepath.Join(newDir, fc.relPath)
			updated := updateFrontmatterField(string(fc.content), "notebook", safeNewNotebook)
			a.tracker.RegisterWrite(newMdPath)
			if err := parser.WriteFileAtomic(newMdPath, []byte(updated)); err != nil {
				writeErrs = append(writeErrs, fmt.Sprintf("write %s: %v", fc.relPath, err))
			}
		}
		if len(writeErrs) > 0 {
			runErr = fmt.Errorf("RenameNotebook: %d file(s) failed frontmatter update at new path: %s", len(writeErrs), strings.Join(writeErrs, "; "))
			return
		}

		// 4. Clear old index entries + re-index all files at new paths.
		for _, fc := range files {
			rel, err := filepath.Rel(oldDir, fc.oldPath)
			if err != nil {
				continue
			}
			// Derive section/page from the relative path for ClearFileBlocks.
			relParts := strings.Split(filepath.ToSlash(rel), "/")
			var section, page string
			if len(relParts) == 1 {
				page = strings.TrimSuffix(relParts[0], ".md")
			} else {
				section = relParts[0]
				page = strings.TrimSuffix(relParts[len(relParts)-1], ".md")
			}
			// Clear old index entries via the typed API (not raw SQL) so
			// the files mtime cache is also cleaned via ForgetFile.
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ClearFileBlocks(nil, "vault", safeOldNotebook, section, page)
				_ = a.db.ForgetFile(fc.oldPath)
			})
			newMdPath := filepath.Join(newDir, rel)
			a.reindexFile(newMdPath, safeNewNotebook, section, page)
		}
	})

	return runErr
}

// DeletePage moves a single page file to .system/trash/ and clears its index
// entries. The file is recoverable from the trash folder (#62).
func (a *App) DeletePage(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("notebook and page names are required")
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("page %q not found", safePage)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	linked := strings.HasPrefix(source, "linked:")
	var runErr error
	a.coordinator.LockFileWrite(filePath, func() {
		a.tracker.RegisterWrite(filePath)
		if linked {
			// External folder is the source of truth — delete in place. Silt
			// never copies linked content into the vault trash (#100).
			if err := os.Remove(filePath); err != nil {
				runErr = err
				return
			}
		} else {
			if _, err := a.moveToTrash(filePath); err != nil {
				runErr = err
				return
			}
		}
		var blockIDs []string
		a.coordinator.WithDBWrite(func() {
			blockIDs, _ = a.db.BlockIDsForPage(source, safeNotebook, safeSection, safePage)
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, safePage)
			_ = a.db.ForgetFile(filePath)
		})
		// Release the deleted blocks' per-block mutex entries (#122).
		a.coordinator.ReleaseBlockMutexes(blockIDs)
	})

	return runErr
}

// DeleteSection moves a section folder (all pages) to .system/trash/ and clears
// their index entries (#62).
func (a *App) DeleteSection(notebook, section string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	if safeNotebook == "" || safeSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	secPath := filepath.Join(notebookDir, safeSection)
	if !isPathWithinRoot(secPath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(secPath); os.IsNotExist(err) {
		return fmt.Errorf("section %q not found", safeSection)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	linked := strings.HasPrefix(source, "linked:")
	var runErr error
	a.coordinator.LockFileWrite(secPath, func() {
		// Collect page files before deletion for index cleanup.
		entries, _ := os.ReadDir(secPath)
		var pageNames []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				pageNames = append(pageNames, strings.TrimSuffix(entry.Name(), ".md"))
			}
		}

		a.tracker.RegisterWrite(secPath)
		if linked {
			// External folder is the source of truth — remove in place (#100).
			if err := os.RemoveAll(secPath); err != nil {
				runErr = err
				return
			}
		} else {
			if _, err := a.moveToTrash(secPath); err != nil {
				runErr = err
				return
			}
		}

		a.coordinator.WithDBWrite(func() {
			for _, pg := range pageNames {
				_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, pg)
			}
		})
	})

	return runErr
}

// DeleteNotebook moves a notebook folder (all sections + pages) to
// .system/trash/ and clears their index entries (#62).
func (a *App) DeleteNotebook(notebook string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	if safeNotebook == "" {
		return fmt.Errorf("notebook name is required")
	}

	nbPath := filepath.Join(a.vaultPath, safeNotebook)
	if !isPathWithinRoot(nbPath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(nbPath); os.IsNotExist(err) {
		return fmt.Errorf("notebook %q not found", safeNotebook)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	a.coordinator.LockFileWrite(nbPath, func() {
		// Walk the subtree BEFORE trashing to collect file paths and their
		// (section, page) for per-page index cleanup via the typed API.
		type pageInfo struct {
			path    string
			section string
			page    string
		}
		var pages []pageInfo
		_ = filepath.WalkDir(nbPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				rel, _ := filepath.Rel(nbPath, path)
				relParts := strings.Split(filepath.ToSlash(rel), "/")
				var section, page string
				if len(relParts) == 1 {
					page = strings.TrimSuffix(relParts[0], ".md")
				} else {
					section = relParts[0]
					page = strings.TrimSuffix(relParts[len(relParts)-1], ".md")
				}
				pages = append(pages, pageInfo{path: path, section: section, page: page})
			}
			return nil
		})

		a.tracker.RegisterWrite(nbPath)
		if _, err := a.moveToTrash(nbPath); err != nil {
			runErr = err
			return
		}
		// Clear blocks + files-cache entries per page via the typed API.
		for _, pg := range pages {
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ClearFileBlocks(nil, "vault", safeNotebook, pg.section, pg.page)
				_ = a.db.ForgetFile(pg.path)
			})
		}
	})

	return runErr
}

// --- Sidebar width / nav order IPC (#63, #68) -----------------------------

// GetSidebarWidth returns the persisted sidebar width from config.yaml.
// Defaults to 256 when unset or below the minimum.
func (a *App) GetSidebarWidth() int {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	w := a.cfg.UI.SidebarWidth
	if w < 200 {
		return 256
	}
	return w
}

// SetSidebarWidth persists a new sidebar width to config.yaml, clamped to
// [200, 480]. Uses RegisterSelfWrite to suppress the config watcher's
// self-write loop.
func (a *App) SetSidebarWidth(px int) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if px < 200 {
		px = 200
	}
	if px > 480 {
		px = 480
	}
	a.configMu.Lock()
	a.cfg.UI.SidebarWidth = px
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// GetNavOrder returns the persisted navigation ordering from config.yaml.
func (a *App) GetNavOrder() (config.NavOrder, error) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.cfg.UI.NavOrder, nil
}

// SetNavOrder persists a new navigation ordering to config.yaml.
func (a *App) SetNavOrder(order config.NavOrder) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	a.configMu.Lock()
	a.cfg.UI.NavOrder = order
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// maxPluginQueryRows caps the number of rows returned by PluginRawQuery so a
// plugin can't exhaust frontend memory with an unbounded SELECT. A `var`
// (not `const`) so tests can temporarily lower the cap without seeding
// thousands of rows.
var maxPluginQueryRows = 5000

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
		dir       string
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

// PluginRawQueryResult is the structured return value for PluginRawQuery.
// `Rows` is the row slice; `Truncated` is true when the result hit
// `maxPluginQueryRows` and the caller should warn the user that more
// rows exist beyond the cap. The cap itself is a security/memory
// safeguard against malicious or accidentally unbounded SELECTs, not a
// design limit on legitimate queries — surfacing `Truncated` lets the
// plugin SDK give the UI a chance to render a "N+ more rows" hint
// rather than silently dropping data on the floor.
type PluginRawQueryResult struct {
	Rows      []map[string]any `json:"rows"`
	Truncated bool             `json:"truncated"`
}

// PluginRawQuery runs a read-only SQL query against the in-memory index.
// Only SELECT / WITH statements are permitted; anything else is rejected so a
// plugin can never mutate the index or schema through this hook. The query
// is also executed against a connection with `PRAGMA query_only = ON`, which
// makes the engine reject any write attempt (including stacked queries like
// `SELECT 1; DROP TABLE blocks;`) regardless of how the prefix check is
// bypassed. Results are returned as PluginRawQueryResult: the row slice plus
// a Truncated flag the SDK can surface when the result hit maxPluginQueryRows.
func (a *App) PluginRawQuery(sqlText string, params []any) (PluginRawQueryResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return PluginRawQueryResult{}, fmt.Errorf("vault database not loaded")
	}
	trimmed := stripSQLComments(sqlText)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return PluginRawQueryResult{}, fmt.Errorf("PluginRawQuery permits only SELECT/WITH statements")
	}

	roDB, err := a.openPluginRODB()
	if err != nil {
		return PluginRawQueryResult{}, err
	}

	a.wg.Add(1)
	defer a.wg.Done()

	out := PluginRawQueryResult{Rows: []map[string]any{}}
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
			out.Rows = append(out.Rows, row)
			// Cap the result set so a malicious plugin can't exhaust memory
			// with SELECT * FROM blocks on a large vault. Surface the cap
			// hit to the caller via Truncated; stop scanning after.
			if len(out.Rows) >= maxPluginQueryRows {
				out.Truncated = true
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

// PluginUpdateTaskMeta updates per-task metadata (pin, progress) by
// round-tripping through the markdown file. Both fields are file-resident
// user intent (ARCHITECTURE §0) — the change is written to the .md file
// as [pin:: true] / [progress:: N] tokens via the parser + renderer, then
// re-indexed so SQLite reflects the new state.
//
// Sentinels allow partial updates:
//
//	pin:      -2 = clear (remove the [pin::] token), -1 = no change,
//	          0 = explicitly unpin ([pin:: false]), 1 = pin ([pin:: true])
//	progress: -1 = no change, 0-100 = set value (0 clears the token)
//
// The tri-state pin sentinel preserves a typed [pin:: false] across UI
// toggles: the renderer emits exactly one pin token from the *bool, so
// pin → unpin → pin can never produce two competing tokens (#123).
func (a *App) PluginUpdateTaskMeta(blockID string, pin int, progress int) (bool, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if pin < -2 || pin > 1 {
		return false, fmt.Errorf("invalid pin value %d (valid: -2=clear, -1=no change, 0=unpin, 1=pin)", pin)
	}
	if progress < -1 || progress > 100 {
		return false, fmt.Errorf("invalid progress value %d (valid: -1=no change, 0-100)", progress)
	}
	if pin == -1 && progress == -1 {
		return true, nil // no-op
	}

	if a.db == nil {
		return false, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var loc db.BlockLocation
	err := a.coordinator.WithDBReadResult(func() error {
		var e error
		loc, e = a.db.GetBlockLocation(blockID)
		return e
	})
	if err != nil {
		return false, fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}
	notebook, section, page, blockType := loc.Notebook, loc.Section, loc.Page, loc.BlockType
	if blockType != string(parser.BlockTask) {
		return false, fmt.Errorf("block %s is not a task", blockID)
	}

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return false, fmt.Errorf("invalid file metadata for block %s", blockID)
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, loc.Source)
	if err != nil {
		return false, fmt.Errorf("resolve notebook dir for block %s: %w", blockID, err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return false, fmt.Errorf("resolved file path escapes notebook root")
	}

	var writeErr error
	a.coordinator.LockBlockWrite(blockID, func() {
		a.coordinator.LockFileWrite(filePath, func() {
			contentBytes, err := os.ReadFile(filePath)
			if err != nil {
				writeErr = err
				return
			}
			fileDate := fileOrDefaultDate(filePath)
			parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, fileDate, a.spacesPerTab)
			if parseErr != nil {
				writeErr = fmt.Errorf("failed to parse file for task meta update: %w", parseErr)
				return
			}
			found := false
			for i := range parsedBlocks {
				if parsedBlocks[i].ID == blockID && parsedBlocks[i].Type == parser.BlockTask {
					if pin != -1 {
						switch pin {
						case -2:
							parsedBlocks[i].Pinned = nil // remove the token
						case 0:
							b := false
							parsedBlocks[i].Pinned = &b // [pin:: false]
						case 1:
							b := true
							parsedBlocks[i].Pinned = &b // [pin:: true]
						}
					}
					if progress != -1 {
						parsedBlocks[i].Progress = progress
					}
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
				// Use the date from the parsed metadata (derived from the
				// file's mtime or frontmatter fallback), NOT time.Now(), so
				// we don't inject today's date over a file whose blocks
				// carry their own per-block file_date.
				fmDate := meta.Date
				if fmDate == "" {
					fmDate = fileDate
				}
				frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(fmDate))
				body = string(contentBytes)
			}
			newContent := parser.RenderFileContent(parsedBlocks, body, frontmatter, a.spacesPerTab)
			a.tracker.RegisterWrite(filePath)
			if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
				writeErr = err
				return
			}

			// Re-index so SQLite reflects the new pin/progress values.
			blocks, remeta, _, _, err := parser.ParseFileContent(newContent, meta.Notebook, meta.Section, meta.Page, meta.Date, a.spacesPerTab)
			if err == nil {
				var idxErr error
				a.coordinator.WithDBWrite(func() {
					idxErr = a.db.IndexFileBlocks(loc.Source, remeta.Notebook, remeta.Section, remeta.Page, blocks, remeta.Tags, remeta.Warnings...)
				})
				if idxErr != nil {
					log.Printf("PluginUpdateTaskMeta: IndexFileBlocks failed: %v", idxErr)
				}
			} else {
				// The file write succeeded but re-parsing the rendered content
				// failed — the index stays stale until the next fsnotify scan.
				// This should never happen (the content was just rendered from
				// successfully-parsed blocks) but log it so the gap is observable.
				log.Printf("PluginUpdateTaskMeta: re-parse of rendered content failed (file written, index stale until next scan): %v", err)
			}

			for _, b := range blocks {
				if b.ID == blockID {
					a.emitBlockChanged(b.ID, safeNotebook, safeSection, safePage, b.FileDate)
				}
			}
		})
	}) // LockBlockWrite
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

// GetPluginRegistry returns the `plugins:` block of .system/config.yaml from
// the in-memory config (the single source of truth maintained by the config
// package + hot-reload watcher), so callers never re-read the file.
func (a *App) GetPluginRegistry() (parser.PluginRegistry, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	a.seedFirstPartyGrants()
}

// seedFirstPartyGrants populates the in-memory grants table with every
// capability for each first-party plugin ID, so bundled plugins are implicitly
// trusted WITHOUT a special-case bypass in requireGrant. This closes the
// spoofing vector where a third-party plugin passes 'silt-attachments' as
// pluginID to bypass all capability checks (#113 security hardening).
//
// The grants are merged into the in-memory config on every applyConfigLocked
// call. If they happen to persist to config.yaml via a later Save, that is
// harmless — they are just grant entries. If a user removes them, they are
// re-seeded on the next config load.
func (a *App) seedFirstPartyGrants() {
	if a.cfg.Plugins.Grants == nil {
		a.cfg.Plugins.Grants = map[string]map[string]string{}
	}
	for id := range firstPartyPluginIDs {
		if a.cfg.Plugins.Grants[id] == nil {
			a.cfg.Plugins.Grants[id] = map[string]string{}
		}
		for cap := range plugins.KnownCapabilities {
			a.cfg.Plugins.Grants[id][string(cap)] = plugins.QualGranted
		}
	}
}

// UpdatePluginSetting atomically updates a single per-plugin setting key and
// persists it — the targeted read-modify-write that replaces the frontend
// read-mutate-saveConfig dance which could race an external config.yaml edit
// (e.g. VS Code) landing between the read and the Go-side atomic write (#120).
// Only plugins.plugin_settings[pluginID][key] is touched; every other config
// field is preserved verbatim, so a concurrent external edit to an unrelated
// section is not clobbered.
//
// Atomicity: configMu is held across the in-memory mutation AND the disk save,
// so concurrent internal callers (and the watcher's applyConfig) cannot
// interleave a snapshot-and-save and lose an update. The external-edit race is
// handled by RegisterSelfWrite (suppresses the watcher's reaction to our own
// write) + this lock. Like SaveSystemConfig it does NOT emit config:changed
// (the frontend store updates optimistically; external edits still flow through
// the watcher -> applyConfig with emit).
func (a *App) UpdatePluginSetting(pluginID string, key string, value any) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if pluginID == "" || key == "" {
		return fmt.Errorf("pluginID and key are required")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Plugins.PluginSettings == nil {
		a.cfg.Plugins.PluginSettings = map[string]any{}
	}
	entry, _ := a.cfg.Plugins.PluginSettings[pluginID].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry[key] = value
	a.cfg.Plugins.PluginSettings[pluginID] = entry
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, a.cfg)
}

// GetPluginSettingsForNotebook resolves a plugin's settings map for the
// ACTIVE notebook, applying the co-located per-notebook override layer (#133).
//
// Merge precedence: vault-scoped config.yaml is the baseline; a linked
// notebook's co-located <root>/.system/config.yaml overlays it per-key
// (linked wins). For a vault notebook (or no active notebook), the vault
// settings are returned unchanged. For a linked notebook with no co-located
// file, the vault settings are returned (the normal case). The merge is
// computed on every call from the live, mtime-cached co-located config, so an
// external edit to either file is reflected on the next call (the watcher
// also emits linked-config:changed to drive reactive refreshes).
//
// pluginID selects which plugin's entry is returned (e.g. "silt-kanban"). An
// unknown pluginID yields an empty map, not an error — a plugin with no
// stored settings is the same as a plugin whose settings are all defaults.
//
// notebookName is the display name (the sidebar label); it is resolved to a
// source via resolveSourceByName. An empty notebookName is treated as the
// vault scope (no active notebook).
func (a *App) GetPluginSettingsForNotebook(pluginID, notebookName string) (map[string]any, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	if pluginID == "" {
		return nil, fmt.Errorf("pluginID is required")
	}

	// Snapshot the vault entry + resolve the source under the config read
	// lock. linkedConfigFor uses its OWN mutex (linkedConfigsMu) for the
	// co-located cache, so we release configMu before calling it — this
	// avoids holding configMu during disk I/O on a cache miss and avoids
	// the concurrent-map-write panic that would arise if linkedConfigFor
	// wrote to linkedConfigs under an RLock.
	//
	// CRITICAL: vaultEntry is cloned (via MergePluginSettings) INSIDE the
	// RLock, not after release. UpdatePluginSetting mutates this map
	// in-place (entry[key]=value) under configMu.Lock(); cloning after
	// RUnlock would expose the clone iteration to a concurrent write.
	a.configMu.RLock()
	vaultEntry, _ := a.cfg.Plugins.PluginSettings[pluginID].(map[string]any)
	if vaultEntry == nil {
		vaultEntry = map[string]any{}
	}
	// Deep-clone under the lock so the returned map is a safe snapshot.
	vaultClone := config.MergePluginSettings(vaultEntry, nil)
	source := config.LinkedNotebooksVaultSource
	var ln config.LinkedNotebook
	if notebookName != "" {
		source = a.resolveSourceByNameLocked(notebookName)
		if source != config.LinkedNotebooksVaultSource {
			for _, candidate := range a.cfg.LinkedNotebooks {
				if candidate.Source() == source {
					ln = candidate
					break
				}
			}
		}
	}
	a.configMu.RUnlock()

	if source == config.LinkedNotebooksVaultSource {
		// Vault notebook (or no active notebook): return the cloned snapshot.
		return vaultClone, nil
	}

	// Linked notebook: if the registry didn't find the source (stale),
	// degrade gracefully to vault settings (already cloned).
	if ln.ID == "" {
		log.Printf("GetPluginSettingsForNotebook(%s,%s): source %q not in registry; returning vault settings", pluginID, notebookName, source)
		return vaultClone, nil
	}
	linkedCfg, err := a.linkedConfigFor(ln)
	if err != nil {
		// Fail-loud: an unparseable co-located config surfaces as an error
		// rather than silently degrading to vault settings (the user must
		// see their broken file). A MISSING file is not an error (LoadLinked
		// returns Defaults + nil in that case).
		return nil, fmt.Errorf("linked config for %s: %w", ln.DisplayName, err)
	}
	linkedEntry, _ := linkedCfg.Plugins.PluginSettings[pluginID].(map[string]any)
	if linkedEntry == nil {
		linkedEntry = map[string]any{}
	}
	return config.MergePluginSettings(vaultClone, linkedEntry), nil
}

// ListPlugins enumerates plugin folders under .system/plugins/, surfacing
// manifest name/version and the disabled sentinel for the manager UI.
func (a *App) ListPlugins() ([]parser.PluginInfo, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
				info.Capabilities = m.Capabilities
				info.Settings = m.Settings
				info.Homepage = m.Homepage
				info.UpdateURL = m.UpdateURL
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	safeID := sanitizePathSegment(pluginID)
	if safeID == "" {
		return "", fmt.Errorf("invalid plugin id")
	}
	srcPath := filepath.Join(a.vaultPath, ".system", "plugins", safeID, "index.js")
	if !isPathWithinRoot(srcPath, a.vaultPath) {
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
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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
	// Publisher-trust gate (#111 distribution v2, #150 follow-up): when the
	// user has populated TrustedPublishers, the plugin's Author must be on
	// the list. An empty TrustedPublishers preserves the current "everyone
	// is welcome" posture — populating the list is an explicit opt-in to
	// a stricter stance. A plugin with an empty Author cannot be matched
	// against a non-empty trust list, which is the correct (defense-
	// in-depth) default: anonymous plugins require no trust decision.
	if verr := enforcePublisherTrust(manifest.Author); verr != nil {
		_ = plugins.Uninstall(a.vaultPath, manifest.ID)
		return parser.PluginManifest{}, verr
	}
	a.emitPluginsChanged()
	return manifestToParser(manifest), nil
}

// UninstallPlugin removes a plugin folder and emits plugins:changed. It also
// revokes every capability grant for the plugin so a later reinstall re-prompts
// rather than inheriting the prior trust decision (#113).
func (a *App) UninstallPlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if err := plugins.Uninstall(a.vaultPath, pluginID); err != nil {
		return err
	}
	// Evict the rate-limiter bucket so uninstalled plugins don't leak entries (#153).
	if a.rateLimiter != nil {
		a.rateLimiter.evict(pluginID)
	}
	// Best-effort grant cleanup; a failure here must not mask the successful
	// uninstall (the folder is already gone). The grants block is harmless if
	// it lingers, but cleaning it keeps the manager UI honest.
	_ = a.revokeAllGrants(pluginID)
	a.emitPluginsChanged()
	return nil
}

// revokeAllGrants removes every capability grant for pluginID without
// emitting plugins:changed (the caller decides whether to emit). Used by
// UninstallPlugin and the vault teardown path. Acquires configMu internally.
func (a *App) revokeAllGrants(pluginID string) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Plugins.Grants == nil {
		return nil
	}
	if _, ok := a.cfg.Plugins.Grants[pluginID]; !ok {
		return nil
	}
	delete(a.cfg.Plugins.Grants, pluginID)
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, a.cfg)
}

// EnablePlugin / DisablePlugin toggle a per-plugin ".disabled" sentinel
// (the loader skips disabled plugins), then emit plugins:changed.
func (a *App) EnablePlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if err := plugins.SetDisabled(a.vaultPath, pluginID, false); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

func (a *App) DisablePlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
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

// --- v2 SDK capability & permission model (#113) -------------------------

// firstPartyPluginIDs is the set of bundled plugin ids. First-party plugins
// ship compiled with the app and are trusted by definition, so the capability
// gate grants them every capability implicitly — they never need a user grant.
// Third-party (disk) plugins route through requireGrant. Kept in sync with the
// frontend registry (frontend/src/plugins/registry.ts); Phase 5 appends
// "silt-attachments".
var firstPartyPluginIDs = map[string]bool{
	"silt-agenda":      true,
	"silt-calendar":    true,
	"silt-kanban":      true,
	"silt-attachments": true,
}

// isFirstPartyPlugin reports whether pluginID is a bundled (trusted) plugin.
func isFirstPartyPlugin(pluginID string) bool {
	return firstPartyPluginIDs[pluginID]
}

// requireGrant is the single server-side enforcement point for every privileged
// v2 SDK binding (#113). It returns nil if the plugin may use the capability,
// or a structured *plugins.CapabilityDeniedError (never a panic) the frontend
// SDK surfaces as an actionable message + re-prompt.
//
// pluginID is validated against IsValidID to reject path-traversal payloads
// before they reach filepath.Join in scratch-dir / audit-log paths. First-party
// plugins receive their grants via seedFirstPartyGrants at config-load time,
// so there is NO special-case bypass here — a third-party plugin cannot
// spoof a first-party ID to bypass capability checks.
//
// Callers that need the qualifier (e.g. to enforce notebook vs vault scope on
// file writes) read it via grantedQualifier after a successful requireGrant.
func (a *App) requireGrant(pluginID string, cap plugins.Capability) error {
	if !plugins.IsValidID(pluginID) {
		return &plugins.CapabilityDeniedError{
			Plugin:     "<invalid>",
			Capability: string(cap),
			Requested:  plugins.QualGranted,
		}
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	if _, ok := a.cfg.Plugins.Grants[pluginID]; ok {
		if qual, ok := a.cfg.Plugins.Grants[pluginID][string(cap)]; ok && qual != "" {
			return nil
		}
	}
	return &plugins.CapabilityDeniedError{
		Plugin:     pluginID,
		Capability: string(cap),
		Requested:  plugins.QualGranted,
	}
}

// grantedQualifier returns the scope qualifier for a granted capability, or
// ("", false) if not granted. Used by bindings that narrow scope (file-write
// notebook vs vault).
func (a *App) grantedQualifier(pluginID string, cap plugins.Capability) (string, bool) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	if caps, ok := a.cfg.Plugins.Grants[pluginID]; ok {
		if qual, ok := caps[string(cap)]; ok && qual != "" {
			return qual, true
		}
	}
	return "", false
}

// RequestCapability grants a capability to a plugin and persists it atomically
// to config.yaml (#113). qualifier is normalized to a known value ("" or
// "true" → "granted"). The capability must be a recognized one (unknown caps
// are rejected). Emits plugins:changed so the manager UI refreshes.
func (a *App) RequestCapability(pluginID, capability, qualifier string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return fmt.Errorf("invalid plugin id %q (must match ^[a-z0-9-]+$)", pluginID)
	}
	if !plugins.KnownCapabilities[plugins.Capability(capability)] {
		return fmt.Errorf("unknown capability %q (recognized: %s)", capability, plugins.ListCapabilities())
	}
	qual := plugins.QualGranted
	if qualifier != "" && qualifier != "true" {
		if !pluginsValidQualifier(qualifier) {
			return fmt.Errorf("invalid qualifier %q", qualifier)
		}
		qual = qualifier
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Plugins.Grants == nil {
		a.cfg.Plugins.Grants = map[string]map[string]string{}
	}
	caps, ok := a.cfg.Plugins.Grants[pluginID]
	if !ok || caps == nil {
		caps = map[string]string{}
	}
	caps[capability] = qual
	a.cfg.Plugins.Grants[pluginID] = caps
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

// RevokeCapability revokes a capability grant. capability == "" revokes every
// grant for the plugin (used on uninstall). Emits plugins:changed.
func (a *App) RevokeCapability(pluginID, capability string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return fmt.Errorf("invalid plugin id %q (must match ^[a-z0-9-]+$)", pluginID)
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Plugins.Grants == nil {
		return nil // nothing to revoke
	}
	caps, ok := a.cfg.Plugins.Grants[pluginID]
	if !ok {
		return nil
	}
	if capability == "" {
		delete(a.cfg.Plugins.Grants, pluginID)
	} else {
		delete(caps, capability)
		if len(caps) == 0 {
			delete(a.cfg.Plugins.Grants, pluginID)
		}
	}
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

// GetGrantedCapabilities returns the full per-plugin capability grant table
// (pluginID → capability → qualifier) so the manager UI can show
// requested-vs-granted. First-party plugins are NOT included (they are
// implicitly granted). Returns an empty (non-nil) map pre-vault.
func (a *App) GetGrantedCapabilities() (map[string]map[string]string, error) {
	if a.vaultPath == "" {
		return map[string]map[string]string{}, nil
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	out := make(map[string]map[string]string, len(a.cfg.Plugins.Grants))
	for pid, caps := range a.cfg.Plugins.Grants {
		if isFirstPartyPlugin(pid) {
			continue
		}
		clone := make(map[string]string, len(caps))
		for k, v := range caps {
			clone[k] = v
		}
		out[pid] = clone
	}
	return out, nil
}

// pluginsValidQualifier is a tiny adapter so app.go does not need to reach into
// the plugins package's unexported validQualifiers map.
func pluginsValidQualifier(q string) bool {
	switch q {
	case plugins.QualGranted, plugins.QualNotebook, plugins.QualVault:
		return true
	}
	return false
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

// enforcePublisherTrust gates a plugin install on its Author matching a name
// in settings.TrustedPublishers (#111 distribution v2, #150 follow-up).
//
// Policy:
//   - Empty/nil TrustedPublishers → allow (preserves the current
//     "everyone-is-welcome" behavior so populating the list is an explicit
//     opt-in to a stricter stance).
//   - Non-empty TrustedPublishers AND author in the list → allow.
//   - Non-empty TrustedPublishers AND author NOT in the list → reject.
//   - Non-empty TrustedPublishers AND empty author → reject (anonymous
//     plugins cannot match a trust list, which is the correct
//     defense-in-depth default).
//
// The function distinguishes "settings file does not exist" (fail-open: no
// trust list configured) from "settings file exists but unreadable/corrupt"
// (fail-closed: a hostile plugin that can interfere with settings reads must
// not disable the trust gate). The error is logged at warn level.
func enforcePublisherTrust(author string) error {
	settings, err := vault.LoadSettings()
	if err != nil {
		log.Printf("enforcePublisherTrust: settings file exists but is unreadable — failing closed to protect the trust gate: %v", err)
		return fmt.Errorf("trusted-publishers list is configured but settings could not be read (corrupt settings.json?): %w", err)
	}
	trusted := settings.TrustedPublishers
	if len(trusted) == 0 {
		return nil
	}
	author = strings.TrimSpace(author)
	if author == "" {
		return fmt.Errorf("plugin author is empty; cannot be matched against the non-empty trusted-publishers list")
	}
	for _, p := range trusted {
		if strings.EqualFold(strings.TrimSpace(p), author) {
			return nil
		}
	}
	return fmt.Errorf("plugin author %q is not in the trusted-publishers list (add it via AddTrustedPublisher to install)", author)
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
		Capabilities:   m.Capabilities,
		Settings:       m.Settings,
		Homepage:       m.Homepage,
		UpdateURL:      m.UpdateURL,
	}
}
