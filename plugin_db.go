package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silt/backend/plugins"
)

// This file implements the per-plugin SQLite store (#213). Each plugin that
// exercises the plugin-db capability gets its own *sql.DB pool at
// <vault>/.system/plugins/<id>/data/plugin.db — a DISTINCT connection from the
// core index, never ATTACH-able to it. sqlite-vec is registered on every
// connection (vec0 virtual tables + vec_distance_cosine) via the
// modernc.org/sqlite/vec blank import in backend/db.
//
// The safety model mirrors PluginRawQuery (session-token gate, comment strip,
// statement-class check) but adapts it: the plugin DB is writable (the plugin
// owns its schema and chooses durability semantics), so Exec permits DDL/DML
// but blocks statements that could escape the plugin file (ATTACH, DETACH,
// and PRAGMAs other than the version-tracking user_version used internally by
// migrate). Query is SELECT/WITH-only with the same row cap.

// pluginDBPragmaSQL is applied on every per-plugin connection open. Mirrors the
// core index pragmas (backend/db/schema.go) for consistency; WAL is persisted
// in the file header so it inherits across connections.
const pluginDBPragmaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA temp_store   = MEMORY;
PRAGMA busy_timeout = 5000;
PRAGMA foreign_keys = ON;
`

// openPluginDB returns the cached *sql.DB for pluginID, opening it lazily at
// <vault>/.system/plugins/<id>/data/plugin.db (creating the dir + file on
// first use). The caller MUST hold vaultMu (at least RLock) so vaultPath is
// stable. The pool is capped at MaxOpenConns(1) to match the core index's
// single-writer model.
func (a *App) openPluginDB(pluginID string) (*sql.DB, error) {
	a.pluginDBsMu.Lock()
	defer a.pluginDBsMu.Unlock()
	if a.pluginDBs == nil {
		a.pluginDBs = make(map[string]*sql.DB)
	}
	if db, ok := a.pluginDBs[pluginID]; ok {
		return db, nil
	}
	safeID := sanitizePathSegment(pluginID)
	if safeID == "" {
		return nil, fmt.Errorf("invalid plugin id %q", pluginID)
	}
	dataDir := filepath.Join(a.vaultPath, ".system", "plugins", safeID, "data")
	if !isPathWithinRoot(dataDir, a.vaultPath) {
		return nil, fmt.Errorf("plugin data dir escapes vault root")
	}
	dataDirCreated := false
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create plugin data dir: %w", err)
	}
	dataDirCreated = true
	dbPath := filepath.Join(dataDir, "plugin.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		// Clean up the data dir we just created so a failing open doesn't
		// litter empty dirs (a hostile plugin could trigger this repeatedly).
		if dataDirCreated {
			_ = os.Remove(filepath.Join(dataDir, "plugin.db"))
			_ = os.Remove(dataDir)
		}
		return nil, fmt.Errorf("open plugin db for %s: %w", pluginID, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(pluginDBPragmaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply plugin db pragmas: %w", err)
	}
	a.pluginDBs[pluginID] = db
	return db, nil
}

// closePluginDB closes and removes the cached pool entry for a single plugin.
// Called from teardownPlugin(id) (frontend, via the ClosePluginDB binding) and
// from UninstallPlugin (before the folder is removed — Windows file lock).
// No-op if the plugin has no open pool. Idempotent.
func (a *App) closePluginDB(pluginID string) {
	a.pluginDBsMu.Lock()
	db, ok := a.pluginDBs[pluginID]
	if ok {
		delete(a.pluginDBs, pluginID)
	}
	a.pluginDBsMu.Unlock()
	if db != nil {
		_ = db.Close()
	}
}

// closeAllPluginDBs closes every per-plugin pool. Called from
// teardownVaultServices on vault close / app shutdown / vault move.
func (a *App) closeAllPluginDBs() {
	a.pluginDBsMu.Lock()
	dbs := a.pluginDBs
	a.pluginDBs = nil
	a.pluginDBsMu.Unlock()
	for _, db := range dbs {
		// Checkpoint the WAL into the main file so plugin.db-wal/-shm don't
		// linger on disk after a clean close (mirrors the core index's
		// Close, which runs TRUNCATE). Best-effort: a checkpoint failure
		// must not block close.
		_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		_ = db.Close()
	}
}

// ClosePluginDB is the Wails binding that closes a single plugin's DB pool.
// Called from the frontend loader's teardownPlugin (after session cleanup,
// before onShutdown) so the file handle is released before any folder
// removal on uninstall (#213 lifecycle).
func (a *App) ClosePluginDB(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	a.closePluginDB(pluginID)
	return nil
}

// pluginDBUserVersionPragmas are the only PRAGMAs permitted in Exec (the
// version-tracking mechanism migrate uses). Any other PRAGMA is rejected so a
// plugin cannot reconfigure its connection to escape isolation (e.g.
// PRAGMA query_only, PRAGMA attach, etc.).
var pluginDBAllowedPragmas = map[string]bool{
	"user_version": true,
}

// containsBlockedStatement reports whether sqlText contains a statement that
// could let the plugin escape its isolated file. ATTACH/DETACH would let it
// reach the core index or arbitrary files; an unguarded PRAGMA could weaken
// the connection's safety properties. This is a defense-in-depth check on top
// of the connection-level isolation (the plugin connection has no handle to
// the core index and ATTACH of the core path is structurally separate).
func containsBlockedStatement(sqlText string) (blocked string, found bool) {
	upper := strings.ToUpper(sqlText)
	// ATTACH / DETACH as standalone statement keywords. stmtContainsKeyword
	// loops over every occurrence (catches stacked-query escapes).
	if stmtContainsKeyword(upper, "ATTACH") {
		return "ATTACH", true
	}
	if stmtContainsKeyword(upper, "DETACH") {
		return "DETACH", true
	}
	// PRAGMA: allow only user_version (used internally by migrate). Any other
	// PRAGMA is rejected — a plugin has no legitimate need to reconfigure its
	// connection pragmas, and several (query_only, etc.) would undermine the
	// contract. Loop over EVERY PRAGMA occurrence so a stacked-query payload
	// like "PRAGMA user_version=1; PRAGMA query_only=OFF" is caught (the
	// second PRAGMA must also be allowed, not just the first).
	searchUpper := upper
	searchOriginal := sqlText
	for {
		idx := strings.Index(searchUpper, "PRAGMA")
		if idx < 0 {
			break
		}
		// Verify token boundary before.
		if idx > 0 {
			c := searchUpper[idx-1]
			if (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' {
				searchUpper = searchUpper[idx+6:]
				searchOriginal = searchOriginal[idx+6:]
				continue
			}
		}
		// Verify token boundary after.
		afterIdx := idx + 6
		afterOK := afterIdx >= len(searchUpper)
		if !afterOK {
			c := searchUpper[afterIdx]
			afterOK = !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_')
		}
		if !afterOK {
			searchUpper = searchUpper[afterIdx:]
			searchOriginal = searchOriginal[afterIdx:]
			continue
		}
		// Extract the pragma name from the original (non-uppercased) text.
		rest := strings.TrimSpace(searchOriginal[afterIdx:])
		nameEnd := len(rest)
		for i, c := range rest {
			if c == '=' || c == ' ' || c == ';' || c == '\n' || c == '\t' {
				nameEnd = i
				break
			}
		}
		name := strings.ToLower(rest[:nameEnd])
		if !pluginDBAllowedPragmas[name] {
			return "PRAGMA " + name, true
		}
		// Advance past this occurrence.
		searchUpper = searchUpper[afterIdx:]
		searchOriginal = searchOriginal[afterIdx:]
	}
	return "", false
}

// containsUnquotedSemicolon reports whether sqlText contains a ';' outside a
// single-quoted string literal. This prevents stacked queries in Exec/Query:
// (1) the modernc driver only binds params to the first statement, so a stacked
// query silently drops params for subsequent statements; (2) a second statement
// could be attacker-controlled text. The migrate path (no params, inside a tx)
// is exempt — it splits and applies each statement individually.
func containsUnquotedSemicolon(sqlText string) bool {
	inString := false
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		if c == '\'' {
			// SQL escapes a single quote inside a string as '' (doubled).
			if inString && i+1 < len(sqlText) && sqlText[i+1] == '\'' {
				i++ // skip the escaped quote
				continue
			}
			inString = !inString
		} else if c == ';' && !inString {
			return true
		}
	}
	return false
}

// stmtContainsKeyword reports whether upper (an UPPERCASE sql string) contains
// the keyword as a statement-leading token (after a ';' or at the start,
// ignoring whitespace). This catches stacked-query escapes like
// "SELECT 1; ATTACH DATABASE '...' AS x".
func stmtContainsKeyword(upper, keyword string) bool {
	for {
		// Find the keyword.
		idx := strings.Index(upper, keyword)
		if idx < 0 {
			return false
		}
		// Verify it's a token boundary before (start-of-string or non-alnum).
		beforeOK := idx == 0
		if idx > 0 {
			c := upper[idx-1]
			beforeOK = !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_')
		}
		// Verify it's a token boundary after.
		afterIdx := idx + len(keyword)
		afterOK := afterIdx >= len(upper)
		if !afterOK {
			c := upper[afterIdx]
			afterOK = !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_')
		}
		if beforeOK && afterOK {
			return true
		}
		// Advance past this occurrence and keep searching.
		upper = upper[afterIdx:]
	}
}

// PluginDBExec executes a write (DDL or DML) against the plugin's own SQLite
// store. Session-token verified; capability-gated. ATTACH/DETACH and
// non-user_version PRAGMAs are rejected so the plugin cannot escape its file.
// Single-statement only (no stacked queries) so params bind correctly — call
// exec multiple times for multi-statement DDL. (Migrate is the exempt path.)
func (a *App) PluginDBExec(pluginID, sessionToken, sqlText string, params []any) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapPluginDB); err != nil {
		return err
	}
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	trimmed := stripSQLComments(sqlText)
	if trimmed == "" {
		return fmt.Errorf("empty SQL statement")
	}
	if blocked, found := containsBlockedStatement(trimmed); found {
		return fmt.Errorf("PluginDBExec blocks %s statements", blocked)
	}
	// Reject stacked queries: the driver only binds params to the first
	// statement, so a semicolon-separated payload would silently drop params
	// for subsequent statements (and the second statement could be hostile
	// text). A plugin with multi-statement DDL should call exec per statement.
	if containsUnquotedSemicolon(trimmed) {
		return fmt.Errorf("PluginDBExec does not permit stacked queries (multiple semicolon-separated statements); call exec once per statement")
	}
	db, err := a.openPluginDB(pluginID)
	if err != nil {
		return err
	}
	a.wg.Add(1)
	defer a.wg.Done()
	if _, err := db.Exec(trimmed, params...); err != nil {
		return fmt.Errorf("plugin db exec: %w", err)
	}
	return nil
}

// PluginDBQuery runs a read-only query (SELECT/WITH only) against the plugin's
// own SQLite store. Mirrors PluginRawQuery's row cap + truncated flag so a
// plugin can't exhaust memory with an unbounded SELECT. Session-token verified;
// capability-gated.
func (a *App) PluginDBQuery(pluginID, sessionToken, sqlText string, params []any) (PluginRawQueryResult, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return PluginRawQueryResult{}, err
	}
	if err := a.requireGrant(pluginID, plugins.CapPluginDB); err != nil {
		return PluginRawQueryResult{}, err
	}
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return PluginRawQueryResult{}, fmt.Errorf("vault not loaded")
	}
	trimmed := stripSQLComments(sqlText)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
		return PluginRawQueryResult{}, fmt.Errorf("PluginDBQuery permits only SELECT/WITH statements")
	}
	// Reject stacked queries: the driver only binds params to the first
	// statement; a "SELECT 1; INSERT ..." payload would silently execute the
	// write against the plugin's own DB.
	if containsUnquotedSemicolon(trimmed) {
		return PluginRawQueryResult{}, fmt.Errorf("PluginDBQuery does not permit stacked queries")
	}
	db, err := a.openPluginDB(pluginID)
	if err != nil {
		return PluginRawQueryResult{}, err
	}
	a.wg.Add(1)
	defer a.wg.Done()

	out := PluginRawQueryResult{Rows: []map[string]any{}}
	rows, err := db.Query(trimmed, params...)
	if err != nil {
		return out, fmt.Errorf("plugin db query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return out, err
	}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return out, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = values[i]
		}
		out.Rows = append(out.Rows, row)
		if len(out.Rows) >= maxPluginQueryRows {
			if rows.Next() {
				out.Truncated = true
			}
			break
		}
	}
	return out, rows.Err()
}

// PluginDBMigrate applies a forward-only schema migration: it runs sqlText in
// a transaction and stamps PRAGMA user_version = version on success. Re-running
// the same version is a no-op (the version is checked first). The plugin
// designs migrations to be idempotent. Session-token verified; capability-gated.
func (a *App) PluginDBMigrate(pluginID, sessionToken string, version int, sqlText string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapPluginDB); err != nil {
		return err
	}
	if version < 0 {
		return fmt.Errorf("version must be non-negative")
	}
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	db, err := a.openPluginDB(pluginID)
	if err != nil {
		return err
	}
	a.wg.Add(1)
	defer a.wg.Done()

	// Check the current version; no-op if already at or past it.
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	if current >= version {
		return nil // already migrated
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	if sqlText != "" {
		trimmed := stripSQLComments(sqlText)
		if blocked, found := containsBlockedStatement(trimmed); found {
			return fmt.Errorf("PluginDBMigrate blocks %s statements", blocked)
		}
		if _, err := tx.Exec(trimmed); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	// PRAGMA user_version cannot run inside a transaction on SQLite, so set it
	// after commit via the connection's separate Exec path.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("stamp user_version = %d: %w", version, err)
	}
	return nil
}
