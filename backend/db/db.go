package db

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"silt/backend/parser"

	_ "modernc.org/sqlite"
)

type DatabaseManager struct {
	db *sql.DB
}

func NewDatabaseManager() (*DatabaseManager, error) {
	// Open a shared in-memory SQLite database.
	// We use cache=shared so multiple connections can access it if needed,
	// and it persists as long as the main connection remains open.
	sqlDB, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite: %w", err)
	}

	// Cap the connection pool at one. SQLite serializes writers internally;
	// allowing multiple Go-level connections would only obscure the
	// locking story without giving us concurrency we could actually use.
	sqlDB.SetMaxOpenConns(1)

	dm := &DatabaseManager{db: sqlDB}
	if err := dm.initSchema(); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return dm, nil
}

// SQLDB exposes the underlying *sql.DB handle. Callers MUST serialize access
// through core.ExecutionCoordinator (e.g. WithDBRead/WithDBWrite) to avoid
// race conditions on the shared in-memory database.
func (dm *DatabaseManager) SQLDB() *sql.DB {
	return dm.db
}

func (dm *DatabaseManager) Close() error {
	if dm.db != nil {
		return dm.db.Close()
	}
	return nil
}

func (dm *DatabaseManager) initSchema() error {
	// Enable foreign key constraints
	_, err := dm.db.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	// Optimize pragmas for in-memory speed
	_, err = dm.db.Exec("PRAGMA journal_mode = MEMORY;")
	if err != nil {
		return fmt.Errorf("failed to set journal mode: %w", err)
	}
	_, err = dm.db.Exec("PRAGMA synchronous = OFF;")
	if err != nil {
		return fmt.Errorf("failed to set synchronous: %w", err)
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
		FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
	);`
	if _, err := dm.db.Exec(createTasksTable); err != nil {
		return fmt.Errorf("failed to create tasks table: %w", err)
	}

	// Tags Table
	createTagsTable := `
	CREATE TABLE IF NOT EXISTS tags (
		block_id TEXT NOT NULL,
		raw_path TEXT NOT NULL,  -- 'work/sogav/milestone-one'
		level_0 TEXT NOT NULL,   -- 'work'
		level_1 TEXT,            -- 'sogav'
		level_2 TEXT,            -- 'milestone-one'
		PRIMARY KEY(block_id, raw_path),
		FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
	);`
	if _, err := dm.db.Exec(createTagsTable); err != nil {
		return fmt.Errorf("failed to create tags table: %w", err)
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

	return nil
}

// ExtractTags finds inline tags starting with # followed by a letter, ignoring numeric priorities.
// Tag names may contain letters, digits, underscores, hyphens, and slashes
// (so #work/sogav/milestone-one is captured in full).
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
func (dm *DatabaseManager) ClearFileBlocks(tx *sql.Tx, notebook, section, page, fileDate string) error {
	query := "DELETE FROM blocks WHERE notebook = ? AND section = ? AND page = ? AND file_date = ?"
	var err error
	if tx != nil {
		_, err = tx.Exec(query, notebook, section, page, fileDate)
	} else {
		_, err = dm.db.Exec(query, notebook, section, page, fileDate)
	}
	return err
}

// IndexFileBlocks updates the index with a set of blocks in a single transaction.
//
// fileWarnings is an optional slice of non-fatal diagnostics from the parser
// (e.g. malformed YAML frontmatter). They are logged at warn level so a
// maintainer can grep the output without changing the call signature or
// the public API.
func (dm *DatabaseManager) IndexFileBlocks(notebook, section, page, fileDate string, blocks []parser.ParsedBlock, fileTags []string, fileWarnings ...string) error {
	for _, w := range fileWarnings {
		log.Printf("db.IndexFileBlocks(%s/%s/%s): %s", notebook, section, fileDate, w)
	}

	tx, err := dm.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete any pre-existing rows for the block IDs we're about to (re)insert.
	// Block IDs are stable across re-parses, but the (notebook, section, file_date)
	// tuple is denormalized on each row. If the file's frontmatter metadata
	// changed since the last index, the old rows still sit under the previous
	// tuple and would collide on PRIMARY KEY. Cascading FKs clean up their
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
	if err := dm.ClearFileBlocks(tx, notebook, section, page, fileDate); err != nil {
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

	stmtTask, err := tx.Prepare("INSERT INTO tasks (block_id, status, owner, start_date, due_date, priority) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmtTask.Close()

	stmtTag, err := tx.Prepare("INSERT INTO tags (block_id, raw_path, level_0, level_1, level_2) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmtTag.Close()

	for blockIdx, block := range blocks {
		// 1. Insert into blocks
		var parentID interface{}
		if block.ParentID != "" {
			parentID = block.ParentID
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
			_, err = stmtTask.Exec(block.ID, block.Status, owner, startDate, dueDate, block.Priority)
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

	stmtTask, err := tx.Prepare("INSERT INTO tasks (block_id, status, owner, start_date, due_date, priority) VALUES (?, ?, ?, ?, ?, ?)")
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
		if err := dm.ClearFileBlocks(tx, res.Notebook, res.Section, res.Page, res.Date); err != nil {
			return 0, skipped, fmt.Errorf("failed to clear blocks for %s: %w", res.Path, err)
		}

		for blockIdx, block := range res.Blocks {
			var parentID interface{}
			if block.ParentID != "" {
				parentID = block.ParentID
			}
			_, err = stmtBlock.Exec(block.ID, parentID, res.Notebook, res.Section, res.Page, res.Date, block.Depth, string(block.Type), block.RawText, block.CleanText, block.LineNumber)
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
				_, err = stmtTask.Exec(block.ID, block.Status, owner, startDate, dueDate, block.Priority)
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

// FetchTimelineDays fetches day-grouped blocks for infinite virtualization.
//
// The implementation issues exactly two queries regardless of the number of
// days requested: one to resolve the paginated date set, and one to load all
// blocks for those dates in a single round-trip. The results are grouped by
// file_date in Go and formatted for the timeline view.
func (dm *DatabaseManager) FetchTimelineDays(notebook, section, page string, limit, offset int) ([]parser.DayGroup, error) {
	// Query 1: resolve paginated distinct dates.
	dateRows, err := dm.db.Query(
		"SELECT DISTINCT file_date FROM blocks WHERE notebook = ? AND section = ? AND page = ? ORDER BY file_date DESC LIMIT ? OFFSET ?",
		notebook, section, page, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query timeline dates: %w", err)
	}
	defer dateRows.Close()

	var dates []string
	for dateRows.Next() {
		var d string
		if err := dateRows.Scan(&d); err != nil {
			return nil, err
		}
		dates = append(dates, d)
	}
	if err := dateRows.Close(); err != nil {
		return nil, err
	}

	if len(dates) == 0 {
		return []parser.DayGroup{}, nil
	}

	// Query 2: load all blocks for the resolved dates in a single round-trip.
	placeholders := make([]string, len(dates))
	args := make([]interface{}, 0, len(dates)+3)
	args = append(args, notebook, section, page)
	for i, d := range dates {
		placeholders[i] = "?"
		args = append(args, d)
	}
	query := fmt.Sprintf(`
		SELECT b.id, b.parent_id, b.depth, b.type, b.raw_content, b.clean_content, b.line_number,
		       b.file_date,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''), COALESCE(t.due_date, ''), COALESCE(t.priority, 0)
		FROM blocks b
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE b.notebook = ? AND b.section = ? AND b.page = ? AND b.file_date IN (%s)
		ORDER BY b.file_date DESC, b.line_number ASC
	`, strings.Join(placeholders, ","))

	rows, err := dm.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query timeline blocks: %w", err)
	}
	defer rows.Close()

	// Group blocks by file_date preserving the date order from Query 1.
	groupOrder := make([]string, 0, len(dates))
	groupIndex := make(map[string]int, len(dates))
	grouped := make(map[string][]parser.ParsedBlock, len(dates))

	for rows.Next() {
		var b parser.ParsedBlock
		var bType, fileDate string
		var parentID sql.NullString
		var status, owner, start, due string
		var priority int

		if err := rows.Scan(&b.ID, &parentID, &b.Depth, &bType, &b.RawText, &b.CleanText, &b.LineNumber, &fileDate, &status, &owner, &start, &due, &priority); err != nil {
			rows.Close()
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

		if _, ok := groupIndex[fileDate]; !ok {
			groupIndex[fileDate] = len(groupOrder)
			groupOrder = append(groupOrder, fileDate)
		}
		grouped[fileDate] = append(grouped[fileDate], b)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Build the result in the original date order (descending).
	groups := make([]parser.DayGroup, 0, len(groupOrder))
	for _, d := range groupOrder {
		formatted := d
		if parsedTime, err := time.Parse("2006-01-02", d); err == nil {
			formatted = parsedTime.Format("Monday, January 2, 2006")
		}
		groups = append(groups, parser.DayGroup{
			Date:          d,
			FormattedDate: formatted,
			Blocks:        grouped[d],
		})
	}

	return groups, nil
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
// block counts (a node's count includes blocks tagged at or beneath it, so
// clicking #work surfaces everything under #work/sogav/milestone-one).
func (dm *DatabaseManager) QueryTagHierarchy() ([]parser.TagNode, error) {
	rows, err := dm.db.Query("SELECT raw_path, COUNT(DISTINCT block_id) FROM tags GROUP BY raw_path")
	if err != nil {
		return nil, fmt.Errorf("failed to query tag hierarchy: %w", err)
	}
	defer rows.Close()

	// direct counts per exact raw_path.
	direct := map[string]int{}
	var paths []string
	for rows.Next() {
		var p string
		var c int
		if err := rows.Scan(&p, &c); err != nil {
			return nil, err
		}
		if _, seen := direct[p]; !seen {
			paths = append(paths, p)
		}
		direct[p] = c
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Build a trie of every segment across all paths.
	type node struct {
		name     string
		path     string
		children map[string]*node
	}
	root := &node{name: "", path: "", children: map[string]*node{}}
	for _, p := range paths {
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

	// A node's aggregate count = sum of direct counts of raw_paths equal to or
	// prefixed by the node's path.
	var aggregate func(path string) int
	aggregate = func(path string) int {
		total := direct[path]
		prefix := path + "/"
		for _, p := range paths {
			if p != path && strings.HasPrefix(p, prefix) {
				// Only count leaf-ish direct entries; we sum all direct counts
				// beneath, which correctly aggregates distinct-path counts.
				total += direct[p]
			}
		}
		return total
	}

	// Note: aggregate above double-counts when a path has both a direct entry
	// and descendants, but since direct[] stores distinct blocks per EXACT
	// path and a block can carry multiple paths, summing distinct-path counts
	// is the intended "blocks at or beneath" semantics for navigation.

	var build func(parent *node) []parser.TagNode
	build = func(parent *node) []parser.TagNode {
		var kids []parser.TagNode
		for _, child := range parent.children {
			node := parser.TagNode{
				Name:  child.name,
				Path:  child.path,
				Count: aggregate(child.path),
			}
			node.Children = build(child)
			kids = append(kids, node)
		}
		// Stable-ish ordering by name.
		sortTagNodes(kids)
		return kids
	}

	return build(root), nil
}

func sortTagNodes(nodes []parser.TagNode) {
	// In-place alphabetical sort to keep tree output deterministic.
	for i := 1; i < len(nodes); i++ {
		for j := i; j > 0 && nodes[j-1].Name > nodes[j].Name; j-- {
			nodes[j-1], nodes[j] = nodes[j], nodes[j-1]
		}
	}
}

// QueryBlocksByTag returns blocks whose tag path equals tagPath or is nested
// beneath it (prefix semantics, so #work matches #work/sogav/milestone-one).
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
// per page, for the sidebar navigator. Ordering is by name at each level.
func (dm *DatabaseManager) ListNavigation() (parser.NavigationTree, error) {
	// A single grouped query is enough: the cardinality (notebooks × sections
	// × pages) is small even for large vaults, and we assemble the tree in Go
	// to preserve insertion order per level.
	rows, err := dm.db.Query(`
		SELECT notebook, section, page, COUNT(*) AS cnt
		FROM blocks
		GROUP BY notebook, section, page
		ORDER BY notebook, section, page
	`)
	if err != nil {
		return parser.NavigationTree{}, fmt.Errorf("failed to query navigation tree: %w", err)
	}
	defer rows.Close()

	type pageKey struct {
		name  string
		count int
	}
	type sectionKey struct {
		pages []pageKey
	}
	nbOrder := []string{}
	nbMap := map[string]*[]string{}      // notebook -> ordered sections
	secMap := map[string]map[string]*sectionKey{}
	pageMap := map[string]map[string]map[string]*pageKey{}

	for rows.Next() {
		var nb, sec, pg string
		var cnt int
		if err := rows.Scan(&nb, &sec, &pg, &cnt); err != nil {
			return parser.NavigationTree{}, err
		}
		if _, ok := nbMap[nb]; !ok {
			nbOrder = append(nbOrder, nb)
			nbMap[nb] = &[]string{}
			secMap[nb] = map[string]*sectionKey{}
			pageMap[nb] = map[string]map[string]*pageKey{}
		}
		if _, ok := secMap[nb][sec]; !ok {
			*nbMap[nb] = append(*nbMap[nb], sec)
			secMap[nb][sec] = &sectionKey{}
			pageMap[nb][sec] = map[string]*pageKey{}
		}
		if _, ok := pageMap[nb][sec][pg]; !ok {
			pageMap[nb][sec][pg] = &pageKey{name: pg}
			secMap[nb][sec].pages = append(secMap[nb][sec].pages, pageKey{name: pg})
		}
		pageMap[nb][sec][pg].count += cnt
	}
	if err := rows.Close(); err != nil {
		return parser.NavigationTree{}, err
	}

	tree := parser.NavigationTree{Notebooks: []parser.NavigationNotebook{}}
	for _, nb := range nbOrder {
		nn := parser.NavigationNotebook{Name: nb, Sections: []parser.NavigationSection{}}
		for _, sec := range *nbMap[nb] {
			ns := parser.NavigationSection{Name: sec, Pages: []parser.NavigationPage{}}
			for _, pg := range secMap[nb][sec].pages {
				ns.Pages = append(ns.Pages, parser.NavigationPage{Name: pg.name, Count: pg.count})
			}
			nn.Sections = append(nn.Sections, ns)
		}
		tree.Notebooks = append(tree.Notebooks, nn)
	}
	return tree, nil
}

// SearchBlocks searches for blocks matching the query. It splits the query into
// terms and matches each term against clean_content, notebook, or section.
func (dm *DatabaseManager) SearchBlocks(query string) ([]parser.TaskResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []parser.TaskResult{}, nil
	}

	words := strings.Fields(query)
	var sqlParts []string
	var args []interface{}

	for _, word := range words {
		sqlParts = append(sqlParts, "(LOWER(b.clean_content) LIKE LOWER(?) OR LOWER(b.notebook) LIKE LOWER(?) OR LOWER(b.section) LIKE LOWER(?))")
		term := "%" + strings.ToLower(word) + "%"
		args = append(args, term, term, term)
	}

	whereClause := strings.Join(sqlParts, " AND ")

	baseQuery := `
		SELECT b.id, b.parent_id, b.notebook, b.section, b.page, b.file_date, b.depth, b.raw_content, b.clean_content, b.line_number,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''), COALESCE(t.due_date, ''), COALESCE(t.priority, 0)
		FROM blocks b
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE ` + whereClause + `
		ORDER BY b.file_date DESC, b.line_number ASC
		LIMIT 100
	`

	rows, err := dm.db.Query(baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search blocks: %w", err)
	}
	defer rows.Close()

	var results []parser.TaskResult
	var blockIDs []interface{}

	for rows.Next() {
		var r parser.TaskResult
		var parentID sql.NullString
		var status, owner, start, due string
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
		r.Status = status
		r.Owner = owner
		r.StartDate = start
		r.DueDate = due
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

	tagPlaceholders := make([]string, len(blockIDs))
	for i := range tagPlaceholders {
		tagPlaceholders[i] = "?"
	}
	tagQuery := "SELECT block_id, raw_path FROM tags WHERE block_id IN (" + strings.Join(tagPlaceholders, ",") + ") ORDER BY block_id, raw_path"
	tagRows, err := dm.db.Query(tagQuery, blockIDs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query search tags: %w", err)
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

