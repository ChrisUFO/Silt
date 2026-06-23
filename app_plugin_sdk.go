package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"silt/backend/config"
	"silt/backend/db"
	"silt/backend/parser"
	"silt/backend/plugins"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// maxPluginQueryRows caps the number of rows returned by PluginRawQuery so a
// plugin can't exhaust frontend memory with an unbounded SELECT. A `var`
// (not `const`) so tests can temporarily lower the cap without seeding
// thousands of rows.
var maxPluginQueryRows = 5000

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
// Session-token verified (#236) — closes the impersonation vector where a
// malicious main-webview plugin bypasses the SDK and calls App.PluginRawQuery
// directly.
func (a *App) PluginRawQuery(pluginID, sessionToken, sqlText string, params []any) (PluginRawQueryResult, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return PluginRawQueryResult{}, err
	}
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
// Session-token verified (#236) — a plugin cannot mutate another plugin's
// blocks by spoofing the call without the SDK.
func (a *App) PluginMutateBlock(pluginID, sessionToken, blockID, newText string) (bool, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return false, err
	}
	if err := a.MutateBlock(blockID, newText); err != nil {
		return false, err
	}
	return true, nil
}

// PluginUpdateBlockState wraps UpdateBlockState for the plugin SDK.
// Session-token verified (#236).
func (a *App) PluginUpdateBlockState(pluginID, sessionToken, blockID, status string) (bool, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return false, err
	}
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
func (a *App) PluginUpdateTaskMeta(pluginID, sessionToken, blockID string, pin int, progress int) (bool, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return false, err
	}
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

			frontmatter, body := parser.SplitFrontmatter(string(contentBytes))
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
// (e.g. an external editor) landing between the read and the Go-side atomic write (#120).
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

// AppendDismissedTip records a one-time UI tip ID as dismissed (#197). Mirrors
// the atomic pattern of UpdatePluginSetting: vaultMu.RLock + configMu.Lock held
// across the in-memory mutation and config.Save, with RegisterSelfWrite
// suppressing the watcher's reaction to our own write. Idempotent — calling
// twice with the same tipID produces a single-entry slice. Like the other
// internal atomic setters, it does NOT emit config:changed; the frontend
// settings store mirrors the change optimistically and external edits still
// flow through watcher → applyConfig.
func (a *App) AppendDismissedTip(tipID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if tipID == "" {
		return fmt.Errorf("tipID is required")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.UI.DismissedTips == nil {
		a.cfg.UI.DismissedTips = []string{}
	}
	for _, existing := range a.cfg.UI.DismissedTips {
		if existing == tipID {
			return nil
		}
	}
	a.cfg.UI.DismissedTips = append(a.cfg.UI.DismissedTips, tipID)
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
				info.ContentSHA256 = m.ContentSHA256
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
