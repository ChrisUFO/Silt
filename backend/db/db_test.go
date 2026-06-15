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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, []string{"work/project"}); err != nil {
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
			RawText:    "remember to follow up on #work/project and #systems/specs <!-- id: 33333333-3333-3333-3333-333333333333 -->",
			CleanText:  "remember to follow up on",
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocksWithInlineTag, nil); err != nil {
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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", first, nil); err != nil {
		t.Fatalf("first IndexFileBlocks: %v", err)
	}

	second := []parser.ParsedBlock{
		sampleTaskBlock("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", 1),
		sampleNoteBlock("cccccccc-cccc-cccc-cccc-cccccccccccc", 2),
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", second, nil); err != nil {
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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", []parser.ParsedBlock{sampleTaskBlock("dddddddd-dddd-dddd-dddd-dddddddddddd", 1)}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Re-index with empty blocks should clear and commit successfully.
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", nil, nil); err != nil {
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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, []string{"cascade-tag"}); err != nil {
		t.Fatalf("index: %v", err)
	}

	if err := dm.ClearFileBlocks(nil, "Work", "Journal", "Daily"); err != nil {
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
			RawText:   "- [x] DONE TASK [Alice]#1 ship #work/project <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText: "ship",
			Status:    "DONE",
			Owner:     "Alice",
			Priority:  1,
			LineNumber: 1,
		},
		{
			ID:        "22222222-2222-2222-2222-222222222222",
			Type:      parser.BlockTask,
			RawText:   "- [/] DOING TASK [Bob]#2 fix #work/project <!-- id: 22222222-2222-2222-2222-222222222222 -->",
			CleanText: "fix",
			Status:    "DOING",
			Owner:     "Bob",
			Priority:  2,
			LineNumber: 1,
		},
		{
			ID:        "33333333-3333-3333-3333-333333333333",
			Type:      parser.BlockTask,
			RawText:   "- [ ] TODO TASK [Alice]#3 research #work/project <!-- id: 33333333-3333-3333-3333-333333333333 -->",
			CleanText: "research",
			Status:    "TODO",
			Owner:     "Alice",
			Priority:  3,
			LineNumber: 1,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, nil); err != nil {
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
			filter:   parser.TaskQueryFilter{Tags: []string{"work/project"}},
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

func TestExtractTags_DeduplicatesAndIgnoresNumeric(t *testing.T) {
	text := "Plan #work/project with #work/project and #1 priority"
	tags := ExtractTags(text)
	if len(tags) != 1 || tags[0] != "work/project" {
		t.Errorf("expected single deduped tag [work/project], got %v", tags)
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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, []string{"welcome", "tutorial"}); err != nil {
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
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", original, nil); err != nil {
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
	if err := dm.IndexFileBlocks("Personal", "Journal", "Daily", updated, nil); err != nil {
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
			RawText:   "- [x] DONE TASK [Alice] ship #work/project #release <!-- id: 11111111-1111-1111-1111-111111111111 -->",
			CleanText: "ship",
			Status:    "DONE",
			Owner:     "Alice",
			Priority:  1,
			LineNumber: 1,
		},
		{
			ID:        "22222222-2222-2222-2222-222222222222",
			Type:      parser.BlockTask,
			RawText:   "- [ ] TODO TASK [Bob] research #work/project <!-- id: 22222222-2222-2222-2222-222222222222 -->",
			CleanText: "research",
			Status:    "TODO",
			Owner:     "Bob",
			Priority:  3,
			LineNumber: 2,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, nil); err != nil {
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
		!contains(got, "work/project") || !contains(got, "release") {
		t.Errorf("expected ship task to have tags [work/project release], got %v", got)
	}
	if got := tagsByID["22222222-2222-2222-2222-222222222222"]; len(got) != 1 ||
		!contains(got, "work/project") {
		t.Errorf("expected research task to have tag [work/project], got %v", got)
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
			RawText:   "#work and #work/project beta <!-- id: bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->",
			CleanText: "and beta",
			Depth:     0,
			LineNumber: 2,
		},
		{
			ID:        "cccccccc-cccc-cccc-cccc-cccccccccccc",
			Type:      parser.BlockNote,
			RawText:   "#work/project/milestone-one gamma <!-- id: cccccccc-cccc-cccc-cccc-cccccccccccc -->",
			CleanText: "gamma",
			Depth:     0,
			LineNumber: 3,
		},
	}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", blocks, nil); err != nil {
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
		{"work/project", 2},               // B, C
		{"work/project/milestone-one", 1}, // C
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
	if err := dm.IndexFileBlocks("NB", "", "PG", blocks, nil); err != nil {
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

// --- Phase 4: FTS5 search (#39) ---

// indexSearchable seeds a DB with a handful of blocks across two pages so the
// FTS ranking/grouping/pagination tests have realistic data. Returns the dm.
func indexSearchable(t *testing.T) *DatabaseManager {
	t.Helper()
	dm := newTestDB(t)
	// Page A (Work/Journal/Daily) — three blocks, "sprint planning" appears
	// most in the first one so bm25 should rank it above the others.
	a1 := parser.ParsedBlock{ID: "a1a1a1a1-1111-1111-1111-111111111111", Type: parser.BlockNote, CleanText: "sprint planning notes for the sprint planning meeting", LineNumber: 1}
	a2 := parser.ParsedBlock{ID: "a2a2a2a2-2222-2222-2222-222222222222", Type: parser.BlockTask, CleanText: "sprint review prep", Status: "TODO", LineNumber: 2}
	a3 := parser.ParsedBlock{ID: "a3a3a3a3-3333-3333-3333-333333333333", Type: parser.BlockNote, CleanText: "a totally unrelated line about weather", LineNumber: 3}
	if err := dm.IndexFileBlocks("Work", "Journal", "Daily", []parser.ParsedBlock{a1, a2, a3}, []string{"work"}); err != nil {
		t.Fatalf("index page A: %v", err)
	}
	// Page B (Work/Journal/Retros) — one highly relevant block from a different
	// page, used to verify per-page grouping surfaces both pages.
	b1 := parser.ParsedBlock{ID: "b1b1b1b1-1111-1111-1111-111111111111", Type: parser.BlockNote, CleanText: "last sprint retrospective action items", LineNumber: 1}
	if err := dm.IndexFileBlocks("Work", "Journal", "Retros", []parser.ParsedBlock{b1}, []string{"work"}); err != nil {
		t.Fatalf("index page B: %v", err)
	}
	return dm
}

func TestSearch_FTS5SmokeAndSync(t *testing.T) {
	dm := indexSearchable(t)
	// The triggers must have kept blocks_fts in sync: a search for "sprint"
	// returns rows from both indexed pages without an explicit rebuild.
	res, err := dm.SearchBlocksPaged("sprint", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total == 0 {
		t.Fatal("FTS5 returned no results — triggers did not sync blocks_fts")
	}
}

func TestSearch_RankingPutsMostRelevantFirst(t *testing.T) {
	dm := indexSearchable(t)
	res, err := dm.SearchBlocksPaged("sprint planning", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("no results")
	}
	// a1 mentions both "sprint" and "planning" multiple times → highest bm25.
	if res.Results[0].ID != "a1a1a1a1-1111-1111-1111-111111111111" {
		t.Errorf("expected most-relevant block first, got %s (clean=%q)", res.Results[0].ID, res.Results[0].CleanContent)
	}
}

func TestSearch_SnippetContainsHighlightMarkers(t *testing.T) {
	dm := indexSearchable(t)
	res, err := dm.SearchBlocksPaged("weather", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if len(res.Results) == 0 {
		t.Fatal("no results for 'weather'")
	}
	if !strings.Contains(res.Results[0].Snippet, "<mark>") || !strings.Contains(res.Results[0].Snippet, "</mark>") {
		t.Errorf("snippet missing <mark> highlight: %q", res.Results[0].Snippet)
	}
	if !strings.Contains(res.Results[0].Snippet, "weather") {
		t.Errorf("snippet does not contain the matched term: %q", res.Results[0].Snippet)
	}
}

func TestSearch_MultiTermIsImplicitAND(t *testing.T) {
	dm := indexSearchable(t)
	// "sprint weather" must AND: only blocks containing BOTH survive. No
	// single block has both terms, so the result set is empty.
	res, err := dm.SearchBlocksPaged("sprint weather", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("AND of disjoint terms should return 0, got %d", res.Total)
	}
	// "sprint review" matches a2 only.
	res, err = dm.SearchBlocksPaged("sprint review", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total != 1 || res.Results[0].ID != "a2a2a2a2-2222-2222-2222-222222222222" {
		t.Errorf("expected single 'sprint review' hit on a2, got total=%d first=%s", res.Total, firstID(res.Results))
	}
}

func TestSearch_PerPageGroupingCapsResultsPerPage(t *testing.T) {
	// Index 5 same-page blocks all matching "alpha" so grouping must cap them.
	dm := newTestDB(t)
	var blocks []parser.ParsedBlock
	for i := 0; i < 5; i++ {
		blocks = append(blocks, parser.ParsedBlock{
			ID:        blockID("caf", i),
			Type:      parser.BlockNote,
			CleanText: "alpha beta gamma item number",
			LineNumber: i + 1,
		})
	}
	if err := dm.IndexFileBlocks("NB", "", "PG", blocks, nil); err != nil {
		t.Fatal(err)
	}
	res, err := dm.SearchBlocksPaged("alpha", 0, 50)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total > searchMaxPerGroup {
		t.Errorf("per-page grouping failed: total=%d, expected <= %d", res.Total, searchMaxPerGroup)
	}
}

func TestSearch_PaginationAndHasMore(t *testing.T) {
	dm := indexSearchable(t)
	// "sprint" matches a1, a2 (page A) + b1 (page B). After per-page grouping
	// (<=3/page) all three survive. Page size 2 → HasMore on the first page.
	page1, err := dm.SearchBlocksPaged("sprint", 0, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Results) != 2 {
		t.Errorf("page1 expected 2 results, got %d", len(page1.Results))
	}
	if !page1.HasMore {
		t.Error("page1 HasMore should be true when total > offset+limit")
	}
	if page1.Total < 3 {
		t.Errorf("total expected >=3, got %d", page1.Total)
	}
	page2, err := dm.SearchBlocksPaged("sprint", 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if page2.HasMore {
		t.Error("page2 HasMore should be false when offset+limit >= total")
	}
	// No overlap between pages.
	seen := map[string]bool{}
	for _, r := range page1.Results {
		seen[r.ID] = true
	}
	for _, r := range page2.Results {
		if seen[r.ID] {
			t.Errorf("block %s appeared on both pages", r.ID)
		}
	}
}

func TestSearch_EmptyQueryReturnsEmpty(t *testing.T) {
	dm := indexSearchable(t)
	res, err := dm.SearchBlocksPaged("   ", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total != 0 || len(res.Results) != 0 {
		t.Errorf("empty query should return no results, got total=%d len=%d", res.Total, len(res.Results))
	}
}

func TestSearch_TagHydrationSurvivesFTS(t *testing.T) {
	dm := newTestDB(t)
	b := parser.ParsedBlock{
		ID: "dddddddd-1111-1111-1111-111111111111", Type: parser.BlockTask,
		CleanText: "ship the release", Status: "TODO",
		RawText: "- [ ] TODO TASK ship the release #dev/release <!-- id: dddddddd-1111-1111-1111-111111111111 -->",
		LineNumber: 1,
	}
	if err := dm.IndexFileBlocks("Work", "", "Daily", []parser.ParsedBlock{b}, []string{"dev/release"}); err != nil {
		t.Fatal(err)
	}
	res, err := dm.SearchBlocksPaged("release", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	if len(res.Results[0].Tags) == 0 {
		t.Errorf("tag hydration broke post-FTS: %+v", res.Results[0])
	}
}

func TestSearch_RebuildFTSIndexRepairs(t *testing.T) {
	dm := indexSearchable(t)
	// A forced rebuild must not error and must preserve searchability.
	if err := dm.RebuildFTSIndex(); err != nil {
		t.Fatalf("RebuildFTSIndex: %v", err)
	}
	res, err := dm.SearchBlocksPaged("sprint", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged after rebuild: %v", err)
	}
	if res.Total == 0 {
		t.Error("rebuild lost all FTS data")
	}
}

func TestSearch_UpdateReplacesOldFTSContent(t *testing.T) {
	// Re-indexing a page (delete-then-insert) must replace the old FTS row,
	// not duplicate it. Search for the old term should drop to 0 after the
	// block's text changes.
	dm := newTestDB(t)
	orig := parser.ParsedBlock{ID: "eeeeeeee-1111-1111-1111-111111111111", Type: parser.BlockNote, CleanText: "needle in a haystack", LineNumber: 1}
	if err := dm.IndexFileBlocks("NB", "", "PG", []parser.ParsedBlock{orig}, nil); err != nil {
		t.Fatal(err)
	}
	if res, _ := dm.SearchBlocksPaged("needle", 0, 10); res.Total != 1 {
		t.Fatalf("pre-update: expected 1 needle, got %d", res.Total)
	}
	updated := parser.ParsedBlock{ID: orig.ID, Type: parser.BlockNote, CleanText: "the needle is gone now replaced by thread", LineNumber: 1}
	if err := dm.IndexFileBlocks("NB", "", "PG", []parser.ParsedBlock{updated}, nil); err != nil {
		t.Fatal(err)
	}
	if res, _ := dm.SearchBlocksPaged("needle", 0, 10); res.Total == 0 {
		// "needle" still present in the updated text, so this should still hit.
		t.Errorf("update lost the FTS row entirely")
	}
	// A term that was ONLY in the old text must no longer match.
	if res, _ := dm.SearchBlocksPaged("haystack", 0, 10); res.Total != 0 {
		t.Errorf("stale FTS row: 'haystack' still matches after update (total=%d)", res.Total)
	}
}

// blockID builds a deterministic UUID-shaped id from a 3-char prefix and an
// index, for the grouping test's batch of blocks.
func blockID(prefix string, i int) string {
	p := prefix + "00000000000000000000000000000" // pad
	p = p[:8] + "-1111-1111-1111-111111111111"
	// overwrite first chars with prefix to keep ids distinct
	b := []byte(p)
	for j := 0; j < len(prefix) && j < 8; j++ {
		b[j] = prefix[j]
	}
	b[7] = byte('a' + i%26)
	return string(b)
}

func firstID(rs []parser.TaskResult) string {
	if len(rs) == 0 {
		return ""
	}
	return rs[0].ID
}

// TestBuildFTSQuery_PreservesUnicode verifies the MATCH-query builder keeps
// Unicode word characters (CJK, accented Latin) so non-English search works
// against the unicode61-tokenized index. The previous ASCII-only filter
// stripped them, silently returning zero results for "café" / "中文".
func TestBuildFTSQuery_PreservesUnicode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"café", "café*"},
		{"über road", "über* road*"},
		{"中文 笔记", "中文* 笔记*"},
		{"кирриллица", "кирриллица*"},
		// FTS5 query-syntax chars (ASCII punctuation) are still stripped.
		{`"inject" OR (*)`, "inject* OR*"},
		// <2-char noise dropped; FTS5 syntax stripped even when ASCII.
		{"a * b", ""}, // "a" too short, "*" all-stripped, "b" too short
	}
	for _, c := range cases {
		got := buildFTSQuery(c.in)
		if got != c.want {
			t.Errorf("buildFTSQuery(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

// TestSearch_UnicodeContentMatches exercises the end-to-end path for the
// scripts the default unicode61 tokenizer handles as whole words: accented
// Latin (é is part of the word, tokenized as one token). This is exactly the
// case the old ASCII-only buildFTSQuery broke ("café" → "caf").
//
// NOTE: multi-character CJK search (e.g. "会議") needs a trigram/ICU
// tokenizer — unicode61 has no CJK word-segmentation dictionary and splits
// CJK into single-character tokens, so a 2-char query finds nothing. The
// buildFTSQuery fix preserves CJK in the query string (covered by
// TestBuildFTSQuery_PreservesUnicode); end-to-end CJK search is tracked as a
// follow-up tokenizer change, not a regression here.
func TestSearch_UnicodeContentMatches(t *testing.T) {
	dm := newTestDB(t)
	b := parser.ParsedBlock{
		ID: "ffffffff-1111-1111-1111-111111111111", Type: parser.BlockNote,
		CleanText: "résumé review for the café launch", LineNumber: 1,
	}
	if err := dm.IndexFileBlocks("NB", "", "PG", []parser.ParsedBlock{b}, nil); err != nil {
		t.Fatal(err)
	}
	// accented-Latin term that the old filter would have stripped to "caf"/"resum".
	res, err := dm.SearchBlocksPaged("café", 0, 10)
	if err != nil {
		t.Fatalf("SearchBlocksPaged: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("expected the accented-Latin block to match the accented query; got total=%d (old ASCII filter would have returned 0)", res.Total)
	}
}

