package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silt/backend/core"
	"silt/backend/db"
	"silt/backend/monitor"
	"silt/backend/parser"
	"silt/backend/vault"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	vaultPath := t.TempDir()

	if err := vault.ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}

	dm, err := db.NewDatabaseManager("")
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })

	coord := core.NewExecutionCoordinator(dm.SQLDB())
	tracker := monitor.NewWriteTracker()

	return &App{
		// ctx intentionally nil: tests have no Wails lifecycle context, so
		// block:changed event emission is skipped (see emitBlockChanged).
		db:          dm,
		coordinator: coord,
		tracker:     tracker,
		vaultPath:   vaultPath,
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
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"- [ ] TODO TASK [Alice] ship <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [/] DOING TASK [Bob] research <!-- id: 33333333-3333-3333-3333-333333333333 -->\n" +
		"- [x] DONE TASK [Carol] done <!-- id: 44444444-4444-4444-4444-444444444444 -->\n"
	writeFile(t, filePath, content)

	// Index the file so the DB has block metadata.
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, blocks, meta.Tags); err != nil {
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
	if err := app.db.IndexFileBlocks("../../..", "etc", "passwd", "2026-01-01", blocks, nil); err != nil {
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
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "# Header <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeFile(t, filePath, content)

	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	if err := app.UpdateBlockState("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "DOING"); err == nil {
		t.Errorf("expected UpdateBlockState to reject a non-task block")
	}
}

func TestUpdateBlockState_RejectsInvalidStatus(t *testing.T) {
	app := newTestApp(t)

	err := app.UpdateBlockState("any-block-id", "INVALID")
	if err == nil {
		t.Fatalf("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid target status") {
		t.Errorf("expected error to mention 'invalid target status', got: %v", err)
	}
}

func TestFetchPageTimeline_GroupsAndPaginates(t *testing.T) {
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
		if err := app.db.IndexFileBlocks("Work", "Journal", "Daily", d, blocks, nil); err != nil {
			t.Fatalf("IndexFileBlocks %s: %v", d, err)
		}
	}

	// First page.
	page1, err := app.FetchPageTimeline("Work", "Journal", "Daily", 0, 2)
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
	page2, err := app.FetchPageTimeline("Work", "Journal", "Daily", 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 1 || page2[0].Date != "2026-06-11" {
		t.Errorf("expected page2 to have only 2026-06-11, got %+v", page2)
	}

	// Empty section.
	empty, err := app.FetchPageTimeline("Work", "Missing", "Daily", 0, 5)
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
			ID:         "t1",
			Type:       parser.BlockTask,
			RawText:    "- [x] DONE TASK [Alice] ship #work/sogav <!-- id: t1 -->",
			CleanText:  "ship",
			Status:     "DONE",
			Owner:      "Alice",
			Priority:   1,
			LineNumber: 1,
		},
		{
			ID:         "t2",
			Type:       parser.BlockTask,
			RawText:    "- [/] DOING TASK [Bob] fix <!-- id: t2 -->",
			CleanText:  "fix",
			Status:     "DOING",
			Owner:      "Bob",
			Priority:   2,
			LineNumber: 2,
		},
		{
			ID:         "t3",
			Type:       parser.BlockTask,
			RawText:    "- [ ] TODO TASK [Alice] research <!-- id: t3 -->",
			CleanText:  "research",
			Status:     "TODO",
			Owner:      "Alice",
			Priority:   3,
			LineNumber: 3,
		},
	}
	if err := app.db.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
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

func TestCreatePage_Scaffolding(t *testing.T) {
	app := newTestApp(t)

	dateStr, err := app.CreatePage("Work", "Meeting Notes", "Daily", "2026-06-13")
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}
	if dateStr != "2026-06-13" {
		t.Errorf("expected date 2026-06-13, got %q", dateStr)
	}

	filePath := filepath.Join(app.vaultPath, "Work", "Meeting Notes", "Daily", "2026-06-13.md")
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read scaffolded file: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, `notebook: "Work"`) || !strings.Contains(content, `section: "Meeting Notes"`) || !strings.Contains(content, `page: "Daily"`) {
		t.Errorf("scaffolded file is missing frontmatter metadata, got:\n%s", content)
	}
}

func TestSaveFileBlocks_PreservesNonBlockLines(t *testing.T) {
	app := newTestApp(t)

	notebook := "Work"
	section := "Journal"
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "---\n" +
		"notebook: Work\n" +
		"section: Journal\n" +
		"date: 2026-06-13\n" +
		"tags: []\n" +
		"---\n" +
		"# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"```go\n" +
		"fmt.Println(\"keep me\") <!-- id: 99999999-9999-9999-9999-999999999999 -->\n" +
		"```\n" +
		"\n" +
		"- [ ] TODO TASK [Alice] ship <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [ ] TODO TASK [Bob] remove <!-- id: 33333333-3333-3333-3333-333333333333 -->\n"
	writeFile(t, filePath, content)

	blocks, _, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	var updated []parser.ParsedBlock
	for _, block := range blocks {
		if block.ID == "33333333-3333-3333-3333-333333333333" {
			continue
		}
		if block.ID == "22222222-2222-2222-2222-222222222222" {
			block.CleanText = "ship the fix"
		}
		updated = append(updated, block)
	}

	if err := app.SaveFileBlocks(notebook, section, page, fileDate, updated); err != nil {
		t.Fatalf("SaveFileBlocks: %v", err)
	}
	writtenBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	written := string(writtenBytes)
	if !strings.Contains(written, "fmt.Println(\"keep me\") <!-- id: 99999999-9999-9999-9999-999999999999 -->") {
		t.Errorf("expected fenced code content to be preserved, got:\n%s", written)
	}
	if !strings.Contains(written, "- [ ] TODO TASK [Alice] ship the fix <!-- id: 22222222-2222-2222-2222-222222222222 -->") {
		t.Errorf("expected updated task text, got:\n%s", written)
	}
	if strings.Contains(written, "remove <!-- id: 33333333-3333-3333-3333-333333333333 -->") {
		t.Errorf("expected removed block to stay deleted, got:\n%s", written)
	}
}

func TestSearchBlocks_FuzzySearch(t *testing.T) {
	app := newTestApp(t)

	blocks := []parser.ParsedBlock{
		{
			ID:         "b1",
			Type:       parser.BlockNote,
			RawText:    "Establish base node connection parameters <!-- id: b1 -->",
			CleanText:  "Establish base node connection parameters",
			LineNumber: 1,
		},
		{
			ID:         "b2",
			Type:       parser.BlockHeader,
			RawText:    "# Cyber-Forest objectives <!-- id: b2 -->",
			CleanText:  "Cyber-Forest objectives",
			LineNumber: 2,
		},
	}
	if err := app.db.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	res, err := app.SearchBlocks("base connection")
	if err != nil {
		t.Fatalf("SearchBlocks failed: %v", err)
	}
	if len(res) != 1 || res[0].ID != "b1" {
		t.Errorf("expected exactly 1 match (b1) for query, got %d results", len(res))
	}

	res, err = app.SearchBlocks("Cyber objectives")
	if err != nil {
		t.Fatalf("SearchBlocks failed: %v", err)
	}
	if len(res) != 1 || res[0].ID != "b2" {
		t.Errorf("expected exactly 1 match (b2) for query, got %d results", len(res))
	}

	// Case-insensitive: lowercase query must match mixed-case content.
	res, err = app.SearchBlocks("base CONNECTION")
	if err != nil {
		t.Fatalf("SearchBlocks failed: %v", err)
	}
	if len(res) != 1 || res[0].ID != "b1" {
		t.Errorf("expected 1 case-insensitive match (b1), got %d results", len(res))
	}

	// Case-insensitive: uppercase query must match lowercase notebook.
	res, err = app.SearchBlocks("WORK")
	if err != nil {
		t.Fatalf("SearchBlocks failed: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("expected 2 matches for WORK notebook, got %d", len(res))
	}
}

func TestFocusLocking_AcquireAndRelease(t *testing.T) {
	app := newTestApp(t)

	watcher, err := monitor.NewDirectoryWatcher(app.vaultPath, app.db, app.tracker, app.coordinator, app.spacesPerTab)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher failed: %v", err)
	}
	app.watcher = watcher

	notebook := "Work"
	section := "Journal"
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")

	if err := app.AcquireFocusLock(notebook, section, page, fileDate); err != nil {
		t.Fatalf("AcquireFocusLock failed: %v", err)
	}
	if !app.watcher.IsFocusLocked(filePath) {
		t.Errorf("expected file to be focus locked")
	}

	if err := app.ReleaseFocusLock(notebook, section, page, fileDate); err != nil {
		t.Fatalf("ReleaseFocusLock failed: %v", err)
	}
	if app.watcher.IsFocusLocked(filePath) {
		t.Errorf("expected file to be unlocked")
	}
}

func TestSaveFileBlocks_DeletesMiddleBlockPreservesNonBlockLines(t *testing.T) {
	app := newTestApp(t)

	notebook := "Work"
	section := "Journal"
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "---\n" +
		"notebook: Work\n" +
		"section: Journal\n" +
		"date: 2026-06-13\n" +
		"tags: []\n" +
		"---\n" +
		"- [ ] TODO TASK [Alice] first <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"```go\n" +
		"// preserved code block\n" +
		"```\n" +
		"\n" +
		"- [ ] TODO TASK [Bob] middle <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [ ] TODO TASK [Carol] last <!-- id: 33333333-3333-3333-3333-333333333333 -->\n"
	writeFile(t, filePath, content)

	blocks, _, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	var updated []parser.ParsedBlock
	for _, block := range blocks {
		if block.ID == "22222222-2222-2222-2222-222222222222" {
			continue
		}
		updated = append(updated, block)
	}

	if err := app.SaveFileBlocks(notebook, section, page, fileDate, updated); err != nil {
		t.Fatalf("SaveFileBlocks: %v", err)
	}
	writtenBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	written := string(writtenBytes)
	if !strings.Contains(written, "// preserved code block") {
		t.Errorf("expected fenced code content to survive middle-block deletion, got:\n%s", written)
	}
	if strings.Contains(written, "middle <!-- id: 22222222-2222-2222-2222-222222222222 -->") {
		t.Errorf("expected deleted middle block to be gone, got:\n%s", written)
	}
	if !strings.Contains(written, "- [ ] TODO TASK [Carol] last <!-- id: 33333333-3333-3333-3333-333333333333 -->") {
		t.Errorf("expected last block to survive, got:\n%s", written)
	}
}

func TestSaveFileBlocks_PreservesUnknownUUIDLine(t *testing.T) {
	app := newTestApp(t)

	notebook := "Work"
	section := "Journal"
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "---\n" +
		"notebook: Work\n" +
		"section: Journal\n" +
		"date: 2026-06-13\n" +
		"tags: []\n" +
		"---\n" +
		"- [ ] TODO TASK [Alice] keep <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"ref to commit <!-- id: deadbeef-dead-beef-dead-beefdeadbeef -->\n" +
		"- [ ] TODO TASK [Bob] also keep <!-- id: 22222222-2222-2222-2222-222222222222 -->\n"
	writeFile(t, filePath, content)

	blocks, _, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}

	if err := app.SaveFileBlocks(notebook, section, page, fileDate, blocks); err != nil {
		t.Fatalf("SaveFileBlocks: %v", err)
	}
	writtenBytes, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	written := string(writtenBytes)
	if !strings.Contains(written, "deadbeef-dead-beef-dead-beefdeadbeef") {
		t.Errorf("expected unknown-UUID line to survive, got:\n%s", written)
	}
}

func TestSanitizePathSegment_StripsControlChars(t *testing.T) {
	// Control characters (including newlines and NUL) must be stripped so
	// they cannot corrupt YAML frontmatter or inject into file paths.
	result := sanitizePathSegment("evil\nsection")
	if strings.ContainsAny(result, "\n\r\x00") {
		t.Errorf("expected control characters stripped, got %q", result)
	}

	result = sanitizePathSegment("clean")
	if result != "clean" {
		t.Errorf("expected 'clean' unchanged, got %q", result)
	}
}

func TestAcquireFocusLock_RejectsTraversalMetadata(t *testing.T) {
	app := newTestApp(t)

	watcher, err := monitor.NewDirectoryWatcher(app.vaultPath, app.db, app.tracker, app.coordinator, app.spacesPerTab)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher failed: %v", err)
	}
	app.watcher = watcher

	err = app.AcquireFocusLock("../../..", "etc", "passwd", "2026-01-01")
	if err == nil {
		t.Fatalf("expected AcquireFocusLock to reject traversal metadata")
	}
	if !strings.Contains(err.Error(), "invalid path metadata") {
		t.Errorf("expected 'invalid path metadata' from sanitization, got: %v", err)
	}
}

// ---- Smart Graph backend (Phase 4) ----

func writeSamplePage(t *testing.T, app *App, notebook, section, page, fileDate, taskID, taskText string) {
	t.Helper()
	filePath := filepath.Join(app.vaultPath, notebook, section, page, fileDate+".md")
	content := "# Title <!-- id: 11111111-1111-1111-1111-111111111111 -->\n\n" +
		"- [ ] TODO TASK [Alice] " + taskText + " <!-- id: " + taskID + " -->\n"
	writeFile(t, filePath, content)
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks(meta.Notebook, meta.Section, meta.Page, meta.Date, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}
}

func TestResolveBlockReference_FoundAndMissing(t *testing.T) {
	app := newTestApp(t)
	taskID := "22222222-2222-2222-2222-222222222222"
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", taskID, "ship the feature")

	ref, err := app.ResolveBlockReference(taskID)
	if err != nil {
		t.Fatalf("ResolveBlockReference: %v", err)
	}
	if !ref.Exists {
		t.Fatalf("expected reference to exist")
	}
	if ref.Notebook != "Work" || ref.Section != "Journal" || ref.Page != "Daily" {
		t.Errorf("unexpected location: %+v", ref)
	}

	missing, err := app.ResolveBlockReference("00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("ResolveBlockReference missing: %v", err)
	}
	if missing.Exists {
		t.Errorf("expected missing reference to report Exists=false")
	}
}

func TestMutateBlock_PreservesTaskSyntaxAndUUID(t *testing.T) {
	app := newTestApp(t)
	taskID := "33333333-3333-3333-3333-333333333333"
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", taskID, "original text")

	if err := app.MutateBlock(taskID, "updated body"); err != nil {
		t.Fatalf("MutateBlock: %v", err)
	}

	filePath := filepath.Join(app.vaultPath, "Work", "Journal", "Daily", "2026-06-13.md")
	content, _ := os.ReadFile(filePath)
	s := string(content)
	// Task syntax and UUID comment must survive.
	if !strings.Contains(s, "- [ ] TODO TASK [Alice] updated body") {
		t.Errorf("expected updated task line, got:\n%s", s)
	}
	if !strings.Contains(s, "<!-- id: "+taskID+" -->") {
		t.Errorf("expected UUID comment preserved, got:\n%s", s)
	}
	// Index reflects the new text.
	var clean string
	_ = app.db.SQLDB().QueryRow("SELECT clean_content FROM blocks WHERE id = ?", taskID).Scan(&clean)
	if clean != "updated body" {
		t.Errorf("expected indexed clean_content 'updated body', got %q", clean)
	}
}

func TestMutateBlock_UnknownIDErrors(t *testing.T) {
	app := newTestApp(t)
	err := app.MutateBlock("00000000-0000-0000-0000-000000000000", "x")
	if err == nil {
		t.Fatalf("expected error for unknown block id")
	}
}

func TestPluginRawQuery_AllowsSelectRejectsWrite(t *testing.T) {
	app := newTestApp(t)
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", "44444444-4444-4444-4444-444444444444", "query me")

	rows, err := app.PluginRawQuery("SELECT id, clean_content FROM blocks WHERE type = ?", []any{"TASK"})
	if err != nil {
		t.Fatalf("PluginRawQuery SELECT: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}

	if _, err := app.PluginRawQuery("DELETE FROM blocks", nil); err == nil {
		t.Errorf("expected PluginRawQuery to reject non-SELECT statements")
	}
}

func TestPluginRawQuery_RejectsStackedWrite(t *testing.T) {
	// Even with a leading SELECT, a stacked write statement must be rejected
	// at the connection level (PRAGMA query_only = ON), not just by the
	// prefix check.
	app := newTestApp(t)
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", "66666666-6666-6666-6666-666666666666", "stacked")

	if _, err := app.PluginRawQuery("SELECT 1; DROP TABLE blocks", nil); err == nil {
		t.Fatalf("expected stacked write to be rejected by read-only connection")
	}
	// Sanity: the index must still be intact.
	rows, err := app.PluginRawQuery("SELECT COUNT(*) AS n FROM blocks", nil)
	if err != nil {
		t.Fatalf("SELECT after rejected stacked write: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestPluginRawQuery_AllowsBlockCommentPrefix(t *testing.T) {
	// A leading block comment is common in authored SQL; the stripper must
	// handle it so a perfectly valid SELECT is not falsely rejected.
	app := newTestApp(t)
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", "77777777-7777-7777-7777-777777777777", "commented")

	rows, err := app.PluginRawQuery("/* explain */ SELECT id FROM blocks LIMIT 1", nil)
	if err != nil {
		t.Fatalf("PluginRawQuery with leading block comment: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(rows))
	}
}

func TestPluginUpdateBlockState_WrapsUpdate(t *testing.T) {
	app := newTestApp(t)
	taskID := "55555555-5555-5555-5555-555555555555"
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", taskID, "do it")

	ok, err := app.PluginUpdateBlockState(taskID, "DONE")
	if err != nil || !ok {
		t.Fatalf("PluginUpdateBlockState: ok=%v err=%v", ok, err)
	}
	var status string
	_ = app.db.SQLDB().QueryRow("SELECT status FROM tasks WHERE block_id = ?", taskID).Scan(&status)
	if status != "DONE" {
		t.Errorf("expected status DONE, got %q", status)
	}
}

func TestGetPluginRegistry_ParsesConfig(t *testing.T) {
	app := newTestApp(t)
	configPath := filepath.Join(app.vaultPath, ".system", "config.yaml")
	writeFile(t, configPath, "plugins:\n  active:\n    - silt-agenda\n    - silt-calendar\n  disabled: []\n  plugin_settings:\n    silt-agenda:\n      window: 7\n")
	registry, err := app.GetPluginRegistry()
	if err != nil {
		t.Fatalf("GetPluginRegistry: %v", err)
	}
	if len(registry.Active) != 2 || registry.Active[0] != "silt-agenda" {
		t.Errorf("expected 2 active plugins, got %v", registry.Active)
	}
	if _, ok := registry.Settings["silt-agenda"]; !ok {
		t.Errorf("expected silt-agenda settings parsed, got %v", registry.Settings)
	}
}

func TestReadPluginSource_ReadsIndexAndRejectsTraversal(t *testing.T) {
	app := newTestApp(t)
	pluginDir := filepath.Join(app.vaultPath, ".system", "plugins", "demo")
	writeFile(t, filepath.Join(pluginDir, "index.js"), "export default { id: 'demo' };\n")

	src, err := app.ReadPluginSource("demo")
	if err != nil {
		t.Fatalf("ReadPluginSource: %v", err)
	}
	if !strings.Contains(src, "demo") {
		t.Errorf("unexpected source: %s", src)
	}

	if _, err := app.ReadPluginSource("../escape"); err == nil {
		t.Errorf("expected traversal plugin id to be rejected")
	}
}

func TestQueryBlocksByTag_PrefixSemantics(t *testing.T) {
	app := newTestApp(t)
	blocks := []parser.ParsedBlock{
		sampleTaskBlockWithText("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 1, "leaf #work/sogav/milestone-one"),
		sampleTaskBlockWithText("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", 2, "mid #work/sogav"),
		sampleTaskBlockWithText("cccccccc-cccc-cccc-cccc-cccccccccccc", 3, "root #work"),
	}
	if err := app.db.IndexFileBlocks("Work", "Journal", "Daily", "2026-06-13", blocks, nil); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	res, err := app.db.QueryBlocksByTag("work")
	if err != nil {
		t.Fatalf("QueryBlocksByTag work: %v", err)
	}
	if len(res) != 3 {
		t.Errorf("expected #work to match all 3 (prefix), got %d", len(res))
	}

	res2, err := app.db.QueryBlocksByTag("work/sogav/milestone-one")
	if err != nil {
		t.Fatalf("QueryBlocksByTag leaf: %v", err)
	}
	if len(res2) != 1 {
		t.Errorf("expected leaf to match 1, got %d", len(res2))
	}
}

func sampleTaskBlockWithText(id string, line int, text string) parser.ParsedBlock {
	return parser.ParsedBlock{
		ID:         id,
		Type:       parser.BlockTask,
		Depth:      0,
		RawText:    "- [ ] TODO TASK " + text + " <!-- id: " + id + " -->",
		CleanText:  text,
		Status:     "TODO",
		LineNumber: line,
	}
}

func TestMutateBlock_RefusesWhileFocusLocked(t *testing.T) {
	app := newTestApp(t)

	watcher, err := monitor.NewDirectoryWatcher(app.vaultPath, app.db, app.tracker, app.coordinator, app.spacesPerTab)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher: %v", err)
	}
	app.watcher = watcher

	taskID := "77777777-7777-7777-7777-777777777777"
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", taskID, "original")

	// Simulate the user editing this file in the timeline editor.
	if err := app.AcquireFocusLock("Work", "Journal", "Daily", "2026-06-13"); err != nil {
		t.Fatalf("AcquireFocusLock: %v", err)
	}

	// An embed (or any plugin) trying to mutate the same block must be refused
	// rather than silently overwriting the in-flight edit.
	err = app.MutateBlock(taskID, "embed edit")
	if err == nil {
		t.Fatalf("expected MutateBlock to be refused while the file is focus-locked")
	}
	if !strings.Contains(err.Error(), "being edited") {
		t.Errorf("expected a 'being edited' refusal, got: %v", err)
	}

	// Once the editor releases the lock, the mutation succeeds.
	if err := app.ReleaseFocusLock("Work", "Journal", "Daily", "2026-06-13"); err != nil {
		t.Fatalf("ReleaseFocusLock: %v", err)
	}
	if err := app.MutateBlock(taskID, "embed edit"); err != nil {
		t.Fatalf("MutateBlock after unlock: %v", err)
	}
}

func TestListNavigation_IncludesEmptySectionsAndNotebooks(t *testing.T) {
	app := newTestApp(t)

	// A populated page (notebook/section/page) with one indexed block.
	writeSamplePage(t, app, "Work", "Projects", "Site", "2026-06-13",
		"66666666-6666-6666-6666-666666666666", "index me")

	// An empty section under Work (folder only — no pages, no blocks).
	if err := os.MkdirAll(filepath.Join(app.vaultPath, "Work", "EmptySection"), 0o755); err != nil {
		t.Fatalf("mkdir empty section: %v", err)
	}
	// An empty notebook (folder only — no sections).
	if err := os.MkdirAll(filepath.Join(app.vaultPath, "Personal"), 0o755); err != nil {
		t.Fatalf("mkdir empty notebook: %v", err)
	}

	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}

	nbByName := map[string]*parser.NavigationNotebook{}
	for i := range tree.Notebooks {
		nbByName[tree.Notebooks[i].Name] = &tree.Notebooks[i]
	}

	// Both notebooks exist, including the empty Personal one.
	if _, ok := nbByName["Work"]; !ok {
		t.Errorf("expected Work notebook; got %+v", tree.Notebooks)
	}
	if _, ok := nbByName["Personal"]; !ok {
		t.Errorf("expected empty Personal notebook to appear; got %+v", tree.Notebooks)
	}

	// Work has both the populated Projects section and the empty EmptySection.
	work := nbByName["Work"]
	secByName := map[string]bool{}
	for _, s := range work.Sections {
		secByName[s.Name] = true
	}
	if !secByName["Projects"] || !secByName["EmptySection"] {
		t.Errorf("expected Projects + EmptySection under Work; got %+v", work.Sections)
	}

	// The populated page has a block count of 2; verify it is surfaced.
	for _, s := range work.Sections {
		if s.Name == "Projects" {
			if len(s.Pages) != 1 || s.Pages[0].Name != "Site" || s.Pages[0].Count != 2 {
				t.Errorf("expected Site page with count 2 (header + task); got %+v", s.Pages)
			}
		}
	}
}

func TestCreatePage_SectionlessThenListed(t *testing.T) {
	app := newTestApp(t)

	// A page created directly under the notebook (no section).
	date, err := app.CreatePage("Work", "", "Inbox", "2026-06-13")
	if err != nil {
		t.Fatalf("section-less CreatePage: %v", err)
	}
	if date != "2026-06-13" {
		t.Errorf("expected date 2026-06-13, got %q", date)
	}

	// The file lives at <vault>/Work/Inbox/... (no section segment).
	fp := filepath.Join(app.vaultPath, "Work", "Inbox", "2026-06-13.md")
	if _, err := os.Stat(fp); err != nil {
		t.Fatalf("expected section-less page file at %s: %v", fp, err)
	}

	// Navigation surfaces it under the section-less group (section == "").
	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}
	for _, nb := range tree.Notebooks {
		if nb.Name != "Work" {
			continue
		}
		var found bool
		for _, sec := range nb.Sections {
			if sec.Name == "" {
				for _, pg := range sec.Pages {
					if pg.Name == "Inbox" {
						found = true
					}
				}
			}
		}
		if !found {
			t.Errorf("expected section-less Inbox page under Work; got %+v", nb.Sections)
		}
	}
}

func TestVersionLessThan(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"1.0.0", "1.0.0", false},
		{"0.1.0", "1.0.0", true},
		{"0.10.0", "0.9.0", false},
	}
	for _, c := range cases {
		if got := versionLessThan(c.a, c.b); got != c.want {
			t.Errorf("versionLessThan(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestEnforceMinVersion(t *testing.T) {
	if err := enforceMinVersion(""); err != nil {
		t.Errorf("expected nil for empty minSiltVersion, got %v", err)
	}
	if err := enforceMinVersion("99.0.0"); err == nil {
		t.Errorf("expected error for minSiltVersion 99.0.0")
	}
	if err := enforceMinVersion("0.0.1"); err != nil {
		t.Errorf("expected nil for minSiltVersion 0.0.1, got %v", err)
	}
}
