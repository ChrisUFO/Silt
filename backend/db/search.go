package db

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"silt/backend/parser"
)

// searchMaxPerGroup caps how many hits SearchBlocks returns from any single
// page (notebook/section/page) before moving on, so one verbose page can't
// monopolize the result list. Tunable; 3 keeps the modal diverse.
const searchMaxPerGroup = 3

// SearchFilters narrows a SearchBlocksPaged query (#186 global enhancements).
// Empty fields mean "no filter on this dimension". Sort is "relevance" (bm25,
// the default) or "recency" (file_date DESC, a per-block date proxy — blocks
// carry their own date in the identity comment). VaultOnly scopes the search
// to in-vault blocks (excludes linked-notebook sources) — the scope control
// on the global search.
type SearchFilters struct {
	Notebook  string `json:"notebook"`
	Section   string `json:"section"`
	Tag       string `json:"tag"`
	Type      string `json:"type"`      // TASK / NOTE / HEADER / CODE / TABLE / DETAILS / CALLOUT
	Sort      string `json:"sort"`      // "" | "relevance" | "recency"
	VaultOnly bool   `json:"vaultOnly"` // exclude linked-notebook sources (source != 'vault')
}

// hasFilters is true when any narrowing dimension is set.
func (f SearchFilters) hasFilters() bool {
	return f.Notebook != "" || f.Section != "" || f.Tag != "" || f.Type != ""
}

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
	res, err := dm.SearchBlocksPaged(query, 0, 50, SearchFilters{})
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
func (dm *DatabaseManager) SearchBlocksPaged(query string, offset, limit int, filters SearchFilters) (parser.SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" || offset < 0 || limit <= 0 {
		return parser.SearchResult{Results: []parser.TaskResult{}, Total: 0, Offset: offset, Limit: limit}, nil
	}
	fts := buildFTSQuery(query)
	if fts == "" {
		return parser.SearchResult{Results: []parser.TaskResult{}, Total: 0, Offset: offset, Limit: limit}, nil
	}

	// Build the dynamic WHERE + ORDER BY from the filters. All user input is
	// parameterized (never string-interpolated) — a synced vault's tag/notebook
	// name can't inject SQL.
	var where []string
	var args []any
	where = append(where, "blocks_fts MATCH ?")
	args = append(args, fts)
	if filters.Notebook != "" {
		where = append(where, "b.notebook = ?")
		args = append(args, filters.Notebook)
	}
	if filters.Section != "" {
		where = append(where, "b.section = ?")
		args = append(args, filters.Section)
	}
	if filters.Type != "" {
		where = append(where, "b.type = ?")
		args = append(args, filters.Type)
	}
	if filters.Tag != "" {
		// Match the exact tag OR any descendant (hierarchical: "work" matches
		// "work/project"). IN-subquery keeps it one clause.
		where = append(where, "b.id IN (SELECT block_id FROM tags WHERE raw_path = ? OR raw_path LIKE ?)")
		args = append(args, filters.Tag, filters.Tag+"/%")
	}
	if filters.VaultOnly {
		// Scope: in-vault blocks only (exclude linked-notebook sources).
		where = append(where, "b.source = 'vault'")
	}
	orderClause := "bm25(blocks_fts) AS rank"
	order := "rank"
	if filters.Sort == "recency" {
		// file_date is the per-block date (YYYY-MM-DD); most-recent first, with
		// bm25 as the tiebreaker.
		orderClause = "b.file_date DESC, bm25(blocks_fts) AS rank"
		order = "b.file_date DESC, rank"
	}

	pageQuery := `
		SELECT b.id, b.parent_id, b.notebook, b.section, b.page, b.file_date, b.depth,
		       b.raw_content, b.clean_content, b.line_number,
		       COALESCE(t.status, ''), COALESCE(t.owner, ''), COALESCE(t.start_date, ''),
		       COALESCE(t.due_date, ''), COALESCE(t.priority, 0),
		       snippet(blocks_fts, 0, '<mark>', '</mark>', '...', 20),
		       ` + orderClause + `
		FROM blocks_fts
		JOIN blocks b ON b.rowid = blocks_fts.rowid
		LEFT JOIN tasks t ON b.id = t.block_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY ` + order + `
		LIMIT ?`
	args = append(args, searchFlatCap)
	rows, err := dm.db.Query(pageQuery, args...)
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
	if err := rows.Err(); err != nil {
		return parser.SearchResult{}, fmt.Errorf("failed iterating search results: %w", err)
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
