package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"silt/backend/config"
	"silt/backend/parser"
)

// TestListNavigation_DeepNesting verifies that #88's recursive walker
// surfaces sections at any depth and preserves the section's own pages
// alongside its nested children. The on-disk layout is:
//
//	<vault>/Work/Projects/Active/Site.md   (section="Projects/Active", page="Site")
//	<vault>/Work/Journal/Daily.md          (section="Journal", page="Daily")
//	<vault>/Work/Top.md                     (no section; page="Top")
//
// The expected tree is:
//
//	Work
//	  ├ "" (no section) -> [Top]
//	  ├ Projects
//	  │   └ Active -> [Site]
//	  └ Journal -> [Daily]
func TestListNavigation_DeepNesting(t *testing.T) {
	app := newTestApp(t)

	root := app.vaultPath
	// Layout
	for _, p := range []string{
		filepath.Join(root, "Work", "Projects", "Active"),
		filepath.Join(root, "Work", "Journal"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	writeFile(t, filepath.Join(root, "Work", "Projects", "Active", "Site.md"),
		"---\nnotebook: Work\nsection: Projects/Active\npage: Site\ndate: 2026-06-15\ntags: []\n---\n# Site\n")
	writeFile(t, filepath.Join(root, "Work", "Journal", "Daily.md"),
		"---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-15\ntags: []\n---\n# Daily\n")
	writeFile(t, filepath.Join(root, "Work", "Top.md"),
		"---\nnotebook: Work\nsection: \"\"\npage: Top\ndate: 2026-06-15\ntags: []\n---\n# Top\n")

	tree, err := app.ListNavigation()
	if err != nil {
		t.Fatalf("ListNavigation: %v", err)
	}
	if len(tree.Notebooks) != 1 {
		t.Fatalf("expected 1 notebook, got %d", len(tree.Notebooks))
	}
	work := tree.Notebooks[0]
	if work.Name != "Work" {
		t.Fatalf("notebook name = %q", work.Name)
	}
	if len(work.Sections) < 3 {
		t.Fatalf("expected at least 3 top-level sections (no-section, Projects, Journal), got %d", len(work.Sections))
	}

	// Find the section-less group first (Name == "").
	var sectionless *parser.NavigationSection
	var projects *parser.NavigationSection
	var journal *parser.NavigationSection
	for i := range work.Sections {
		sec := &work.Sections[i]
		switch sec.Name {
		case "":
			sectionless = sec
		case "Projects":
			projects = sec
		case "Journal":
			journal = sec
		}
	}
	if sectionless == nil || projects == nil || journal == nil {
		t.Fatalf("missing top-level sections: sectionless=%v projects=%v journal=%v", sectionless, projects, journal)
	}

	// Section-less: one page (Top).
	if len(sectionless.Pages) != 1 || sectionless.Pages[0].Name != "Top" {
		t.Errorf("section-less pages = %+v", sectionless.Pages)
	}

	// Projects has one child ("Active") which has the Site page.
	if len(projects.Children) != 1 {
		t.Fatalf("Projects children = %d, want 1", len(projects.Children))
	}
	active := projects.Children[0]
	if active.Name != "Active" {
		t.Errorf("Active.Name = %q", active.Name)
	}
	if len(active.Pages) != 1 || active.Pages[0].Name != "Site" {
		t.Errorf("Active pages = %+v", active.Pages)
	}

	// Journal is a flat section with one page.
	if len(journal.Pages) != 1 || journal.Pages[0].Name != "Daily" {
		t.Errorf("Journal pages = %+v", journal.Pages)
	}
	if len(journal.Children) != 0 {
		t.Errorf("Journal should have no children, got %d", len(journal.Children))
	}
}

// --- Config UI block tests (#63, #68) ---

func TestGetSetSidebarWidth_RoundTrip(t *testing.T) {
	app := newTestApp(t)

	if w := app.GetSidebarWidth(); w != 256 {
		t.Fatalf("default sidebar width: got %d, want 256", w)
	}

	if err := app.SetSidebarWidth(320); err != nil {
		t.Fatalf("SetSidebarWidth(320): %v", err)
	}
	if w := app.GetSidebarWidth(); w != 320 {
		t.Fatalf("after set: got %d, want 320", w)
	}

	// Reload from disk to verify persistence.
	cfg, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.UI.SidebarWidth != 320 {
		t.Fatalf("persisted: got %d, want 320", cfg.UI.SidebarWidth)
	}
}

func TestSetSidebarWidth_Clamps(t *testing.T) {
	app := newTestApp(t)

	if err := app.SetSidebarWidth(50); err != nil {
		t.Fatalf("SetSidebarWidth(50): %v", err)
	}
	if w := app.GetSidebarWidth(); w != 200 {
		t.Fatalf("below min: got %d, want 200 (clamped)", w)
	}

	if err := app.SetSidebarWidth(999); err != nil {
		t.Fatalf("SetSidebarWidth(999): %v", err)
	}
	if w := app.GetSidebarWidth(); w != 480 {
		t.Fatalf("above max: got %d, want 480 (clamped)", w)
	}
}

func TestGetSetNavOrder_RoundTrip(t *testing.T) {
	app := newTestApp(t)

	order := config.NavOrder{
		Notebooks: []string{"Personal", "Work"},
		Sections:  map[string][]string{"Work": {"Projects", "Inbox"}},
	}
	if err := app.SetNavOrder(order); err != nil {
		t.Fatalf("SetNavOrder: %v", err)
	}

	got, err := app.GetNavOrder()
	if err != nil {
		t.Fatalf("GetNavOrder: %v", err)
	}
	if len(got.Notebooks) != 2 || got.Notebooks[0] != "Personal" {
		t.Fatalf("nav order notebooks: got %v", got.Notebooks)
	}
	if len(got.Sections["Work"]) != 2 || got.Sections["Work"][0] != "Projects" {
		t.Fatalf("nav order sections: got %v", got.Sections["Work"])
	}
}

// --- Rename tests (#62, #83) ---

func TestRenamePage_UpdatesFrontmatterAndFile(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "", "OldPage", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	if err := app.RenamePage("TestNB", "", "OldPage", "NewPage"); err != nil {
		t.Fatalf("RenamePage: %v", err)
	}

	oldPath := filepath.Join(app.vaultPath, "TestNB", "OldPage.md")
	newPath := filepath.Join(app.vaultPath, "TestNB", "NewPage.md")

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old file should not exist after rename")
	}
	content, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("new file should exist: %v", err)
	}
	if !strings.Contains(string(content), `"NewPage"`) {
		t.Fatalf("frontmatter should contain NewPage: %s", content)
	}
}

func TestRenamePage_NameCollision(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "", "Page1", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage Page1: %v", err)
	}
	if _, err := app.CreatePage("TestNB", "", "Page2", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage Page2: %v", err)
	}

	err := app.RenamePage("TestNB", "", "Page1", "Page2")
	if err == nil {
		t.Fatalf("rename to existing name should fail")
	}
}

func TestRenamePage_PathTraversal(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "", "Safe", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// sanitizePathSegment strips ".." and "/", so traversal is neutralized.
	// The rename succeeds with a sanitized name, but the file stays in the vault.
	if err := app.RenamePage("TestNB", "", "Safe", "../../../etc/passwd"); err != nil {
		t.Fatalf("rename with traversal chars should succeed (sanitized): %v", err)
	}

	// The file should be inside the vault with a sanitized name, not at /etc/passwd.
	origPath := filepath.Join(app.vaultPath, "TestNB", "Safe.md")
	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Fatalf("old file should not exist after rename")
	}
	// /etc/passwd.md should NOT exist as a result (path stayed in vault).
	if _, err := os.Stat("/etc/passwd.md"); err == nil {
		t.Fatalf("path traversal escaped the vault!")
	}
	// The sanitized file (etcpasswd.md) should exist inside the vault.
	sanitizedPath := filepath.Join(app.vaultPath, "TestNB", "etcpasswd.md")
	if _, err := os.Stat(sanitizedPath); err != nil {
		t.Fatalf("sanitized file should exist inside vault: %v", err)
	}
}

func TestRenameSection_UpdatesAllFiles(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "OldSec", "Page1", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage Page1: %v", err)
	}
	if _, err := app.CreatePage("TestNB", "OldSec", "Page2", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage Page2: %v", err)
	}

	if err := app.RenameSection("TestNB", "OldSec", "NewSec"); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}

	// Both files should be in the new section folder with updated frontmatter.
	for _, pg := range []string{"Page1", "Page2"} {
		path := filepath.Join(app.vaultPath, "TestNB", "NewSec", pg+".md")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("file %s should exist in new section: %v", pg, err)
		}
		if !strings.Contains(string(content), `"NewSec"`) {
			t.Fatalf("frontmatter should contain NewSec for %s: %s", pg, content)
		}
	}
}

// --- Delete tests (#62) ---

func TestDeletePage_MovesToTrash(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "", "Doomed", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	origPath := filepath.Join(app.vaultPath, "TestNB", "Doomed.md")
	if _, err := os.Stat(origPath); err != nil {
		t.Fatalf("page should exist before delete: %v", err)
	}

	if err := app.DeletePage("TestNB", "", "Doomed"); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}

	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Fatalf("page should not exist after delete")
	}

	// Verify file exists in trash.
	trashDir := filepath.Join(app.vaultPath, ".system", "trash")
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		t.Fatalf("trash dir should exist: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("trash should contain at least one timestamped folder")
	}
}

func TestDeleteSection_DeletesAllPages(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("TestNB", "DoomSec", "P1", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage P1: %v", err)
	}
	if _, err := app.CreatePage("TestNB", "DoomSec", "P2", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage P2: %v", err)
	}

	secPath := filepath.Join(app.vaultPath, "TestNB", "DoomSec")
	if _, err := os.Stat(secPath); err != nil {
		t.Fatalf("section should exist: %v", err)
	}

	if err := app.DeleteSection("TestNB", "DoomSec"); err != nil {
		t.Fatalf("DeleteSection: %v", err)
	}

	if _, err := os.Stat(secPath); !os.IsNotExist(err) {
		t.Fatalf("section should not exist after delete")
	}
}

// --- Notebook-level tests (#62) ---

func TestRenameNotebook_UpdatesAllFiles(t *testing.T) {
	app := newTestApp(t)

	// Seed a section-less page and a sectioned page.
	if _, err := app.CreatePage("OldNB", "", "TopPage", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage TopPage: %v", err)
	}
	if _, err := app.CreatePage("OldNB", "Sec1", "NestedPage", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage NestedPage: %v", err)
	}

	if err := app.RenameNotebook("OldNB", "NewNB"); err != nil {
		t.Fatalf("RenameNotebook: %v", err)
	}

	// Old notebook folder should not exist.
	oldDir := filepath.Join(app.vaultPath, "OldNB")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old notebook dir should not exist after rename")
	}

	// Both files should be under NewNB with updated notebook: frontmatter.
	checks := []struct {
		relPath string
	}{
		{filepath.Join("NewNB", "TopPage.md")},
		{filepath.Join("NewNB", "Sec1", "NestedPage.md")},
	}
	for _, c := range checks {
		full := filepath.Join(app.vaultPath, c.relPath)
		content, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("file %s should exist under NewNB: %v", c.relPath, err)
		}
		if !strings.Contains(string(content), `"NewNB"`) {
			t.Fatalf("frontmatter in %s should contain notebook:\"NewNB\": %s", c.relPath, content)
		}
	}
}

func TestDeleteNotebook_TrashesAll(t *testing.T) {
	app := newTestApp(t)

	if _, err := app.CreatePage("DoomNB", "", "P1", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage P1: %v", err)
	}
	if _, err := app.CreatePage("DoomNB", "Sub", "P2", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage P2: %v", err)
	}

	nbPath := filepath.Join(app.vaultPath, "DoomNB")
	if _, err := os.Stat(nbPath); err != nil {
		t.Fatalf("notebook should exist: %v", err)
	}

	if err := app.DeleteNotebook("DoomNB"); err != nil {
		t.Fatalf("DeleteNotebook: %v", err)
	}

	// Notebook folder should be gone from vault root.
	if _, err := os.Stat(nbPath); !os.IsNotExist(err) {
		t.Fatalf("notebook should not exist after delete")
	}

	// Notebook content should be in trash.
	trashDir := filepath.Join(app.vaultPath, ".system", "trash")
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		t.Fatalf("trash dir should exist: %v", err)
	}
	found := false
	for _, e := range entries {
		trashNB := filepath.Join(trashDir, e.Name(), "DoomNB")
		if _, err := os.Stat(trashNB); err == nil {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("notebook subtree should exist under .system/trash/<ts>/DoomNB/")
	}
}

// --- Per-block write-intent lock test (#64) ---

func TestLockBlocksWrite_NoDeadlock(t *testing.T) {
	app := newTestApp(t)

	// Create a page with multiple blocks.
	if _, err := app.CreatePage("TestNB", "", "LockTest", "2026-01-01"); err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	// Concurrent SaveFileBlocks calls should not deadlock.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// LockBlocksWrite with overlapping sets should not deadlock
			// because acquisition is sorted.
			app.coordinator.LockBlocksWrite([]string{"a-1", "b-2", "c-3"}, func() {})
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		app.coordinator.LockBlockWrite("b-2", func() {})
	}()
	wg.Wait()
}

// --- Open tabs IPC tests (#142) ---

// writePageOnDisk creates a minimal page file so ListNavigation surfaces it.
// The page carries standard frontmatter matching the parser's expectations.
func writePageOnDisk(t *testing.T, vaultPath, notebook, section, page string) {
	t.Helper()
	var dir string
	if section == "" {
		dir = filepath.Join(vaultPath, notebook)
	} else {
		dir = filepath.Join(vaultPath, notebook, section)
	}
	content := "---\nnotebook: " + notebook + "\nsection: \"" + section + "\"\npage: " + page + "\ndate: 2026-06-15\ntags: []\n---\n# " + page + "\n"
	writeFile(t, filepath.Join(dir, page+".md"), content)
}

func TestGetSetOpenTabs_RoundTrip(t *testing.T) {
	app := newTestApp(t)

	// Create pages on disk so the nav tree has valid targets.
	writePageOnDisk(t, app.vaultPath, "Work", "Projects", "Site")
	writePageOnDisk(t, app.vaultPath, "Work", "", "Top")
	writePageOnDisk(t, app.vaultPath, "Personal", "Journal", "Daily")

	tabs := []config.TabRef{
		{Notebook: "Work", Section: "Projects", Page: "Site"},
		{Notebook: "Work", Section: "", Page: "Top"},
	}
	active := &config.TabRef{Notebook: "Work", Section: "Projects", Page: "Site"}

	if err := app.SetOpenTabs(tabs, active); err != nil {
		t.Fatalf("SetOpenTabs: %v", err)
	}

	result, err := app.GetOpenTabs()
	if err != nil {
		t.Fatalf("GetOpenTabs: %v", err)
	}
	if len(result.OpenTabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d: %+v", len(result.OpenTabs), result.OpenTabs)
	}
	// Both tabs should survive (both pages exist on disk).
	pages := map[string]bool{}
	for _, tab := range result.OpenTabs {
		pages[tab.Page] = true
	}
	if !pages["Site"] || !pages["Top"] {
		t.Errorf("expected Site + Top tabs, got %v", pages)
	}
	if result.ActiveTab == nil || result.ActiveTab.Page != "Site" {
		t.Errorf("active tab: got %+v, want Site", result.ActiveTab)
	}
}

func TestGetOpenTabs_PruneStaleTabs(t *testing.T) {
	app := newTestApp(t)

	// Create two pages.
	writePageOnDisk(t, app.vaultPath, "Work", "Projects", "KeepMe")
	writePageOnDisk(t, app.vaultPath, "Work", "Projects", "DeleteMe")

	// Persist tabs for both.
	tabs := []config.TabRef{
		{Notebook: "Work", Section: "Projects", Page: "KeepMe"},
		{Notebook: "Work", Section: "Projects", Page: "DeleteMe"},
	}
	active := &config.TabRef{Notebook: "Work", Section: "Projects", Page: "DeleteMe"}
	if err := app.SetOpenTabs(tabs, active); err != nil {
		t.Fatalf("SetOpenTabs: %v", err)
	}

	// Delete the "DeleteMe" page from disk.
	os.Remove(filepath.Join(app.vaultPath, "Work", "Projects", "DeleteMe.md"))

	// GetOpenTabs should prune the stale tab AND clear the stale active.
	result, err := app.GetOpenTabs()
	if err != nil {
		t.Fatalf("GetOpenTabs: %v", err)
	}
	if len(result.OpenTabs) != 1 || result.OpenTabs[0].Page != "KeepMe" {
		t.Errorf("expected only KeepMe tab after prune, got %+v", result.OpenTabs)
	}
	if result.ActiveTab != nil {
		t.Errorf("expected nil active tab (stale active pruned), got %+v", *result.ActiveTab)
	}
}

func TestGetOpenTabs_PruneMalformedEntries(t *testing.T) {
	app := newTestApp(t)

	writePageOnDisk(t, app.vaultPath, "Work", "", "Valid")

	// A malformed entry with an empty Page should be dropped silently.
	tabs := []config.TabRef{
		{Notebook: "Work", Section: "", Page: "Valid"},
		{Notebook: "Work", Section: "", Page: ""}, // malformed
	}
	if err := app.SetOpenTabs(tabs, nil); err != nil {
		t.Fatalf("SetOpenTabs: %v", err)
	}

	result, err := app.GetOpenTabs()
	if err != nil {
		t.Fatalf("GetOpenTabs: %v", err)
	}
	if len(result.OpenTabs) != 1 || result.OpenTabs[0].Page != "Valid" {
		t.Errorf("expected only Valid tab (malformed pruned), got %+v", result.OpenTabs)
	}
}

func TestSetOpenTabs_AtomicWrite(t *testing.T) {
	app := newTestApp(t)
	tabs := []config.TabRef{{Notebook: "Work", Section: "", Page: "Page1"}}
	if err := app.SetOpenTabs(tabs, nil); err != nil {
		t.Fatalf("SetOpenTabs: %v", err)
	}
	// The .system directory should contain exactly config.yaml (no leftover
	// temp files from the atomic write).
	entries, err := os.ReadDir(filepath.Join(app.vaultPath, ".system"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		// Allow config.yaml, themes/, plugins/, templates/ dirs; reject any
		// .tmp file (leftover from a failed atomic write).
		if strings.HasSuffix(name, ".tmp") {
			t.Errorf("leftover temp file in .system: %s", name)
		}
	}
}

func TestSetOpenTabs_NilBecomesEmptySlice(t *testing.T) {
	app := newTestApp(t)
	// Passing nil for openTabs should persist as an empty slice, not null
	// (so the frontend JSON layer never sees null).
	if err := app.SetOpenTabs(nil, nil); err != nil {
		t.Fatalf("SetOpenTabs(nil): %v", err)
	}
	cfg, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.UI.OpenTabs == nil || len(cfg.UI.OpenTabs) != 0 {
		t.Errorf("expected non-nil empty slice, got %v", cfg.UI.OpenTabs)
	}
}

func TestGetOpenTabs_EmptyVault(t *testing.T) {
	app := newTestApp(t)
	// No tabs persisted → empty slice, nil active, no error.
	result, err := app.GetOpenTabs()
	if err != nil {
		t.Fatalf("GetOpenTabs on empty vault: %v", err)
	}
	if len(result.OpenTabs) != 0 {
		t.Errorf("expected empty tab slice, got %d", len(result.OpenTabs))
	}
	if result.ActiveTab != nil {
		t.Errorf("expected nil active, got %+v", *result.ActiveTab)
	}
}

// TestSetOpenTabs_SelfWriteSuppressed verifies that SetOpenTabs calls
// RegisterSelfWrite so the config watcher does NOT fire a config:changed
// reload for Silt's own write (#142). This is the PLAN's promised
// self-write suppression test, exercised end-to-end via a real ConfigWatcher.
func TestSetOpenTabs_SelfWriteSuppressed(t *testing.T) {
	app := newTestApp(t)

	// Set up a real config watcher so RegisterSelfWrite is meaningful.
	changed := make(chan config.SystemConfig, 4)
	cw, err := config.NewConfigWatcher(app.vaultPath, func(c config.SystemConfig) {
		changed <- c
	}, nil)
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()
	app.configWatcher = cw
	defer func() { app.configWatcher = nil }()

	// Give the watcher time to settle.
	time.Sleep(150 * time.Millisecond)

	tabs := []config.TabRef{{Notebook: "Work", Section: "", Page: "Page1"}}
	if err := app.SetOpenTabs(tabs, nil); err != nil {
		t.Fatalf("SetOpenTabs: %v", err)
	}

	// The watcher should NOT fire within the self-write cooldown window.
	select {
	case <-changed:
		t.Fatalf("self-write should be suppressed, but config:changed fired")
	case <-time.After(700 * time.Millisecond):
		// expected: no reload within the cooldown window
	}
}

// TestAppendDismissedTip_AtomicWrite confirms the atomic tip-dismiss writer
// (#197) leaves no leftover temp files in .system after a successful write.
func TestAppendDismissedTip_AtomicWrite(t *testing.T) {
	app := newTestApp(t)
	if err := app.AppendDismissedTip("formatting_tip_v1"); err != nil {
		t.Fatalf("AppendDismissedTip: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(app.vaultPath, ".system"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file in .system: %s", e.Name())
		}
	}
}

// TestAppendDismissedTip_SelfWriteSuppressed verifies that AppendDismissedTip
// calls RegisterSelfWrite so the config watcher does NOT fire a
// config:changed reload for Silt's own write (#197), mirroring the
// SetOpenTabs contract.
func TestAppendDismissedTip_SelfWriteSuppressed(t *testing.T) {
	app := newTestApp(t)

	changed := make(chan config.SystemConfig, 4)
	cw, err := config.NewConfigWatcher(app.vaultPath, func(c config.SystemConfig) {
		changed <- c
	}, nil)
	if err != nil {
		t.Fatalf("NewConfigWatcher: %v", err)
	}
	defer cw.Close()
	cw.Start()
	app.configWatcher = cw
	defer func() { app.configWatcher = nil }()

	time.Sleep(150 * time.Millisecond)

	if err := app.AppendDismissedTip("formatting_tip_v1"); err != nil {
		t.Fatalf("AppendDismissedTip: %v", err)
	}

	select {
	case <-changed:
		t.Fatalf("self-write should be suppressed, but config:changed fired")
	case <-time.After(700 * time.Millisecond):
		// expected: no reload within the cooldown window
	}
}
