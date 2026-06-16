package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"silt/backend/parser"

	_ "modernc.org/sqlite"
)

// ErrNetworkFilesystem is returned when the vault index path is detected to be
// on a network filesystem (NFS/SMB/CIFS/…). WAL mode requires shared memory
// which network mounts cannot provide, so the index would fail with an opaque
// SQLite error. This sentinel lets the UI surface a clear, actionable message
// instead (#79).
var ErrNetworkFilesystem = errors.New("network filesystem detected")

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

func (dm *DatabaseManager) initSchema() error {
	// Foreign-key enforcement is per-connection.
	if _, err := dm.db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// journal_mode is persistent in the DB file header; on an in-memory DB
	// SQLite silently keeps "memory" (the call still succeeds). Setting WAL
	// here means the first on-disk open creates a WAL-mode file and every
	// later connection — including the plugin read-only handle — inherits it
	// without re-running the pragma.
	if _, err := dm.db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return fmt.Errorf("failed to set journal mode: %w", err)
	}

	// Belt-and-suspenders (#79): assert the journal mode actually stuck. Some
	// mounts silently downgrade away from WAL (returning "memory" or "delete"
	// instead of erroring). On an in-memory DB the mode is "memory" which is
	// expected — only assert for on-disk databases.
	if dm.path != "" {
		var mode string
		if err := dm.db.QueryRow("PRAGMA journal_mode;").Scan(&mode); err != nil {
			return fmt.Errorf("failed to read journal mode: %w", err)
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("%w: PRAGMA journal_mode returned %q instead of \"wal\" — the filesystem may not support shared memory", ErrWALRejected, mode)
		}
	}
	// Per-connection pragmas. synchronous=NORMAL is safe under WAL (the WAL
	// itself preserves durability across app crashes; only an OS crash can
	// lose the last few transactions, an acceptable trade for local-first
	// speed). mmap_size memory-maps the file for faster reads on large
	// indexes; cache_size is the per-connection page cache (negative = KB,
	// so -64000 ≈ 64 MB). busy_timeout makes a contended write wait rather
	// than fail instantly.
	pragmas := []string{
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA temp_store = MEMORY;",
		"PRAGMA mmap_size = 268435456;", // 256 MiB mmap threshold
		"PRAGMA cache_size = -64000;",   // 64 MiB page cache
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := dm.db.Exec(p); err != nil {
			return fmt.Errorf("failed to apply pragma %q: %w", p, err)
		}
	}

	// Blocks Table
	createBlocksTable := `
	CREATE TABLE IF NOT EXISTS blocks (
		id TEXT PRIMARY KEY,
		parent_id TEXT,
		notebook TEXT NOT NULL,
		section TEXT NOT NULL,
		page TEXT NOT NULL,
		file_date TEXT NOT NULL, -- YYYY-MM-DD
		depth INTEGER DEFAULT 0,
		type TEXT NOT NULL,      -- 'TASK', 'NOTE', 'HEADER'
		raw_content TEXT NOT NULL,
		clean_content TEXT NOT NULL,
		line_number INTEGER NOT NULL,
		FOREIGN KEY(parent_id) REFERENCES blocks(id) ON DELETE SET NULL
	);`
	if _, err := dm.db.Exec(createBlocksTable); err != nil {
		return fmt.Errorf("failed to create blocks table: %w", err)
	}

	// Tasks Metadata Table
	createTasksTable := `
	CREATE TABLE IF NOT EXISTS tasks (
		block_id TEXT PRIMARY KEY,
		status TEXT NOT NULL,    -- 'TODO', 'DOING', 'DONE'
		owner TEXT,
		start_date TEXT,         -- YYYY-MM-DD or NULL
		due_date TEXT,           -- YYYY-MM-DD or NULL
		priority INTEGER,        -- 1, 2, 3
		pinned INTEGER DEFAULT 0,           -- 0/1; file-resident user intent (cached for query speed)
		progress INTEGER DEFAULT 0,         -- 0-100; file-resident user intent (cached for query speed)
		comments_count INTEGER DEFAULT 0,   -- count of child NOTE blocks (derived cache)
		links_count INTEGER DEFAULT 0,      -- count of ((uuid)) refs in raw_content (derived cache)
		FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
	);`
	if _, err := dm.db.Exec(createTasksTable); err != nil {
		return fmt.Errorf("failed to create tasks table: %w", err)
	}

	// Migration: add new columns to existing tasks tables (a vault that
	// was created before the pinned/progress/comments_count/links_count
	// columns shipped). SQLite's ALTER TABLE ADD COLUMN is idempotent-
	// safe only via the try-ignore pattern below (it errors if the column
	// already exists). Each column is nullable/defaulted so existing rows
	// stay valid without a data backfill — a re-index populates them.
	for _, col := range []struct{ name, defn string }{
		{"pinned", "INTEGER DEFAULT 0"},
		{"progress", "INTEGER DEFAULT 0"},
		{"comments_count", "INTEGER DEFAULT 0"},
		{"links_count", "INTEGER DEFAULT 0"},
	} {
		alter := fmt.Sprintf("ALTER TABLE tasks ADD COLUMN %s %s", col.name, col.defn)
		if _, err := dm.db.Exec(alter); err != nil {
			// "duplicate column name" → already migrated; ignore.
			// Any other error is real.
			if !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("failed to migrate tasks table (add %s): %w", col.name, err)
			}
		}
	}

	// Tags Table
	createTagsTable := `
	CREATE TABLE IF NOT EXISTS tags (
		block_id TEXT NOT NULL,
		raw_path TEXT NOT NULL,  -- 'work/project/milestone-one'
		level_0 TEXT NOT NULL,   -- 'work'
		level_1 TEXT,            -- 'project'
		level_2 TEXT,            -- 'milestone-one'
		PRIMARY KEY(block_id, raw_path),
		FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
	);`
	if _, err := dm.db.Exec(createTagsTable); err != nil {
		return fmt.Errorf("failed to create tags table: %w", err)
	}

	// Files Table — records the last-seen mtime + size of every indexed file
	// so a warm restart can skip re-parsing/re-indexing unchanged files (#29).
	// Lives in the same (on-disk, WAL) database as the blocks index so it
	// persists across restarts naturally. Keyed by absolute path; a renamed
	// file is treated as a new path, with the stale old path pruned by
	// PruneStaleFiles on the next startup scan.
	createFilesTable := `
	CREATE TABLE IF NOT EXISTS files (
		path       TEXT PRIMARY KEY,
		mtime      INTEGER NOT NULL,
		size       INTEGER NOT NULL,
		indexed_at INTEGER NOT NULL
	);`
	if _, err := dm.db.Exec(createFilesTable); err != nil {
		return fmt.Errorf("failed to create files table: %w", err)
	}

	// Create covered indexes
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_blocks_file ON blocks(notebook, section, page, file_date);",
		"CREATE INDEX IF NOT EXISTS idx_tasks_dates ON tasks(start_date, due_date) WHERE start_date IS NOT NULL OR due_date IS NOT NULL;",
		"CREATE INDEX IF NOT EXISTS idx_tags_lookup ON tags(level_0, level_1, level_2);",
		// Functional indexes for case-insensitive search (SearchBlocks).
		"CREATE INDEX IF NOT EXISTS idx_blocks_clean_lower ON blocks(LOWER(clean_content));",
		"CREATE INDEX IF NOT EXISTS idx_blocks_notebook_lower ON blocks(LOWER(notebook));",
		"CREATE INDEX IF NOT EXISTS idx_blocks_section_lower ON blocks(LOWER(section));",
	}

	for _, idxQuery := range indexes {
		if _, err := dm.db.Exec(idxQuery); err != nil {
			return fmt.Errorf("failed to create index: %w", err)
		}
	}

	// FTS5 full-text index for SearchBlocks (#39). External-content table
	// linked to blocks by rowid, kept in sync by AFTER INSERT/UPDATE/DELETE
	// triggers so every code path that mutates blocks (IndexFileBlocks,
	// IndexScanResults, ClearFileBlocks) keeps the FTS index consistent
	// without each caller knowing about FTS. Created once; on first creation
	// we rebuild from any pre-existing blocks rows so the migration is
	// additive and lossless.
	if err := dm.ensureFTS(); err != nil {
		return fmt.Errorf("failed to initialize FTS index: %w", err)
	}

	return nil
}

// ensureFTS creates the blocks_fts virtual table and its sync triggers if they
// do not yet exist, and (on first creation) repopulates FTS from the current
// blocks table. Idempotent: a no-op on every subsequent open where the FTS
// table already exists and the triggers are in place.
func (dm *DatabaseManager) ensureFTS() error {
	var ftsExists int
	if err := dm.db.QueryRow(
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='blocks_fts'").Scan(&ftsExists); err != nil {
		return fmt.Errorf("failed to check blocks_fts existence: %w", err)
	}

	// External-content FTS5: the virtual table mirrors blocks.clean_content,
	// notebook, and section, linked by the implicit rowid. Queries join back
	// to blocks on rowid.
	createFTS := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS blocks_fts USING fts5(
			clean_content, notebook, section,
			content='blocks', content_rowid='rowid',
			tokenize='unicode61'
		);`,
		`CREATE TRIGGER IF NOT EXISTS blocks_fts_ai AFTER INSERT ON blocks BEGIN
			INSERT INTO blocks_fts(rowid, clean_content, notebook, section)
			VALUES (new.rowid, new.clean_content, new.notebook, new.section);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS blocks_fts_ad AFTER DELETE ON blocks BEGIN
			INSERT INTO blocks_fts(blocks_fts, rowid, clean_content, notebook, section)
			VALUES ('delete', old.rowid, old.clean_content, old.notebook, old.section);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS blocks_fts_au AFTER UPDATE ON blocks BEGIN
			INSERT INTO blocks_fts(blocks_fts, rowid, clean_content, notebook, section)
			VALUES ('delete', old.rowid, old.clean_content, old.notebook, old.section);
			INSERT INTO blocks_fts(rowid, clean_content, notebook, section)
			VALUES (new.rowid, new.clean_content, new.notebook, new.section);
		END;`,
	}
	for _, q := range createFTS {
		if _, err := dm.db.Exec(q); err != nil {
			return fmt.Errorf("failed to create FTS object: %w", err)
		}
	}

	// First creation: populate FTS from whatever blocks rows already exist
	// (the migration case — an upgraded vault with blocks but no FTS yet).
	if ftsExists == 0 {
		if _, err := dm.db.Exec("INSERT INTO blocks_fts(blocks_fts) VALUES ('rebuild');"); err != nil {
			return fmt.Errorf("failed to rebuild FTS index: %w", err)
		}
	}
	return nil
}

// RebuildFTSIndex forces a full repopulation of blocks_fts from the current
// blocks table. Call this after a bulk reindex or any path that bypassed the
// sync triggers (none in normal operation, but available for recovery). On an
// empty blocks table this is a no-op.
func (dm *DatabaseManager) RebuildFTSIndex() error {
	_, err := dm.db.Exec("INSERT INTO blocks_fts(blocks_fts) VALUES ('rebuild');")
	return err
}

// IsFileUnchanged reports whether the file at `path` was previously indexed
// with the exact same mtime (Unix nanoseconds) and size. A warm restart uses
// this to skip re-parsing files the user has not touched since the last index.
func (dm *DatabaseManager) IsFileUnchanged(path string, mtime, size int64) (bool, error) {
	var fmtime, fsize int64
	err := dm.db.QueryRow("SELECT mtime, size FROM files WHERE path = ?", path).Scan(&fmtime, &fsize)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to query files table: %w", err)
	}
	return fmtime == mtime && fsize == size, nil
}

// MarkFileIndexed records that the file at `path` was fully indexed with the
// given mtime/size. If tx is non-nil the upsert joins the caller's transaction
// (used by the bulk startup reindex so all per-file rows commit atomically);
// otherwise it runs against the shared connection.
func (dm *DatabaseManager) MarkFileIndexed(tx *sql.Tx, path string, mtime, size int64) error {
	now := time.Now().UnixNano()
	if tx != nil {
		_, err := tx.Exec(
			"INSERT INTO files (path, mtime, size, indexed_at) VALUES (?, ?, ?, ?) "+
				"ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, size=excluded.size, indexed_at=excluded.indexed_at",
			path, mtime, size, now)
		return err
	}
	_, err := dm.db.Exec(
		"INSERT INTO files (path, mtime, size, indexed_at) VALUES (?, ?, ?, ?) "+
			"ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime, size=excluded.size, indexed_at=excluded.indexed_at",
		path, mtime, size, now)
	return err
}

// PruneStaleFiles deletes `files` rows for paths that are no longer present on
// disk (the file was deleted, moved, or renamed). `seenPaths` is the complete
// set of file paths the latest vault scan observed. Returns the pruned paths so
// callers can surface them as one-time init warnings (a renamed file shows up
// as "pruned old path + indexed new path").
func (dm *DatabaseManager) PruneStaleFiles(seenPaths []string) ([]string, error) {
	// Build the parameter list for the "NOT IN (...)" clause. A single
	// round-trip DELETE keeps this cheap even for thousands of files.
	if len(seenPaths) == 0 {
		// No files on disk at all: drop every recorded row.
		_, err := dm.db.Exec("DELETE FROM files")
		return nil, err
	}
	placeholders := make([]string, len(seenPaths))
	args := make([]interface{}, len(seenPaths))
	for i, p := range seenPaths {
		placeholders[i] = "?"
		args[i] = p
	}

	// Collect the about-to-be-pruned paths first so we can report them.
	rows, err := dm.db.Query(
		"SELECT path FROM files WHERE path NOT IN ("+strings.Join(placeholders, ",")+")", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query stale files: %w", err)
	}
	var pruned []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		pruned = append(pruned, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if len(pruned) > 0 {
		if _, err := dm.db.Exec(
			"DELETE FROM files WHERE path NOT IN ("+strings.Join(placeholders, ",")+")", args...); err != nil {
			return nil, fmt.Errorf("failed to prune stale files: %w", err)
		}
	}
	return pruned, nil
}

// ForgetFile deletes the files-table row for a single path. Called by the
// watcher when a file is removed or renamed so the next startup scan does not
// treat the path as "unchanged" and skip re-indexing the new occupant.
func (dm *DatabaseManager) ForgetFile(path string) error {
	_, err := dm.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// KnownFiles returns the full path→FileStat map currently recorded in the
// index. Used for diagnostics (e.g. surfacing how many files are tracked).
func (dm *DatabaseManager) KnownFiles() (map[string]FileStat, error) {
	rows, err := dm.db.Query("SELECT path, mtime, size, indexed_at FROM files")
	if err != nil {
		return nil, fmt.Errorf("failed to query known files: %w", err)
	}
	defer rows.Close()
	out := make(map[string]FileStat)
	for rows.Next() {
		var path string
		var fs FileStat
		if err := rows.Scan(&path, &fs.MTime, &fs.Size, &fs.IndexedAt); err != nil {
			return nil, err
		}
		out[path] = fs
	}
	return out, nil
}

// ExtractTags finds inline tags starting with # followed by a letter, ignoring numeric priorities.
// Tag names may contain letters, digits, underscores, hyphens, and slashes
// (so #work/project/milestone-one is captured in full).
func ExtractTags(text string) []string {
	tagRegex := regexp.MustCompile(`\B#([a-zA-Z][a-zA-Z0-9_/-]*)`)
	matches := tagRegex.FindAllStringSubmatch(text, -1)
	var tags []string
	seen := make(map[string]bool)
	for _, match := range matches {
		if len(match) > 1 {
			// Trim a trailing slash or hyphen so "#work-" doesn't store "work-".
			t := strings.TrimRight(match[1], "/-")
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			tags = append(tags, t)
		}
	}
	return tags
}

// ClearFileBlocks deletes all blocks, tasks, and tags associated with a specific page on a given day.
func (dm *DatabaseManager) ClearFileBlocks(tx *sql.Tx, notebook, section, page string) error {
	query := "DELETE FROM blocks WHERE notebook = ? AND section = ? AND page = ?"
	var err error
	if tx != nil {
		_, err = tx.Exec(query, notebook, section, page)
	} else {
		_, err = dm.db.Exec(query, notebook, section, page)
	}
	return err
}

// IndexFileBlocks updates the index with a set of blocks in a single transaction.
//
// fileWarnings is an optional slice of non-fatal diagnostics from the parser
// (e.g. malformed YAML frontmatter). They are logged at warn level so a
// maintainer can grep the output without changing the call signature or
// the public API.
func (dm *DatabaseManager) IndexFileBlocks(notebook, section, page string, blocks []parser.ParsedBlock, fileTags []string, fileWarnings ...string) error {
	for _, w := range fileWarnings {
		log.Printf("db.IndexFileBlocks(%s/%s/%s): %s", notebook, section, page, w)
	}

	tx, err := dm.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete any pre-existing rows for the block IDs we're about to (re)insert.
	// Block IDs are stable across re-parses. Cascading FKs clean up their
	// related tasks and tags.
	if len(blocks) > 0 {
		placeholders := make([]string, len(blocks))
		args := make([]interface{}, len(blocks))
		for i, b := range blocks {
			placeholders[i] = "?"
			args[i] = b.ID
		}
		query := "DELETE FROM blocks WHERE id IN (" + strings.Join(placeholders, ",") + ")"
		if _, err := tx.Exec(query, args...); err != nil {
			return fmt.Errorf("failed to clear blocks by id: %w", err)
		}
	}

	// Also clear by metadata to catch blocks that the user removed from the
	// file (their IDs are no longer in the new parse output).
	if err := dm.ClearFileBlocks(tx, notebook, section, page); err != nil {
		return fmt.Errorf("failed to clear old blocks: %w", err)
	}

	if len(blocks) == 0 {
		return tx.Commit()
	}

	stmtBlock, err := tx.Prepare("INSERT INTO blocks (id, parent_id, notebook, section, page, file_date, depth, type, raw_content, clean_content, line_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmtBlock.Close()

	stmtTask, err := tx.Prepare("INSERT INTO tasks (block_id, status, owner, start_date, due_date, priority, pinned, progress, comments_count, links_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmtTask.Close()

	stmtTag, err := tx.Prepare("INSERT INTO tags (block_id, raw_path, level_0, level_1, level_2) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmtTag.Close()

	// Pre-compute comments_count per task: the number of child NOTE blocks
	// (indented reply bullets in the Stitch "comments on a task" sense).
	// A child is any block whose ParentID points at a TASK block AND whose
	// Type is NOTE. We walk the blocks slice once and count.
	childNotesByParent := make(map[string]int)
	for _, b := range blocks {
		if b.ParentID != "" && b.Type == parser.BlockNote {
			childNotesByParent[b.ParentID]++
		}
	}

	for blockIdx, block := range blocks {
		// 1. Insert into blocks — each block carries its own file_date.
		var parentID interface{}
		if block.ParentID != "" {
			parentID = block.ParentID
		}
		fileDate := block.FileDate
		if fileDate == "" {
			fileDate = time.Now().Format("2006-01-02")
		}
		_, err = stmtBlock.Exec(block.ID, parentID, notebook, section, page, fileDate, block.Depth, string(block.Type), block.RawText, block.CleanText, block.LineNumber)
		if err != nil {
			return fmt.Errorf("failed to insert block %s: %w", block.ID, err)
		}

		// 2. Insert task metadata if it's a task
		if block.Type == parser.BlockTask {
			var owner, startDate, dueDate interface{}
			if block.Owner != "" {
				owner = block.Owner
			}
			if block.StartDate != "" {
				startDate = block.StartDate
			}
			if block.DueDate != "" {
				dueDate = block.DueDate
			}
			pinnedVal := 0
			if block.Pinned {
				pinnedVal = 1
			}
			linksCount := len(parser.BlockRefRegex.FindAllString(block.RawText, -1))
			_, err = stmtTask.Exec(block.ID, block.Status, owner, startDate, dueDate, block.Priority, pinnedVal, block.Progress, childNotesByParent[block.ID], linksCount)
			if err != nil {
				return fmt.Errorf("failed to insert task for block %s: %w", block.ID, err)
			}
		}

		// 3. Extract and insert tags for this block
		tags := ExtractTags(block.RawText)
		// Attach file-level frontmatter tags to the first parsed block. The
		// previous implementation checked block.LineNumber == 1, which is
		// never true when the file has YAML frontmatter (the first block
		// sits after the closing `---`).
		if blockIdx == 0 {
			for _, ft := range fileTags {
				trimmedFT := strings.TrimPrefix(ft, "#")
				found := false
				for _, t := range tags {
					if t == trimmedFT {
						found = true
						break
					}
				}
				if !found && trimmedFT != "" {
					tags = append(tags, trimmedFT)
				}
			}
		}

		for _, tagPath := range tags {
			parts := strings.Split(tagPath, "/")
			var level0, level1, level2 interface{}
			if len(parts) > 0 {
				level0 = parts[0]
			}
			if len(parts) > 1 {
				level1 = parts[1]
			}
			if len(parts) > 2 {
				level2 = parts[2]
			}
			_, err = stmtTag.Exec(block.ID, tagPath, level0, level1, level2)
			if err != nil {
				// PRIMARY KEY is (block_id, raw_path) so most collisions
				// are just duplicate tags, but we log to stderr so a
				// real DB error (constraint violations from a schema
				// change, for example) is still visible during dev.
				log.Printf("db.IndexFileBlocks: tag insert error for block %s tag %q: %v", block.ID, tagPath, err)
				continue
			}
		}
	}

	return tx.Commit()
}

// IndexScanResults inserts multiple scan results into the database in a single
// transaction. It returns the count of files that were successfully indexed,
// plus a slice describing files that were skipped because the scanner
// reported a per-file error. Callers should surface the skipped set so
// users can distinguish a fully-loaded vault from one with unreadable files.
func (dm *DatabaseManager) IndexScanResults(results []parser.ScanResult) (int, []string, error) {
	tx, err := dm.db.Begin()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmtBlock, err := tx.Prepare("INSERT INTO blocks (id, parent_id, notebook, section, page, file_date, depth, type, raw_content, clean_content, line_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return 0, nil, err
	}
	defer stmtBlock.Close()

	stmtTask, err := tx.Prepare("INSERT INTO tasks (block_id, status, owner, start_date, due_date, priority, pinned, progress, comments_count, links_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return 0, nil, err
	}
	defer stmtTask.Close()

	stmtTag, err := tx.Prepare("INSERT INTO tags (block_id, raw_path, level_0, level_1, level_2) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return 0, nil, err
	}
	defer stmtTag.Close()

	indexedCount := 0
	var skipped []string

	for _, res := range results {
		if res.Err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", res.Path, res.Err))
			continue
		}

		// Files that did not resolve to a notebook/section/page (e.g. live
		// too shallow in the vault) arrive with a warning and no notebook.
		// Surface them as skipped rather than indexing under empty strings.
		if res.Notebook == "" {
			for _, w := range res.Warnings {
				skipped = append(skipped, fmt.Sprintf("%s: %s", res.Path, w))
			}
			if len(res.Warnings) == 0 {
				skipped = append(skipped, fmt.Sprintf("%s: missing notebook/section/page", res.Path))
			}
			continue
		}

		// Clear any pre-existing rows for these block IDs (handles the case
		// where frontmatter metadata changed since the previous index, since
		// block IDs are stable but (notebook, section, date) is denormalized).
		if len(res.Blocks) > 0 {
			placeholders := make([]string, len(res.Blocks))
			args := make([]interface{}, len(res.Blocks))
			for i, b := range res.Blocks {
				placeholders[i] = "?"
				args[i] = b.ID
			}
			query := "DELETE FROM blocks WHERE id IN (" + strings.Join(placeholders, ",") + ")"
			if _, err := tx.Exec(query, args...); err != nil {
				return 0, skipped, fmt.Errorf("failed to clear blocks by id for %s: %w", res.Path, err)
			}
		}

		// Also clear by metadata to catch blocks the user removed from the file.
		if err := dm.ClearFileBlocks(tx, res.Notebook, res.Section, res.Page); err != nil {
			return 0, skipped, fmt.Errorf("failed to clear blocks for %s: %w", res.Path, err)
		}

		for blockIdx, block := range res.Blocks {
			var parentID interface{}
			if block.ParentID != "" {
				parentID = block.ParentID
			}
			// Per-block file_date: the parser fills FileDate from the comment
			// or meta.Date before blocks reach either indexer. This fallback
			// is a last resort — kept consistent with IndexFileBlocks.
			fileDate := block.FileDate
			if fileDate == "" {
				fileDate = time.Now().Format("2006-01-02")
			}
			_, err = stmtBlock.Exec(block.ID, parentID, res.Notebook, res.Section, res.Page, fileDate, block.Depth, string(block.Type), block.RawText, block.CleanText, block.LineNumber)
			if err != nil {
				return 0, skipped, fmt.Errorf("failed to insert block %s: %w", block.ID, err)
			}

			if block.Type == parser.BlockTask {
				var owner, startDate, dueDate interface{}
				if block.Owner != "" {
					owner = block.Owner
				}
				if block.StartDate != "" {
					startDate = block.StartDate
				}
				if block.DueDate != "" {
					dueDate = block.DueDate
				}
				pinnedVal := 0
				if block.Pinned {
					pinnedVal = 1
				}
				// Compute comments_count (child NOTE blocks) and links_count
				// ((uuid) refs) for this task — same derived-cache approach
				// as IndexFileBlocks (see childNotesByParent + BlockRefRegex).
				commentsCount := 0
				for _, b2 := range res.Blocks {
					if b2.ParentID == block.ID && b2.Type == parser.BlockNote {
						commentsCount++
					}
				}
				linksCount := len(parser.BlockRefRegex.FindAllString(block.RawText, -1))
				_, err = stmtTask.Exec(block.ID, block.Status, owner, startDate, dueDate, block.Priority, pinnedVal, block.Progress, commentsCount, linksCount)
				if err != nil {
					return 0, skipped, fmt.Errorf("failed to insert task for block %s: %w", block.ID, err)
				}
			}

			tags := ExtractTags(block.RawText)
			// Associate file frontmatter tags to the first parsed block.
			// The previous implementation checked block.LineNumber == 1,
			// which is never true when the file has YAML frontmatter.
			if blockIdx == 0 {
				for _, ft := range res.Tags {
					trimmedFT := strings.TrimPrefix(ft, "#")
					found := false
					for _, t := range tags {
						if t == trimmedFT {
							found = true
							break
						}
					}
					if !found && trimmedFT != "" {
						tags = append(tags, trimmedFT)
					}
				}
			}

			for _, tagPath := range tags {
				parts := strings.Split(tagPath, "/")
				var level0, level1, level2 interface{}
				if len(parts) > 0 {
					level0 = parts[0]
				}
				if len(parts) > 1 {
					level1 = parts[1]
				}
				if len(parts) > 2 {
					level2 = parts[2]
				}
			_, err = stmtTag.Exec(block.ID, tagPath, level0, level1, level2)
			if err != nil {
				log.Printf("db.IndexScanResults: tag insert error for block %s tag %q: %v", block.ID, tagPath, err)
				continue
			}
			}
		}

		indexedCount++
	}

	if err := tx.Commit(); err != nil {
		return 0, skipped, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return indexedCount, skipped, nil
}

// BlockLocation holds the file-level coordinates of a block, used by write
// paths (UpdateBlockState, MutateBlock, PluginUpdateTaskMeta) to resolve the
// on-disk file path from a block UUID.
type BlockLocation struct {
	Notebook  string
	Section   string
	Page      string
	BlockType string
}

// GetBlockLocation looks up the (notebook, section, page, type) for a block
// UUID. This is the typed API replacement for the raw SQLDB().QueryRow calls
// that were scattered across app.go write paths.
func (dm *DatabaseManager) GetBlockLocation(blockID string) (BlockLocation, error) {
	var loc BlockLocation
	err := dm.db.QueryRow(
		"SELECT notebook, section, page, type FROM blocks WHERE id = ?",
		blockID,
	).Scan(&loc.Notebook, &loc.Section, &loc.Page, &loc.BlockType)
	return loc, err
}

// FetchPageBlocks returns a flat ordered list of all blocks for a page.
// A page is a single file; all blocks share the same (notebook, section,
// page) and are ordered by line_number. Each block carries its own file_date.
func (dm *DatabaseManager) FetchPageBlocks(notebook, section, page string) ([]parser.ParsedBlock, error) {
	rows, err := dm.db.Query(`
		SELECT b.id, b.parent_id, b.depth, b.type, b.raw_content, b.clean_content, b.line_number,
		       b.file_date,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''), COALESCE(t.due_date, ''), COALESCE(t.priority, 0)
		FROM blocks b
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE b.notebook = ? AND b.section = ? AND b.page = ?
		ORDER BY b.line_number ASC
	`, notebook, section, page)
	if err != nil {
		return nil, fmt.Errorf("failed to query page blocks: %w", err)
	}
	defer rows.Close()

	var blocks []parser.ParsedBlock
	for rows.Next() {
		var b parser.ParsedBlock
		var bType, fileDate string
		var parentID sql.NullString
		var status, owner, start, due string
		var priority int

		if err := rows.Scan(&b.ID, &parentID, &b.Depth, &bType, &b.RawText, &b.CleanText, &b.LineNumber, &fileDate, &status, &owner, &start, &due, &priority); err != nil {
			return nil, err
		}
		if parentID.Valid {
			b.ParentID = parentID.String
		}
		b.Type = parser.BlockType(bType)
		b.Status = status
		b.Owner = owner
		b.StartDate = start
		b.DueDate = due
		b.Priority = priority
		b.FileDate = fileDate
		blocks = append(blocks, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating page blocks: %w", err)
	}
	return blocks, nil
}

// QueryTasksWithFilters fetches task results matching the provided query filters.
func (dm *DatabaseManager) QueryTasksWithFilters(filter parser.TaskQueryFilter) ([]parser.TaskResult, error) {
	baseQuery := `
		SELECT b.id, b.parent_id, b.notebook, b.section, b.page, b.file_date, b.depth, b.raw_content, b.clean_content, b.line_number,
		       t.status, t.owner, t.start_date, t.due_date, t.priority
		FROM blocks b
		INNER JOIN tasks t ON b.id = t.block_id
		WHERE 1=1
	`

	var args []interface{}

	if filter.Owner != "" {
		baseQuery += " AND t.owner = ?"
		args = append(args, filter.Owner)
	}

	if filter.Priority > 0 {
		baseQuery += " AND t.priority = ?"
		args = append(args, filter.Priority)
	}

	if filter.StartDate != "" {
		baseQuery += " AND (t.start_date >= ? OR t.due_date >= ?)"
		args = append(args, filter.StartDate, filter.StartDate)
	}

	if filter.EndDate != "" {
		baseQuery += " AND (t.due_date <= ? OR t.start_date <= ?)"
		args = append(args, filter.EndDate, filter.EndDate)
	}

	if len(filter.Tags) > 0 {
		var tagConditions []string
		for _, tag := range filter.Tags {
			trimmedTag := strings.TrimPrefix(tag, "#")
			if trimmedTag != "" {
				tagConditions = append(tagConditions, "b.id IN (SELECT block_id FROM tags WHERE raw_path = ? OR raw_path LIKE ?)")
				args = append(args, trimmedTag, trimmedTag+"/%")
			}
		}
		if len(tagConditions) > 0 {
			baseQuery += " AND (" + strings.Join(tagConditions, " OR ") + ")"
		}
	}

	baseQuery += " ORDER BY b.file_date DESC, b.line_number ASC"

	rows, err := dm.db.Query(baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

	var results []parser.TaskResult
	var blockIDs []interface{}
	for rows.Next() {
		var r parser.TaskResult
		var parentID sql.NullString
		var status, owner, start, due interface{}
		var priority int

		err := rows.Scan(
			&r.ID, &parentID, &r.Notebook, &r.Section, &r.Page, &r.FileDate, &r.Depth, &r.RawContent, &r.CleanContent, &r.LineNumber,
			&status, &owner, &start, &due, &priority,
		)
		if err != nil {
			return nil, err
		}
		if parentID.Valid {
			r.ParentID = parentID.String
		}

		if statusStr, ok := status.(string); ok {
			r.Status = statusStr
		}
		if ownerStr, ok := owner.(string); ok {
			r.Owner = ownerStr
		}
		if startStr, ok := start.(string); ok {
			r.StartDate = startStr
		}
		if dueStr, ok := due.(string); ok {
			r.DueDate = dueStr
		}
		r.Priority = priority

		results = append(results, r)
		blockIDs = append(blockIDs, r.ID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return results, nil
	}

	// Fetch all tags for the returned blocks in a single secondary query to
	// avoid the N+1 pattern of one SELECT per block.
	tagPlaceholders := make([]string, len(blockIDs))
	for i := range tagPlaceholders {
		tagPlaceholders[i] = "?"
	}
	tagQuery := "SELECT block_id, raw_path FROM tags WHERE block_id IN (" + strings.Join(tagPlaceholders, ",") + ") ORDER BY block_id, raw_path"
	tagRows, err := dm.db.Query(tagQuery, blockIDs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query task tags: %w", err)
	}
	defer tagRows.Close()

	tagIndex := make(map[string][]string, len(results))
	for tagRows.Next() {
		var blockID, tag string
		if err := tagRows.Scan(&blockID, &tag); err != nil {
			return nil, err
		}
		tagIndex[blockID] = append(tagIndex[blockID], tag)
	}
	if err := tagRows.Close(); err != nil {
		return nil, err
	}

	for i := range results {
		if tags, ok := tagIndex[results[i].ID]; ok {
			results[i].Tags = tags
		}
	}

	return results, nil
}

// QueryTagHierarchy returns the hierarchical tag tree with per-node distinct
// block counts. A node's count is the number of distinct blocks that are
// tagged at or beneath that path, so clicking #work surfaces every block
// reachable via #work or any of its descendants — without double-counting a
// block that happens to carry several nested tags (e.g. #work and
// #work/project/milestone-one).
func (dm *DatabaseManager) QueryTagHierarchy() ([]parser.TagNode, error) {
	rows, err := dm.db.Query("SELECT raw_path, block_id FROM tags")
	if err != nil {
		return nil, fmt.Errorf("failed to query tag hierarchy: %w", err)
	}
	defer rows.Close()

	// direct maps each exact raw_path to the set of block_ids tagged with it.
	// Keeping the set (rather than just a count) is what lets us compute a
	// node's count as the *distinct* number of blocks at-or-beneath it via
	// a bottom-up union pass over the trie.
	direct := map[string]map[string]struct{}{}
	for rows.Next() {
		var p, id string
		if err := rows.Scan(&p, &id); err != nil {
			return nil, err
		}
		if direct[p] == nil {
			direct[p] = make(map[string]struct{})
		}
		direct[p][id] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Build a trie of every segment across all paths.
	type node struct {
		name     string
		path     string
		children map[string]*node
		// blocks is the union of (a) blocks tagged exactly at this path and
		// (b) blocks tagged at any descendant path. Populated bottom-up by
		// aggregate below.
		blocks map[string]struct{}
	}
	root := &node{name: "", path: "", children: map[string]*node{}}
	for p := range direct {
		segs := strings.Split(p, "/")
		cur := root
		acc := ""
		for i, seg := range segs {
			if i > 0 {
				acc += "/"
			}
			acc += seg
			child, ok := cur.children[seg]
			if !ok {
				child = &node{name: seg, path: acc, children: map[string]*node{}}
				cur.children[seg] = child
			}
			cur = child
		}
	}

	// Bottom-up pass: each node's blocks-set starts with the blocks tagged
	// exactly at that path, then absorbs the union of its children's sets.
	// The size of the resulting set is the count we surface to the UI.
	var aggregate func(n *node) map[string]struct{}
	aggregate = func(n *node) map[string]struct{} {
		merged := make(map[string]struct{}, len(direct[n.path]))
		for id := range direct[n.path] {
			merged[id] = struct{}{}
		}
		for _, child := range n.children {
			for id := range aggregate(child) {
				merged[id] = struct{}{}
			}
		}
		n.blocks = merged
		return merged
	}
	aggregate(root)

	var build func(parent *node) []parser.TagNode
	build = func(parent *node) []parser.TagNode {
		var kids []parser.TagNode
		// Sort the children map by name for deterministic output independent
		// of Go's randomized map iteration order.
		names := make([]string, 0, len(parent.children))
		for name := range parent.children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child := parent.children[name]
			node := parser.TagNode{
				Name:  child.name,
				Path:  child.path,
				Count: len(child.blocks),
			}
			node.Children = build(child)
			kids = append(kids, node)
		}
		return kids
	}

	return build(root), nil
}

// QueryBlocksByTag returns blocks whose tag path equals tagPath or is nested
// beneath it (prefix semantics, so #work matches #work/project/milestone-one).
func (dm *DatabaseManager) QueryBlocksByTag(tagPath string) ([]parser.TaskResult, error) {
	tagPath = strings.TrimSpace(strings.TrimPrefix(tagPath, "#"))
	if tagPath == "" {
		return []parser.TaskResult{}, nil
	}
	query := `
		SELECT b.id, b.parent_id, b.notebook, b.section, b.page, b.file_date, b.depth, b.raw_content, b.clean_content, b.line_number,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''), COALESCE(t.due_date, ''), COALESCE(t.priority, 0)
		FROM blocks b
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE b.id IN (SELECT block_id FROM tags WHERE raw_path = ? OR raw_path LIKE ?)
		ORDER BY b.notebook, b.section, b.page, b.file_date DESC, b.line_number ASC
		LIMIT 500
	`
	rows, err := dm.db.Query(query, tagPath, tagPath+"/%")
	if err != nil {
		return nil, fmt.Errorf("failed to query blocks by tag: %w", err)
	}
	defer rows.Close()

	var results []parser.TaskResult
	for rows.Next() {
		var r parser.TaskResult
		var parentID sql.NullString
		var status, owner, start, due string
		var priority int
		if err := rows.Scan(
			&r.ID, &parentID, &r.Notebook, &r.Section, &r.Page, &r.FileDate, &r.Depth, &r.RawContent, &r.CleanContent, &r.LineNumber,
			&status, &owner, &start, &due, &priority,
		); err != nil {
			return nil, err
		}
		if parentID.Valid {
			r.ParentID = parentID.String
		}
		r.Status = status
		r.Owner = owner
		r.StartDate = start
		r.DueDate = due
		r.Priority = priority
		results = append(results, r)
	}
	return results, nil
}

// searchMaxPerGroup caps how many hits SearchBlocks returns from any single
// page (notebook/section/page) before moving on, so one verbose page can't
// monopolize the result list. Tunable; 3 keeps the modal diverse.
const searchMaxPerGroup = 3

// buildFTSQuery turns a free-text user query into a safe FTS5 MATCH
// expression. The index uses tokenize='unicode61', so non-ASCII content
// (CJK, accented Latin, Cyrillic, …) IS tokenized and searchable — the query
// builder must not strip those code points. We keep Unicode letters and
// digits (covering every script unicode61 would treat as word characters)
// and drop everything else, which removes ALL FTS5 query-syntax characters
// (`"`, `*`, `(`, `)`, `:`, `^`, `-` are ASCII punctuation, never letters or
// digits). Each surviving term gets a trailing `*` for prefix matching
// (closer to the old LIKE %term% feel than bare-token exact matching). Terms
// are space-joined → FTS5 implicit AND. Returns "" when no usable terms
// survive, which the caller treats as "no search".
func buildFTSQuery(query string) string {
	var parts []string
	for _, w := range strings.Fields(query) {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, w)
		if len(clean) < 2 {
			continue
		}
		parts = append(parts, clean+"*")
	}
	return strings.Join(parts, " ")
}

// SearchBlocks searches indexed blocks via the FTS5 virtual table, ranked by
// bm25 relevance, with highlighted snippets and per-page grouping. It is a
// thin wrapper over SearchBlocksPaged returning the first page (offset 0,
// limit 50) for backwards compatibility with the original single-shot binding.
func (dm *DatabaseManager) SearchBlocks(query string) ([]parser.TaskResult, error) {
	res, err := dm.SearchBlocksPaged(query, 0, 50)
	if err != nil {
		return nil, err
	}
	return res.Results, nil
}

// searchFlatCap bounds the flat ranked fetch that feeds the Go-side per-page
// grouping. A common-term query on a large vault can match many blocks; this
// cap keeps the query fast and memory bounded while still returning far more
// than any reasonable modal needs to display. (FTS5 helper functions like
// snippet()/bm25() only work when the FTS table is in the direct FROM clause,
// so per-page grouping via a window-function subquery is not possible — we
// group in Go instead.)
const searchFlatCap = 500

// SearchBlocksPaged runs the FTS5 search and returns a ranked, paginated
// envelope with highlighted snippets, the total match count, and a HasMore
// flag so the frontend knows whether to fetch the next page.
//
// The query selects flat bm25-ranked matches with snippet() highlights (capped
// at searchFlatCap rows). Per-page grouping (top searchMaxPerGroup hits per
// notebook/section/page) is applied in Go because FTS5 helper functions
// cannot survive a window-function subquery wrap. Tag hydration is a single
// secondary SELECT (no N+1).
func (dm *DatabaseManager) SearchBlocksPaged(query string, offset, limit int) (parser.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || offset < 0 || limit <= 0 {
		return parser.SearchResult{Results: []parser.TaskResult{}, Total: 0, Offset: offset, Limit: limit}, nil
	}
	fts := buildFTSQuery(query)
	if fts == "" {
		return parser.SearchResult{Results: []parser.TaskResult{}, Total: 0, Offset: offset, Limit: limit}, nil
	}

	pageQuery := `
		SELECT b.id, b.parent_id, b.notebook, b.section, b.page, b.file_date, b.depth,
		       b.raw_content, b.clean_content, b.line_number,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''),
		       COALESCE(t.due_date, ''), COALESCE(t.priority, 0),
		       snippet(blocks_fts, 0, '<mark>', '</mark>', '...', 12),
		       bm25(blocks_fts) AS rank
		FROM blocks_fts
		JOIN blocks b ON b.rowid = blocks_fts.rowid
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE blocks_fts MATCH ?
		ORDER BY rank
		LIMIT ?`
	rows, err := dm.db.Query(pageQuery, fts, searchFlatCap)
	if err != nil {
		return parser.SearchResult{}, fmt.Errorf("failed to search blocks: %w", err)
	}

	var flat []parser.TaskResult
	for rows.Next() {
		var r parser.TaskResult
		var parentID sql.NullString
		var status, owner, start, due string
		var priority int
		var rank float64
		if err := rows.Scan(
			&r.ID, &parentID, &r.Notebook, &r.Section, &r.Page, &r.FileDate, &r.Depth,
			&r.RawContent, &r.CleanContent, &r.LineNumber,
			&status, &owner, &start, &due, &priority,
			&r.Snippet, &rank,
		); err != nil {
			rows.Close()
			return parser.SearchResult{}, err
		}
		if parentID.Valid {
			r.ParentID = parentID.String
		}
		r.Status = status
		r.Owner = owner
		r.StartDate = start
		r.DueDate = due
		r.Priority = priority
		flat = append(flat, r)
	}
	if err := rows.Close(); err != nil {
		return parser.SearchResult{}, err
	}

	// Per-page grouping: keep at most searchMaxPerGroup hits per
	// (notebook, section, page), preserving the bm25 rank order from SQL.
	grouped := make([]parser.TaskResult, 0, len(flat))
	perPage := make(map[string]int, len(flat))
	for _, r := range flat {
		key := r.Notebook + "\x00" + r.Section + "\x00" + r.Page
		if perPage[key] >= searchMaxPerGroup {
			continue
		}
		perPage[key]++
		grouped = append(grouped, r)
	}
	total := len(grouped)

	end := offset + limit
	if end > total {
		end = total
	}
	var page []parser.TaskResult
	if offset < total {
		page = grouped[offset:end]
	}
	if page == nil {
		page = []parser.TaskResult{}
	}

	// Tag hydration for just this page (single secondary SELECT).
	if len(page) > 0 {
		blockIDs := make([]interface{}, len(page))
		for i, r := range page {
			blockIDs[i] = r.ID
		}
		placeholders := make([]string, len(page))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		tagQuery := "SELECT block_id, raw_path FROM tags WHERE block_id IN (" + strings.Join(placeholders, ",") + ") ORDER BY block_id, raw_path"
		tagRows, err := dm.db.Query(tagQuery, blockIDs...)
		if err != nil {
			return parser.SearchResult{}, fmt.Errorf("failed to query search tags: %w", err)
		}
		tagIndex := make(map[string][]string, len(page))
		for tagRows.Next() {
			var blockID, tag string
			if err := tagRows.Scan(&blockID, &tag); err != nil {
				tagRows.Close()
				return parser.SearchResult{}, err
			}
			tagIndex[blockID] = append(tagIndex[blockID], tag)
		}
		if err := tagRows.Close(); err != nil {
			return parser.SearchResult{}, err
		}
		for i := range page {
			if tags, ok := tagIndex[page[i].ID]; ok {
				page[i].Tags = tags
			}
		}
	}

	return parser.SearchResult{
		Results: page,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
		HasMore: end < total,
	}, nil
}

