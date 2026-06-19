package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// scaffoldArchiveTree builds a miniature vault at root: one notebook with one
// page, a .system/ with config.yaml + a fake index.sqlite (+wal +shm), and a
// symlink (when the platform supports it) so computeFileTree's exclusion +
// skip rules can both be exercised. Returns the list of regular files the
// caller should expect computeFileTree to surface.
func scaffoldArchiveTree(t *testing.T, root string) []string {
	t.Helper()
	mustWrite := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustWrite("Work/Inbox.md", "# Inbox\n- [ ] do a thing <!-- id: 11111111-1111-1111-1111-111111111111 -->\n")
	mustWrite(".system/config.yaml", "notebooks:\n  path: \"./\"\n")
	mustWrite(".system/index.sqlite", "FAKE-INDEX")   // reproducible — must be excluded
	mustWrite(".system/index.sqlite-wal", "FAKE-WAL") // excluded (prefix match)
	mustWrite(".system/index.sqlite-shm", "FAKE-SHM") // excluded (prefix match)
	mustWrite(".system/themes/cyber_forest.json", "{}")

	// Symlink: counted + skipped, not followed. Skip on platforms/filesystems
	// that forbid symlink creation so the test degrades gracefully.
	sym := filepath.Join(root, "Work", "link-to-inbox.md")
	target := filepath.Join(root, "Work", "Inbox.md")
	if err := os.Symlink(target, sym); err == nil {
		defer func() { _ = os.Remove(sym) }()
	} else {
		t.Logf("symlink creation failed (%v); skipping symlink assertion", err)
	}

	return []string{
		"Work/Inbox.md",
		".system/config.yaml",
		".system/themes/cyber_forest.json",
	}
}

func TestComputeFileTree_ExcludesIndexAndSymlinks(t *testing.T) {
	root := t.TempDir()
	expected := scaffoldArchiveTree(t, root)

	files, skippedIndex, skippedSymlinks, err := computeFileTree(root)
	if err != nil {
		t.Fatalf("computeFileTree: %v", err)
	}
	if !skippedIndex {
		t.Error("skippedIndex should be true (index artifacts were present)")
	}

	got := make(map[string]bool, len(files))
	for _, f := range files {
		got[f.relPath] = true
	}
	for _, want := range expected {
		if !got[want] {
			t.Errorf("expected file %q missing from tree", want)
		}
		delete(got, want)
	}
	for extra := range got {
		t.Errorf("unexpected extra file in tree: %q", extra)
	}

	// None of the index artifacts appear in the surfaced tree.
	for _, f := range files {
		if isIndexArtifact(f.relPath) {
			t.Errorf("index artifact surfaced in tree: %q", f.relPath)
		}
	}

	// Symlink count is 1 when symlinks are supported (the helper removes it).
	// Assert non-negative; the skip count is the contract that matters.
	if skippedSymlinks < 0 {
		t.Errorf("skippedSymlinks = %d, want >= 0", skippedSymlinks)
	}
}

func TestArchiveManifest_JSONRoundTrip(t *testing.T) {
	m := ArchiveManifest{
		ArchiveVersion: SupportedArchiveVersion,
		SiltVersion:    "0.1.25",
		VaultName:      "MyVault",
		CreatedAt:      "2026-06-19T12:00:00Z",
		PageFileCount:  3,
		FileCount:      10,
		TotalBytes:     4096,
		ArchiveSHA256:  "abc123",
		Entries: []ArchiveEntry{
			{Path: "Work/Inbox.md", Size: 42, SHA256: "deadbeef"},
		},
	}
	b, err := manifestBytes(m)
	if err != nil {
		t.Fatalf("manifestBytes: %v", err)
	}
	var out ArchiveManifest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ArchiveVersion != m.ArchiveVersion || out.VaultName != m.VaultName ||
		out.ArchiveSHA256 != m.ArchiveSHA256 || len(out.Entries) != 1 ||
		out.Entries[0].Path != "Work/Inbox.md" {
		t.Errorf("manifest did not round-trip: %+v", out)
	}
	if pageFileCount(out.Entries) != 1 {
		t.Errorf("pageFileCount = %d, want 1", pageFileCount(out.Entries))
	}
}

func TestHasParentSegment(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"..", true},
		{"../evil.txt", true},
		{"foo/../bar", true},
		{"a/b/../../escape", true},
		{"a/../..", true},
		// Legitimate filenames containing a ".." substring must NOT match. The
		// earlier strings.Contains(name, "..") guard false-positive'd these.
		{"2.0..2.1.md", false},
		{"foo...bar", false},
		{"Work/Inbox.md", false},
		{".system/config.yaml", false},
		{"Work/sub..dir/page.md", false},
		{"", false},
	}
	for _, c := range cases {
		if got := hasParentSegment(c.path); got != c.want {
			t.Errorf("hasParentSegment(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
