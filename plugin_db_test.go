package main

import (
	"os"
	"path/filepath"
	"testing"

	"silt/backend/plugins"
)

// These tests cover the per-plugin SQLite store (#213): create-on-first-use,
// migrate + user_version, vec0 insert + cosine KNN query, the negative test
// that the core index is unreachable from the plugin connection, and the
// safety rejections (write via Query, ATTACH/PRAGMA via Exec).
//
// The test app uses a real on-disk vaultPath (t.TempDir) with an in-memory
// core index; the plugin DB is created lazily at
// <vaultPath>/.system/plugins/<id>/data/plugin.db on disk, so the tests
// exercise the real file-creation path.

// pluginDBTestApp returns a test app with a registered session for pluginID
// "test-plugin" and a granted plugin-db capability, returning the session
// token. First-party plugins are implicitly granted; a third-party id is used
// here so the grant path is exercised explicitly.
func pluginDBTestApp(t *testing.T) (*App, string) {
	t.Helper()
	app := newTestApp(t)
	token, err := app.RegisterPluginSession("test-plugin")
	if err != nil {
		t.Fatalf("RegisterPluginSession: %v", err)
	}
	// Grant the plugin-db capability for the test plugin.
	app.configMu.Lock()
	if app.grants == nil {
		app.grants = map[string]map[string]string{}
	}
	if app.grants["test-plugin"] == nil {
		app.grants["test-plugin"] = map[string]string{}
	}
	app.grants["test-plugin"][string(plugins.CapPluginDB)] = plugins.QualGranted
	app.configMu.Unlock()
	// Close any plugin DB pools on test exit so Windows can remove the temp dir.
	t.Cleanup(app.closeAllPluginDBs)
	return app, token
}

func TestPluginDB_CreateOnFirstUse(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// Before first use, no plugin.db file exists.
	dbPath := filepath.Join(app.vaultPath, ".system", "plugins", "test-plugin", "data", "plugin.db")
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("plugin.db should not exist before first use (stat err=%v)", err)
	}
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x INTEGER)", nil); err != nil {
		t.Fatalf("PluginDBExec create: %v", err)
	}
	// After first use, the file exists at the expected path.
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("plugin.db should exist after first use: %v", err)
	}
	// Insert + read back.
	if err := app.PluginDBExec("test-plugin", token, "INSERT INTO t (x) VALUES (?)", []any{42}); err != nil {
		t.Fatalf("PluginDBExec insert: %v", err)
	}
	res, err := app.PluginDBQuery("test-plugin", token, "SELECT x FROM t", nil)
	if err != nil {
		t.Fatalf("PluginDBQuery: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}

func TestPluginDB_MigrateStampsUserVersion(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// Migration 1: create a table.
	if err := app.PluginDBMigrate("test-plugin", token, 1, "CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatalf("migrate v1: %v", err)
	}
	// user_version should now be 1.
	db, _ := app.openPluginDB("test-plugin")
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if v != 1 {
		t.Fatalf("user_version = %d, want 1", v)
	}
	// Re-running the same version is a no-op (no error, no re-execution).
	if err := app.PluginDBMigrate("test-plugin", token, 1, "CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT)"); err != nil {
		t.Fatalf("re-run migrate v1: %v", err)
	}
	// Migration 2: add a column.
	if err := app.PluginDBMigrate("test-plugin", token, 2, "ALTER TABLE notes ADD COLUMN ts INTEGER"); err != nil {
		t.Fatalf("migrate v2: %v", err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("read user_version after v2: %v", err)
	}
	if v != 2 {
		t.Fatalf("user_version = %d, want 2", v)
	}
}

func TestPluginDB_Vec0InsertAndCosineKNN(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// Create a vec0 virtual table with cosine distance (4-dim for test speed).
	createSQL := "CREATE VIRTUAL TABLE IF NOT EXISTS embeddings USING vec0(id INTEGER PRIMARY KEY, embedding float[4] distance_metric=cosine)"
	if err := app.PluginDBExec("test-plugin", token, createSQL, nil); err != nil {
		t.Fatalf("create vec0: %v", err)
	}
	// Insert three vectors.
	inserts := []string{
		"INSERT INTO embeddings (id, embedding) VALUES (1, vec_f32('[1.0, 0.0, 0.0, 0.0]'))",
		"INSERT INTO embeddings (id, embedding) VALUES (2, vec_f32('[0.0, 1.0, 0.0, 0.0]'))",
		"INSERT INTO embeddings (id, embedding) VALUES (3, vec_f32('[0.9, 0.1, 0.0, 0.0]'))",
	}
	for _, sql := range inserts {
		if err := app.PluginDBExec("test-plugin", token, sql, nil); err != nil {
			t.Fatalf("insert: %v (sql=%s)", err, sql)
		}
	}
	// KNN query: vector closest to [1,0,0,0] should be id=1 (distance 0),
	// then id=3 (very close to [1,0,0,0]), then id=2.
	res, err := app.PluginDBQuery(
		"test-plugin", token,
		"SELECT id, distance FROM embeddings WHERE embedding MATCH vec_f32('[1.0, 0.0, 0.0, 0.0]') AND k = 3 ORDER BY distance",
		nil,
	)
	if err != nil {
		t.Fatalf("knn query: %v", err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 KNN results, got %d", len(res.Rows))
	}
	// The nearest neighbor must be id=1 (the query vector itself → distance 0).
	firstID, _ := res.Rows[0]["id"].(int64)
	if firstID != 1 {
		t.Fatalf("nearest neighbor id = %d, want 1", firstID)
	}
}

func TestPluginDB_CoreIndexUnreachable(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// The core index has a `blocks` table (created by newTestApp's in-memory DB).
	// From the plugin connection, a query against `blocks` must fail — the plugin
	// DB is a distinct file/connection, never ATTACH-ed to the core index.
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE local (x INTEGER)", nil); err != nil {
		t.Fatalf("create local table: %v", err)
	}
	_, err := app.PluginDBQuery("test-plugin", token, "SELECT COUNT(*) FROM blocks", nil)
	if err == nil {
		t.Fatal("expected error querying core 'blocks' table from plugin DB, got nil")
	}
	// ATTACH of the core index path must be rejected by the statement blocker.
	corePath := filepath.Join(app.vaultPath, ".system", "index.sqlite")
	// The core index is in-memory in tests, but the ATTACH rejection happens at
	// the statement-class check (before SQLite sees it), so the path validity
	// is irrelevant.
	err = app.PluginDBExec("test-plugin", token, "ATTACH DATABASE '"+corePath+"' AS core", nil)
	if err == nil {
		t.Fatal("expected ATTACH to be blocked, got nil")
	}
}

func TestPluginDB_QueryRejectsWrites(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// PluginDBQuery permits only SELECT/WITH.
	_, err := app.PluginDBQuery("test-plugin", token, "CREATE TABLE evil (x INTEGER)", nil)
	if err == nil {
		t.Fatal("expected error for CREATE via PluginDBQuery, got nil")
	}
	_, err = app.PluginDBQuery("test-plugin", token, "INSERT INTO t VALUES (1)", nil)
	if err == nil {
		t.Fatal("expected error for INSERT via PluginDBQuery, got nil")
	}
}

func TestPluginDB_ExecRejectsBlockedStatements(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// PRAGMA (non-user_version) rejected.
	if err := app.PluginDBExec("test-plugin", token, "PRAGMA query_only = ON", nil); err == nil {
		t.Fatal("expected PRAGMA query_only to be blocked")
	}
	// DETACH rejected.
	if err := app.PluginDBExec("test-plugin", token, "DETACH DATABASE x", nil); err == nil {
		t.Fatal("expected DETACH to be blocked")
	}
	// Stacked-query ATTACH rejected (defense against "SELECT 1; ATTACH ...").
	if err := app.PluginDBExec("test-plugin", token, "SELECT 1; ATTACH DATABASE ':memory:' AS x", nil); err == nil {
		t.Fatal("expected stacked-query ATTACH to be blocked")
	}
}

func TestPluginDB_CloseReleasesAndReopens(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x INTEGER)", nil); err != nil {
		t.Fatalf("exec: %v", err)
	}
	// The pool is cached.
	app.pluginDBsMu.Lock()
	_, cached := app.pluginDBs["test-plugin"]
	app.pluginDBsMu.Unlock()
	if !cached {
		t.Fatal("plugin DB pool should be cached after first use")
	}
	// Close it.
	if err := app.ClosePluginDB("test-plugin"); err != nil {
		t.Fatalf("ClosePluginDB: %v", err)
	}
	app.pluginDBsMu.Lock()
	_, stillCached := app.pluginDBs["test-plugin"]
	app.pluginDBsMu.Unlock()
	if stillCached {
		t.Fatal("plugin DB pool should be removed after ClosePluginDB")
	}
	// Re-opening works and the persisted table survives (WAL checkpointed on close).
	if err := app.PluginDBExec("test-plugin", token, "INSERT INTO t (x) VALUES (1)", nil); err != nil {
		t.Fatalf("exec after reopen: %v", err)
	}
}

func TestPluginDB_CapabilityGate(t *testing.T) {
	app := newTestApp(t)
	token, _ := app.RegisterPluginSession("nodb-plugin")
	// No plugin-db grant for "nodb-plugin".
	err := app.PluginDBExec("nodb-plugin", token, "CREATE TABLE t (x INTEGER)", nil)
	if err == nil {
		t.Fatal("expected capability-denied error for ungranted plugin-db, got nil")
	}
}

func TestPluginDB_SessionTokenVerified(t *testing.T) {
	app, _ := pluginDBTestApp(t)
	// Wrong token rejected.
	err := app.PluginDBExec("test-plugin", "wrong-token", "CREATE TABLE t (x INTEGER)", nil)
	if err == nil {
		t.Fatal("expected session-token rejection, got nil")
	}
}

// --- Hardened paths (audit follow-up) ---------------------------------------

func TestPluginDB_PragmaMultiStatementBypass(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// H1: a stacked-query PRAGMA payload like
	// "PRAGMA user_version=1; PRAGMA query_only=OFF" must be caught. The
	// semicolon check now rejects stacked queries entirely, so this fails at
	// the semicolon gate. Verify the non-semicolon stacked variant is also
	// caught by the looping PRAGMA scanner would require a real stacked
	// execution path (none exists now), so this test asserts the semicolon
	// gate fires.
	err := app.PluginDBExec("test-plugin", token,
		"PRAGMA user_version=1; PRAGMA query_only=OFF", nil)
	if err == nil {
		t.Fatal("expected stacked-query PRAGMA bypass to be blocked")
	}
}

func TestPluginDB_ExecRejectsStackedQueries(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x INTEGER)", nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Stacked query rejected (params would silently bind to first statement only).
	err := app.PluginDBExec("test-plugin", token, "INSERT INTO t VALUES (1); INSERT INTO t VALUES (2)", nil)
	if err == nil {
		t.Fatal("expected stacked-query rejection in Exec")
	}
}

func TestPluginDB_QueryRejectsStackedQueries(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x INTEGER)", nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := app.PluginDBQuery("test-plugin", token, "SELECT 1; INSERT INTO t VALUES (1)", nil)
	if err == nil {
		t.Fatal("expected stacked-query rejection in Query")
	}
}

func TestPluginDB_ExecAllowsSemicolonInStringLiteral(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x TEXT)", nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// A semicolon inside a single-quoted string literal is legitimate and must
	// NOT be rejected by the stacked-query guard.
	err := app.PluginDBExec("test-plugin", token, "INSERT INTO t VALUES ('hello; world')", nil)
	if err != nil {
		t.Fatalf("semicolon in string literal should be allowed: %v", err)
	}
}

func TestPluginDB_QueryTruncatedFlag(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (x INTEGER)", nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Insert more rows than a low cap.
	for i := 0; i < 5; i++ {
		if err := app.PluginDBExec("test-plugin", token, "INSERT INTO t VALUES (?)", []any{i}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	// Temporarily lower the cap so we don't need 5000+ rows.
	old := maxPluginQueryRows
	maxPluginQueryRows = 3
	defer func() { maxPluginQueryRows = old }()
	res, err := app.PluginDBQuery("test-plugin", token, "SELECT x FROM t", nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected Truncated=true when result exceeds row cap")
	}
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 rows (capped), got %d", len(res.Rows))
	}
}

func TestPluginDB_MigrateRejectsBlockedStatements(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// A migration body smuggling a PRAGMA that undermines the contract.
	err := app.PluginDBMigrate("test-plugin", token, 1, "CREATE TABLE t (x INTEGER); PRAGMA query_only = ON")
	if err == nil {
		t.Fatal("expected migration with smuggled PRAGMA to be blocked")
	}
}

// --- Security regression: VACUUM INTO sandbox escape + Query DML gate ---

func TestPluginDB_ExecRejectsVacuumInto(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// VACUUM INTO writes a DB snapshot to an arbitrary path — a vault escape
	// the path sanitizer never sees (the path is in the SQL text).
	escapePath := filepath.Join(app.vaultPath, "..", "escaped.db")
	err := app.PluginDBExec("test-plugin", token, "VACUUM INTO '"+escapePath+"'", nil)
	if err == nil {
		t.Fatal("expected VACUUM INTO to be blocked (filesystem sandbox escape)")
	}
	// Plain VACUUM is also blocked (close-time wal_checkpoint covers compaction).
	err = app.PluginDBExec("test-plugin", token, "VACUUM", nil)
	if err == nil {
		t.Fatal("expected VACUUM to be blocked")
	}
	// Verify no file was created outside the vault.
	if _, err := os.Stat(escapePath); !os.IsNotExist(err) {
		t.Fatalf("escaped file should not exist (stat err=%v)", err)
	}
}

func TestPluginDB_QueryRejectsWithDML(t *testing.T) {
	app, token := pluginDBTestApp(t)
	if err := app.PluginDBExec("test-plugin", token, "CREATE TABLE t (c INTEGER)", nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// WITH-prefixed INSERT passes the SELECT/WITH prefix check but must be
	// rejected by the write-statement gate.
	_, err := app.PluginDBQuery("test-plugin", token,
		"WITH x AS (SELECT 999 v) INSERT INTO t SELECT v FROM x RETURNING c", nil)
	if err == nil {
		t.Fatal("expected WITH...INSERT to be blocked by PluginDBQuery")
	}
	// Direct UPDATE/DELETE also blocked.
	_, err = app.PluginDBQuery("test-plugin", token, "UPDATE t SET c = 1", nil)
	if err == nil {
		t.Fatal("expected UPDATE to be blocked by PluginDBQuery")
	}
	_, err = app.PluginDBQuery("test-plugin", token, "DELETE FROM t", nil)
	if err == nil {
		t.Fatal("expected DELETE to be blocked by PluginDBQuery")
	}
	// Plain WITH...SELECT must still work (not a false positive).
	if err := app.PluginDBExec("test-plugin", token, "INSERT INTO t VALUES (42)", nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := app.PluginDBQuery("test-plugin", token, "WITH x AS (SELECT c FROM t) SELECT c FROM x", nil)
	if err != nil {
		t.Fatalf("WITH...SELECT should be allowed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}

func TestPluginDB_QueryRejectsVacuum(t *testing.T) {
	app, token := pluginDBTestApp(t)
	// VACUUM must be blocked via Query too (not just Exec).
	_, err := app.PluginDBQuery("test-plugin", token, "VACUUM INTO '../../../tmp/evil.db'", nil)
	if err == nil {
		t.Fatal("expected VACUUM INTO to be blocked by PluginDBQuery")
	}
}
