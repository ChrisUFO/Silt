package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	_ "modernc.org/sqlite"
	// sqlite-vec: registers vec0 virtual tables + vec_distance_* on every
	// connection opened by the modernc driver (inert on the core index —
	// no schema change; available to per-plugin DBs, #213). Pure-Go via
	// ccgo transpilation; no CGo.
	_ "modernc.org/sqlite/vec"
)

// ErrNetworkFilesystem is returned when the vault index path is detected to be
// on a network filesystem (NFS/SMB/CIFS/…). WAL mode requires shared memory
// which network mounts cannot provide, so the index would fail with an opaque
// SQLite error. This sentinel lets the UI surface a clear, actionable message
// instead (#79).
var ErrNetworkFilesystem = errors.New("network filesystem detected")

// IsNetworkFS reports whether path lives on a network filesystem that cannot
// support SQLite WAL (NFS/SMB/CIFS/…). Returns nil for local filesystems and
// on platforms without a detector. Exported so the vault mover (#141) can
// reject a network destination with the same clear message the index-opener
// surfaces, without re-implementing the per-platform detection.
func IsNetworkFS(path string) error {
	return detectNetworkFilesystem(path)
}

// ErrWALRejected is returned when the database is on-disk but SQLite did not
// accept WAL mode (the PRAGMA returned a different journal mode). This is a
// belt-and-suspenders check: some mounts silently downgrade away from WAL
// without erroring (#79).
var ErrWALRejected = errors.New("WAL mode rejected by the filesystem")

type DatabaseManager struct {
	db   *sql.DB
	path string // "" for the in-memory shared-cache DB; otherwise the on-disk file path
}

// FileStat records the last-seen filesystem attributes of an indexed file, used
// to skip unchanged files on a warm restart (#29). MTime is Unix nanoseconds
// so it round-trips losslessly across SQLite's INTEGER storage.
type FileStat struct {
	MTime     int64
	Size      int64
	IndexedAt int64
}

// NewDatabaseManager opens the Silt index. Pass the on-disk path (typically
// `<vault>/.system/index.sqlite`) for the production persistent WAL database,
// or "" for an ephemeral in-memory shared-cache DB (used by tests and before a
// vault is open).
//
// On-disk databases run in WAL mode (journal_mode is persistent in the file
// header, so it is set once and inherited by every subsequent connection,
// including the plugin read-only handle). The remaining pragmas
// (synchronous=NORMAL, temp_store=MEMORY, mmap_size, busy_timeout, cache_size,
// foreign_keys) are per-connection and are re-applied on every open. On an
// in-memory DB, `journal_mode=WAL` is a safe no-op (SQLite keeps "memory").
func NewDatabaseManager(dbPath string) (*DatabaseManager, error) {
	// Pre-open guard (#79): detect network filesystems before sql.Open so the
	// user gets a clear "move to a local folder" message instead of an opaque
	// SQLite shared-memory error. Only check for on-disk paths.
	if dbPath != "" {
		if err := detectNetworkFilesystem(filepath.Dir(dbPath)); err != nil {
			return nil, err
		}
	}

	dsn := dbPath
	if dsn == "" {
		// cache=shared lets a second connection (pluginRawQuery's read-only
		// handle) attach to the same ephemeral DB during tests.
		dsn = "file::memory:?cache=shared"
	}
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite: %w", err)
	}

	// Cap the connection pool at one. The ExecutionCoordinator already
	// serializes all DB access at the Go level; a larger pool would only
	// obscure the locking story without yielding usable concurrency. WAL
	// still helps (OS-level sync blocking moves to the WAL append path).
	sqlDB.SetMaxOpenConns(1)

	dm := &DatabaseManager{db: sqlDB, path: dbPath}
	if err := dm.initSchema(); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return dm, nil
}

// SQLDB exposes the underlying *sql.DB handle. Callers MUST serialize access
// through core.ExecutionCoordinator (e.g. WithDBRead/WithDBWrite) to avoid
// race conditions on the shared database.
func (dm *DatabaseManager) SQLDB() *sql.DB {
	return dm.db
}

// Path returns the on-disk index path ("" for the in-memory DB). Used by the
// watcher/app to open the plugin read-only handle against the same file.
func (dm *DatabaseManager) Path() string {
	return dm.path
}

// IsOnDisk reports whether the index is a persistent on-disk database (true)
// or an ephemeral in-memory one (false).
func (dm *DatabaseManager) IsOnDisk() bool {
	return dm.path != ""
}

func (dm *DatabaseManager) Close() error {
	if dm.db == nil {
		return nil // already closed (idempotent)
	}
	// Nil the field first so a second Close (e.g. test cleanup after a manual
	// close) is a no-op instead of double-checkpointing.
	db := dm.db
	dm.db = nil
	// Merge any pending WAL frames into the main file on a clean close so the
	// WAL does not grow unbounded across sessions. On in-memory databases this
	// is a no-op. A checkpoint failure is logged but not surfaced: SQLite
	// auto-checkpoints anyway and recovers on next open.
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
		log.Printf("db.Close: wal_checkpoint failed: %v", err)
	}
	return db.Close()
}

// Checkpoint forces a WAL checkpoint (TRUNCATE). Called on shutdown and after
// the startup reindex pass to keep the WAL file bounded. No-op on in-memory.
func (dm *DatabaseManager) Checkpoint() error {
	_, err := dm.db.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	return err
}
