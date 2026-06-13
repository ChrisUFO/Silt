package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notes-sharp/backend/core"
	"notes-sharp/backend/db"
	"notes-sharp/backend/monitor"
	"notes-sharp/backend/parser"
	"notes-sharp/backend/vault"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	vaultPath := t.TempDir()

	if err := vault.ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}

	dm, err := db.NewDatabaseManager()
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })

	coord := core.NewExecutionCoordinator(dm.SQLDB())
	tracker := monitor.NewWriteTracker()

	return &App{
		ctx:          context.Background(),
		db:           dm,
		coordinator:  coord,
		tracker:      tracker,
		vaultPath:    vaultPath,
		spacesPerTab: 4,
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestUpdateBlockState_TransitionsTaskStatus(t *testing.T) {
	app := newTestApp(t)

	notebook := "Work"
	section := "Journal"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, fileDate+".md")
	content := "# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"- [ ] TODO TASK [Alice] ship <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [/] DOING TASK [Bob] research <!-- id: 33333333-3333-3333-3333-333333333333 -->\n" +
		"- [x] DONE TASK [Carol] done <!-- id: 44444444-4444-4444-4444-444444444444 -->\n"
	writeFile(t, filePath, content)

	// Index the file so the DB has block metadata.
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Date, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	// TODO -> DOING
	if err := app.UpdateBlockState("22222222-2222-2222-2222-222222222222", "DOING"); err != nil {
		t.Fatalf("UpdateBlockState TODO->DOING: %v", err)
	}
	updated, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read after write: %v", err)
	}
	if !strings.Contains(string(updated), "- [/] DOING TASK [Alice] ship") {
		t.Errorf("expected line updated to DOING, got:\n%s", updated)
	}

	// DOING -> DONE
	if err := app.UpdateBlockState("22222222-2222-2222-2222-222222222222", "DONE"); err != nil {
		t.Fatalf("UpdateBlockState DOING->DONE: %v", err)
	}
	updated, _ = os.ReadFile(filePath)
	if !strings.Contains(string(updated), "- [x] DONE TASK [Alice] ship") {
		t.Errorf("expected line updated to DONE, got:\n%s", updated)
	}

	// DONE -> TODO
	if err := app.UpdateBlockState("22222222-2222-2222-2222-222222222222", "TODO"); err != nil {
		t.Fatalf("UpdateBlockState DONE->TODO: %v", err)
	}
	updated, _ = os.ReadFile(filePath)
	if !strings.Contains(string(updated), "- [ ] TODO TASK [Alice] ship") {
		t.Errorf("expected line reverted to TODO, got:\n%s", updated)
	}
}

func TestUpdateBlockState_RejectsTraversalMetadata(t *testing.T) {
	// Inject a block with malicious frontmatter-derived metadata directly
	// into the DB. UpdateBlockState must sanitize the path before touching
	// the filesystem and reject anything escaping the vault.
	app := newTestApp(t)

	blocks := []parser.ParsedBlock{
		{
			ID:         "55555555-5555-5555-5555-555555555555",
			Type:       parser.BlockTask,
			RawText:    "- [ ] TODO TASK evil <!-- id: 55555555-5555-5555-5555-555555555555 -->",
			CleanText:  "evil",
			Status:     "TODO",
			LineNumber: 1,
		},
	}
	if err := app.db.IndexFileBlocks("../../..", "etc", "passwd", blocks, nil); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	err := app.UpdateBlockState("55555555-5555-5555-5555-555555555555", "DOING")
	if err == nil {
		t.Fatalf("expected UpdateBlockState to reject traversal metadata")
	}
	if !strings.Contains(err.Error(), "invalid file metadata") && !strings.Contains(err.Error(), "escapes vault") {
		t.Errorf("expected path-sanitization error, got: %v", err)
	}
}

func TestUpdateBlockState_RejectsNonTaskBlock(t *testing.T) {
	app := newTestApp(t)
	notebook := "Work"
	section := "Journal"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, fileDate+".md")
	content := "# Header <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeFile(t, filePath, content)

	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Date, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	if err := app.UpdateBlockState("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "DOING"); err == nil {
		t.Errorf("expected UpdateBlockState to reject a non-task block")
	}
}

func TestFetchSectionTimeline_GroupsAndPaginates(t *testing.T) {
	app := newTestApp(t)

	for _, d := range []string{"2026-06-13", "2026-06-12", "2026-06-11"} {
		blocks := []parser.ParsedBlock{
			{
				ID:         "block-" + d,
				Type:       parser.BlockNote,
				RawText:    "note for " + d + " <!-- id: block-" + d + " -->",
				CleanText:  "note for " + d,
				LineNumber: 1,
			},
		}
		if err := app.db.IndexFileBlocks("Work", "Journal", d, blocks, nil); err != nil {
			t.Fatalf("IndexFileBlocks %s: %v", d, err)
		}
	}

	// First page.
	page1, err := app.FetchSectionTimeline("Work", "Journal", 0, 2)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2 day groups, got %d", len(page1))
	}
	if page1[0].Date != "2026-06-13" || page1[1].Date != "2026-06-12" {
		t.Errorf("unexpected date order on page1: %s, %s", page1[0].Date, page1[1].Date)
	}

	// Second page.
	page2, err := app.FetchSectionTimeline("Work", "Journal", 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Date != "2026-06-11" {
		t.Errorf("expected page2 to have only 2026-06-11, got %+v", page2)
	}

	// Empty section.
	empty, err := app.FetchSectionTimeline("Work", "Missing", 0, 5)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 groups for missing section, got %d", len(empty))
	}
}

func TestQueryTasks_FiltersByOwnerAndPriority(t *testing.T) {
	app := newTestApp(t)

	blocks := []parser.ParsedBlock{
		{
			ID:        "t1",
			Type:      parser.BlockTask,
			RawText:   "- [x] DONE TASK [Alice] ship #work/sogav <!-- id: t1 -->",
			CleanText: "ship",
			Status:    "DONE",
			Owner:     "Alice",
			Priority:  1,
			LineNumber: 1,
		},
		{
			ID:        "t2",
			Type:      parser.BlockTask,
			RawText:   "- [/] DOING TASK [Bob] fix <!-- id: t2 -->",
			CleanText: "fix",
			Status:    "DOING",
			Owner:     "Bob",
			Priority:  2,
			LineNumber: 2,
		},
		{
			ID:        "t3",
			Type:      parser.BlockTask,
			RawText:   "- [ ] TODO TASK [Alice] research <!-- id: t3 -->",
			CleanText: "research",
			Status:    "TODO",
			Owner:     "Alice",
			Priority:  3,
			LineNumber: 3,
		},
	}
	if err := app.db.IndexFileBlocks("Work", "Journal", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	all, err := app.QueryTasks(parser.TaskQueryFilter{})
	if err != nil {
		t.Fatalf("QueryTasks all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(all))
	}

	aliceOnly, err := app.QueryTasks(parser.TaskQueryFilter{Owner: "Alice"})
	if err != nil {
		t.Fatalf("QueryTasks alice: %v", err)
	}
	if len(aliceOnly) != 2 {
		t.Errorf("expected 2 tasks for Alice, got %d", len(aliceOnly))
	}
	for _, r := range aliceOnly {
		if r.Owner != "Alice" {
			t.Errorf("expected Owner=Alice, got %q", r.Owner)
		}
	}

	tagged, err := app.QueryTasks(parser.TaskQueryFilter{Tags: []string{"work/sogav"}})
	if err != nil {
		t.Fatalf("QueryTasks tag: %v", err)
	}
	if len(tagged) != 1 || tagged[0].ID != "t1" {
		t.Errorf("expected 1 tagged task (t1), got %+v", tagged)
	}
	if len(tagged) > 0 && len(tagged[0].Tags) == 0 {
		t.Errorf("expected tag hydration on result, got empty Tags")
	}
}
