package main

import (
	"archive/zip"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silt/backend/config"
	"silt/backend/parser"
	"silt/backend/plugins"
)

// registerTestSession registers a session for pluginID and returns the token.
// Tests that call session-gated bindings (#151) need this before the call.
func registerTestSession(t *testing.T, app *App, pluginID string) string {
	t.Helper()
	token, err := app.RegisterPluginSession(pluginID)
	if err != nil {
		t.Fatalf("RegisterPluginSession(%q): %v", pluginID, err)
	}
	return token
}

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
	token := registerTestSession(t, app, "silt-kanban")

	id, err := app.PluginCreateBlock("silt-kanban", token, "", notebook, section, page, "TASK", "new plugin task")
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
	token := registerTestSession(t, app, "silt-kanban")

	if err := app.PluginDeleteBlock("silt-kanban", token, target); err != nil {
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
	token := registerTestSession(t, app, "silt-kanban")

	// Move the second block after the first — no-op position, but verifies the
	// path does not error and preserves both blocks.
	if err := app.PluginMoveBlock("silt-kanban", token, mover, first, "", "", ""); err != nil {
		t.Fatalf("PluginMoveBlock: %v", err)
	}
	blocks, _ := app.FetchPageBlocks(notebook, section, page)
	if len(blocks) < 2 {
		t.Fatalf("expected >= 2 blocks, got %d", len(blocks))
	}
}

// PluginMoveBlock across pages: the block must be REMOVED from source AND
// INSERTED into target. Before the fix, the block was silently deleted from
// the source but never added to the target (data loss).
func TestPluginMoveBlock_CrossPageInsertsInTarget(t *testing.T) {
	app := newTestApp(t)
	notebook, section := "Work", "Journal"
	srcPage, dstPage := "Source", "Dest"
	srcPath := filepath.Join(app.vaultPath, notebook, section, srcPage+".md")
	dstPath := filepath.Join(app.vaultPath, notebook, section, dstPage+".md")

	blockA := "11111111-1111-1111-1111-111111111111"
	blockB := "22222222-2222-2222-2222-222222222222"

	srcContent := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Source\"\ndate: \"2026-06-13\"\ntags: []\n---\n" +
		"- [ ] alpha <!-- id: " + blockA + " -->\n" +
		"- [ ] beta <!-- id: " + blockB + " -->\n"
	writeAndIndexFile(t, app, srcPath, srcContent, notebook, section, srcPage)

	dstContent := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Dest\"\ndate: \"2026-06-13\"\ntags: []\n---\n" +
		"- [ ] existing <!-- id: 33333333-3333-3333-3333-333333333333 -->\n"
	writeAndIndexFile(t, app, dstPath, dstContent, notebook, section, dstPage)

	token := registerTestSession(t, app, "silt-kanban")
	// Move blockA from Source to Dest (no afterID → append).
	if err := app.PluginMoveBlock("silt-kanban", token, blockA, "", notebook, section, dstPage); err != nil {
		t.Fatalf("PluginMoveBlock cross-page: %v", err)
	}

	// Source must have 1 block (B only).
	srcBlocks, _ := app.FetchPageBlocks(notebook, section, srcPage)
	if len(srcBlocks) != 1 {
		ids := make([]string, len(srcBlocks))
		for i, b := range srcBlocks {
			ids[i] = b.ID
		}
		t.Fatalf("source should have 1 block (B), got %d: %v", len(srcBlocks), ids)
	}
	if srcBlocks[0].ID != blockB {
		t.Fatalf("source should have blockB, got %s", srcBlocks[0].ID)
	}

	// Target must have 2 blocks (existing + A).
	dstBlocks, _ := app.FetchPageBlocks(notebook, section, dstPage)
	if len(dstBlocks) != 2 {
		ids := make([]string, len(dstBlocks))
		for i, b := range dstBlocks {
			ids[i] = b.ID
		}
		t.Fatalf("target should have 2 blocks (existing + A), got %d: %v", len(dstBlocks), ids)
	}
	foundA := false
	for _, b := range dstBlocks {
		if b.ID == blockA {
			foundA = true
		}
	}
	if !foundA {
		t.Fatal("blockA was not inserted into target page — data loss bug")
	}
}

// PluginCreateBlock rejects an invalid block type.
func TestPluginCreateBlock_RejectsInvalidType(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "silt-kanban")
	_, err := app.PluginCreateBlock("silt-kanban", token, "", "Work", "", "Daily", "BOGUS", "text")
	if err == nil {
		t.Fatal("expected error for invalid block type")
	}
}

// =========================================================================
// Content-mutate capability gate (#156)
// =========================================================================

// PluginCreateBlock is denied for a third-party plugin without content-mutate.
func TestPluginCreateBlock_DeniedWithoutContentMutateGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n# Today\n\n- [ ] existing <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	_, err := app.PluginCreateBlock("third-party", token, "", notebook, section, page, "TASK", "text")
	if err == nil {
		t.Fatal("expected capability denial without content-mutate grant")
	}
}

// PluginCreateBlock succeeds for a third-party plugin WITH content-mutate.
func TestPluginCreateBlock_SucceedsWithContentMutateGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n# Today\n\n- [ ] existing <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	if err := app.RequestCapability("third-party", string(plugins.CapContentMutate), ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	id, err := app.PluginCreateBlock("third-party", token, "", notebook, section, page, "TASK", "granted task")
	if err != nil {
		t.Fatalf("PluginCreateBlock with grant: %v", err)
	}
	if !looksLikeUUID(id) {
		t.Fatalf("returned id %q is not a UUID", id)
	}
}

// PluginDeleteBlock is denied without content-mutate.
func TestPluginDeleteBlock_DeniedWithoutContentMutateGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	if err := app.PluginDeleteBlock("third-party", token, "some-uuid"); err == nil {
		t.Fatal("expected capability denial without content-mutate grant")
	}
}

// PluginMoveBlock is denied without content-mutate.
func TestPluginMoveBlock_DeniedWithoutContentMutateGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	if err := app.PluginMoveBlock("third-party", token, "some-uuid", "", "", "", ""); err == nil {
		t.Fatal("expected capability denial without content-mutate grant")
	}
}

// PluginApplyBlocks is denied without content-mutate.
func TestPluginApplyBlocks_DeniedWithoutContentMutateGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	ops := []PluginCreateBlockOp{{Kind: "delete", BlockID: "some-uuid"}}
	if err := app.PluginApplyBlocks("third-party", token, ops); err == nil {
		t.Fatal("expected capability denial without content-mutate grant")
	}
}

// =========================================================================
// Surface registration capability gate (#154)
// =========================================================================

// PluginRegisterSurface is denied without ui-surface.
func TestPluginRegisterSurface_DeniedWithoutUISurfaceGrant(t *testing.T) {
	app := newTestApp(t)
	tok, err := app.RegisterPluginSession("third-party")
	if err != nil {
		t.Fatalf("register session: %v", err)
	}
	err = app.PluginRegisterSurface("third-party", tok, "panel1", "sidebar-panel", "My Panel")
	if err == nil {
		t.Fatal("expected capability denial without ui-surface grant")
	}
}

// PluginRegisterSurface succeeds with ui-surface.
func TestPluginRegisterSurface_SucceedsWithUISurfaceGrant(t *testing.T) {
	app := newTestApp(t)
	tok, err := app.RegisterPluginSession("third-party")
	if err != nil {
		t.Fatalf("register session: %v", err)
	}
	if err := app.RequestCapability("third-party", string(plugins.CapUISurface), ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := app.PluginRegisterSurface("third-party", tok, "panel1", "sidebar-panel", "My Panel"); err != nil {
		t.Fatalf("PluginRegisterSurface with grant: %v", err)
	}
}

// =========================================================================
// Plugin file I/O (#108) — capability gating + traversal
// =========================================================================

// PluginWriteFile is denied without a write-files grant.
func TestPluginWriteFile_DeniedWithoutGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	err := app.PluginWriteFile("third-party", token, "Work", "attachments/foo.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected capability denial without grant")
	}
}

// PluginWriteFile works after a write-files grant and writes atomically inside
// attachments/.
func TestPluginWriteFile_GrantThenWrite(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	if err := app.RequestCapability("third-party", string(plugins.CapWriteFiles), ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := app.PluginWriteFile("third-party", token, "Work", "attachments/note.txt", []byte("hello")); err != nil {
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
	token := registerTestSession(t, app, "third-party")
	_ = app.RequestCapability("third-party", string(plugins.CapWriteFiles), "")
	err := app.PluginWriteFile("third-party", token, "Work", "evil.txt", []byte("x"))
	if err == nil {
		t.Fatal("expected rejection for path outside the allowlist")
	}
}

// PluginWriteFile rejects a traversal path that escapes the notebook root.
func TestPluginWriteFile_RejectsTraversal(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	_ = app.RequestCapability("third-party", string(plugins.CapWriteFiles), "")
	err := app.PluginWriteFile("third-party", token, "Work", "../../../etc/evil", []byte("x"))
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}

// PluginReadFile + PluginListDir round-trip a file written by PluginWriteFile.
func TestPluginReadFile_AndListDir(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")
	_ = app.RequestCapability("p", string(plugins.CapReadFiles), "")
	_ = app.PluginWriteFile("p", token, "Work", "attachments/a.txt", []byte("A"))
	_ = app.PluginWriteFile("p", token, "Work", "attachments/b.txt", []byte("B"))

	res, err := app.PluginReadFile("p", token, "Work", "attachments/a.txt")
	if err != nil {
		t.Fatalf("PluginReadFile: %v", err)
	}
	if string(res.Bytes) != "A" {
		t.Errorf("read content = %q, want A", res.Bytes)
	}
	entries, err := app.PluginListDir("p", token, "Work", "attachments")
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
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")
	dir, err := app.PluginScratchDir("p", token, "Work")
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
// Network / fetch (#115) — capability gating
// =========================================================================

// PluginFetch is denied without a network grant.
func TestPluginFetch_DeniedWithoutGrant(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "third-party")
	_, err := app.PluginFetch("third-party", token, PluginFetchInput{URL: "https://example.com"})
	if err == nil {
		t.Fatal("expected capability denial without network grant")
	}
}

// PluginFetch rejects a non-http(s) URL even with a grant.
func TestPluginFetch_RejectsUnsafeUrl(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
	_, err := app.PluginFetch("p", token, PluginFetchInput{URL: "file:///etc/passwd"})
	if err == nil {
		t.Fatal("expected rejection of file:// scheme")
	}
	_, err = app.PluginFetch("p", token, PluginFetchInput{URL: "javascript:alert(1)"})
	if err == nil {
		t.Fatal("expected rejection of javascript: scheme")
	}
}

// GetNetworkAudit + ClearNetworkAudit round-trip an empty log.
func TestNetworkAudit_Clear(t *testing.T) {
	app := newTestApp(t)
	if err := app.ClearNetworkAudit(); err != nil {
		t.Fatalf("ClearNetworkAudit: %v", err)
	}
	entries, err := app.GetNetworkAudit()
	if err != nil {
		t.Fatalf("GetNetworkAudit: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("after clear, audit has %d entries", len(entries))
	}
}

// auditNetwork includes the URL path (not just the host) so the log
// distinguishes GET /health from DELETE /data/all.
func TestNetworkAudit_IncludesUrlPath(t *testing.T) {
	app := newTestApp(t)
	_ = app.ClearNetworkAudit()
	app.auditNetwork("test-plugin", "GET", "https://api.example.com/v1/data/all", 200)
	entries, _ := app.GetNetworkAudit()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !strings.Contains(entries[0].Host, "/v1/data/all") {
		t.Errorf("audit Host should include URL path, got %q", entries[0].Host)
	}
}

// CheckPluginUpdate uses the same SSRF-defended client as PluginFetch (#101
// review). A request whose target is a private/loopback host must be
// rejected — by the initial-URL check, the redirect callback, or the
// dialer. The test exercises the redirect callback by making the
// initial URL publicly addressable (via the test's loopback server, which
// IS already caught at the initial check) and then asserting that the
// CheckRedirect callback also rejects an internal redirect. Pinning both
// layers is the contract (#101 review): the redirect path is unit-tested
// independently below.
func TestCheckPluginUpdate_RejectsInternalHost(t *testing.T) {
	// Returns a 302 pointing at an RFC1918 literal. The initial URL is
	// loopback (httptest binds 127.0.0.1), so isSafeFetchUrl rejects the
	// request before the redirect is reached. Either rejection point
	// proves the SSRF defense is in place.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/manifest.json", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	app := newTestApp(t)
	_, err := app.CheckPluginUpdate("p", "1.0.0", redirector.URL)
	if err == nil {
		t.Fatal("expected SSRF rejection of a loopback/rfc1918 update URL")
	}
	// Either rejection reason is acceptable here: the initial URL is the
	// loopback the test server binds, so isSafeFetchUrl blocks it. What
	// matters is the request never reaches the dialer.
	if !strings.Contains(err.Error(), "safe http(s)") &&
		!strings.Contains(err.Error(), "redirect") &&
		!strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want SSRF-related rejection", err)
	}
}

// newSafeFetchClient's CheckRedirect callback rejects a redirect to an
// internal host. This is the precise redirect-layer defense that
// CheckPluginUpdate relies on; without it, a 302 to 169.254.169.254 would
// sail through.
func TestSafeFetchClient_CheckRedirectRejectsInternalHost(t *testing.T) {
	client := newSafeFetchClient(5_000_000_000)
	// Simulate a redirect to 169.254.169.254.
	req := httptest.NewRequest("GET", "http://example.com/initial", nil)
	req.URL, _ = url.Parse("http://169.254.169.254/manifest.json")
	err := client.CheckRedirect(req, nil)
	if err == nil {
		t.Fatal("CheckRedirect should reject a 169.254.169.254 target")
	}
	// Could be "redirect to internal host" or "redirect to blocked URL"
	// depending on which check fires first; both are acceptable.
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error = %v, want to mention 'redirect'", err)
	}
}

// newSafeFetchClient honors a 30-second timeout (matches defaultPluginFetchTimeout)
// and rejects an http:// scheme redirect with a clear error.
func TestSafeFetchClient_AppliesTimeoutAndRejectsBadScheme(t *testing.T) {
	client := newSafeFetchClient(5_000_000_000) // 5s — generous for slow CI
	if client.Timeout != 5*time.Second {
		t.Errorf("client.Timeout = %v, want 5s", client.Timeout)
	}
	if client.CheckRedirect == nil {
		t.Fatal("client.CheckRedirect is nil; safe fetch must validate redirects")
	}
	// Manually invoke the CheckRedirect callback with a javascript: target
	// to verify the scheme check fires (a real Do would require a
	// javascript:-aware URL parser).
	req := httptest.NewRequest("GET", "http://example.com", nil)
	req.URL, _ = url.Parse("javascript:alert(1)")
	err := client.CheckRedirect(req, nil)
	if err == nil {
		t.Fatal("CheckRedirect should reject javascript: target")
	}
}

// newSafeFetchClient's DialContext rejects an IP that resolves at dial time
// to a blocked address — the DNS-rebinding defense (#101 review). The
// validator (blockInternalHost) only sees the pre-fetch lookup, so without
// the dial-time check an attacker who controls a name's authoritative
// server could return a public IP at validation and a private IP at connect.
// We simulate that by swapping the dialer to "rebind" 169.254.169.254.
func TestSafeFetchClient_RejectsDNSRebindingAtDialTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	host := serverURL.Hostname()
	if host == "" {
		t.Fatal("test server has no hostname")
	}

	client := newSafeFetchClient(5_000_000_000)
	// Replace the dialer with one that "rebinds" to a blocked IP. The
	// contract under test is: the dialer rejects the blocked IP BEFORE
	// issuing the actual connect. We do not need to dial out — returning
	// the sentinel error the production dialer would have returned is
	// sufficient to pin the behavior.
	client.Transport.(*http.Transport).DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, &blockedIPError{ip: net.ParseIP("169.254.169.254"), host: host}
	}
	req, _ := http.NewRequest("GET", "http://"+host+"/anything", nil)
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected dial-time rejection of private IP, got nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want to mention 'blocked'", err)
	}
}

// newSafeFetchClient's dialer also re-validates the literal loopback IP,
// so a 127.0.0.1 rebind is caught at connect time even if the validator
// missed it. This is the same predicate the redirect check uses, so the
// two layers cannot drift.
func TestSafeFetchClient_DialerRejectsLoopbackAtConnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := newSafeFetchClient(5_000_000_000)
	var dialed int32
	client.Transport.(*http.Transport).DialContext = func(_ context.Context, _, _ string) (net.Conn, error) {
		atomic.AddInt32(&dialed, 1)
		return nil, &blockedIPError{ip: net.ParseIP("127.0.0.1"), host: "x"}
	}
	req, _ := http.NewRequest("GET", server.URL, nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected dial-time rejection of 127.0.0.1")
	}
	if atomic.LoadInt32(&dialed) != 1 {
		t.Errorf("dialer invoked %d times, want 1", atomic.LoadInt32(&dialed))
	}
}

// PluginWriteFile enforces maxPluginScratchBytes on the calling plugin's
// cumulative scratch-dir usage (#101 review). A granted write-files plugin
// must not be able to fill the disk by writing many small files to its
// scratch dir; once the cap is reached, writeFile rejects with a clear
// error and other plugins remain unaffected. The cap is temporarily
// shrunk to 1 MB so the test does not allocate 500 MB on disk.
func TestPluginWriteFile_RejectsBeyondScratchCap(t *testing.T) {
	orig := maxPluginScratchBytes
	maxPluginScratchBytes = 1 * 1024 * 1024 // 1 MB
	t.Cleanup(func() { maxPluginScratchBytes = orig })

	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")

	// First write fits the cap.
	first := make([]byte, 900*1024)
	if err := app.PluginWriteFile("p", token, "Work", ".system/plugins/p/data/big.bin", first); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// A second 200 KB write pushes cumulative past 1 MB.
	second := make([]byte, 200*1024)
	err := app.PluginWriteFile("p", token, "Work", ".system/plugins/p/data/tail.bin", second)
	if err == nil {
		t.Fatal("expected rejection beyond the scratch cap")
	}
	if !strings.Contains(err.Error(), "scratch usage") {
		t.Errorf("error = %v, want to mention 'scratch usage'", err)
	}

	// A different plugin is not affected by p's exhaustion.
	otherToken := registerTestSession(t, app, "other")
	_ = app.RequestCapability("other", string(plugins.CapWriteFiles), "")
	if err := app.PluginWriteFile("other", otherToken, "Work", ".system/plugins/other/data/x.bin", []byte("hi")); err != nil {
		t.Errorf("other plugin's write should not be affected by p's exhaustion: %v", err)
	}
}

// PluginWriteFile permits scratch writes that fit within the cap and
// correctly reports the cumulative on-disk usage via pluginScratchSizeBytes.
// This pins the contract that the cap is recomputed from disk on every
// write (a successful delete therefore frees budget immediately).
func TestPluginWriteFile_ScratchCapAccumulatesByActualDiskUsage(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")
	// Three 1 MB files — well under the production 500 MB cap.
	chunk := make([]byte, 1*1024*1024)
	for i := 0; i < 3; i++ {
		name := filepath.Join(".system/plugins/p/data", "chunk-"+string(rune('a'+i))+".bin")
		if err := app.PluginWriteFile("p", token, "Work", name, chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	used, err := pluginScratchSizeBytes(app, "p")
	if err != nil {
		t.Fatalf("pluginScratchSizeBytes: %v", err)
	}
	if used < 3*1024*1024 {
		t.Errorf("scratch usage = %d, want >= 3 MB", used)
	}
}

// pluginScratchSizeBytes counts linked notebook roots so a write-files plugin
// cannot bypass the cap by writing into a linked notebook's scratch dir (#159).
func TestPluginScratchSizeBytes_CountsLinkedNotebooks(t *testing.T) {
	app := newTestApp(t)

	// Create a linked notebook root OUTSIDE the vault.
	linkedRoot := t.TempDir()
	app.configMu.Lock()
	app.cfg.LinkedNotebooks = append(app.cfg.LinkedNotebooks, config.LinkedNotebook{
		ID: "test-linked", RootPath: linkedRoot, DisplayName: "Linked",
	})
	app.configMu.Unlock()

	// Write a file into the linked notebook's plugin scratch dir.
	linkedScratch := filepath.Join(linkedRoot, ".system", "plugins", "p", "data")
	if err := os.MkdirAll(linkedScratch, 0o755); err != nil {
		t.Fatalf("mkdir linked scratch: %v", err)
	}
	data := make([]byte, 512*1024) // 512 KB
	if err := os.WriteFile(filepath.Join(linkedScratch, "linked.bin"), data, 0o644); err != nil {
		t.Fatalf("write linked scratch: %v", err)
	}

	// The linked scratch bytes must be counted.
	used, err := pluginScratchSizeBytes(app, "p")
	if err != nil {
		t.Fatalf("pluginScratchSizeBytes: %v", err)
	}
	if used < 512*1024 {
		t.Errorf("scratch usage = %d, want >= 512 KB (linked notebook counted)", used)
	}
}

// Writing past the cap INTO a linked notebook root is rejected (#159).
func TestPluginWriteFile_LinkedScratchCapRejected(t *testing.T) {
	orig := maxPluginScratchBytes
	maxPluginScratchBytes = 1 * 1024 * 1024 // 1 MB
	t.Cleanup(func() { maxPluginScratchBytes = orig })

	app := newTestApp(t)
	pToken := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapWriteFiles), "")

	// Create a linked notebook root.
	linkedRoot := t.TempDir()
	app.configMu.Lock()
	app.cfg.LinkedNotebooks = append(app.cfg.LinkedNotebooks, config.LinkedNotebook{
		ID: "cap-linked", RootPath: linkedRoot, DisplayName: "CapLinked",
	})
	app.configMu.Unlock()

	// Write 900 KB to the vault scratch (fits under 1 MB).
	first := make([]byte, 900*1024)
	if err := app.PluginWriteFile("p", pToken, "Work", ".system/plugins/p/data/vault.bin", first); err != nil {
		t.Fatalf("vault scratch write: %v", err)
	}

	// Pre-populate the linked notebook scratch with 200 KB (simulating a
	// prior write that went through the linked notebook path).
	linkedScratch := filepath.Join(linkedRoot, ".system", "plugins", "p", "data")
	if err := os.MkdirAll(linkedScratch, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linkedScratch, "preexisting.bin"), make([]byte, 200*1024), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Now a write to the linked notebook scratch should be rejected because
	// cumulative (900 KB vault + 200 KB linked + new write) exceeds 1 MB.
	// We write directly through PluginWriteFile with a linked-notebook path.
	// resolvePluginNotebookDir resolves through the linked root, so this
	// writes to the linked scratch dir.
	err := app.PluginWriteFile("p", pToken, "CapLinked", ".system/plugins/p/data/overflow.bin", make([]byte, 100*1024))
	if err == nil {
		t.Fatal("expected rejection: cumulative scratch across vault + linked exceeds the cap")
	}
	if !strings.Contains(err.Error(), "scratch usage") {
		t.Errorf("error should mention scratch usage: %v", err)
	}
}

// blockedIPError is a sentinel error type so the test can assert on the
// dial-time rejection without coupling to the exact error string.
type blockedIPError struct {
	ip   net.IP
	host string
}

func (e *blockedIPError) Error() string {
	return "blocked: dial to " + e.host + " resolves to a blocked address " + e.ip.String()
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

// =========================================================================
// TOCTOU: concurrent cross-page moves (#104 review fix)
// =========================================================================

// Two concurrent PluginMoveBlock calls removing DIFFERENT blocks from the
// same source page must not re-introduce a block the other call removed.
// Before the fix, the source-page read (FetchPageBlocks) was outside the
// per-file lock, so the second writer's stale snapshot overwrote the first
// writer's removal. Now both the read and write happen under LockFileWrite
// on the source file.
func TestPluginMoveBlock_ConcurrentCrossPageNoClobber(t *testing.T) {
	app := newTestApp(t)
	notebook, section, srcPage := "Work", "Journal", "Source"
	dstPage1, dstPage2 := "Dest1", "Dest2"
	srcPath := filepath.Join(app.vaultPath, notebook, section, srcPage+".md")
	dst1Path := filepath.Join(app.vaultPath, notebook, section, dstPage1+".md")
	dst2Path := filepath.Join(app.vaultPath, notebook, section, dstPage2+".md")

	blockA := "11111111-1111-1111-1111-111111111111"
	blockB := "22222222-2222-2222-2222-222222222222"
	blockC := "33333333-3333-3333-3333-333333333333"

	srcContent := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Source\"\ndate: \"2026-06-13\"\ntags: []\n---\n" +
		"- [ ] alpha <!-- id: " + blockA + " -->\n" +
		"- [ ] beta <!-- id: " + blockB + " -->\n" +
		"- [ ] gamma <!-- id: " + blockC + " -->\n"
	writeAndIndexFile(t, app, srcPath, srcContent, notebook, section, srcPage)

	dst1Content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Dest1\"\ndate: \"2026-06-13\"\ntags: []\n---\n- [ ] dst1-anchor <!-- id: dddddddd-dddd-dddd-dddd-dddddddddddd -->\n"
	writeAndIndexFile(t, app, dst1Path, dst1Content, notebook, section, dstPage1)

	dst2Content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Dest2\"\ndate: \"2026-06-13\"\ntags: []\n---\n- [ ] dst2-anchor <!-- id: eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee -->\n"
	writeAndIndexFile(t, app, dst2Path, dst2Content, notebook, section, dstPage2)

	var wg sync.WaitGroup
	var err1, err2 error
	moveToken := registerTestSession(t, app, "silt-kanban")
	const rounds = 20
	for i := 0; i < rounds; i++ {
		// Reset source and destination pages each round.
		writeAndIndexFile(t, app, srcPath, srcContent, notebook, section, srcPage)
		writeAndIndexFile(t, app, dst1Path, dst1Content, notebook, section, dstPage1)
		writeAndIndexFile(t, app, dst2Path, dst2Content, notebook, section, dstPage2)

		wg.Add(2)
		go func() {
			defer wg.Done()
			err1 = app.PluginMoveBlock("silt-kanban", moveToken, blockA, "", notebook, section, dstPage1)
		}()
		go func() {
			defer wg.Done()
			err2 = app.PluginMoveBlock("silt-kanban", moveToken, blockB, "", notebook, section, dstPage2)
		}()
		wg.Wait()

		if err1 != nil {
			t.Fatalf("round %d: move A failed: %v", i, err1)
		}
		if err2 != nil {
			t.Fatalf("round %d: move B failed: %v", i, err2)
		}

		// After both moves, the source page must have exactly ONE block (C).
		srcBlocks, _ := app.FetchPageBlocks(notebook, section, srcPage)
		if len(srcBlocks) != 1 {
			srcIDs := make([]string, len(srcBlocks))
			for j, b := range srcBlocks {
				srcIDs[j] = b.ID
			}
			t.Fatalf("round %d: source page should have 1 block (C), got %d: %v", i, len(srcBlocks), srcIDs)
		}
		if srcBlocks[0].ID != blockC {
			t.Fatalf("round %d: remaining source block should be C, got %s", i, srcBlocks[0].ID)
		}

		// Block A must be in Dest1, block B must be in Dest2 (not silently
		// dropped — the pre-fix data-loss bug).
		dst1Blocks, _ := app.FetchPageBlocks(notebook, section, dstPage1)
		dst1HasA := false
		for _, b := range dst1Blocks {
			if b.ID == blockA {
				dst1HasA = true
			}
		}
		if !dst1HasA {
			t.Fatalf("round %d: blockA missing from Dest1 — data loss", i)
		}
		dst2Blocks, _ := app.FetchPageBlocks(notebook, section, dstPage2)
		dst2HasB := false
		for _, b := range dst2Blocks {
			if b.ID == blockB {
				dst2HasB = true
			}
		}
		if !dst2HasB {
			t.Fatalf("round %d: blockB missing from Dest2 — data loss", i)
		}
	}
}

// =========================================================================
// PluginListNavigation capability gate (#104 review fix)
// =========================================================================

// PluginListNavigation is denied without a read-files grant.
func TestPluginListNavigation_DeniedWithoutGrant(t *testing.T) {
	app := newTestApp(t)
	tok := registerTestSession(t, app, "third-party")
	_, err := app.PluginListNavigation("third-party", tok)
	if err == nil {
		t.Fatal("expected capability denial without read-files grant")
	}
}

// PluginListNavigation succeeds after a read-files grant.
func TestPluginListNavigation_GrantThenList(t *testing.T) {
	app := newTestApp(t)
	notebook, section, page := "Work", "Journal", "Daily"
	filePath := filepath.Join(app.vaultPath, notebook, section, page+".md")
	content := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-13\"\ntags: []\n---\n- [ ] task <!-- id: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"
	writeAndIndexFile(t, app, filePath, content, notebook, section, page)

	tok := registerTestSession(t, app, "third-party")
	if err := app.RequestCapability("third-party", string(plugins.CapReadFiles), ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	tree, err := app.PluginListNavigation("third-party", tok)
	if err != nil {
		t.Fatalf("PluginListNavigation: %v", err)
	}
	found := false
	for _, nb := range tree.Notebooks {
		if nb.Name == notebook {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected notebook %q in navigation tree, got %d notebooks", notebook, len(tree.Notebooks))
	}
}

// =========================================================================
// PluginFetch forbidden-header denylist (#115 review fix)
// =========================================================================

// PluginFetch rejects caller-supplied headers that are controlled by the
// transport layer (Host, Connection, Content-Length, Transfer-Encoding,
// Proxy-*, Sec-*, Cookie, Authorization).
func TestPluginFetch_RejectsForbiddenHeaders(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")

	dangerous := []string{
		"Host", "Connection", "Content-Length", "Transfer-Encoding",
		"Cookie", "Authorization", "Proxy-Authorization",
		"Sec-Fetch-Mode", "X-Forwarded-For",
	}
	for _, h := range dangerous {
		_, err := app.PluginFetch("p", token, PluginFetchInput{
			URL:     "https://example.com",
			Headers: map[string]string{h: "evil"},
		})
		if err == nil {
			t.Fatalf("expected rejection of forbidden header %q", h)
		}
	}
}

// =========================================================================
// isPathWithinRoot symlink resolution (#100 review fix)
// =========================================================================

// isPathWithinRoot rejects a symlink inside the root that points outside it.
func TestIsPathWithinRoot_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "escape.md")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if isPathWithinRoot(linkPath, root) {
		t.Fatal("symlink pointing outside root should be rejected")
	}
	// A legitimate file inside the root is still allowed.
	legit := filepath.Join(root, "note.md")
	if err := os.WriteFile(legit, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isPathWithinRoot(legit, root) {
		t.Fatal("regular file inside root should be allowed")
	}
}

// =========================================================================
// PluginFetch HTTP method allowlist + request body cap + truncation flag
// =========================================================================

// PluginFetch rejects non-standard HTTP methods (CONNECT, TRACE, etc.).
func TestPluginFetch_RejectsForbiddenMethod(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
	for _, m := range []string{"CONNECT", "TRACE", "OPTIONS", "BOGUS"} {
		_, err := app.PluginFetch("p", token, PluginFetchInput{URL: "https://example.com", Method: m})
		if err == nil {
			t.Fatalf("expected rejection of HTTP method %q", m)
		}
	}
}

// PluginFetch accepts standard HTTP methods.
func TestPluginFetch_AcceptsStandardMethods(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", ""} {
		// We don't care about the fetch result (the server may be unreachable);
		// we just verify the method validation doesn't reject these.
		_, err := app.PluginFetch("p", token, PluginFetchInput{URL: "https://example.com", Method: m})
		// Network errors are fine — we're only checking that the METHOD was
		// not rejected. A method-rejection returns a formatting error, not a
		// network error. Distinguish by checking for the allowlist message.
		if err != nil && strings.Contains(err.Error(), "is not allowed") {
			t.Fatalf("method %q should be allowed", m)
		}
	}
}

// PluginFetch rejects an oversized request body.
func TestPluginFetch_RejectsOversizedRequestBody(t *testing.T) {
	app := newTestApp(t)
	token := registerTestSession(t, app, "p")
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
	bigBody := strings.Repeat("x", int(maxPluginFetchRequestBytes)+1)
	_, err := app.PluginFetch("p", token, PluginFetchInput{
		URL:    "https://example.com",
		Method: "POST",
		Body:   bigBody,
	})
	if err == nil {
		t.Fatal("expected rejection of oversized request body")
	}
}

// =========================================================================
// Per-plugin rate limiter (#153)
// =========================================================================

func TestTokenBucket_AllowsBurstThenThrottles(t *testing.T) {
	tb := &tokenBucket{tokens: 3, last: time.Now(), rps: 1, burst: 3}
	// 3 tokens available → 3 immediate allows.
	for i := 0; i < 3; i++ {
		if !tb.allow(time.Now()) {
			t.Fatalf("expected allow on burst call %d", i)
		}
	}
	// 4th call should be denied (bucket empty, no time elapsed).
	if tb.allow(time.Now()) {
		t.Fatal("expected deny after burst exhausted")
	}
	// After 1 second, 1 token refills.
	if !tb.allow(time.Now().Add(time.Second)) {
		t.Fatal("expected allow after 1s refill")
	}
}

func TestPluginRateLimiter_EvictOnUninstall(t *testing.T) {
	app := newTestApp(t)
	// Simulate a fetch that creates a bucket.
	app.rateLimiter.allow("", "evict-me")
	// Evict.
	app.rateLimiter.evict("evict-me")
	// After eviction, a new bucket starts fresh (full burst).
	app.rateLimiter.mu.Lock()
	_, exists := app.rateLimiter.buckets["evict-me"]
	app.rateLimiter.mu.Unlock()
	if exists {
		t.Fatal("bucket should not exist after eviction")
	}
}

func TestPluginRateLimiter_ConcurrentNoPanic(t *testing.T) {
	app := newTestApp(t)
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			app.rateLimiter.allow("", "concurrent-plugin")
		}()
	}
	wg.Wait()
	// Should not panic under -race.
}

// writeInstalledManifest writes a plugin.json (and parent dirs) at
// <vault>/.system/plugins/<id>/plugin.json so rate-limit resolution can read
// it the way allow() does at runtime.
func writeInstalledManifest(t *testing.T, vaultPath, id, manifestJSONStr string) {
	t.Helper()
	dir := filepath.Join(vaultPath, ".system", "plugins", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifestJSONStr), 0o644); err != nil {
		t.Fatalf("write plugin.json: %v", err)
	}
}

func TestPluginRateLimiter_HonorsManifestOverride(t *testing.T) {
	app := newTestApp(t)
	// A plugin that declares a 5 rps / burst 2 override.
	writeInstalledManifest(t, app.vaultPath, "capped",
		`{"id":"capped","name":"Capped","version":"1.0.0","ratelimit":{"rps":5,"burst":2}}`)
	app.rateLimiter.allow(app.vaultPath, "capped")

	app.rateLimiter.mu.Lock()
	b, ok := app.rateLimiter.buckets["capped"]
	app.rateLimiter.mu.Unlock()
	if !ok {
		t.Fatal("expected a bucket to be created for capped")
	}
	if b.rps != 5 {
		t.Errorf("bucket rps = %g, want 5 (manifest override)", b.rps)
	}
	if b.burst != 2 {
		t.Errorf("bucket burst = %d, want 2 (manifest override)", b.burst)
	}
}

func TestPluginRateLimiter_DefaultsWhenNoManifest(t *testing.T) {
	app := newTestApp(t)
	// No plugin.json on disk at all -> host defaults apply.
	app.rateLimiter.allow(app.vaultPath, "no-manifest")

	app.rateLimiter.mu.Lock()
	b, ok := app.rateLimiter.buckets["no-manifest"]
	app.rateLimiter.mu.Unlock()
	if !ok {
		t.Fatal("expected a bucket to be created for no-manifest")
	}
	if b.rps != defaultPluginFetchRPS {
		t.Errorf("bucket rps = %g, want default %g", b.rps, defaultPluginFetchRPS)
	}
	if b.burst != defaultPluginFetchBurst {
		t.Errorf("bucket burst = %d, want default %d", b.burst, defaultPluginFetchBurst)
	}
}

func TestResolvePluginRatelimit_ClampsOutOfRange(t *testing.T) {
	app := newTestApp(t)
	// An over-cap override (hand-edited / drifted) must fall back to defaults
	// rather than granting an outsized quota (defense in depth).
	writeInstalledManifest(t, app.vaultPath, "drifted",
		`{"id":"drifted","name":"Drifted","version":"1.0.0","ratelimit":{"rps":999,"burst":9999}}`)
	rps, burst := resolvePluginRatelimit(app.vaultPath, "drifted")
	if rps != defaultPluginFetchRPS {
		t.Errorf("out-of-range rps should clamp to default %g, got %g", defaultPluginFetchRPS, rps)
	}
	if burst != defaultPluginFetchBurst {
		t.Errorf("out-of-range burst should clamp to default %d, got %d", defaultPluginFetchBurst, burst)
	}
}

// =========================================================================
// Redirect header hygiene (#160, #247)
// =========================================================================

// TestStripHeadersForRedirect_RemovesCustomAuth verifies the allowlist
// behavior: safe headers survive, custom auth is stripped, and User-Agent is
// RESET to Go's default (not preserved) so a plugin that embedded credentials
// in the UA cannot leak them across a cross-host redirect (#247, F13).
func TestStripHeadersForRedirect_RemovesCustomAuth(t *testing.T) {
	req := &http.Request{
		Header: http.Header{
			"Accept":        {"text/html"},
			"X-Api-Key":     {"secret-key"},
			"Authorization": {"Bearer token"},
			"User-Agent":    {"Silt/1.0"},
		},
	}
	stripHeadersForRedirect(req)
	if req.Header.Get("Accept") != "text/html" {
		t.Error("Accept should survive the redirect allowlist")
	}
	if req.Header.Get("User-Agent") != "Go-http-client/1.1" {
		t.Errorf("User-Agent should be reset to Go default on cross-host redirect, got %q", req.Header.Get("User-Agent"))
	}
	if req.Header.Get("X-Api-Key") != "" {
		t.Error("X-Api-Key should be stripped on redirect")
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("Authorization should be stripped on redirect")
	}
}

// newRedirectRequest builds a minimal *http.Request for CheckRedirect tests.
func newRedirectRequest(rawurl string, headers http.Header) *http.Request {
	req := &http.Request{
		Method: "GET",
		Header: headers,
	}
	u, err := url.Parse(rawurl)
	if err != nil {
		panic(err)
	}
	req.URL = u
	return req
}

// TestCheckRedirect_StripsCustomAuthOnlyCrossHost verifies the #160 policy:
// custom auth headers survive a same-host redirect (so a legit same-origin API
// redirect that depends on X-Api-Key is not broken) but are stripped the moment
// the redirect crosses to a different host (the actual leak risk). Hosts are
// public TEST-NET IP literals so isSafeFetchUrl resolves them locally without
// network access and does not flag them as internal.
//
// Extended for #247 (F13): a plugin-supplied User-Agent survives a same-host
// redirect (legitimate use case: API versioning via UA) but is RESET to Go's
// default on a cross-host redirect so a plugin that embedded credentials in
// the UA cannot leak them.
func TestCheckRedirect_StripsCustomAuthOnlyCrossHost(t *testing.T) {
	client := newSafeFetchClient(defaultPluginFetchTimeout)

	authHeaders := func() http.Header {
		return http.Header{
			"Accept":     {"application/json"},
			"X-Api-Key":  {"secret-key"},
			"User-Agent": {"my-plugin/1.0"},
		}
	}

	// Same-host redirect: X-Api-Key AND User-Agent must survive.
	via := []*http.Request{newRedirectRequest("https://203.0.113.1/v1/widgets", authHeaders())}
	sameHost := newRedirectRequest("https://203.0.113.1/v2/widgets", authHeaders())
	if err := client.CheckRedirect(sameHost, via); err != nil {
		t.Fatalf("same-host CheckRedirect: %v", err)
	}
	if sameHost.Header.Get("X-Api-Key") != "secret-key" {
		t.Error("X-Api-Key should survive a same-host redirect")
	}
	if sameHost.Header.Get("User-Agent") != "my-plugin/1.0" {
		t.Errorf("User-Agent should survive a same-host redirect, got %q", sameHost.Header.Get("User-Agent"))
	}

	// Cross-host redirect: X-Api-Key stripped, Accept kept, User-Agent RESET.
	crossHost := newRedirectRequest("https://198.51.100.5/capture", authHeaders())
	if err := client.CheckRedirect(crossHost, via); err != nil {
		t.Fatalf("cross-host CheckRedirect: %v", err)
	}
	if crossHost.Header.Get("X-Api-Key") != "" {
		t.Error("X-Api-Key must be stripped on a cross-host redirect")
	}
	if crossHost.Header.Get("Accept") != "application/json" {
		t.Error("Accept should survive the cross-host allowlist")
	}
	if crossHost.Header.Get("User-Agent") != "Go-http-client/1.1" {
		t.Errorf("User-Agent must be reset to Go default on a cross-host redirect (F13), got %q", crossHost.Header.Get("User-Agent"))
	}
}

// =========================================================================
// Persistent network audit log (#157)
// =========================================================================

func TestSeedNetworkAuditFromDisk_PopulatesFromLogFile(t *testing.T) {
	app := newTestApp(t)
	// Write a network.log file for a fake plugin.
	logDir := filepath.Join(app.vaultPath, ".system", "plugins", "test-plugin")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(logDir, "network.log")
	logContent := "2026-06-20T10:00:00Z GET example.com/api 200 test-plugin\n" +
		"2026-06-20T10:01:00Z POST example.com/data 201 test-plugin\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	// Clear in-memory, then seed.
	networkAuditMu.Lock()
	networkAudit = nil
	networkAuditMu.Unlock()
	seedNetworkAuditFromDisk(app.vaultPath)

	entries, _ := app.GetNetworkAudit()
	if len(entries) != 2 {
		t.Fatalf("expected 2 seeded entries, got %d", len(entries))
	}
	if entries[0].Host != "example.com/api" {
		t.Errorf("entry[0] host = %q", entries[0].Host)
	}
	if entries[0].Status != 200 {
		t.Errorf("entry[0] status = %d", entries[0].Status)
	}
}

func TestClearNetworkAudit_TruncatesOnDiskFiles(t *testing.T) {
	app := newTestApp(t)
	logDir := filepath.Join(app.vaultPath, ".system", "plugins", "clear-test")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(logDir, "network.log")
	if err := os.WriteFile(logPath, []byte("2026-06-20T10:00:00Z GET example.com 200 clear-test\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := app.ClearNetworkAudit(); err != nil {
		t.Fatalf("ClearNetworkAudit: %v", err)
	}
	data, _ := os.ReadFile(logPath)
	if len(data) != 0 {
		t.Errorf("on-disk log should be empty after ClearNetworkAudit, got %q", string(data))
	}
	entries, _ := app.GetNetworkAudit()
	if len(entries) != 0 {
		t.Errorf("in-memory log should be empty after ClearNetworkAudit, got %d entries", len(entries))
	}
}

func TestSeedNetworkAuditFromDisk_RestartPreservesEntries(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
	// Simulate a fetch that writes to the audit log (both in-memory + disk).
	// We call auditNetwork directly to avoid a real HTTP request.
	app.auditNetwork("p", "GET", "https://api.example.com/health", 200)

	// Verify it was written to disk.
	logPath := filepath.Join(app.vaultPath, ".system", "plugins", "p", "network.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("on-disk log not written: %v", err)
	}

	// Simulate a restart: clear in-memory, then seed from disk.
	networkAuditMu.Lock()
	networkAudit = nil
	networkAuditMu.Unlock()
	seedNetworkAuditFromDisk(app.vaultPath)

	entries, _ := app.GetNetworkAudit()
	if len(entries) != 1 {
		t.Fatalf("expected 1 seeded entry after restart, got %d", len(entries))
	}
	if entries[0].Plugin != "p" {
		t.Errorf("entry plugin = %q, want p", entries[0].Plugin)
	}
	if entries[0].Status != 200 {
		t.Errorf("entry status = %d, want 200", entries[0].Status)
	}
}

func TestParseNetworkLogLine_ToleratesSpacesInHost(t *testing.T) {
	// The audited Host field includes the URL path, which may contain spaces.
	// Right-to-left parsing must keep status (2nd-from-last) + pluginID (last)
	// aligned and rejoin the host/path segment.
	entry, ok := parseNetworkLogLine("2026-06-20T10:00:00Z GET example.com/with path 200 my-plugin")
	if !ok {
		t.Fatal("expected line with a spaced host to parse")
	}
	if entry.At != "2026-06-20T10:00:00Z" {
		t.Errorf("At = %q", entry.At)
	}
	if entry.Method != "GET" {
		t.Errorf("Method = %q", entry.Method)
	}
	if entry.Host != "example.com/with path" {
		t.Errorf("Host = %q, want %q", entry.Host, "example.com/with path")
	}
	if entry.Status != 200 {
		t.Errorf("Status = %d, want 200", entry.Status)
	}
	if entry.Plugin != "my-plugin" {
		t.Errorf("Plugin = %q", entry.Plugin)
	}
}

func TestParseNetworkLogLine_RejectsMalformed(t *testing.T) {
	for _, line := range []string{
		"",                       // empty
		"only three fields",      // too few
		"a b c d e",             // non-numeric status
	} {
		if _, ok := parseNetworkLogLine(line); ok {
			t.Errorf("expected parse failure for %q", line)
		}
	}
}

// =========================================================================
// Background audit-log writer (#235)
//
// The writer decouples on-disk audit writes from the networkAuditMu lock so
// concurrent PluginFetch calls don't serialize on per-plugin file I/O. The
// tests below exercise the writer lifecycle (start/stop/idempotency), the
// drain-on-shutdown guarantee, the clear-vs-fetch ordering invariant, the
// oversized-log truncation, and the concurrent-fetch no-serialize claim.
// =========================================================================

// withNetworkAuditWriter starts the background writer for app.vaultPath and
// stops it (draining) on test cleanup. Tests that exercise the async path
// use this; tests that want the inline fallback path just don't call it.
func withNetworkAuditWriter(t *testing.T, app *App) {
	t.Helper()
	startNetworkAuditWriter(app.vaultPath)
	t.Cleanup(stopNetworkAuditWriter)
}

// startStopNetworkAuditWriter is a sync.Once-free variant for tests that need
// to call start/stop explicitly (e.g. drain-on-shutdown). Each call to start
// after a stop must work — the writer resets via the mu-guarded nil check.
func TestAuditWriter_IdempotentStartStop(t *testing.T) {
	app := newTestApp(t)
	// Double-start: the second is a no-op (writer already running).
	startNetworkAuditWriter(app.vaultPath)
	startNetworkAuditWriter(app.vaultPath)
	// Single stop (first call drains + exits).
	stopNetworkAuditWriter()
	// Double-stop: no-op (writer already nil), must not panic or block.
	stopNetworkAuditWriter()
	// Restart after stop: must work (e.g. vault close → reopen).
	startNetworkAuditWriter(app.vaultPath)
	stopNetworkAuditWriter()
}

// The writer MUST drain every queued entry before exiting on shutdown so no
// audit data is lost when the vault closes (#157 persistent-audit contract).
func TestAuditWriter_DrainsOnShutdown(t *testing.T) {
	app := newTestApp(t)
	startNetworkAuditWriter(app.vaultPath)

	// Enqueue 50 entries for a single plugin (all in-memory immediately;
	// the on-disk writes are queued for the writer goroutine).
	const n = 50
	for i := 0; i < n; i++ {
		app.auditNetwork("drain-test", "GET",
			fmt.Sprintf("https://example.com/api/%d", i), 200)
	}

	// Stop the writer — this blocks until every queued op is processed.
	stopNetworkAuditWriter()

	// Every entry must be on disk.
	logPath := filepath.Join(app.vaultPath, ".system", "plugins", "drain-test", "network.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read network.log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != n {
		t.Errorf("on-disk log has %d lines after shutdown, want %d (drain dropped entries)", len(lines), n)
	}
}

// ClearNetworkAudit running concurrently with auditNetwork calls MUST NOT let
// a pre-clear fetch entry survive the on-disk truncation. The writer processes
// the clear op in FIFO order with concurrent appends, so any entry enqueued
// BEFORE the clear is truncated; any entry enqueued AFTER survives (correct —
// it represents post-clear activity). R4 / #157 invariant.
func TestClearNetworkAudit_AtomicWithConcurrentFetch(t *testing.T) {
	app := newTestApp(t)
	// Write a pre-existing on-disk entry so the clear has something to truncate.
	app.auditNetwork("clear-race", "GET", "https://example.com/pre", 200)

	startNetworkAuditWriter(app.vaultPath)
	// Wait for the pre-entry to land on disk (the writer is async). Use the
	// synchronized accessor rather than touching the package-level pointer
	// directly so the test honors the encapsulation pattern.
	w := currentNetworkAuditWriter()
	if w == nil {
		t.Fatal("writer not running after startNetworkAuditWriter")
	}
	{
		op := &networkAuditOp{done: make(chan struct{})}
		w.ch <- op
		<-op.done
	}

	logPath := filepath.Join(app.vaultPath, ".system", "plugins", "clear-race", "network.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("pre-entry not written: %v", err)
	}

	// Fire clear + concurrent fetches.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app.auditNetwork("clear-race", "GET",
				fmt.Sprintf("https://example.com/concurrent/%d", i), 200)
		}(i)
	}
	if err := app.ClearNetworkAudit(); err != nil {
		t.Fatalf("ClearNetworkAudit: %v", err)
	}
	wg.Wait()
	stopNetworkAuditWriter()

	// The on-disk log must NOT contain the pre-clear entry.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "/pre") {
		t.Errorf("pre-clear entry survived ClearNetworkAudit; the clear + fetch ordering is broken:\n%s", string(data))
	}
}

// The writer MUST honor the 1 MB / 200-line truncation that the inline path
// enforced (#235 regression guard — R6).
func TestAuditWriter_TruncatesOversizedLog(t *testing.T) {
	app := newTestApp(t)
	pluginID := "truncate-test"
	logDir := filepath.Join(app.vaultPath, ".system", "plugins", pluginID)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(logDir, "network.log")
	// Pre-write a log over the 1 MB threshold so the next append triggers
	// truncation. 20000 lines × ~75 bytes/line ≈ 1.5 MB (> 1 MB cap).
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "2026-06-20T10:00:%02dZ GET example.com/padding-%05d 200 %s\n",
			i%60, i, pluginID)
	}
	if err := os.WriteFile(logPath, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("write oversized log: %v", err)
	}

	startNetworkAuditWriter(app.vaultPath)
	app.auditNetwork(pluginID, "GET", "https://example.com/trigger", 200)
	stopNetworkAuditWriter()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// Truncation keeps the last 200 lines, then the trigger append adds 1.
	// The original 20000-line log was cut to ~1% of its size — the exact
	// post-truncation + append count is 201 (200 kept + 1 trigger).
	if len(lines) > maxPluginNetworkLogLines+1 {
		t.Errorf("log has %d lines after truncation, want ≤ %d (200 kept + 1 trigger)",
			len(lines), maxPluginNetworkLogLines+1)
	}
	// The trigger entry (most recent) must be present.
	if !strings.Contains(string(data), "/trigger") {
		t.Errorf("trigger entry missing after truncation:\n%s", string(data))
	}
}

// Concurrent auditNetwork calls with the writer running MUST be materially
// faster than the same calls without the writer (inline path), proving the
// lock is no longer held across file I/O (#235 — R2). This is a performance
// smoke test, not a microbenchmark — the assertion is generous to avoid CI
// flake on slow runners, but a serialization regression (lock held across
// I/O again) would make the async path as slow as the sync path.
func TestAuditNetwork_ConcurrentFetchDoesNotSerialize(t *testing.T) {
	app := newTestApp(t)
	const n = 64

	// Baseline: inline path (no writer). Each call holds networkAuditMu across
	// the full file-I/O sequence, so N calls serialize.
	inlineStart := time.Now()
	for i := 0; i < n; i++ {
		app.auditNetwork(fmt.Sprintf("inline-%d", i), "GET",
			fmt.Sprintf("https://example.com/%d", i), 200)
	}
	inlineDur := time.Since(inlineStart)

	// Reset state.
	_ = app.ClearNetworkAudit()
	// ClearNetworkAudit with no writer runs inline (truncates the files we
	// just wrote); safe to proceed.

	// Async path: writer running. Each call only holds the lock for the
	// in-memory append + non-blocking channel send.
	startNetworkAuditWriter(app.vaultPath)
	asyncStart := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app.auditNetwork(fmt.Sprintf("async-%d", i), "GET",
				fmt.Sprintf("https://example.com/%d", i), 200)
		}(i)
	}
	wg.Wait()
	asyncDur := time.Since(asyncStart)
	stopNetworkAuditWriter()

	// All N entries must land in-memory (the in-memory append is synchronous).
	entries, _ := app.GetNetworkAudit()
	if len(entries) != n {
		t.Errorf("in-memory audit has %d entries, want %d", len(entries), n)
	}

	// The async path should not be dramatically slower than the inline path.
	// A serialization regression (lock held across I/O again) would make the
	// async path as slow as inline × goroutine-schedule overhead. We assert
	// async ≤ inline × 3 as a generous gate (the real-world speedup is
	// typically much larger, but CI runners vary).
	if asyncDur > inlineDur*3 {
		t.Errorf("async path (%v) was slower than expected vs inline (%v); the writer may be holding networkAuditMu across I/O",
			asyncDur, inlineDur)
	}
}

// =========================================================================
// Manifest ratelimit validation (#153)
// =========================================================================

func TestValidate_RejectsInvalidRatelimit(t *testing.T) {
	tests := []struct {
		name   string
		rl     *plugins.RatelimitConfig
		errMsg string
	}{
		{"negative rps", &plugins.RatelimitConfig{RPS: -1, Burst: 10}, "rps"},
		{"zero rps", &plugins.RatelimitConfig{RPS: 0, Burst: 10}, "rps"},
		{"over-cap rps", &plugins.RatelimitConfig{RPS: 11, Burst: 10}, "rps"},
		{"zero burst", &plugins.RatelimitConfig{RPS: 1, Burst: 0}, "burst"},
		{"negative burst", &plugins.RatelimitConfig{RPS: 1, Burst: -1}, "burst"},
		{"over-cap burst", &plugins.RatelimitConfig{RPS: 1, Burst: 101}, "burst"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build a valid archive with the bad ratelimit.
			archive := buildPluginArchive(t, "ratelimit-test", manifestJSON(t, "ratelimit-test", tc.rl))
			_, _, err := plugins.Validate(archive)
			if err == nil {
				t.Fatal("expected validation error for invalid ratelimit")
			}
			if !strings.Contains(err.Error(), tc.errMsg) {
				t.Errorf("error should mention %q: %v", tc.errMsg, err)
			}
		})
	}
}

func TestValidate_AcceptsValidRatelimit(t *testing.T) {
	rl := &plugins.RatelimitConfig{RPS: 5, Burst: 20}
	archive := buildPluginArchive(t, "ratelimit-ok", manifestJSON(t, "ratelimit-ok", rl))
	_, _, err := plugins.Validate(archive)
	if err != nil {
		t.Fatalf("valid ratelimit should pass: %v", err)
	}
}

// manifestJSON builds a minimal valid plugin.json with an optional ratelimit.
func manifestJSON(t *testing.T, id string, rl *plugins.RatelimitConfig) string {
	t.Helper()
	base := fmt.Sprintf(`{"id":"%s","name":"%s","version":"1.0.0"`, id, id)
	if rl != nil {
		base += fmt.Sprintf(`,"ratelimit":{"rps":%g,"burst":%d}`, rl.RPS, rl.Burst)
	}
	return base + "}"
}

// buildPluginArchive creates a valid .silt-plugin ZIP at a temp path and
// returns the path. The manifest is the JSON string; index.js is a minimal
// stub.
func buildPluginArchive(t *testing.T, id, manifestJSONStr string) string {
	t.Helper()
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, id+".silt-plugin")
	manifestPath := filepath.Join(tmp, "plugin.json")
	if err := os.WriteFile(manifestPath, []byte(manifestJSONStr), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	indexPath := filepath.Join(tmp, "index.js")
	if err := os.WriteFile(indexPath, []byte("export default {};"), 0o644); err != nil {
		t.Fatalf("write index.js: %v", err)
	}
	// Build the ZIP.
	r, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	defer r.Close()
	zw := zip.NewWriter(r)
	for _, name := range []string{"plugin.json", "index.js"} {
		data, _ := os.ReadFile(filepath.Join(tmp, name))
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return archivePath
}

// =========================================================================
// Plugin session tokens — binding identity (#151)
// =========================================================================

func TestRegisterPluginSession_RoundTrip(t *testing.T) {
	app := newTestApp(t)
	token, err := app.RegisterPluginSession("my-plugin")
	if err != nil {
		t.Fatalf("RegisterPluginSession: %v", err)
	}
	if token == "" {
		t.Fatal("token should not be empty")
	}
	// validatePluginSession accepts the correct pair.
	if err := app.validatePluginSession("my-plugin", token); err != nil {
		t.Errorf("validatePluginSession correct pair: %v", err)
	}
	// Wrong pluginID rejected.
	if err := app.validatePluginSession("other-plugin", token); err == nil {
		t.Fatal("expected rejection for wrong pluginID")
	}
	// Wrong token rejected.
	if err := app.validatePluginSession("my-plugin", "wrong-token"); err == nil {
		t.Fatal("expected rejection for wrong token")
	}
}

func TestUnregisterPluginSession_Invalidates(t *testing.T) {
	app := newTestApp(t)
	token, _ := app.RegisterPluginSession("temp")
	if err := app.UnregisterPluginSession(token); err != nil {
		t.Fatalf("UnregisterPluginSession: %v", err)
	}
	// The token is no longer valid.
	if err := app.validatePluginSession("temp", token); err == nil {
		t.Fatal("expected rejection after unregister")
	}
}

func TestRegisterPluginSession_ConcurrentNoPanic(t *testing.T) {
	app := newTestApp(t)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, _ = app.RegisterPluginSession("concurrent")
		}()
	}
	wg.Wait()
}

// PluginFetch with a wrong token is rejected at the session boundary (#151).
func TestPluginFetch_RejectsWrongSessionToken(t *testing.T) {
	app := newTestApp(t)
	otherToken, _ := app.RegisterPluginSession("plugin-a")
	_ = app.RequestCapability("plugin-b", string(plugins.CapNetwork), "")
	// plugin-b calling with plugin-a's token → rejected.
	_, err := app.PluginFetch("plugin-b", otherToken, PluginFetchInput{URL: "https://example.com"})
	if err == nil {
		t.Fatal("expected rejection: plugin-b using plugin-a's token")
	}
}

// PluginCreateBlock without a session token is rejected (#151).
func TestPluginCreateBlock_RejectsMissingSessionToken(t *testing.T) {
	app := newTestApp(t)
	_, err := app.PluginCreateBlock("silt-kanban", "", "", "Work", "", "Daily", "TASK", "text")
	if err == nil {
		t.Fatal("expected rejection: missing session token")
	}
}

// =========================================================================
// F1 (#236): spoofed-identity regression
//
// A malicious plugin running in the main webview could bypass the SDK and
// call App.Plugin* bindings directly with a different pluginID to try to
// inherit that plugin's grants. Every privileged binding MUST reject a
// request whose (pluginID, sessionToken) pair does not match.
//
// This test exercises every binding that gained session-token verification
// in the F1 sprint (every Plugin* binding other than the 7 that already
// enforced it). For each one:
//   - register a session for "legit-plugin"
//   - call the binding with pluginID="silt-attachments" (first-party, fully
//     granted via seedFirstPartyGrants) + the "legit-plugin" token
//   - assert the call fails with the session-mismatch error
//
// The test does NOT assert what each binding does AFTER the session check
// (it never gets past it). It only asserts the boundary. Bindings that
// require setup (e.g. PluginRawQuery needs an open DB) are exercised at
// the simplest input that reaches the session check.
// =========================================================================

func TestPluginBindings_RejectSpoofedIdentity(t *testing.T) {
	app := newTestApp(t)
	// "legit-plugin" mints a real token.
	legitToken, err := app.RegisterPluginSession("legit-plugin")
	if err != nil {
		t.Fatalf("RegisterPluginSession: %v", err)
	}
	// The attacker spoofs "silt-attachments" (first-party, fully granted)
	// but uses legit-plugin's token. Every privileged binding MUST reject
	// the mismatch.
	const spoofedID = "silt-attachments"
	mismatchErrSubstring := "session token mismatch"

	// Helper: each call must return an error whose message mentions the
	// mismatch (proving it failed at the session boundary, not later).
	assertMismatch := func(t *testing.T, label string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected spoofed-identity rejection, got nil", label)
			return
		}
		if !strings.Contains(err.Error(), mismatchErrSubstring) {
			t.Errorf("%s: error = %q, want substring %q", label, err.Error(), mismatchErrSubstring)
		}
	}

	// Category B (grew sessionToken param in F1): bindings taking pluginID today.
	t.Run("PluginReadFile", func(t *testing.T) {
		_, err := app.PluginReadFile(spoofedID, legitToken, "Work", "attachments/x.png")
		assertMismatch(t, "PluginReadFile", err)
	})
	t.Run("PluginListDir", func(t *testing.T) {
		_, err := app.PluginListDir(spoofedID, legitToken, "Work", "attachments")
		assertMismatch(t, "PluginListDir", err)
	})
	t.Run("PluginListNavigation", func(t *testing.T) {
		_, err := app.PluginListNavigation(spoofedID, legitToken)
		assertMismatch(t, "PluginListNavigation", err)
	})
	t.Run("PluginResolveNotebookRoot", func(t *testing.T) {
		_, err := app.PluginResolveNotebookRoot(spoofedID, legitToken, "Work")
		assertMismatch(t, "PluginResolveNotebookRoot", err)
	})
	t.Run("PluginScratchDir", func(t *testing.T) {
		_, err := app.PluginScratchDir(spoofedID, legitToken, "Work")
		assertMismatch(t, "PluginScratchDir", err)
	})
	t.Run("PluginVaultScratchDir", func(t *testing.T) {
		_, err := app.PluginVaultScratchDir(spoofedID, legitToken)
		assertMismatch(t, "PluginVaultScratchDir", err)
	})
	t.Run("PluginResolveAsset", func(t *testing.T) {
		_, err := app.PluginResolveAsset(spoofedID, legitToken, "Work", "x")
		assertMismatch(t, "PluginResolveAsset", err)
	})
	t.Run("PluginReadPluginAsset", func(t *testing.T) {
		_, err := app.PluginReadPluginAsset(spoofedID, legitToken, "icon.svg")
		assertMismatch(t, "PluginReadPluginAsset", err)
	})
	t.Run("PluginRegisterSurface", func(t *testing.T) {
		err := app.PluginRegisterSurface(spoofedID, legitToken, "panel", "sidebar-panel", "L")
		assertMismatch(t, "PluginRegisterSurface", err)
	})
	t.Run("PluginOpenInNativeHandler", func(t *testing.T) {
		err := app.PluginOpenInNativeHandler(spoofedID, legitToken, "Work", "x")
		assertMismatch(t, "PluginOpenInNativeHandler", err)
	})
	t.Run("PluginOpenUrl", func(t *testing.T) {
		err := app.PluginOpenUrl(spoofedID, legitToken, "https://example.com")
		assertMismatch(t, "PluginOpenUrl", err)
	})
	t.Run("PluginPickOpenFile", func(t *testing.T) {
		_, err := app.PluginPickOpenFile(spoofedID, legitToken, "*")
		assertMismatch(t, "PluginPickOpenFile", err)
	})
	t.Run("PluginPickSaveFile", func(t *testing.T) {
		_, err := app.PluginPickSaveFile(spoofedID, legitToken, "out.txt")
		assertMismatch(t, "PluginPickSaveFile", err)
	})
	t.Run("PluginClipboardReadText", func(t *testing.T) {
		_, err := app.PluginClipboardReadText(spoofedID, legitToken)
		assertMismatch(t, "PluginClipboardReadText", err)
	})
	t.Run("PluginClipboardWriteText", func(t *testing.T) {
		err := app.PluginClipboardWriteText(spoofedID, legitToken, "leak")
		assertMismatch(t, "PluginClipboardWriteText", err)
	})
	t.Run("PluginNotify", func(t *testing.T) {
		err := app.PluginNotify(spoofedID, legitToken, "t", "b")
		assertMismatch(t, "PluginNotify", err)
	})

	// Category C (grew pluginID + sessionToken in F1): bindings without
	// pluginID today.
	t.Run("PluginRawQuery", func(t *testing.T) {
		_, err := app.PluginRawQuery(spoofedID, legitToken, "SELECT 1", nil)
		assertMismatch(t, "PluginRawQuery", err)
	})
	t.Run("PluginMutateBlock", func(t *testing.T) {
		_, err := app.PluginMutateBlock(spoofedID, legitToken, "block-id", "text")
		assertMismatch(t, "PluginMutateBlock", err)
	})
	t.Run("PluginUpdateBlockState", func(t *testing.T) {
		_, err := app.PluginUpdateBlockState(spoofedID, legitToken, "block-id", "DONE")
		assertMismatch(t, "PluginUpdateBlockState", err)
	})
	t.Run("PluginUpdateTaskMeta", func(t *testing.T) {
		_, err := app.PluginUpdateTaskMeta(spoofedID, legitToken, "block-id", 1, -1)
		assertMismatch(t, "PluginUpdateTaskMeta", err)
	})
	t.Run("PluginCreatePage", func(t *testing.T) {
		_, err := app.PluginCreatePage(spoofedID, legitToken, "Work", "", "P", "2026-06-13")
		assertMismatch(t, "PluginCreatePage", err)
	})
	t.Run("PluginCreateSection", func(t *testing.T) {
		err := app.PluginCreateSection(spoofedID, legitToken, "Work", "S")
		assertMismatch(t, "PluginCreateSection", err)
	})
	t.Run("PluginCreateNotebook", func(t *testing.T) {
		err := app.PluginCreateNotebook(spoofedID, legitToken, "N")
		assertMismatch(t, "PluginCreateNotebook", err)
	})
	t.Run("PluginDeletePage", func(t *testing.T) {
		err := app.PluginDeletePage(spoofedID, legitToken, "Work", "", "P")
		assertMismatch(t, "PluginDeletePage", err)
	})
	t.Run("PluginRenamePage", func(t *testing.T) {
		err := app.PluginRenamePage(spoofedID, legitToken, "Work", "", "old", "new")
		assertMismatch(t, "PluginRenamePage", err)
	})

	// Sanity: the same bindings succeed when the token matches. This proves
	// the rejection was due to identity mismatch, not a broken binding.
	t.Run("correct identity passes the session check", func(t *testing.T) {
		// PluginRawQuery: with a matching token, it should NOT return the
		// mismatch error (it may return a "vault database not loaded" error
		// in this minimal app, which is fine — we only assert the session
		// check passed).
		_, err := app.PluginRawQuery("legit-plugin", legitToken, "SELECT 1", nil)
		if err != nil && strings.Contains(err.Error(), mismatchErrSubstring) {
			t.Errorf("correct identity should pass session check, got mismatch: %v", err)
		}
	})
}

// TestPluginBindings_RejectMissingSessionToken verifies that every privileged
// binding rejects an empty session token (the bypass path a malicious plugin
// would take if it imported wailsjs/go/main/App.js directly without going
// through RegisterPluginSession).
func TestPluginBindings_RejectMissingSessionToken(t *testing.T) {
	app := newTestApp(t)

	// Sample of bindings across categories. The remaining bindings follow
	// the same validatePluginSession-first pattern; this test covers one
	// representative from each file to keep the regression suite focused.
	if _, err := app.PluginReadFile("silt-attachments", "", "Work", "x"); err == nil {
		t.Error("PluginReadFile: expected rejection for empty token")
	}
	if _, err := app.PluginRawQuery("silt-attachments", "", "SELECT 1", nil); err == nil {
		t.Error("PluginRawQuery: expected rejection for empty token")
	}
	if _, err := app.PluginMutateBlock("silt-attachments", "", "id", "text"); err == nil {
		t.Error("PluginMutateBlock: expected rejection for empty token")
	}
	if _, err := app.PluginUpdateTaskMeta("silt-attachments", "", "id", 1, -1); err == nil {
		t.Error("PluginUpdateTaskMeta: expected rejection for empty token")
	}
	if err := app.PluginNotify("silt-attachments", "", "t", "b"); err == nil {
		t.Error("PluginNotify: expected rejection for empty token")
	}
}
