package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"silt/backend/config"
	"silt/backend/core"
	"silt/backend/db"
	"silt/backend/monitor"
	"silt/backend/parser"
	"silt/backend/templates"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

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

	// grants is the per-host plugin capability grant table (F4). It lives in
	// <configDir>/silt/grants.json (NOT in vault-scoped config.yaml) so a
	// vault synced from another host cannot carry the counterpart's grant
	// decisions. Guarded by configMu (grants are config-tier state even
	// though they persist to a different file than config.yaml). Loaded in
	// initializeVaultServices, torn down in teardownVaultServices.
	grants vault.GrantsStore
	// quarantinedLinks holds the IDs of linked notebooks whose on-disk root
	// no longer matches the stored RootFingerprint (F3). Presence in this set
	// means the link is quarantined: excluded from indexing, reads, and
	// writes; the user sees a re-link prompt. Guarded by configMu. Populated
	// at vault open (fingerprint mismatch) and on fsnotify reload (root_path
	// changed); cleared by UnlinkNotebook (re-link = unlink + link).
	quarantinedLinks map[string]struct{}

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

	// pluginDBs holds the per-plugin SQLite store connections (#213). Each
	// plugin that exercises the plugin-db capability gets its own *sql.DB
	// pool (MaxOpenConns=1) at <vault>/.system/plugins/<id>/data/plugin.db —
	// a distinct file from the core index, never ATTACH-able to it. Opened
	// lazily by openPluginDB; closed on teardownPlugin(id), on uninstall
	// (before the folder is removed — Windows file lock), and on vault close.
	// Guarded by pluginDBsMu.
	pluginDBsMu sync.Mutex
	pluginDBs   map[string]*sql.DB

	// rateLimiter caps per-plugin PluginFetch RPS so a network-granted plugin
	// cannot hammer external services (#153). Guarded by its own internal
	// mutex; eviction happens on uninstall.
	rateLimiter *pluginRateLimiter

	// pluginSessions maps session tokens → pluginIDs for binding-identity
	// verification (#151). The loader calls RegisterPluginSession at load
	// time; privileged bindings validate the token before proceeding so a
	// plugin cannot impersonate another by passing a different pluginID. This
	// is a stepping stone — the full fix requires per-plugin isolated webviews
	// (#152), which is deferred. Guarded by pluginSessionsMu.
	pluginSessionsMu sync.RWMutex
	pluginSessions   map[string]string // token → pluginID

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
		spacesPerTab:   4,
		rateLimiter:    newPluginRateLimiter(),
		pluginSessions: make(map[string]string),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	settings, err := vault.LoadSettings()
	if err != nil && !errors.Is(err, vault.ErrSettingsFingerprintMismatch) {
		// The settings file exists on disk but is unreadable or
		// malformed. Don't silently fall through to "no vault" — the
		// user has a vault setup, something is just broken.
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "vault:init-error",
				fmt.Sprintf("failed to load settings.json: %v", err))
		}
		return
	}
	// F20: settings loaded fine but the trust-anchor fingerprint changed
	// since last launch (possible tampering, or a legit external edit the
	// user hasn't acknowledged yet). Surface a confirmation dialog so the
	// user can accept or reject the change. The settings are still used
	// in-memory (they are valid JSON with a valid schema).
	if errors.Is(err, vault.ErrSettingsFingerprintMismatch) && a.ctx != nil {
		runtime.EventsEmit(a.ctx, "settings:fingerprint-mismatch", nil)
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

// ConfirmSettingsChange is the F20 user-ack binding. When the frontend detects
// a settings:fingerprint-mismatch event (the trust-anchor fields in
// settings.json changed since last launch), it shows a confirmation dialog;
// if the user confirms the change was intentional, it calls this binding,
// which updates the on-disk fingerprint to match the current values so the
// next launch proceeds without a prompt. A user who rejects the dialog can
// manually fix settings.json; the mismatch persists across relaunches until
// either the values are restored or the user confirms.
func (a *App) ConfirmSettingsChange() error {
	if _, err := vault.ConfirmSettingsChange(); err != nil {
		return fmt.Errorf("confirm settings change: %w", err)
	}
	return nil
}

// ConfirmGrantsMigration is the F4 user-ack binding. When the frontend detects
// a grants:migration-required event (the vault's legacy config.yaml carries a
// grants block this host has never seen), it shows a one-time confirmation
// dialog. If the user confirms, this binding:
//  1. Merges the legacy grants into the per-host store (preserving any grants
//     the host already has — e.g. first-party seeds).
//  2. Persists the merged store to grants.json.
//  3. Rewrites config.yaml WITHOUT the grants block (the field is already
//     gone from the struct, so a normalize + Save strips it from disk).
//
// If the user denies, the host store keeps its first-party seeds only; every
// third-party plugin re-prompts on first use (the safe default).
func (a *App) ConfirmGrantsMigration(legacyGrants map[string]map[string]string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	// Merge legacy grants into the host store. First-party IDs are skipped —
	// they are always seeded implicitly and the user never granted them
	// manually.
	if a.grants == nil {
		a.grants = vault.GrantsStore{}
	}
	for pid, caps := range legacyGrants {
		if isFirstPartyPlugin(pid) {
			continue
		}
		if a.grants[pid] == nil {
			a.grants[pid] = map[string]string{}
		}
		for cap, qual := range caps {
			a.grants[pid][cap] = qual
		}
	}
	if err := vault.SaveGrants(a.grants); err != nil {
		return fmt.Errorf("persist migrated grants: %w", err)
	}
	// Rewrite config.yaml so the legacy grants block is stripped from disk.
	// The struct no longer has a Grants field, so a round-trip through Save
	// drops it. RegisterSelfWrite suppresses the watcher's reaction.
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return fmt.Errorf("strip legacy grants from config.yaml: %w", err)
	}
	a.emitPluginsChanged()
	return nil
}

// DeclineGrantsMigration is the F4 user-decline binding. When the user
// dismisses the grants-migration dialog, this strips the legacy grants:
// block from config.yaml so the dialog does NOT re-fire on the next launch.
// The host store keeps its first-party seeds only; every third-party plugin
// re-prompts on first use (the safe default). The user's third-party grants
// are lost — they chose not to migrate.
func (a *App) DeclineGrantsMigration() error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	// config.Save drops the grants field (it's gone from the struct), so
	// the on-disk file no longer carries the legacy block.
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return fmt.Errorf("strip legacy grants from config.yaml: %w", err)
	}
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	// Emit vault:closing so the frontend plugin loader runs every plugin's
	// onVaultClose/onShutdown hook (#106) before IPC tears down. Best-effort:
	// a nil ctx (headless test) skips the emit.
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "vault:closing", struct{}{})
	}
	// Wait for any in-flight Wails-bound calls (UpdateBlockState,
	// QueryTasks) to complete before tearing
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
	// Stop the audit writer FIRST so it drains queued entries for the closing
	// vault before any service it depends on (just vaultPath at this point)
	// goes away. After this returns, every enqueued audit write is on disk.
	stopNetworkAuditWriter()
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
	// Close every per-plugin DB pool (#213). These point at files under the
	// closing vault's .system/plugins/<id>/data/, so they must be released
	// before the vault path goes away (and before any folder removal on a
	// vault move — Windows file lock).
	a.closeAllPluginDBs()
	if a.db != nil {
		// Close runs PRAGMA wal_checkpoint(TRUNCATE) so the WAL is merged
		// into the main index file on a clean close (#29).
		_ = a.db.Close()
		a.db = nil
	}
	a.coordinator = nil
	a.vaultPath = ""
	// F4: clear the per-host grants store so a subsequent vault open starts
	// fresh (LoadGrants + seedFirstPartyGrants repopulate). The on-disk file
	// is untouched — it persists across vault sessions.
	a.configMu.Lock()
	a.grants = nil
	a.quarantinedLinks = nil
	a.configMu.Unlock()
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
	// F4: load the per-host grants store BEFORE applyConfigLocked so the
	// first-party seed merges into the real store, not a transient empty one.
	// Grants live in <configDir>/silt/grants.json (NOT vault-scoped config.yaml)
	// so a synced vault cannot carry the counterpart's grant decisions.
	grantsStore, grantsErr := vault.LoadGrants()
	if grantsErr != nil {
		// A corrupt grants file is non-fatal — log + start with an empty
		// store. The user re-grants on first use (the safe default). Every
		// third-party plugin will prompt; first-party plugins seed regardless.
		log.Printf("initializeVaultServices: grants load failed (starting with empty store): %v", grantsErr)
		grantsStore = vault.GrantsStore{}
	}
	a.configMu.Lock()
	a.grants = grantsStore
	a.configMu.Unlock()
	a.applyConfigLocked(cfg) // sets a.cfg + a.spacesPerTab + seeds first-party grants into a.grants
	// The config:error event above fires before the frontend mounts and
	// subscribes, so it is typically lost. Stash the error for
	// GetConfigLoadError() to surface on the frontend's initial loadConfig().
	a.configMu.Lock()
	a.configLoadErr = cfgErr
	a.configMu.Unlock()

	// F4 migration: if the vault's config.yaml still carries a legacy
	// `plugins.grants:` block AND the host store was empty before we seeded
	// first-party grants, this is a pre-F4 vault opening on a host that has
	// never seen it. Emit grants:migration-required so the frontend shows a
	// one-time confirmation dialog. The user's confirm calls
	// ConfirmGrantsMigration, which writes the legacy grants to the host file
	// and rewrites config.yaml without the grants block. If the user denies,
	// the host store stays seeded with first-party only; every third-party
	// plugin re-prompts on first use (the safe default).
	if len(grantsStore) == 0 && grantsErr == nil {
		legacy := vault.LoadLegacyVaultGrants(vaultPath)
		// Strip first-party entries — they are always seeded implicitly, never
		// migrated (the user never granted them manually).
		hasThirdParty := false
		for pid := range legacy {
			if !isFirstPartyPlugin(pid) {
				hasThirdParty = true
				break
			}
		}
		if hasThirdParty && a.ctx != nil {
			runtime.EventsEmit(a.ctx, "grants:migration-required", legacy)
		}
	}

	// F3: verify linked-notebook fingerprints before the vault scan. Legacy
	// links (pre-F3, no fingerprint) get one assigned silently; mismatched
	// links are quarantined (excluded from indexing/reads/writes) and emit
	// linked-notebook:quarantined so the frontend shows a re-link prompt.
	a.configMu.Lock()
	a.quarantinedLinks = make(map[string]struct{})
	a.configMu.Unlock()
	a.verifyLinkedNotebookFingerprints()

	// Persistent on-disk WAL index at <vault>/.system/index.sqlite. Survives
	// restarts so a warm launch re-indexes only changed files (#29). Markdown
	// remains the source of truth; deleting the 3 index files forces a clean
	// full rebuild. The .system dir is created by ScaffoldVault.
	systemDir := filepath.Join(vaultPath, ".system")
	if err := os.MkdirAll(systemDir, 0o700); err != nil {
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
	migrationWarnings := vault.MigratePerDayFiles(vaultPath, a.spacesPerTab)

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
	// network.log files so entries survive a restart (#157). The writer is
	// started AFTER seeding so it never races the seed (#235).
	seedNetworkAuditFromDisk(vaultPath)
	startNetworkAuditWriter(vaultPath)

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
	// to detect an external edit (e.g. an external editor) that landed in the source
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
		if err != nil && !errors.Is(err, vault.ErrSettingsFingerprintMismatch) {
			return fmt.Errorf("move vault: snapshot settings: %w", err)
		}
		a.teardownVaultServices()
		if _, err := vault.UpdateSettings(func(s *vault.AppSettings) {
			s.VaultPath = dest
		}); err != nil {
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

		if _, err := vault.UpdateSettings(func(s *vault.AppSettings) {
			s.VaultPath = abs
		}); err != nil {
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
