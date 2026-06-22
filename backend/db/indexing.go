package db

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"silt/backend/parser"
)

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

// ClearFileBlocks deletes all blocks, tasks, and tags associated with a
// specific page on a given day, scoped to the notebook's source so a linked
// notebook sharing a display name with a vault notebook cannot clear the
// vault's rows (#100).
func (dm *DatabaseManager) ClearFileBlocks(tx *sql.Tx, source, notebook, section, page string) error {
	if source == "" {
		source = "vault"
	}
	query := "DELETE FROM blocks WHERE source = ? AND notebook = ? AND section = ? AND page = ?"
	var err error
	if tx != nil {
		_, err = tx.Exec(query, source, notebook, section, page)
	} else {
		_, err = dm.db.Exec(query, source, notebook, section, page)
	}
	return err
}

// DeleteBlockFromPage removes a single block by ID, but ONLY if it is at the
// specified source/notebook/section/page. This page-scoping is critical for
// the cross-page-move source-removal path: the block has already been indexed
// at the TARGET page by the first pass. A non-scoped delete would remove it
// from the target too (#104 concurrency fix).
func (dm *DatabaseManager) DeleteBlockFromPage(blockID, source, notebook, section, page string) error {
	if source == "" {
		source = "vault"
	}
	_, err := dm.db.Exec(
		"DELETE FROM blocks WHERE id = ? AND source = ? AND notebook = ? AND section = ? AND page = ?",
		blockID, source, notebook, section, page)
	return err
}

// BlockIDsForPage returns the IDs of every block currently indexed for a page,
// without materializing the full ParsedBlock rows. Used by the eviction paths
// (DeletePage, watcher Remove/Rename, SaveFileBlocks replacement) to release the
// per-block mutex entries (#122) for blocks that no longer exist. Scoped by
// source (#100).
func (dm *DatabaseManager) BlockIDsForPage(source, notebook, section, page string) ([]string, error) {
	if source == "" {
		source = "vault"
	}
	rows, err := dm.db.Query(
		"SELECT id FROM blocks WHERE source = ? AND notebook = ? AND section = ? AND page = ?",
		source, notebook, section, page,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// IndexFileBlocks updates the index with a set of blocks in a single transaction.
//
// fileWarnings is an optional slice of non-fatal diagnostics from the parser
// (e.g. malformed YAML frontmatter). They are logged at warn level so a
// maintainer can grep the output without changing the call signature or
// the public API.
func (dm *DatabaseManager) IndexFileBlocks(source, notebook, section, page string, blocks []parser.ParsedBlock, fileTags []string, fileWarnings ...string) error {
	if source == "" {
		source = "vault"
	}
	for _, w := range fileWarnings {
		log.Printf("db.IndexFileBlocks(%s/%s/%s/%s): %s", source, notebook, section, page, w)
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
	// file (their IDs are no longer in the new parse output). Scope by source
	// so a linked notebook sharing a display name with a vault notebook cannot
	// clear the vault's rows (#100).
	if err := dm.ClearFileBlocks(tx, source, notebook, section, page); err != nil {
		return fmt.Errorf("failed to clear old blocks: %w", err)
	}

	if len(blocks) == 0 {
		return tx.Commit()
	}

	stmtBlock, err := tx.Prepare("INSERT INTO blocks (id, parent_id, source, notebook, section, page, file_date, depth, type, raw_content, clean_content, line_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
		_, err = stmtBlock.Exec(block.ID, parentID, source, notebook, section, page, fileDate, block.Depth, string(block.Type), block.RawText, block.CleanText, block.LineNumber)
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
			// Pin projection (#135): the column accepts NULL/0/1 so the
			// cache can represent the parser's tri-state — NULL when no
			// [pin::] token is present (nil), 0 for an explicit [pin::
			// false] (&false), 1 for [pin:: true] (&true). The column is
			// reproducible cache; the markdown is the source of truth.
			var pinnedVal sql.NullInt64
			if block.Pinned != nil {
				pinnedVal = sql.NullInt64{Int64: 0, Valid: true}
				if *block.Pinned {
					pinnedVal = sql.NullInt64{Int64: 1, Valid: true}
				}
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

	stmtBlock, err := tx.Prepare("INSERT INTO blocks (id, parent_id, source, notebook, section, page, file_date, depth, type, raw_content, clean_content, line_number) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
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
		// Source comes from the ScanResult so the batched linked-tree scan
		// (#134) scopes its own rows; the vault startup scan leaves Source
		// empty, defaulting to 'vault' (the historical behavior).
		source := res.Source
		if source == "" {
			source = "vault"
		}
		if err := dm.ClearFileBlocks(tx, source, res.Notebook, res.Section, res.Page); err != nil {
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
			_, err = stmtBlock.Exec(block.ID, parentID, source, res.Notebook, res.Section, res.Page, fileDate, block.Depth, string(block.Type), block.RawText, block.CleanText, block.LineNumber)
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
				// Pin projection (#135): tri-state NULL/0/1 mirroring
				// IndexFileBlocks — NULL=absent, 0=[pin:: false], 1=[pin::
				// true]. Reproducible cache; markdown is source of truth.
				var pinnedVal sql.NullInt64
				if block.Pinned != nil {
					pinnedVal = sql.NullInt64{Int64: 0, Valid: true}
					if *block.Pinned {
						pinnedVal = sql.NullInt64{Int64: 1, Valid: true}
					}
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

// ClearSourceBlocks deletes every block (and, via CASCADE, its tasks/tags)
// for a given source. Used by UnlinkNotebook to drop a linked notebook's
// local index rows without touching the external files (#100).
func (dm *DatabaseManager) ClearSourceBlocks(source string) error {
	if source == "" {
		return nil
	}
	_, err := dm.db.Exec("DELETE FROM blocks WHERE source = ?", source)
	return err
}
