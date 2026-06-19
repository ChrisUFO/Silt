package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silt/backend/parser"
	"silt/backend/plugins"
)

// writeAndIndexFile writes content to a page file AND indexes it, so block-
// location lookups (GetBlockLocation) and FetchPageBlocks work in the test.
// Mirrors the setup pattern in app_api_test.go's block-mutation tests.
func writeAndIndexFile(t *testing.T, app *App, filePath, content, notebook, section, page string) {
	t.Helper()
	writeFile(t, filePath, content)
	blocks, meta, _, _, err := parser.ParseFileContent(content, notebook, section, page, "2026-06-13", app.spacesPerTab)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if err := app.db.IndexFileBlocks("vault", meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags); err != nil {
		t.Fatalf("IndexFileBlocks: %v", err)
	}
}

// =========================================================================
// Expanded content API (#104) — block CRUD
// =========================================================================

// PluginCreateBlock inserts a real block into a page file and returns its UUID;
// the block round-trips through the markdown serializer.
func TestPluginCreateBlock_InsertsAndPersists(t *testing.T) {
	app := newTestApp(t)
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n# Today\n\n- [ ] existing task <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	id, err := app.PluginCreateBlock("", notebook, section, page, "TASK", "new plugin task")
	if err != nil {
		t.Fatalf("PluginCreateBlock: %v", err)
	}
	if !looksLikeUUID(id) {
		t.Fatalf("returned id %q is not a UUID", id)
	}

	// The block is in the index now.
	blocks, err := app.FetchPageBlocks(notebook, section, page)
	if err != nil {
		t.Fatalf("FetchPageBlocks: %v", err)
	}
	found := false
	for _, b := range blocks {
		if b.ID == id && strings.Contains(b.CleanText, "new plugin task") {
			found = true
		}
	}
	if !found {
		t.Fatalf("created block %s not found in page blocks", id)
	}
}

// PluginDeleteBlock removes a block by UUID from its file.
func TestPluginDeleteBlock_RemovesBlock(t *testing.T) {
	app := newTestApp(t)
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	target := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n# Today\n\n- [ ] keep <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n- [ ] delete me <!-- id: " + target + " -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	if err := app.PluginDeleteBlock(target); err != nil {
		t.Fatalf("PluginDeleteBlock: %v", err)
	}
	blocks, _ := app.FetchPageBlocks(notebook, section, page)
	for _, b := range blocks {
		if b.ID == target {
			t.Fatalf("deleted block %s still present", target)
		}
	}
}

// PluginMoveBlock reorders a block within a page (after another block).
func TestPluginMoveBlock_ReordersInPage(t *testing.T) {
	app := newTestApp(t)
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	first := "11111111-1111-1111-1111-111111111111"
	mover := "22222222-2222-2222-2222-222222222222"
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n# Today\n\n- [ ] first <!-- id: " + first + " -->\n- [ ] second <!-- id: " + mover + " -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	// Move the second block after the first — no-op position, but verifies the
	// path does not error and preserves both blocks.
	if err := app.PluginMoveBlock(mover, first, "", "", ""); err != nil {
		t.Fatalf("PluginMoveBlock: %v", err)
	}
	blocks, _ := app.FetchPageBlocks(notebook, section, page)
	if len(blocks) < 2 {
		t.Fatalf("expected >= 2 blocks, got %d", len(blocks))
	}
}

// PluginCreateBlock rejects an invalid block type.
func TestPluginCreateBlock_RejectsInvalidType(t *testing.T) {
	app := newTestApp(t)
	_, err := app.PluginCreateBlock("", "Work", "", "Daily", "BOGUS", "text")
	if err == nil {
		t.Fatal("expected error for invalid block type")
	}
}

// =========================================================================
// Plugin file I/O (#108) — capability gating + traversal
// =========================================================================

// PluginWriteFile is denied without a write-files grant.
func TestPluginWriteFile_DeniedWithoutGrant(t *testing.T) {
	app := newTestApp(t)
	err := app.PluginWriteFile("third-party", "Work", "attachments/foo.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected capability denial without grant")
	}
}

// PluginWriteFile works after a write-files grant and writes atomically inside
// attachments/.
func TestPluginWriteFile_GrantThenWrite(t *testing.T) {
	app := newTestApp(t)
	if err := app.RequestCapability("third-party", string(plugins.CapWriteFiles), ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := app.PluginWriteFile("third-party", "Work", "attachments/note.txt", []byte("hello")); err != nil {
		t.Fatalf("PluginWriteFile: %v", err)
	}
	abs := filepath.Join(app.vaultPath, "Work", "attachments", "note.txt")
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

// PluginWriteFile rejects a path outside the allowlist (not attachments/ or
// scratch).
func TestPluginWriteFile_RejectsOutsideAllowlist(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("third-party", string(plugins.CapWriteFiles), "")
	err := app.PluginWriteFile("third-party", "Work", "evil.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected rejection for path outside the allowlist")
	}
}

// PluginWriteFile rejects a traversal path that escapes the notebook root.
func TestPluginWriteFile_RejectsTraversal(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("third-party", string(plugins.CapWriteFiles), "")
	err := app.PluginWriteFile("third-party", "Work", "../../../etc/evil", []byte("x"))
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}

// PluginReadFile + PluginListDir round-trip a file written by PluginWriteFile.
func TestPluginReadFile_AndListDir(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")
	_ = app.RequestCapability("p", string(plugins.CapReadFiles), "")
	_ = app.PluginWriteFile("p", "Work", "attachments/a.txt", []byte("A"))
	_ = app.PluginWriteFile("p", "Work", "attachments/b.txt", []byte("B"))

	res, err := app.PluginReadFile("p", "Work", "attachments/a.txt")
	if err != nil {
		t.Fatalf("PluginReadFile: %v", err)
	}
	if string(res.Bytes) != "A" {
		t.Errorf("read content = %q, want A", res.Bytes)
	}
	entries, err := app.PluginListDir("p", "Work", "attachments")
	if err != nil {
		t.Fatalf("PluginListDir: %v", err)
	}
	if !contains(entries, "a.txt") || !contains(entries, "b.txt") {
		t.Errorf("list = %v, want both files", entries)
	}
}

// PluginScratchDir creates and returns the per-notebook plugin data dir.
func TestPluginScratchDir(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")
	dir, err := app.PluginScratchDir("p", "Work")
	if err != nil {
		t.Fatalf("PluginScratchDir: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(dir), "Work/.system/plugins/p/data") {
		t.Errorf("scratch dir = %q, want suffix Work/.system/plugins/p/data", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("scratch dir not created: %v", err)
	}
}

// =========================================================================
// OS integration (#114) — URL safety
// =========================================================================

// isSafeUrl accepts http/https/mailto and rejects dangerous schemes.
func TestIsSafeUrl(t *testing.T) {
	good := []string{"https://example.com", "http://localhost:3000", "mailto:a@b.com", "HTTPS://X.COM"}
	for _, u := range good {
		if !isSafeUrl(u) {
			t.Errorf("isSafeUrl(%q) = false, want true", u)
		}
	}
	bad := []string{"file:///etc/passwd", "javascript:alert(1)", "data:text/html,x", "ftp://x", "", "  "}
	for _, u := range bad {
		if isSafeUrl(u) {
			t.Errorf("isSafeUrl(%q) = true, want false", u)
		}
	}
}

// pluginWritePathAllowed honors the attachments/ + scratch allowlist.
func TestPluginWritePathAllowed(t *testing.T) {
	good := []string{
		"attachments/foo.png",
		"attachments/sub/bar.pdf",
		".system/plugins/my-plugin/data/cache.json",
	}
	for _, p := range good {
		if !pluginWritePathAllowed("my-plugin", p) {
			t.Errorf("pluginWritePathAllowed(%q) = false, want true", p)
		}
	}
	bad := []string{
		"evil.txt",
		"Journal/Daily.md",
		".system/config.yaml",
		// Another plugin's scratch dir is NOT writable.
		".system/plugins/other-plugin/data/x",
	}
	for _, p := range bad {
		if pluginWritePathAllowed("my-plugin", p) {
			t.Errorf("pluginWritePathAllowed(%q) = true, want false", p)
		}
	}
}

// =========================================================================
// Helpers
// =========================================================================

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

func contains(slice []string, s string) bool {
	for _, x := range slice {
		if x == s {
			return true
		}
	}
	return false
}
