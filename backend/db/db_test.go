package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silt/backend/parser"
)

func newTestDB(t *testing.T) *DatabaseManager {
	t.Helper()
	dm, err := NewDatabaseManager("")
	if err != nil {
		t.Fatalf("failed to create DatabaseManager: %v", err)
	}
	t.Cleanup(func() {
		_ = dm.Close()
	})
	return dm
}

func sampleTaskBlock(id string, line int) parser.ParsedBlock {
	return parser.ParsedBlock{
		ID:         id,
		Type:       parser.BlockTask,
		Depth:      0,
		RawText:    "- [ ] TODO TASK [Alice] sample task <!-- id: " + id + " -->",
		CleanText:  "sample task",
		Status:     "TODO",
		Owner:      "Alice",
		StartDate:  "2026-06-01",
		DueDate:    "2026-06-15",
		Priority:   2,
		LineNumber: line,
	}
}

func sampleNoteBlock(id string, line int) parser.ParsedBlock {
	return parser.ParsedBlock{
		ID:         id,
		Type:       parser.BlockNote,
		Depth:      0,
		RawText:    "a note <!-- id: " + id + " -->",
		CleanText:  "a note",
		LineNumber: line,
	}
}

func TestIndexFileBlocks_InsertsBlocksTasksAndTags(t *testing.T) {
	dm := newTestDB(t)

	blocks := []parser.ParsedBlock{
		sampleTaskBlock("11111111-1111-1111-1111-111111111111", 1),
		sampleNoteBlock("22222222-2222-2222-2222-222222222222", 2),
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, []string{"work/sogav"}); err != nil {
		t.Fatalf("IndexFileBlocks failed: %v", err)
	}

	var blockCount int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&blockCount); err != nil {
		t.Fatalf("count blocks: %v", err)
	}
	if blockCount != 2 {
		t.Errorf("expected 2 blocks, got %d", blockCount)
	}

	var taskCount int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("expected 1 task row, got %d", taskCount)
	}

	var tagCount int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM tags").Scan(&tagCount); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	// The task raw text has no inline #tag, so only the frontmatter tag is indexed.
	if tagCount != 1 {
		t.Errorf("expected 1 tag row (frontmatter only), got %d", tagCount)
	}

	// Inline tags in the raw text should also be indexed.
	blocksWithInlineTag := []parser.ParsedBlock{
		{
			ID:         "33333333-3333-3333-3333-333333333333",
			Type:       parser.BlockNote,
			RawText:    "remember to follow up on #work/sogav and #systems/specs <!-- id: 33333333-3333-3333-3333-333333333333 -->",
			CleanText:  "remember to follow up on",
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-14", blocksWithInlineTag, nil); err != nil {
		t.Fatalf("index inline tags: %v", err)
	}
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM tags WHERE block_id = ?", "33333333-3333-3333-3333-333333333333").Scan(&tagCount); err != nil {
		t.Fatalf("count inline tags: %v", err)
	}
	if tagCount != 2 {
		t.Errorf("expected 2 inline tag rows, got %d", tagCount)
	}
}

func TestIndexFileBlocks_ReplacesExistingRows(t *testing.T) {
	dm := newTestDB(t)

	first := []parser.ParsedBlock{sampleTaskBlock("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 1)}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", first, nil); err != nil {
		t.Fatalf("first IndexFileBlocks: %v", err)
	}

	second := []parser.ParsedBlock{
		sampleTaskBlock("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", 1),
		sampleNoteBlock("cccccccc-cccc-cccc-cccc-cccccccccccc", 2),
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", second, nil); err != nil {
		t.Fatalf("second IndexFileBlocks: %v", err)
	}

	var count int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected blocks to be replaced (2 rows), got %d", count)
	}

	// Old task row should be gone (CASCADE).
	var oldTasks int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM tasks WHERE block_id = ?", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa").Scan(&oldTasks); err != nil {
		t.Fatalf("old task count: %v", err)
	}
	if oldTasks != 0 {
		t.Errorf("expected old task to be removed, got %d rows", oldTasks)
	}
}

func TestIndexFileBlocks_EmptyBlocksCommits(t *testing.T) {
	dm := newTestDB(t)

	// Seed with a block first.
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", []parser.ParsedBlock{sampleTaskBlock("dddddddd-dddd-dddd-dddd-dddddddddddd", 1)}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-index with empty blocks should clear and commit successfully.
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", nil, nil); err != nil {
		t.Fatalf("empty re-index: %v", err)
	}

	var count int
	if err := dm.db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected blocks to be cleared, got %d", count)
	}
}

func TestClearFileBlocks_CascadesToTasksAndTags(t *testing.T) {
	dm := newTestDB(t)

	blocks := []parser.ParsedBlock{sampleTaskBlock("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", 1)}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, []string{"cascade-tag"}); err != nil {
		t.Fatalf("index: %v", err)
	}

	if err := dm.ClearFileBlocks(nil, "Work", "Journal", "Daily", "2026-06-13"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	for _, table := range []string{"blocks", "tasks", "tags"} {
		var c int
		if err := dm.db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if c != 0 {
			t.Errorf("expected 0 rows in %s after cascade, got %d", table, c)
		}
	}
}

func TestQueryTasksWithFilters_FilterCombinations(t *testing.T) {
	dm := newTestDB(t)

	blocks := []parser.ParsedBlock{
		{
			ID:        "11111111-1111-1111-1111-111111111111",
			Type:      parser.BlockTask,
			RawText:   "- [x] DONE TASK [Alice]#1 ship #work/sogav <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText: "ship",
			Status:    "DONE",
			Owner:     "Alice",
			Priority:  1,
			LineNumber: 1,
		},
		{
			ID:        "22222222-2222-2222-2222-222222222222",
			Type:      parser.BlockTask,
			RawText:   "- [/] DOING TASK [Bob]#2 fix #work/sogav <!-- id: 22222222-2222-2222-2222-222222222222 -->",
			CleanText: "fix",
			Status:    "DOING",
			Owner:     "Bob",
			Priority:  2,
			LineNumber: 1,
		},
		{
			ID:        "33333333-3333-3333-3333-333333333333",
			Type:      parser.BlockTask,
			RawText:   "- [ ] TODO TASK [Alice]#3 research #work/sogav <!-- id: 33333333-3333-3333-3333-333333333333 -->",
			CleanText: "research",
			Status:    "TODO",
			Owner:     "Alice",
			Priority:  3,
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("index: %v", err)
	}

	tests := []struct {
		name     string
		filter   parser.TaskQueryFilter
		expected int
	}{
		{
			name:     "no filters returns all",
			filter:   parser.TaskQueryFilter{},
			expected: 3,
		},
		{
			name:     "filter by owner Alice",
			filter:   parser.TaskQueryFilter{Owner: "Alice"},
			expected: 2,
		},
		{
			name:     "filter by priority 2",
			filter:   parser.TaskQueryFilter{Priority: 2},
			expected: 1,
		},
		{
			name:     "filter by owner and priority",
			filter:   parser.TaskQueryFilter{Owner: "Alice", Priority: 1},
			expected: 1,
		},
		{
			name:     "filter by tag prefix",
			filter:   parser.TaskQueryFilter{Tags: []string{"work/sogav"}},
			expected: 3,
		},
		{
			name:     "filter excludes non-matching owner",
			filter:   parser.TaskQueryFilter{Owner: "nobody"},
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, err := dm.QueryTasksWithFilters(tc.filter)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if len(results) != tc.expected {
				t.Errorf("expected %d results, got %d", tc.expected, len(results))
			}
		})
	}
}

func TestFetchTimelineDays_GroupsByDateAndOrdersDesc(t *testing.T) {
	dm := newTestDB(t)

	// Two days with multiple blocks each, plus an unrelated section to verify filtering.
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", []parser.ParsedBlock{
		sampleTaskBlock("11111111-1111-1111-1111-111111111111", 1),
		sampleNoteBlock("22222222-2222-2222-2222-222222222222", 2),
	}, nil); err != nil {
		t.Fatalf("index day1: %v", err)
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-12", []parser.ParsedBlock{
		sampleNoteBlock("33333333-3333-3333-3333-333333333333", 1),
	}, nil); err != nil {
		t.Fatalf("index day2: %v", err)
	}
	if err := dm.IndexFileBlocks("Work", "Other", "Daily", "2026-06-13", []parser.ParsedBlock{
		sampleTaskBlock("44444444-4444-4444-4444-444444444444", 1),
	}, nil); err != nil {
		t.Fatalf("index other section: %v", err)
	}

	groups, err := dm.FetchTimelineDays("Work", "Journal", "Daily", 10, 0)
	if err != nil {
		t.Fatalf("FetchTimelineDays: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected 2 day groups, got %d", len(groups))
	}
	if groups[0].Date != "2026-06-13" {
		t.Errorf("expected most recent date first, got %q", groups[0].Date)
	}
	if groups[1].Date != "2026-06-12" {
		t.Errorf("expected second date 2026-06-12, got %q", groups[1].Date)
	}
	if len(groups[0].Blocks) != 2 {
		t.Errorf("expected 2 blocks on 2026-06-13, got %d", len(groups[0].Blocks))
	}
	if len(groups[1].Blocks) != 1 {
		t.Errorf("expected 1 block on 2026-06-12, got %d", len(groups[1].Blocks))
	}
	if groups[0].FormattedDate == "" {
		t.Errorf("expected formatted date to be populated")
	}
}

func TestFetchTimelineDays_PaginationAndEmpty(t *testing.T) {
	dm := newTestDB(t)

	// Empty case.
	groups, err := dm.FetchTimelineDays("Work", "Journal", "Daily", 10, 0)
	if err != nil {
		t.Fatalf("empty FetchTimelineDays: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("expected 0 groups for empty section, got %d", len(groups))
	}

	// Seed 3 distinct dates.
	for i, d := range []string{"2026-06-13", "2026-06-12", "2026-06-11"} {
		block := sampleNoteBlock("00000000-0000-0000-0000-00000000000"+string(rune('1'+i)), i+1)
		if err := dm.IndexFileBlocks("Work", "Journal", "Daily", d, []parser.ParsedBlock{block}, nil); err != nil {
			t.Fatalf("index %s: %v", d, err)
		}
	}

	// First page: limit 2, offset 0.
	first, err := dm.FetchTimelineDays("Work", "Journal", "Daily", 2, 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 groups on first page, got %d", len(first))
	}
	if first[0].Date != "2026-06-13" || first[1].Date != "2026-06-12" {
		t.Errorf("unexpected date order on first page: %s, %s", first[0].Date, first[1].Date)
	}

	// Second page: limit 2, offset 2.
	second, err := dm.FetchTimelineDays("Work", "Journal", "Daily", 2, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 1 {
		t.Fatalf("expected 1 group on second page, got %d", len(second))
	}
	if second[0].Date != "2026-06-11" {
		t.Errorf("expected third page date 2026-06-11, got %q", second[0].Date)
	}
}

func TestExtractTags_DeduplicatesAndIgnoresNumeric(t *testing.T) {
	text := "Plan #work/sogav with #work/sogav and #1 priority"
	tags := ExtractTags(text)
	if len(tags) != 1 || tags[0] != "work/sogav" {
		t.Errorf("expected single deduped tag [work/sogav], got %v", tags)
	}
}

func TestSQLDB_ExposesUnderlyingHandle(t *testing.T) {
	dm := newTestDB(t)
	if dm.SQLDB() == nil {
		t.Fatalf("expected SQLDB to return non-nil handle")
	}
}

func TestIndexFileBlocks_AttachesFrontmatterTagsByLoopIndex(t *testing.T) {
	// Reproduces the welcome-note case: frontmatter pushes the first block
	// off line 1, so the old `block.LineNumber == 1` check never fired.
	dm := newTestDB(t)

	blocks := []parser.ParsedBlock{
		// Mimic a file with frontmatter: first block sits on line 6.
		{
			ID:         "11111111-1111-1111-1111-111111111111",
			Type:       parser.BlockHeader,
			RawText:    "# Welcome <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText:  "Welcome",
			LineNumber: 6,
		},
		{
			ID:         "22222222-2222-2222-2222-222222222222",
			Type:       parser.BlockTask,
			RawText:    "- [ ] TODO TASK sample <!-- id: 22222222-2222-2222-2222-222222222222 -->",
			CleanText:  "sample",
			Status:     "TODO",
			LineNumber: 7,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, []string{"welcome", "tutorial"}); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	// Frontmatter tags should attach to the FIRST block (the header), not
	// to all blocks, and not be silently dropped due to the line-number check.
	for _, wantID := range []string{"11111111-1111-1111-1111-111111111111"} {
		var got int
		if err := dm.db.QueryRow(
			"SELECT COUNT(*) FROM tags WHERE block_id = ? AND raw_path IN ('welcome','tutorial')", wantID,
		).Scan(&got); err != nil {
			t.Fatalf("count tags for %s: %v", wantID, err)
		}
		if got != 2 {
			t.Errorf("expected first block %s to have 2 frontmatter tags, got %d", wantID, got)
		}
	}

	// Second block should not have those tags attached.
	var got int
	if err := dm.db.QueryRow(
		"SELECT COUNT(*) FROM tags WHERE block_id = ?", "22222222-2222-2222-2222-222222222222",
	).Scan(&got); err != nil {
		t.Fatalf("count tags for second block: %v", err)
	}
	if got != 0 {
		t.Errorf("expected second block to have no tags, got %d", got)
	}
}

func TestIndexFileBlocks_ReindexAfterFrontmatterMetadataChange(t *testing.T) {
	// When a file's frontmatter metadata (notebook/section/date) changes,
	// re-indexing must not leave stale rows under the OLD metadata key.
	// Block IDs are stable, so the new rows must end up at the new metadata.
	dm := newTestDB(t)

	original := []parser.ParsedBlock{
		{
			ID:         "11111111-1111-1111-1111-111111111111",
			Type:       parser.BlockTask,
			RawText:    "- [ ] TODO TASK ship <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText:  "ship",
			Status:     "TODO",
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", original, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	// Re-index with the same block ID but under a different notebook/section.
	updated := []parser.ParsedBlock{
		{
			ID:         "11111111-1111-1111-1111-111111111111",
			Type:       parser.BlockTask,
			RawText:    "- [/] DOING TASK ship <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText:  "ship",
			Status:     "DOING",
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Personal", "Journal", "Daily", "2026-06-13", updated, nil); err != nil {
		t.Fatalf("re-index with new metadata: %v", err)
	}

	// Old metadata key should have zero rows.
	var oldRows int
	if err := dm.db.QueryRow(
		"SELECT COUNT(*) FROM blocks WHERE notebook = ? AND section = ?",
		"Work", "Journal",
	).Scan(&oldRows); err != nil {
		t.Fatalf("count old metadata: %v", err)
	}
	if oldRows != 0 {
		t.Errorf("expected 0 rows under old metadata, got %d", oldRows)
	}

	// New metadata key should have exactly one row, with the updated status.
	var newStatus string
	if err := dm.db.QueryRow(
		"SELECT t.status FROM blocks b JOIN tasks t ON b.id = t.block_id WHERE b.notebook = ? AND b.section = ? AND b.id = ?",
		"Personal", "Journal", "11111111-1111-1111-1111-111111111111",
	).Scan(&newStatus); err != nil {
		t.Fatalf("lookup new metadata: %v", err)
	}
	if newStatus != "DOING" {
		t.Errorf("expected status DOING under new metadata, got %q", newStatus)
	}
}

func TestQueryTasksWithFilters_PopulatesTags(t *testing.T) {
	// Verifies the N+1 fix: tags should be hydrated on the returned
	// TaskResult slice without an extra query per row.
	dm := newTestDB(t)

	blocks := []parser.ParsedBlock{
		{
			ID:        "11111111-1111-1111-1111-111111111111",
			Type:      parser.BlockTask,
			RawText:   "- [x] DONE TASK [Alice] ship #work/sogav #release <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText: "ship",
			Status:    "DONE",
			Owner:     "Alice",
			Priority:  1,
			LineNumber: 1,
		},
		{
			ID:        "22222222-2222-2222-2222-222222222222",
			Type:      parser.BlockTask,
			RawText:   "- [ ] TODO TASK [Bob] research #work/sogav <!-- id: 22222222-2222-2222-2222-222222222222 -->",
			CleanText: "research",
			Status:    "TODO",
			Owner:     "Bob",
			Priority:  3,
			LineNumber: 2,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("index: %v", err)
	}

	results, err := dm.QueryTasksWithFilters(parser.TaskQueryFilter{})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	tagsByID := map[string][]string{}
	for _, r := range results {
		tagsByID[r.ID] = r.Tags
	}
	if got := tagsByID["11111111-1111-1111-1111-111111111111"]; len(got) != 2 ||
		!contains(got, "work/sogav") || !contains(got, "release") {
		t.Errorf("expected ship task to have tags [work/sogav release], got %v", got)
	}
	if got := tagsByID["22222222-2222-2222-2222-222222222222"]; len(got) != 1 ||
		!contains(got, "work/sogav") {
		t.Errorf("expected research task to have tag [work/sogav], got %v", got)
	}
}

func contains(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}

func TestIndexScanResults_ReturnsSkippedFiles(t *testing.T) {
	// Files that the scanner reports as errored must appear in the skip
	// slice returned alongside the count. The caller uses this to notify
	// the user which notes are not indexed.
	dm := newTestDB(t)

	results := []parser.ScanResult{
		{
			Path:     "ok.md",
			Notebook: "Work",
			Section:  "Journal",
			Page:     "Daily",
			Date:     "2026-06-13",
			Blocks:   []parser.ParsedBlock{sampleNoteBlock("11111111-1111-1111-1111-111111111111", 1)},
		},
		{
			Path: "bad.md",
			Err:  errors.New("simulated parse failure"),
		},
	}

	count, skipped, err := dm.IndexScanResults(results)
	if err != nil {
		t.Fatalf("IndexScanResults: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 indexed file, got %d", count)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped file, got %d", len(skipped))
	}
	if !strings.Contains(skipped[0], "bad.md") || !strings.Contains(skipped[0], "simulated parse failure") {
		t.Errorf("expected skip message to mention bad.md and the error, got: %s", skipped[0])
	}
}

func TestQueryTagHierarchy_DistinctCountsAtOrBeneath(t *testing.T) {
	dm := newTestDB(t)

	// Three blocks, indexed in a single file so they all share the
	// (notebook, section, page, file_date) tuple and are not wiped by
	// the per-file replace in IndexFileBlocks.
	//
	// Block A: tagged with the parent path only.
	// Block B: tagged with both the parent path and a child path — this
	//   is the case that previously double-counted: distinct-path
	//   counts summed to 2 for #work even though there is only one
	//   block B reachable at-or-beneath #work.
	// Block C: tagged with a child of a child of the parent.
	blocks := []parser.ParsedBlock{
		{
			ID:        "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			Type:      parser.BlockNote,
			RawText:   "#work alpha <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->",
			CleanText: "alpha",
			Depth:     0,
			LineNumber: 1,
		},
		{
			ID:        "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
			Type:      parser.BlockNote,
			RawText:   "#work and #work/sogav beta <!-- id: bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->",
			CleanText: "and beta",
			Depth:     0,
			LineNumber: 2,
		},
		{
			ID:        "cccccccc-cccc-cccc-cccc-cccccccccccc",
			Type:      parser.BlockNote,
			RawText:   "#work/sogav/milestone-one gamma <!-- id: cccccccc-cccc-cccc-cccc-cccccccccccc -->",
			CleanText: "gamma",
			Depth:     0,
			LineNumber: 3,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("index: %v", err)
	}

	tree, err := dm.QueryTagHierarchy()
	if err != nil {
		t.Fatalf("QueryTagHierarchy: %v", err)
	}

	find := func(path string) *parser.TagNode {
		var walk func([]parser.TagNode) *parser.TagNode
		walk = func(nodes []parser.TagNode) *parser.TagNode {
			for i := range nodes {
				if nodes[i].Path == path {
					return &nodes[i]
				}
				if found := walk(nodes[i].Children); found != nil {
					return found
				}
			}
			return nil
		}
		return walk(tree)
	}

	cases := []struct {
		path string
		want int
	}{
		{"work", 3},                     // A, B, C all reachable at or beneath
		{"work/sogav", 2},               // B, C
		{"work/sogav/milestone-one", 1}, // C
	}
	for _, c := range cases {
		got := find(c.path)
		if got == nil {
			t.Errorf("path %q missing from hierarchy", c.path)
			continue
		}
		if got.Count != c.want {
			t.Errorf("path %q: got count %d, want %d", c.path, got.Count, c.want)
		}
	}
}

// --- Phase 3: persistent on-disk WAL index + incremental re-indexing (#29) ---

// newOnDiskDB opens a DatabaseManager against a fresh on-disk path in the
// test's temp dir. Returns the manager and the .sqlite path. Cleanup closes
// the manager and removes the index files.
func newOnDiskDB(t *testing.T) (*DatabaseManager, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.sqlite")
	dm, err := NewDatabaseManager(dbPath)
	if err != nil {
		t.Fatalf("NewDatabaseManager(on-disk): %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })
	if !dm.IsOnDisk() {
		t.Fatalf("expected on-disk DB, got in-memory")
	}
	return dm, dbPath
}

func TestFilesTable_ColdStartPopulatesAndWarmStartSkips(t *testing.T) {
	dm, dbPath := newOnDiskDB(t)

	path := "/vault/Work/Journal/Daily/2026-06-14.md"
	mtime := time.Now().UnixNano()
	const size int64 = 4096

	// Cold start: file never indexed → unchanged=false.
	unchanged, err := dm.IsFileUnchanged(path, mtime, size)
	if err != nil {
		t.Fatalf("IsFileUnchanged cold: %v", err)
	}
	if unchanged {
		t.Fatal("cold start should report file as changed")
	}

	// Simulate a successful index: mark it.
	if err := dm.MarkFileIndexed(nil, path, mtime, size); err != nil {
		t.Fatalf("MarkFileIndexed: %v", err)
	}

	// Warm restart (same mtime+size): unchanged=true → skip.
	unchanged, err = dm.IsFileUnchanged(path, mtime, size)
	if err != nil {
		t.Fatalf("IsFileUnchanged warm: %v", err)
	}
	if !unchanged {
		t.Fatal("warm start with identical mtime+size should report unchanged")
	}

	// Close + reopen the on-disk DB to prove the files table persists.
	if err := dm.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	dm2, err := NewDatabaseManager(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer dm2.Close()

	unchanged, err = dm2.IsFileUnchanged(path, mtime, size)
	if err != nil {
		t.Fatalf("IsFileUnchanged after reopen: %v", err)
	}
	if !unchanged {
		t.Fatal("files table did not persist across restart — warm start broke")
	}

	// Modified file (mtime bumped): unchanged=false → reindex.
	unchanged, err = dm2.IsFileUnchanged(path, mtime+1, size)
	if err != nil {
		t.Fatalf("IsFileUnchanged modified: %v", err)
	}
	if unchanged {
		t.Fatal("changed mtime should report file as changed")
	}
	// Size change alone also triggers reindex.
	unchanged, err = dm2.IsFileUnchanged(path, mtime, size+1)
	if err != nil {
		t.Fatalf("IsFileUnchanged size: %v", err)
	}
	if unchanged {
		t.Fatal("changed size should report file as changed")
	}
}

func TestPruneStaleFiles_DropsRenamedAndDeletedPaths(t *testing.T) {
	dm, _ := newOnDiskDB(t)

	old := "/vault/Work/Journal/Daily/2026-06-13.md"
	keep := "/vault/Work/Journal/Daily/2026-06-14.md"
	now := time.Now().UnixNano()
	if err := dm.MarkFileIndexed(nil, old, now, 100); err != nil {
		t.Fatal(err)
	}
	if err := dm.MarkFileIndexed(nil, keep, now, 200); err != nil {
		t.Fatal(err)
	}

	// Simulate a scan that only sees `keep` (old was renamed/deleted).
	pruned, err := dm.PruneStaleFiles([]string{keep})
	if err != nil {
		t.Fatalf("PruneStaleFiles: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != old {
		t.Fatalf("expected prune [old], got %v", pruned)
	}

	known, err := dm.KnownFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := known[old]; exists {
		t.Error("old path was not pruned from files table")
	}
	if _, exists := known[keep]; !exists {
		t.Error("keep path was incorrectly pruned")
	}
}

func TestPruneStaleFiles_EmptyScanClearsAll(t *testing.T) {
	dm, _ := newOnDiskDB(t)
	if err := dm.MarkFileIndexed(nil, "/a.md", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := dm.MarkFileIndexed(nil, "/b.md", 2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := dm.PruneStaleFiles(nil); err != nil {
		t.Fatalf("PruneStaleFiles(nil): %v", err)
	}
	known, err := dm.KnownFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 0 {
		t.Fatalf("expected empty files table after pruning all, got %d rows", len(known))
	}
}

func TestOnDiskWAL_CreatesWALFiles(t *testing.T) {
	dm, dbPath := newOnDiskDB(t)

	// A write should create the WAL sidecar files (WAL mode is persistent).
	if err := dm.MarkFileIndexed(nil, "/x.md", time.Now().UnixNano(), 10); err != nil {
		t.Fatal(err)
	}
	walPath := dbPath + "-wal"
	if _, err := os.Stat(walPath); err != nil {
		t.Errorf("expected WAL file at %s after write, got: %v", walPath, err)
	}
}

func TestOnDiskWAL_CheckpointOnCloseCollapsesWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.sqlite")
	dm, err := NewDatabaseManager(dbPath)
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	for i := 0; i < 50; i++ {
		if err := dm.MarkFileIndexed(nil, "/f"+string(rune('a'+i%26))+".md", int64(i), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Close runs PRAGMA wal_checkpoint(TRUNCATE).
	if err := dm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("main DB file missing after close: %v", err)
	}
	if info.Size() == 0 {
		t.Error("main DB file is empty — checkpoint did not merge WAL into the main file")
	}
	walInfo, walErr := os.Stat(dbPath + "-wal")
	if walErr == nil && walInfo.Size() > 0 {
		// After TRUNCATE checkpoint the wal file should be empty (0 bytes);
		// a non-empty wal means checkpoint did not run.
		t.Errorf("WAL file is %d bytes after close+checkpoint; expected 0 or absent", walInfo.Size())
	}
}

func TestOnDiskWAL_DeleteIndexForcesCleanRebuild(t *testing.T) {
	dm, dbPath := newOnDiskDB(t)
	path := "/vault/nb/pg/2026-06-14.md"
	mtime := time.Now().UnixNano()
	if err := dm.MarkFileIndexed(nil, path, mtime, 512); err != nil {
		t.Fatal(err)
	}
	if err := dm.Close(); err != nil {
		t.Fatal(err)
	}

	// Delete the 3 index files (the documented recovery path).
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", suffix, err)
		}
	}

	// Reopen: should be a clean DB with no files table data.
	dm2, err := NewDatabaseManager(dbPath)
	if err != nil {
		t.Fatalf("reopen after delete: %v", err)
	}
	defer dm2.Close()
	unchanged, err := dm2.IsFileUnchanged(path, mtime, 512)
	if err != nil {
		t.Fatalf("IsFileUnchanged: %v", err)
	}
	if unchanged {
		t.Error("deleted index should rebuild from scratch — file should not be 'unchanged'")
	}
	known, err := dm2.KnownFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 0 {
		t.Errorf("expected empty files table after rebuild, got %d rows", len(known))
	}
}

// TestPluginRODB_ReadsOnDiskIndex confirms a second read-only connection to
// the on-disk file sees data the main connection wrote (WAL multi-connection
// visibility). This is the property PluginRawQuery depends on.
func TestPluginRODB_ReadsOnDiskIndex(t *testing.T) {
	dm, dbPath := newOnDiskDB(t)
	blocks := []parser.ParsedBlock{sampleTaskBlock("aaaaaaaa-1111-1111-1111-111111111111", 1)}
	if err := dm.IndexFileBlocks("NB", "", "PG", "2026-06-14", blocks, nil); err != nil {
		t.Fatal(err)
	}

	// Open a second read-only handle the way openPluginRODB does.
	ro, err := openRawReadonly(t, dbPath)
	if err != nil {
		t.Fatalf("open readonly: %v", err)
	}
	defer ro.Close()

	var got int
	if err := ro.QueryRow("SELECT count(*) FROM blocks").Scan(&got); err != nil {
		t.Fatalf("readonly count: %v", err)
	}
	if got != 1 {
		t.Errorf("read-only handle saw %d blocks, expected 1 (WAL visibility failed)", got)
	}
	// query_only must reject writes.
	if _, err := ro.Exec("DELETE FROM blocks"); err == nil {
		t.Error("read-only handle accepted a write — query_only is not enforced")
	}
}

// openRawReadonly opens a second *sql.DB handle to dbPath with query_only=ON,
// mirroring what app.openPluginRODB does for the plugin SDK. Used to verify a
// read-only connection sees WAL-committed data and cannot write.
func openRawReadonly(t *testing.T, dbPath string) (*sql.DB, error) {
	t.Helper()
	ro, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	ro.SetMaxOpenConns(1)
	if _, err := ro.Exec("PRAGMA query_only = ON"); err != nil {
		ro.Close()
		return nil, err
	}
	return ro, nil
}

// BenchmarkWarmStart_5000Files measures the cost of the warm-restart diff
// loop (IsFileUnchanged per file) against a pre-seeded 5k-row files table.
// This is the new hot path added by #29; it must stay cheap so a warm restart
// of a thousands-of-pages vault remains fast.
func BenchmarkWarmStart_5000Files(b *testing.B) {
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "index.sqlite")
	dm, err := NewDatabaseManager(dbPath)
	if err != nil {
		b.Fatalf("NewDatabaseManager: %v", err)
	}
	defer dm.Close()

	const n = 5000
	now := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		p := "/vault/nb" + itoa(i) + "/pg/file.md"
		if err := dm.MarkFileIndexed(nil, p, now+int64(i), int64(i)); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	// Snapshot the seeded stats so the benchmark loop can re-query them
	// (simulating ScanWorkspace handing the same mtime/size back).
	known, err := dm.KnownFiles()
	if err != nil {
		b.Fatalf("KnownFiles: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		for p, fs := range known {
			if _, err := dm.IsFileUnchanged(p, fs.MTime, fs.Size); err != nil {
				b.Fatalf("IsFileUnchanged: %v", err)
			}
		}
	}
}

// itoa is a tiny allocation-free int→string to keep the benchmark seed loop
// cheap (fmt.Sprintf would dominate the seed time and skew nothing here, but
// this avoids pulling fmt into the hot seed path).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

