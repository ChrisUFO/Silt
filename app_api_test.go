package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"silt/backend/config"
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

	app := &App{
		// ctx intentionally nil: tests have no Wails lifecycle context, so
		// block:changed / config:changed event emission is skipped.
		db:           dm,
		coordinator:  coord,
		tracker:      tracker,
		vaultPath:    vaultPath,
		spacesPerTab: 4,
	}
	// Load the scaffolded config.yaml so config-backed bindings
	// (GetPluginRegistry, GetSystemConfig) behave as in production.
	cfg, err := config.Load(vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	app.applyConfigLocked(cfg)
	return app
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"- [ ] ship [owner:: Alice] <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [/] research [owner:: Bob] <!-- id: 33333333-3333-3333-3333-333333333333 -->\n" +
		"- [x] done [owner:: Carol] <!-- id: 44444444-4444-4444-4444-444444444444 -->\n"
	writeFile(t, filePath, content)

	// Index the file so the DB has block metadata.
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
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
	if !strings.Contains(string(updated), "- [/] ship [owner:: Alice]") {
		t.Errorf("expected line updated to DOING, got:\n%s", updated)
	}

	// DOING -> DONE
	if err := app.UpdateBlockState("22222222-2222-2222-2222-222222222222", "DONE"); err != nil {
		t.Fatalf("UpdateBlockState DOING->DONE: %v", err)
	}
	updated, _ = os.ReadFile(filePath)
	if !strings.Contains(string(updated), "- [x] ship [owner:: Alice]") {
		t.Errorf("expected line updated to DONE, got:\n%s", updated)
	}

	// DONE -> TODO
	if err := app.UpdateBlockState("22222222-2222-2222-2222-222222222222", "TODO"); err != nil {
		t.Fatalf("UpdateBlockState DONE->TODO: %v", err)
	}
	updated, _ = os.ReadFile(filePath)
	if !strings.Contains(string(updated), "- [ ] ship [owner:: Alice]") {
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
			RawText:    "- [ ] evil <!-- id: 55555555-5555-5555-5555-555555555555 -->",
			CleanText:  "evil",
			Status:     "TODO",
			LineNumber: 1,
		},
	}
	if err := app.db.IndexFileBlocks("vault", "../../..", "etc", "passwd", blocks, nil); err != nil {
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "# Header <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeFile(t, filePath, content)

	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
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

func TestQueryTasks_FiltersByOwnerAndPriority(t *testing.T) {
	app := newTestApp(t)

	blocks := []parser.ParsedBlock{
		{
			ID:         "t1",
			Type:       parser.BlockTask,
			RawText:    "- [x] ship [owner:: Alice] #work/project <!-- id: t1 -->",
			CleanText:  "ship",
			Status:     "DONE",
			Owner:      "Alice",
			Priority:   1,
			LineNumber: 1,
		},
		{
			ID:         "t2",
			Type:       parser.BlockTask,
			RawText:    "- [/] fix [owner:: Bob] <!-- id: t2 -->",
			CleanText:  "fix",
			Status:     "DOING",
			Owner:      "Bob",
			Priority:   2,
			LineNumber: 2,
		},
		{
			ID:         "t3",
			Type:       parser.BlockTask,
			RawText:    "- [ ] research [owner:: Alice] <!-- id: t3 -->",
			CleanText:  "research",
			Status:     "TODO",
			Owner:      "Alice",
			Priority:   3,
			LineNumber: 3,
		},
	}
	if err := app.db.IndexFileBlocks("vault", "Work", "Journal", "Daily", blocks, nil); err != nil {
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

	tagged, err := app.QueryTasks(parser.TaskQueryFilter{Tags: []string{"work/project"}})
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

	filePath := filepath.Join(app.vaultPath, "Work", "Meeting Notes", "Daily.md")
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
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
		"- [ ] ship [owner:: Alice] <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [ ] remove [owner:: Bob] <!-- id: 33333333-3333-3333-3333-333333333333 -->\n"
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

	if err := app.SaveFileBlocks(notebook, section, page, updated); err != nil {
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
	if !strings.Contains(written, "- [ ] ship the fix [owner:: Alice] <!-- id: 22222222-2222-2222-2222-222222222222 @") {
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
	if err := app.db.IndexFileBlocks("vault", "Work", "Journal", "Daily", blocks, nil); err != nil {
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")

	if err := app.AcquireFocusLock(notebook, section, page); err != nil {
		t.Fatalf("AcquireFocusLock failed: %v", err)
	}
	if !app.watcher.IsFocusLocked(filePath) {
		t.Errorf("expected file to be focus locked")
	}

	if err := app.ReleaseFocusLock(notebook, section, page); err != nil {
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\n" +
		"notebook: Work\n" +
		"section: Journal\n" +
		"date: 2026-06-13\n" +
		"tags: []\n" +
		"---\n" +
		"- [ ] first [owner:: Alice] <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"```go\n" +
		"// preserved code block\n" +
		"```\n" +
		"\n" +
		"- [ ] middle [owner:: Bob] <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
		"- [ ] last [owner:: Carol] <!-- id: 33333333-3333-3333-3333-333333333333 -->\n"
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

	if err := app.SaveFileBlocks(notebook, section, page, updated); err != nil {
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
	if !strings.Contains(written, "- [ ] last [owner:: Carol] <!-- id: 33333333-3333-3333-3333-333333333333 @") {
		t.Errorf("expected last block to survive, got:\n%s", written)
	}
}

func TestSaveFileBlocks_PreservesUnknownUUIDLine(t *testing.T) {
	app := newTestApp(t)

	notebook := "Work"
	section := "Journal"
	page := "Daily"
	fileDate := "2026-06-13"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\n" +
		"notebook: Work\n" +
		"section: Journal\n" +
		"date: 2026-06-13\n" +
		"tags: []\n" +
		"---\n" +
		"- [ ] keep [owner:: Alice] <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"ref to commit <!-- id: deadbeef-dead-beef-dead-beefdeadbeef -->\n" +
		"- [ ] also keep [owner:: Bob] <!-- id: 22222222-2222-2222-2222-222222222222 -->\n"
	writeFile(t, filePath, content)

	blocks, _, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}

	if err := app.SaveFileBlocks(notebook, section, page, blocks); err != nil {
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

	err = app.AcquireFocusLock("../../..", "etc", "passwd")
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
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "# Title <!-- id: 11111111-1111-1111-1111-111111111111 -->\n\n" +
		"- [ ] " + taskText + " [owner:: Alice] <!-- id: " + taskID + " -->\n"
	writeFile(t, filePath, content)
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, fileDate, app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
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

	filePath := filepath.Join(app.vaultPath, "Work", "Journal", "Daily.md")
	content, _ := os.ReadFile(filePath)
	s := string(content)
	// Task syntax and UUID comment must survive.
	if !strings.Contains(s, "- [ ] updated body [owner:: Alice]") {
		t.Errorf("expected updated task line, got:\n%s", s)
	}
	if !strings.Contains(s, "<!-- id: "+taskID+" @") {
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

	res, err := app.PluginRawQuery("SELECT id, clean_content FROM blocks WHERE type = ?", []any{"TASK"})
	if err != nil {
		t.Fatalf("PluginRawQuery SELECT: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(res.Rows))
	}
	if res.Truncated {
		t.Errorf("single-row result must not report Truncated=true")
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
	res, err := app.PluginRawQuery("SELECT COUNT(*) AS n FROM blocks", nil)
	if err != nil {
		t.Fatalf("SELECT after rejected stacked write: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
}

func TestPluginRawQuery_AllowsBlockCommentPrefix(t *testing.T) {
	// A leading block comment is common in authored SQL; the stripper must
	// handle it so a perfectly valid SELECT is not falsely rejected.
	app := newTestApp(t)
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", "77777777-7777-7777-7777-777777777777", "commented")

	res, err := app.PluginRawQuery("/* explain */ SELECT id FROM blocks LIMIT 1", nil)
	if err != nil {
		t.Fatalf("PluginRawQuery with leading block comment: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Errorf("expected 1 row, got %d", len(res.Rows))
	}
}

// TestPluginRawQuery_TruncationFlag (#92 review): when a query returns
// more than maxPluginQueryRows, the result must report Truncated=true so
// the plugin SDK can surface a "more rows exist" hint to the user
// instead of silently dropping data on the floor. Run a small number of
// writes (well under the cap) to confirm Truncated=false; the cap itself
// is unit-tested at a smaller cardinality by hand in the actual env
// where the production cap (5000) would dominate the test runtime.
func TestPluginRawQuery_TruncationFlag(t *testing.T) {
	app := newTestApp(t)
	writeSamplePage(t, app, "Work", "Journal", "Daily", "2026-06-13", "88888888-8888-8888-8888-888888888888", "truncation-guard")

	// Far under the cap → Truncated must be false.
	res, err := app.PluginRawQuery("SELECT id FROM blocks LIMIT 10", nil)
	if err != nil {
		t.Fatalf("PluginRawQuery under cap: %v", err)
	}
	if res.Truncated {
		t.Errorf("Truncated should be false under the cap; rows=%d", len(res.Rows))
	}
	if len(res.Rows) == 0 {
		t.Errorf("expected at least 1 row from sample page, got 0")
	}
}

// TestPluginRawQuery_TruncationCapHit verifies the cap-hit path: when a
// query exceeds maxPluginQueryRows, Truncated must be true and the result
// must be capped to exactly maxPluginQueryRows rows. Uses a temporarily
// lowered cap to avoid seeding 5000+ rows in CI.
func TestPluginRawQuery_TruncationCapHit(t *testing.T) {
	app := newTestApp(t)
	// Seed 10 task blocks in a single file (writeSamplePage overwrites
	// per-file, so we build one file with multiple blocks).
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	var lines []string
	lines = append(lines, "# Title <!-- id: 11111111-1111-1111-1111-111111111111 -->")
	lines = append(lines, "")
	for i := range 10 {
		id := fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i)
		lines = append(lines, fmt.Sprintf("- [ ] task %d <!-- id: %s -->", i, id))
	}
	content := strings.Join(lines, "\n") + "\n"
	writeFile(t, filePath, content)
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, "2026-06-13", app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	// Temporarily lower the cap to 3 so we can exercise the truncation
	// branch without seeding thousands of rows.
	origCap := maxPluginQueryRows
	maxPluginQueryRows = 3
	t.Cleanup(func() { maxPluginQueryRows = origCap })

	res, err := app.PluginRawQuery("SELECT id FROM blocks", nil)
	if err != nil {
		t.Fatalf("PluginRawQuery: %v", err)
	}
	if !res.Truncated {
		t.Errorf("expected Truncated=true when result exceeds cap (%d rows, cap %d)", len(res.Rows), maxPluginQueryRows)
	}
	if len(res.Rows) != maxPluginQueryRows {
		t.Errorf("expected exactly %d rows (cap), got %d", maxPluginQueryRows, len(res.Rows))
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
	// Reload the in-memory config (production uses the hot-reload watcher).
	loaded, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	app.applyConfigLocked(loaded)

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

func TestGetSystemConfig_ReturnsLoadedConfig(t *testing.T) {
	app := newTestApp(t)
	cfg, err := app.GetSystemConfig()
	if err != nil {
		t.Fatalf("GetSystemConfig: %v", err)
	}
	// The scaffolded config.yaml matches Defaults(), so the loaded config
	// must carry those values.
	if cfg.Editor.FontFamily == "" {
		t.Errorf("expected scaffolded editor.font_family to be populated")
	}
	if cfg.Editor.TabIndentSpaces != 4 {
		t.Errorf("expected scaffolded tab_indent_spaces=4, got %d", cfg.Editor.TabIndentSpaces)
	}
	if _, ok := cfg.Hotkeys["open_search"]; !ok {
		t.Errorf("expected scaffolded hotkeys.open_search present")
	}
}

func TestSaveSystemConfig_PersistsAndApplies(t *testing.T) {
	app := newTestApp(t)

	cfg, err := app.GetSystemConfig()
	if err != nil {
		t.Fatalf("GetSystemConfig: %v", err)
	}
	cfg.Editor.TabIndentSpaces = 8
	cfg.Editor.FontFamily = "TestFont"
	cfg.Hotkeys["custom"] = "Ctrl+Shift+T"

	if err := app.SaveSystemConfig(cfg); err != nil {
		t.Fatalf("SaveSystemConfig: %v", err)
	}

	// Live knob applied immediately.
	if app.spacesPerTab != 8 {
		t.Errorf("expected spacesPerTab=8 after save, got %d", app.spacesPerTab)
	}

	// A fresh App reading the same vault sees the persisted change (proves it
	// hit disk, not just memory).
	loaded, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.Editor.TabIndentSpaces != 8 {
		t.Errorf("persisted tab_indent_spaces: want 8, got %d", loaded.Editor.TabIndentSpaces)
	}
	if loaded.Editor.FontFamily != "TestFont" {
		t.Errorf("persisted font_family: want TestFont, got %q", loaded.Editor.FontFamily)
	}
	if loaded.Hotkeys["custom"] != "Ctrl+Shift+T" {
		t.Errorf("persisted custom hotkey missing: %v", loaded.Hotkeys)
	}
}

func TestSaveSystemConfig_RejectsInvalid(t *testing.T) {
	app := newTestApp(t)
	cfg, _ := app.GetSystemConfig()

	cfg.Editor.TabIndentSpaces = 0
	if err := app.SaveSystemConfig(cfg); err == nil {
		t.Errorf("expected error for tab_indent_spaces=0")
	}

	cfg.Editor.TabIndentSpaces = 4
	cfg.Editor.FontSizePx = 0
	if err := app.SaveSystemConfig(cfg); err == nil {
		t.Errorf("expected error for font_size_px=0")
	}

	cfg.Editor.FontSizePx = 14
	cfg.Editor.LineHeight = 0
	if err := app.SaveSystemConfig(cfg); err == nil {
		t.Errorf("expected error for line_height=0")
	}

	cfg.Editor.LineHeight = 1.6
	cfg.Editor.AutoSaveDelayMs = -1
	if err := app.SaveSystemConfig(cfg); err == nil {
		t.Errorf("expected error for auto_save_delay_ms=-1")
	}

	// auto_save_delay_ms = 0 is valid (means save immediately / disabled).
	cfg.Editor.AutoSaveDelayMs = 0
	if err := app.SaveSystemConfig(cfg); err != nil {
		t.Errorf("expected no error for auto_save_delay_ms=0, got %v", err)
	}

	// Hotkey validation: empty is allowed (intentional disable)...
	cfg.Hotkeys["open_search"] = ""
	if err := app.SaveSystemConfig(cfg); err != nil {
		t.Errorf("expected no error for empty (disabled) hotkey, got %v", err)
	}
	// ...but a modifier-only binding is rejected.
	cfg.Hotkeys["open_search"] = "Ctrl+Shift"
	if err := app.SaveSystemConfig(cfg); err == nil {
		t.Errorf("expected error for modifier-only hotkey \"Ctrl+Shift\"")
	}
	// A valid binding is accepted; restore it so the config is clean.
	cfg.Hotkeys["open_search"] = "Ctrl+P"
	if err := app.SaveSystemConfig(cfg); err != nil {
		t.Errorf("expected no error for valid hotkey, got %v", err)
	}
}

func TestGetConfigLoadError_OneShot(t *testing.T) {
	app := newTestApp(t)
	// Simulate the startup load error stashed by initializeVaultServices when
	// config.yaml is malformed (the config:error event it emits is lost because
	// the frontend hasn't subscribed yet).
	app.configLoadErr = fmt.Errorf("failed to parse config.yaml: bad indent")
	got := app.GetConfigLoadError()
	if !strings.Contains(got, "bad indent") {
		t.Errorf("expected surfaced load error, got %q", got)
	}
	// The binding is one-shot: a second read clears it.
	if app.GetConfigLoadError() != "" {
		t.Errorf("expected empty after one-shot read, got %q", app.GetConfigLoadError())
	}
}

func TestSaveSystemConfig_RoundTripThroughGet(t *testing.T) {
	app := newTestApp(t)
	cfg, _ := app.GetSystemConfig()
	cfg.Editor.AutoSaveDelayMs = 1234
	if err := app.SaveSystemConfig(cfg); err != nil {
		t.Fatalf("SaveSystemConfig: %v", err)
	}
	got, err := app.GetSystemConfig()
	if err != nil {
		t.Fatalf("GetSystemConfig: %v", err)
	}
	if got.Editor.AutoSaveDelayMs != 1234 {
		t.Errorf("round-trip autosave: want 1234, got %d", got.Editor.AutoSaveDelayMs)
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
		sampleTaskBlockWithText("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 1, "leaf #work/project/milestone-one"),
		sampleTaskBlockWithText("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", 2, "mid #work/project"),
		sampleTaskBlockWithText("cccccccc-cccc-cccc-cccc-cccccccccccc", 3, "root #work"),
	}
	if err := app.db.IndexFileBlocks("vault", "Work", "Journal", "Daily", blocks, nil); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}

	res, err := app.db.QueryBlocksByTag("work")
	if err != nil {
		t.Fatalf("QueryBlocksByTag work: %v", err)
	}
	if len(res) != 3 {
		t.Errorf("expected #work to match all 3 (prefix), got %d", len(res))
	}

	res2, err := app.db.QueryBlocksByTag("work/project/milestone-one")
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
		RawText:    "- [ ] " + text + " <!-- id: " + id + " -->",
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
	if err := app.AcquireFocusLock("Work", "Journal", "Daily"); err != nil {
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
	if err := app.ReleaseFocusLock("Work", "Journal", "Daily"); err != nil {
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
	fp := filepath.Join(app.vaultPath, "Work", "Inbox.md")
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

// --- Phase 6: CloseVault / switch-workspace (#33) ---

// TestCloseVault_TearsDownServices confirms CloseVault nils out every service
// field so IsVaultInitialized flips to false (the signal the frontend uses to
// re-show the onboarding screen).
func TestCloseVault_TearsDownServices(t *testing.T) {
	app := newTestApp(t)
	if !app.IsVaultInitialized() {
		t.Fatal("expected vault initialized before close")
	}
	if err := app.CloseVault(); err != nil {
		t.Fatalf("CloseVault: %v", err)
	}
	if app.IsVaultInitialized() {
		t.Error("IsVaultInitialized should be false after CloseVault")
	}
	if app.db != nil || app.watcher != nil || app.tracker != nil || app.coordinator != nil {
		t.Errorf("CloseVault left a service non-nil: db=%v watcher=%v tracker=%v coord=%v",
			app.db != nil, app.watcher != nil, app.tracker != nil, app.coordinator != nil)
	}
	if app.vaultPath != "" {
		t.Error("CloseVault should clear vaultPath")
	}
}

// TestCloseVault_Idempotent verifies calling CloseVault twice (or on an
// already-closed app) is a no-op rather than panicking.
func TestCloseVault_Idempotent(t *testing.T) {
	app := newTestApp(t)
	if err := app.CloseVault(); err != nil {
		t.Fatalf("first CloseVault: %v", err)
	}
	if err := app.CloseVault(); err != nil {
		t.Fatalf("second CloseVault: %v", err)
	}
	// Closing a never-opened app is also safe.
	fresh := NewApp()
	if err := fresh.CloseVault(); err != nil {
		t.Fatalf("CloseVault on never-opened app: %v", err)
	}
}

// TestCloseVault_ReopenUsesWarmRestart confirms the close→reopen round-trip
// works and that reopening the same on-disk vault takes the warm-restart path
// (files table populated → unchanged files skipped). This exercises the #29 +
// #33 interaction end-to-end.
func TestCloseVault_ReopenUsesWarmRestart(t *testing.T) {
	// newTestApp uses an in-memory DB (""), so to test the on-disk warm path
	// we build a real app with initializeVaultServices against a scaffolded
	// vault containing one note.
	vaultPath := t.TempDir()
	if err := vault.ScaffoldVault(vaultPath); err != nil {
		t.Fatalf("ScaffoldVault: %v", err)
	}
	noteDir := filepath.Join(vaultPath, "Work", "Journal")
	if err := os.MkdirAll(noteDir, 0755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(noteDir, "Daily.md")
	writeFile(t, notePath, "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-14\ntags: []\n---\n# Note <!-- id: aaaaaaaa-1111-1111-1111-111111111111 -->\n")

	app := &App{spacesPerTab: 4}
	if err := app.initializeVaultServices(vaultPath); err != nil {
		t.Fatalf("first initializeVaultServices: %v", err)
	}
	// The note's blocks must be indexed.
	var count int
	_ = app.db.SQLDB().QueryRow("SELECT count(*) FROM blocks").Scan(&count)
	if count == 0 {
		t.Fatal("expected blocks indexed on first init")
	}
	filesPath := filepath.Join(vaultPath, ".system", "index.sqlite")
	if _, err := os.Stat(filesPath); err != nil {
		t.Fatalf("on-disk index not created: %v", err)
	}

	// Close + reopen. The second init must reuse the persistent index and the
	// files-table skip (the file is unchanged → not re-indexed).
	if err := app.CloseVault(); err != nil {
		t.Fatalf("CloseVault: %v", err)
	}
	if err := app.initializeVaultServices(vaultPath); err != nil {
		t.Fatalf("second initializeVaultServices: %v", err)
	}
	defer app.CloseVault()

	// Blocks survive (persistent index), and the file is marked known.
	var count2 int
	_ = app.db.SQLDB().QueryRow("SELECT count(*) FROM blocks").Scan(&count2)
	if count2 != count {
		t.Errorf("block count changed across close/reopen: first=%d second=%d", count, count2)
	}
	known, err := app.db.KnownFiles()
	if err != nil {
		t.Fatalf("KnownFiles: %v", err)
	}
	if len(known) == 0 {
		t.Error("warm restart did not retain the files table")
	}
}

func TestGetAppVersion_MatchesEmbeddedVersion(t *testing.T) {
	app := newTestApp(t)
	got := app.GetAppVersion()
	if got == "" {
		t.Fatalf("GetAppVersion returned empty string")
	}
	// The embedded VERSION file is the source of truth for appVersion.
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if got != want {
		t.Errorf("GetAppVersion = %q, want %q (VERSION file)", got, want)
	}
}

func TestListPlugins_PopulatesManifestFields(t *testing.T) {
	app := newTestApp(t)
	pluginDir := filepath.Join(app.vaultPath, ".system", "plugins", "rich-demo")
	writeFile(t, filepath.Join(pluginDir, "index.js"), "export default {};\n")
	writeFile(t, filepath.Join(pluginDir, "plugin.json"), `{
		"id": "rich-demo",
		"name": "Rich Demo",
		"version": "2.3.4",
		"author": "Ada",
		"description": "A demo with full manifest fields.",
		"icon": "extension"
	}`)

	list, err := app.ListPlugins()
	if err != nil {
		t.Fatalf("ListPlugins: %v", err)
	}
	var info *parser.PluginInfo
	for i := range list {
		if list[i].ID == "rich-demo" {
			info = &list[i]
			break
		}
	}
	if info == nil {
		t.Fatalf("rich-demo not returned by ListPlugins: %+v", list)
	}
	if !info.HasManifest || !info.HasIndex {
		t.Errorf("expected HasManifest+HasIndex, got %+v", info)
	}
	if info.Name != "Rich Demo" || info.Version != "2.3.4" {
		t.Errorf("name/version: got %+v", info)
	}
	if info.Author != "Ada" {
		t.Errorf("author: want Ada, got %q", info.Author)
	}
	if info.Description != "A demo with full manifest fields." {
		t.Errorf("description not populated: got %q", info.Description)
	}
	if info.Icon != "extension" {
		t.Errorf("icon: want extension, got %q", info.Icon)
	}
}

func TestMigratePerDayFiles_MergesDateFilesIntoPageFile(t *testing.T) {
	vaultPath := t.TempDir()
	pageDir := filepath.Join(vaultPath, "Work", "Journal", "Daily")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	day1 := "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-01\ntags: []\n---\n- first day note <!-- id: 11111111-1111-4111-8111-111111111111 -->\n"
	day2 := "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-02\ntags: []\n---\n- second day note <!-- id: 22222222-2222-4222-8222-222222222222 -->\n"
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-01.md"), []byte(day1), 0644); err != nil {
		t.Fatalf("write day1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-02.md"), []byte(day2), 0644); err != nil {
		t.Fatalf("write day2: %v", err)
	}

	warnings := migratePerDayFiles(vaultPath, 4)

	targetPath := filepath.Join(vaultPath, "Work", "Journal", "Daily.md")
	contentBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("expected merged file at %q: %v", targetPath, err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "11111111-1111-4111-8111-111111111111") {
		t.Errorf("merged file missing first block id:\n%s", content)
	}
	if !strings.Contains(content, "22222222-2222-4222-8222-222222222222") {
		t.Errorf("merged file missing second block id:\n%s", content)
	}

	if _, err := os.Stat(pageDir); !os.IsNotExist(err) {
		t.Errorf("expected old directory %q to be removed", pageDir)
	}

	if len(warnings) == 0 {
		t.Errorf("expected at least one migration warning (success notice)")
	}

	blocks, _, _, _, parseErr := parser.ParseFileContent(content, "Work", "Journal", "Daily", "2026-06-02", 4)
	if parseErr != nil {
		t.Fatalf("merged file failed to parse: %v", parseErr)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 blocks in merged file, got %d", len(blocks))
	}
}

func TestMigratePerDayFiles_IdempotentSecondRunIsNoOp(t *testing.T) {
	vaultPath := t.TempDir()

	warnings1 := migratePerDayFiles(vaultPath, 4)
	if len(warnings1) != 0 {
		t.Errorf("expected no warnings on first run with empty vault, got %v", warnings1)
	}

	warnings2 := migratePerDayFiles(vaultPath, 4)
	if len(warnings2) != 0 {
		t.Errorf("expected no warnings on second run, got %v", warnings2)
	}
}

func TestMigratePerDayFiles_SkipsWhenTargetExists(t *testing.T) {
	vaultPath := t.TempDir()
	pageDir := filepath.Join(vaultPath, "Work", "Daily")
	if err := os.MkdirAll(pageDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageDir, "2026-06-01.md"), []byte("- old note\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	targetPath := filepath.Join(vaultPath, "Work", "Daily.md")
	if err := os.WriteFile(targetPath, []byte("- already migrated\n"), 0644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	warnings := migratePerDayFiles(vaultPath, 4)

	found := false
	for _, w := range warnings {
		if strings.Contains(w, "already exists") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a 'target already exists' warning, got %v", warnings)
	}

	if _, err := os.Stat(filepath.Join(pageDir, "2026-06-01.md")); err != nil {
		t.Errorf("original date file should be preserved when target exists")
	}
}

// seedTaskFile writes a page with one task + one note, indexes it, and
// returns the file path + the task/note block IDs. Shared setup for the
// PluginUpdateTaskMeta coverage below.
func seedTaskFile(t *testing.T, app *App) (filePath, taskID, noteID string) {
	t.Helper()
	notebook, section, page := "Work", "Journal", "Daily"
	filePath = filepath.Join(app.vaultPath, notebook, section, page+".md")
	taskID = "22222222-2222-2222-2222-222222222222"
	noteID = "55555555-5555-5555-5555-555555555555"
	content := "# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
		"\n" +
		"- [ ] ship [owner:: Alice] <!-- id: " + taskID + " -->\n" +
		"- a note <!-- id: " + noteID + " -->\n"
	writeFile(t, filePath, content)
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, "2026-06-13", app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}
	return filePath, taskID, noteID
}

func TestPluginUpdateTaskMeta(t *testing.T) {
	t.Run("pin toggle round-trips through markdown", func(t *testing.T) {
		app := newTestApp(t)
		filePath, taskID, _ := seedTaskFile(t, app)

		ok, err := app.PluginUpdateTaskMeta(taskID, 1, -1)
		if err != nil || !ok {
			t.Fatalf("PluginUpdateTaskMeta pin=1: ok=%v err=%v", ok, err)
		}
		got, _ := os.ReadFile(filePath)
		if !strings.Contains(string(got), "[pin:: true]") {
			t.Errorf("expected [pin:: true] in file after pin, got:\n%s", string(got))
		}

		// Unpin writes an explicit [pin:: false] (tri-state #123): the token
		// is preserved so a user-typed [pin:: false] survives round-trips and
		// toggling cannot silently revert.
		ok, err = app.PluginUpdateTaskMeta(taskID, 0, -1)
		if err != nil || !ok {
			t.Fatalf("PluginUpdateTaskMeta pin=0: ok=%v err=%v", ok, err)
		}
		got, _ = os.ReadFile(filePath)
		if !strings.Contains(string(got), "[pin:: false]") {
			t.Errorf("expected [pin:: false] in file after unpin, got:\n%s", string(got))
		}
		if strings.Count(string(got), "[pin::") != 1 {
			t.Errorf("expected exactly one [pin:: token after unpin, got:\n%s", string(got))
		}

		// pin=-2 clears the token entirely (nil → renderer omits it).
		ok, err = app.PluginUpdateTaskMeta(taskID, -2, -1)
		if err != nil || !ok {
			t.Fatalf("PluginUpdateTaskMeta pin=-2: ok=%v err=%v", ok, err)
		}
		got, _ = os.ReadFile(filePath)
		if strings.Contains(string(got), "[pin::") {
			t.Errorf("expected [pin::] token removed after clear (pin=-2), got:\n%s", string(got))
		}
	})

	t.Run("progress set round-trips through markdown", func(t *testing.T) {
		app := newTestApp(t)
		filePath, taskID, _ := seedTaskFile(t, app)

		ok, err := app.PluginUpdateTaskMeta(taskID, -1, 75)
		if err != nil || !ok {
			t.Fatalf("PluginUpdateTaskMeta progress=75: ok=%v err=%v", ok, err)
		}
		got, _ := os.ReadFile(filePath)
		if !strings.Contains(string(got), "[progress:: 75]") {
			t.Errorf("expected [progress:: 75] in file, got:\n%s", string(got))
		}
	})

	t.Run("no-op sentinels return true without writing", func(t *testing.T) {
		app := newTestApp(t)
		filePath, taskID, _ := seedTaskFile(t, app)
		before, _ := os.ReadFile(filePath)

		ok, err := app.PluginUpdateTaskMeta(taskID, -1, -1)
		if err != nil || !ok {
			t.Fatalf("no-op: ok=%v err=%v", ok, err)
		}
		after, _ := os.ReadFile(filePath)
		if string(before) != string(after) {
			t.Errorf("no-op should not touch the file\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("invalid pin value rejected", func(t *testing.T) {
		app := newTestApp(t)
		_, taskID, _ := seedTaskFile(t, app)
		ok, err := app.PluginUpdateTaskMeta(taskID, 2, -1)
		if ok || err == nil {
			t.Errorf("pin=2 should be rejected, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("invalid progress value rejected", func(t *testing.T) {
		app := newTestApp(t)
		_, taskID, _ := seedTaskFile(t, app)
		ok, err := app.PluginUpdateTaskMeta(taskID, -1, 200)
		if ok || err == nil {
			t.Errorf("progress=200 should be rejected, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("non-task block rejected", func(t *testing.T) {
		app := newTestApp(t)
		_, _, noteID := seedTaskFile(t, app)
		ok, err := app.PluginUpdateTaskMeta(noteID, 1, -1)
		if ok || err == nil {
			t.Errorf("non-task block should be rejected, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("block missing from file returns error", func(t *testing.T) {
		app := newTestApp(t)
		filePath, taskID, _ := seedTaskFile(t, app)
		// Overwrite the file so the indexed block is no longer present
		// (simulates a concurrent external edit that removed the line).
		writeFile(t, filePath, "# Empty\n- [ ] different task <!-- id: 99999999-9999-9999-9999-999999999999 -->\n")

		ok, err := app.PluginUpdateTaskMeta(taskID, 1, -1)
		if ok || err == nil {
			t.Errorf("missing block should error, got ok=%v err=%v", ok, err)
		}
	})
}

// TestUpdatePluginSetting_PreservesOtherFields confirms the atomic per-plugin
// setter (#120) writes ONLY the targeted plugins.plugin_settings[id][key] and
// leaves every other config field intact — the property the read-mutate-
// saveConfig dance could violate when an external edit landed mid-call.
func TestUpdatePluginSetting_PreservesOtherFields(t *testing.T) {
	app := newTestApp(t)

	// Seed an in-memory + on-disk config with unrelated fields that must
	// survive a targeted plugin-setting update.
	app.configMu.Lock()
	cfg := app.cfg
	cfg.Editor.FontFamily = "MyFont"
	cfg.Plugins.PluginSettings = map[string]any{
		"silt-agenda": map[string]any{"interval": 30},
		"silt-kanban": map[string]any{"columns": []string{"Old"}},
	}
	app.cfg = cfg
	app.configMu.Unlock()
	if err := config.Save(app.vaultPath, cfg); err != nil {
		t.Fatalf("seed config.Save: %v", err)
	}

	if err := app.UpdatePluginSetting("silt-kanban", "columns", []string{"TODO", "DOING"}); err != nil {
		t.Fatalf("UpdatePluginSetting: %v", err)
	}

	// In-memory: targeted key updated to the new value. The value retains its
	// concrete Go type here ([]string); it only generalises to []any after a
	// YAML round-trip (checked below on the reload).
	app.configMu.RLock()
	kanban, _ := app.cfg.Plugins.PluginSettings["silt-kanban"].(map[string]any)
	app.configMu.RUnlock()
	if kanban == nil {
		t.Fatal("in-memory silt-kanban settings missing")
	}
	colsStr, _ := kanban["columns"].([]string)
	if len(colsStr) != 2 || colsStr[0] != "TODO" || colsStr[1] != "DOING" {
		t.Errorf("in-memory columns = %v, want [TODO DOING]", kanban["columns"])
	}

	// On-disk reload: unrelated fields preserved verbatim.
	loaded, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("reload config.Load: %v", err)
	}
	if loaded.Editor.FontFamily != "MyFont" {
		t.Errorf("Editor.FontFamily not preserved: got %q", loaded.Editor.FontFamily)
	}
	agenda, _ := loaded.Plugins.PluginSettings["silt-agenda"].(map[string]any)
	if fmt.Sprint(agenda["interval"]) != "30" {
		t.Errorf("silt-agenda.interval not preserved: got %v", agenda["interval"])
	}
	kanban2, _ := loaded.Plugins.PluginSettings["silt-kanban"].(map[string]any)
	cols2, _ := kanban2["columns"].([]any)
	if len(cols2) != 2 || fmt.Sprint(cols2[0]) != "TODO" {
		t.Errorf("on-disk columns = %v, want [TODO DOING]", kanban2["columns"])
	}
}

// TestUpdatePluginSetting_ConcurrentWithExternalReload runs targeted updates
// concurrently with a watcher-style wholesale config replacement (applyConfig),
// confirming the configMu serialization keeps both paths race-clean and the
// on-disk config never corrupts (#120). Run under -race.
func TestUpdatePluginSetting_ConcurrentWithExternalReload(t *testing.T) {
	app := newTestApp(t)
	if err := app.UpdatePluginSetting("silt-kanban", "columns", []string{"TODO"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snapshot := func() config.SystemConfig {
		app.configMu.RLock()
		defer app.configMu.RUnlock()
		return app.cfg
	}

	stop := make(chan struct{})
	var extWg sync.WaitGroup
	extWg.Add(1)
	go func() {
		defer extWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// Watcher-driven external reload: config replaced wholesale
				// under configMu (applyConfig locks). a.ctx is nil in tests,
				// so no event emission / nil-ctx panic.
				app.applyConfig(snapshot())
			}
		}
	}()

	const n = 60
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "filters"
			if i%2 == 0 {
				key = "columns"
			}
			if err := app.UpdatePluginSetting("silt-kanban", key, map[string]any{"i": i}); err != nil {
				t.Errorf("UpdatePluginSetting: %v", err)
			}
		}(i)
	}
	wg.Wait()
	close(stop)
	extWg.Wait()

	// After the storm the on-disk config must still parse (no corruption) and
	// the targeted plugin's settings map must be intact.
	loaded, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("final config.Load: %v", err)
	}
	if _, ok := loaded.Plugins.PluginSettings["silt-kanban"].(map[string]any); !ok {
		t.Errorf("silt-kanban settings lost after concurrent updates: %#v", loaded.Plugins.PluginSettings)
	}
}

// TestUpdatePluginSetting_RequiresIDs rejects empty pluginID / key (fail-loud
// guard against a no-op targeted write that silently writes nothing).
func TestUpdatePluginSetting_RequiresIDs(t *testing.T) {
	app := newTestApp(t)
	if err := app.UpdatePluginSetting("", "columns", []string{"TODO"}); err == nil {
		t.Error("expected error for empty pluginID, got nil")
	}
	if err := app.UpdatePluginSetting("silt-kanban", "", []string{"TODO"}); err == nil {
		t.Error("expected error for empty key, got nil")
	}
}

// TestResolveNotebookDir covers the #100 notebook content-root resolver: the
// vault path is byte-identical to the legacy join (zero regression), a linked
// source resolves to its registered root, and unknown/missing ids fail loud.
func TestResolveNotebookDir(t *testing.T) {
	app := newTestApp(t)

	// Vault source → <vault>/<notebook> (identical to the old filepath.Join).
	got, err := app.resolveNotebookDir("Work", config.LinkedNotebooksVaultSource)
	if err != nil {
		t.Fatalf("vault resolve: %v", err)
	}
	want := filepath.Join(app.vaultPath, "Work")
	if got != want {
		t.Errorf("vault resolve = %q, want %q", got, want)
	}
	// Empty source is treated as vault (back-compat for callers without a source).
	if got2, _ := app.resolveNotebookDir("Work", ""); got2 != want {
		t.Errorf("empty-source resolve = %q, want %q", got2, want)
	}

	// Linked source → registered root path.
	root := t.TempDir()
	app.configMu.Lock()
	app.cfg.LinkedNotebooks = append(app.cfg.LinkedNotebooks, config.LinkedNotebook{
		ID: "abc", RootPath: root, DisplayName: "Ext",
	})
	app.configMu.Unlock()
	got3, err := app.resolveNotebookDir("Ext", "linked:abc")
	if err != nil {
		t.Fatalf("linked resolve: %v", err)
	}
	if got3 != root {
		t.Errorf("linked resolve = %q, want %q", got3, root)
	}

	// Unregistered linked id and unknown source prefix fail loud.
	if _, err := app.resolveNotebookDir("X", "linked:ghost"); err == nil {
		t.Error("expected error for unregistered linked id, got nil")
	}
	if _, err := app.resolveNotebookDir("X", "bogus:1"); err == nil {
		t.Error("expected error for unknown source prefix, got nil")
	}
}

// --- #100 linked / external notebooks ---------------------------------------

// TestLinkNotebook_IndexesAndUnlinkLeavesFiles covers the link → index → nav →
// unlink lifecycle: a linked notebook is indexed under source='linked:<id>',
// appears in ListNavigation with Source/RootPath, the registry persists, and
// unlink drops the index rows while leaving the external file untouched.
func TestLinkNotebook_IndexesAndUnlinkLeavesFiles(t *testing.T) {
	app := newTestApp(t)

	ext := t.TempDir()
	pageFile := filepath.Join(ext, "Plan.md")
	writeFile(t, pageFile, "---\nnotebook: Ext\nsection: \"\"\npage: Plan\ndate: 2026-06-16\ntags: []\n---\n# Plan\n- [ ] do a thing <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n")

	ln, err := app.LinkNotebook(ext)
	if err != nil {
		t.Fatalf("LinkNotebook: %v", err)
	}
	if ln.ID == "" || !strings.HasPrefix(ln.Source(), "linked:") {
		t.Fatalf("unexpected linked notebook: %+v", ln)
	}
	if ln.RootPath != filepath.Clean(ext) {
		t.Errorf("RootPath = %q, want %q", ln.RootPath, filepath.Clean(ext))
	}
	src := ln.Source()

	// Indexed under the linked source.
	var n int
	app.coordinator.WithDBRead(func() {
		_ = app.db.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE source = ?", src).Scan(&n)
	})
	if n == 0 {
		t.Errorf("expected linked blocks indexed under source=%q, got 0", src)
	}

	// Appears in navigation as a linked notebook with the right metadata.
	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}
	var found *parser.NavigationNotebook
	for i := range tree.Notebooks {
		if tree.Notebooks[i].Source == src {
			found = &tree.Notebooks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("linked notebook not in nav tree")
	}
	if found.Name != ln.DisplayName || found.RootPath != ln.RootPath {
		t.Errorf("nav entry mismatch: %+v", found)
	}

	// Registry persisted to config.yaml.
	loaded, _ := config.Load(app.vaultPath)
	if len(loaded.LinkedNotebooks) != 1 || loaded.LinkedNotebooks[0].ID != ln.ID {
		t.Errorf("registry not persisted: %+v", loaded.LinkedNotebooks)
	}

	// Unlink drops index rows and leaves the external file untouched.
	if err := app.UnlinkNotebook(ln.ID); err != nil {
		t.Fatalf("UnlinkNotebook: %v", err)
	}
	app.coordinator.WithDBRead(func() {
		_ = app.db.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE source = ?", src).Scan(&n)
	})
	if n != 0 {
		t.Errorf("expected linked index rows dropped after unlink, got %d", n)
	}
	if _, err := os.Stat(pageFile); err != nil {
		t.Errorf("external file was touched by unlink: %v", err)
	}
	loaded2, _ := config.Load(app.vaultPath)
	if len(loaded2.LinkedNotebooks) != 0 {
		t.Errorf("registry not cleared after unlink: %+v", loaded2.LinkedNotebooks)
	}
}

// TestLinkNotebook_RejectsCollisions verifies the fail-loud guards: a folder
// inside the vault must use OpenNotebook (not link), and a name collision with
// a vault notebook is rejected so the sidebar stays unambiguous.
func TestLinkNotebook_RejectsCollisions(t *testing.T) {
	app := newTestApp(t)
	if err := os.MkdirAll(filepath.Join(app.vaultPath, "Work"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Inside the vault → rejected (use OpenNotebook instead).
	inside := filepath.Join(app.vaultPath, "Inside")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LinkNotebook(inside); err == nil {
		t.Error("expected error linking a folder inside the vault, got nil")
	}

	// Name collision with a vault notebook → rejected.
	parent := t.TempDir()
	collision := filepath.Join(parent, "Work")
	if err := os.MkdirAll(collision, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LinkNotebook(collision); err == nil {
		t.Error("expected error linking a folder whose name collides with a vault notebook, got nil")
	}
}

// TestListNavigation_LinkedDisconnectedWhenRootMissing verifies the offline
// failure mode (#100): a linked notebook whose root can't be read (mount drop)
// stays in the tree marked Disconnected so its last-synced rows remain visible
// and the UI can badge it. LinkNotebook validates existence at link time, so
// the missing-root case is simulated by registering the link directly.
func TestListNavigation_LinkedDisconnectedWhenRootMissing(t *testing.T) {
	app := newTestApp(t)
	app.configMu.Lock()
	app.cfg.LinkedNotebooks = []config.LinkedNotebook{{
		ID: "ghost", RootPath: filepath.Join(t.TempDir(), "does-not-exist"), DisplayName: "Ghost",
	}}
	app.configMu.Unlock()

	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}
	var found *parser.NavigationNotebook
	for i := range tree.Notebooks {
		if tree.Notebooks[i].Source == "linked:ghost" {
			found = &tree.Notebooks[i]
			break
		}
	}
	if found == nil {
		t.Fatal("expected the linked notebook in the tree even when its root is offline")
	}
	if !found.Disconnected {
		t.Error("expected Disconnected=true when the linked root is missing/offline")
	}
}

// TestLinkedNotebook_PageCRUD_RoutesToLinkedRoot verifies the #100 lifecycle
// fix: page create/rename/delete inside a linked notebook route to the linked
// root (not the vault), index under the linked source, and linked deletes
// happen IN PLACE (never trash into the vault).
func TestLinkedNotebook_PageCRUD_RoutesToLinkedRoot(t *testing.T) {
	app := newTestApp(t)
	ext := t.TempDir()
	ln, err := app.LinkNotebook(ext)
	if err != nil {
		t.Fatalf("LinkNotebook: %v", err)
	}

	// CreatePage in the linked notebook → file lands in the linked root.
	if _, err := app.CreatePage(ln.DisplayName, "", "Plan", ""); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	linkedFile := filepath.Join(ext, "Plan.md")
	if _, err := os.Stat(linkedFile); err != nil {
		t.Errorf("expected linked page at %s, got %v", linkedFile, err)
	}
	// Vault must be untouched by the linked create.
	if _, err := os.Stat(filepath.Join(app.vaultPath, ln.DisplayName, "Plan.md")); err == nil {
		t.Error("linked CreatePage leaked a file into the vault")
	}

	// CreatePage on an empty page produces no blocks, but nothing should be
	// indexed under the VAULT source for this notebook name (no misroute).
	var vaultRows int
	app.coordinator.WithDBRead(func() {
		_ = app.db.SQLDB().QueryRow(
			"SELECT COUNT(*) FROM blocks WHERE source = 'vault' AND notebook = ?",
			ln.DisplayName,
		).Scan(&vaultRows)
	})
	if vaultRows != 0 {
		t.Errorf("linked CreatePage leaked %d row(s) into the vault index", vaultRows)
	}

	// Rename the page within the linked root.
	if err := app.RenamePage(ln.DisplayName, "", "Plan", "Renamed"); err != nil {
		t.Fatalf("RenamePage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ext, "Renamed.md")); err != nil {
		t.Errorf("rename: expected Renamed.md in the linked root: %v", err)
	}
	if _, err := os.Stat(linkedFile); err == nil {
		t.Error("old linked page file should no longer exist after rename")
	}

	// Delete the page → removed in place from the linked root, NOT trashed
	// into the vault.
	if err := app.DeletePage(ln.DisplayName, "", "Renamed"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ext, "Renamed.md")); err == nil {
		t.Error("expected the linked page deleted in place")
	}
	trash := filepath.Join(app.vaultPath, ".system", "trash")
	if entries, _ := os.ReadDir(trash); len(entries) > 0 {
		t.Errorf("linked delete should not trash into the vault; trash contained: %v", entries)
	}
}

// TestRenameNotebook_RefusesLinked confirms RenameNotebook fails loud for a
// linked notebook (renaming the external source-of-truth folder is out of
// scope; rename = unlink + re-link).
func TestRenameNotebook_RefusesLinked(t *testing.T) {
	app := newTestApp(t)
	ext := t.TempDir()
	ln, err := app.LinkNotebook(ext)
	if err != nil {
		t.Fatalf("LinkNotebook: %v", err)
	}
	if err := app.RenameNotebook(ln.DisplayName, "NewName"); err == nil {
		t.Error("expected RenameNotebook to refuse a linked notebook, got nil")
	}
}

// TestVaultNotebookOps_RejectLinkedNameCollision locks the global name-uniqueness
// invariant from the VAULT side (#100): CreateNotebook / OpenNotebook /
// RenameNotebook refuse a name that collides with a registered linked notebook,
// so resolveSourceByName can never misroute a vault op to an external root
// (which would silently write to / delete files on the external mount).
func TestVaultNotebookOps_RejectLinkedNameCollision(t *testing.T) {
	app := newTestApp(t)
	ext := t.TempDir()
	ln, err := app.LinkNotebook(ext)
	if err != nil {
		t.Fatalf("LinkNotebook: %v", err)
	}

	// CreateNotebook with the linked display name → rejected.
	if err := app.CreateNotebook(ln.DisplayName); err == nil {
		t.Error("CreateNotebook: expected a linked-name collision error, got nil")
	}
	// RenameNotebook of a vault notebook TO the linked display name → rejected.
	if err := os.MkdirAll(filepath.Join(app.vaultPath, "VaultNB"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := app.RenameNotebook("VaultNB", ln.DisplayName); err == nil {
		t.Error("RenameNotebook: expected a linked-name collision error, got nil")
	}
	// OpenNotebook of a vault folder whose name collides with the link → rejected.
	col := filepath.Join(app.vaultPath, ln.DisplayName)
	if err := os.MkdirAll(col, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.OpenNotebook(col); err == nil {
		t.Error("OpenNotebook: expected a linked-name collision error, got nil")
	}
}

// TestResolveSourceByName locks resolveSourceByName: a pure-vault name returns
// 'vault', a registered link's name returns 'linked:<id>', and an unrelated
// name returns 'vault'. resolveSourceByName is the linchpin of the whole
// notebook routing layer.
func TestResolveSourceByName(t *testing.T) {
	app := newTestApp(t)

	if got := app.resolveSourceByName("Work"); got != config.LinkedNotebooksVaultSource {
		t.Errorf("pure-vault name: got %q, want %q", got, config.LinkedNotebooksVaultSource)
	}

	app.configMu.Lock()
	app.cfg.LinkedNotebooks = []config.LinkedNotebook{{ID: "abc", RootPath: "/tmp/x", DisplayName: "Ext"}}
	app.configMu.Unlock()

	if got := app.resolveSourceByName("Ext"); got != "linked:abc" {
		t.Errorf("linked name: got %q, want linked:abc", got)
	}
	if got := app.resolveSourceByName("Other"); got != config.LinkedNotebooksVaultSource {
		t.Errorf("unrelated name: got %q, want vault", got)
	}
}

// TestSaveFileBlocks_LinkedRootUnusable_ReturnsClearError locks the documented
// offline/unusable-root contract (#100): a write to a linked notebook whose
// root can't be used must return a clear error (no panic, no crash). We proxy
// an offline mount with a root path that IS a file (so page ops go
// "through-the-file" and fail at the OS level rather than being recreatable).
func TestSaveFileBlocks_LinkedRootUnusable_ReturnsClearError(t *testing.T) {
	app := newTestApp(t)
	notADir := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.configMu.Lock()
	app.cfg.LinkedNotebooks = []config.LinkedNotebook{{ID: "dead", RootPath: notADir, DisplayName: "Dead"}}
	app.configMu.Unlock()

	blocks := []parser.ParsedBlock{{
		ID:         "11111111-1111-1111-1111-111111111111",
		Type:       parser.BlockTask,
		RawText:    "- [ ] task <!-- id: 11111111-1111-1111-1111-111111111111 -->",
		CleanText:  "task",
		Status:     "TODO",
		LineNumber: 1,
	}}
	err := app.SaveFileBlocks("Dead", "", "Plan", blocks)
	if err == nil {
		t.Fatal("expected a clear error saving to an unusable linked root, got nil")
	}
	if err.Error() == "" {
		t.Error("expected a non-empty error message for an unusable linked root")
	}
}
