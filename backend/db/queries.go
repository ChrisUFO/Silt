package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"silt/backend/parser"
)

// BlockLocation holds the file-level coordinates of a block, used by write
// paths (UpdateBlockState, MutateBlock, PluginUpdateTaskMeta) to resolve the
// on-disk file path from a block UUID. Source ('vault' | 'linked:<id>') tells
// the path-resolution layer which root the file lives under (#100).
type BlockLocation struct {
	Source    string
	Notebook  string
	Section   string
	Page      string
	BlockType string
}

// GetBlockLocation looks up the (source, notebook, section, page, type) for a
// block UUID. This is the typed API replacement for the raw SQLDB().QueryRow
// calls that were scattered across app.go write paths.
func (dm *DatabaseManager) GetBlockLocation(blockID string) (BlockLocation, error) {
	var loc BlockLocation
	err := dm.db.QueryRow(
		"SELECT COALESCE(source, 'vault'), notebook, section, page, type FROM blocks WHERE id = ?",
		blockID,
	).Scan(&loc.Source, &loc.Notebook, &loc.Section, &loc.Page, &loc.BlockType)
	if loc.Source == "" {
		loc.Source = "vault"
	}
	return loc, err
}

// FetchPageBlocks returns a flat ordered list of all blocks for a page.
// A page is a single file; all blocks share the same (notebook, section,
// page) and are ordered by line_number. Each block carries its own file_date.
func (dm *DatabaseManager) FetchPageBlocks(source, notebook, section, page string) ([]parser.ParsedBlock, error) {
	if source == "" {
		source = "vault"
	}
	rows, err := dm.db.Query(`
		SELECT b.id, b.parent_id, b.depth, b.type, b.raw_content, b.clean_content, b.line_number,
		       b.file_date,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''), COALESCE(t.due_date, ''), COALESCE(t.priority, 0)
		FROM blocks b
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE b.source = ? AND b.notebook = ? AND b.section = ? AND b.page = ?
		ORDER BY b.line_number ASC
	`, source, notebook, section, page)
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
		       t.status, t.owner, t.start_date, t.due_date, t.priority, t.pinned
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
		var pinned sql.NullInt64

		err := rows.Scan(
			&r.ID, &parentID, &r.Notebook, &r.Section, &r.Page, &r.FileDate, &r.Depth, &r.RawContent, &r.CleanContent, &r.LineNumber,
			&status, &owner, &start, &due, &priority, &pinned,
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
		// Hydrate the tri-state pin from the cache column (#135): NULL
		// stays nil (no [pin::] token), 0 -> &false ([pin:: false]), 1 ->
		// &true ([pin:: true]). Mirrors the IndexFileBlocks projection.
		if pinned.Valid {
			b := pinned.Int64 != 0
			r.Pinned = &b
		}

		results = append(results, r)
		blockIDs = append(blockIDs, r.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating task results: %w", err)
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating tag hierarchy: %w", err)
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
		// Non-nil empty slice so leaf nodes serialize as JSON [] (not null);
		// the frontend dereferences node.children.length unconditionally.
		kids := make([]parser.TagNode, 0, len(parent.children))
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating blocks by tag: %w", err)
	}
	return results, nil
}

// DistinctOwners returns the sorted, de-duplicated set of non-empty task owners
// across the whole vault. This is the read-only projection the @-mention
// typeahead (#184) offers: typing `@` surfaces every owner already assigned to
// a task. SQLite stays working memory — no mention state is stored here; the
// `@[name]` token round-trips through markdown as the source of truth.
func (dm *DatabaseManager) DistinctOwners() ([]string, error) {
	rows, err := dm.db.Query("SELECT DISTINCT owner FROM tasks WHERE owner != '' ORDER BY owner")
	if err != nil {
		return nil, fmt.Errorf("failed to query distinct owners: %w", err)
	}
	defer rows.Close()

	var owners []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		owners = append(owners, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed iterating distinct owners: %w", err)
	}
	return owners, nil
}
