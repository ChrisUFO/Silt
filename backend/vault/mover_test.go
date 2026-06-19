package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeVaultFixture creates a representative vault tree at root: two
// notebooks with pages, a full .system/ (config, themes, templates, plugins,
// trash), AND the 3 SQLite index artifacts that a copy must exclude. Returns
// the list of regular files that SHOULD be present at a destination (i.e. all
// of them except the index artifacts).
func writeVaultFixture(t *testing.T, root string) []string {
	t.Helper()
	must := func(p string, err error) {
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}

	// Notebook content (the source of truth).
	must("notebook page", os.MkdirAll(filepath.Join(root, "Work", "Projects"), 0755))
	must("write inbox", os.WriteFile(filepath.Join(root, "Work", "Inbox.md"), []byte("# Inbox\n- [ ] do thing\n"), 0644))
	must("write project page", os.WriteFile(filepath.Join(root, "Work", "Projects", "Redesign.md"), []byte("# Redesign\n"), 0644))
	must("personal notebook", os.MkdirAll(filepath.Join(root, "Personal"), 0755))
	must("write personal page", os.WriteFile(filepath.Join(root, "Personal", "Daily.md"), []byte("# Daily\n"), 0644))

	// .system/ contents — all of this is source of truth and MUST travel.
	must("system dir", os.MkdirAll(filepath.Join(root, ".system", "themes"), 0755))
	must("system templates", os.MkdirAll(filepath.Join(root, ".system", "templates"), 0755))
	must("system plugins", os.MkdirAll(filepath.Join(root, ".system", "plugins"), 0755))
	must("system trash", os.MkdirAll(filepath.Join(root, ".system", "trash", "2026-06-19"), 0755))
	must("write config", os.WriteFile(filepath.Join(root, ".system", "config.yaml"), []byte("notebooks:\n  path: "+root+"\n"), 0644))
	must("write theme", os.WriteFile(filepath.Join(root, ".system", "themes", "cyber_forest.json"), []byte(`{"id":"cyber_forest"}`), 0644))
	must("write template", os.WriteFile(filepath.Join(root, ".system", "templates", "standup.md"), []byte("# Standup {{date}}"), 0644))
	must("write plugin readme", os.WriteFile(filepath.Join(root, ".system", "plugins", "README.md"), []byte("# Plugins"), 0644))
	must("write trashed file", os.WriteFile(filepath.Join(root, ".system", "trash", "2026-06-19", "Old.md"), []byte("# Old\n"), 0644))

	// The 3 reproducible SQLite index artifacts — MUST be excluded from copies.
	must("write index", os.WriteFile(filepath.Join(root, ".system", "index.sqlite"), []byte("SQLite format 3\x00"), 0644))
	must("write index-wal", os.WriteFile(filepath.Join(root, ".system", "index.sqlite-wal"), []byte("WAL HEADER"), 0644))
	must("write index-shm", os.WriteFile(filepath.Join(root, ".system", "index.sqlite-shm"), []byte("SHM"), 0644))

	// Enumerate the expected (non-index) regular files.
	var expected []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel == "." {
			return nil
		}
		if isIndexArtifact(filepath.ToSlash(rel)) {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			expected = append(expected, filepath.ToSlash(rel))
		}
		return nil
	})
	return expected
}

func TestIsIndexArtifact(t *testing.T) {
	cases := map[string]bool{
		".system/index.sqlite":         true,
		".system/index.sqlite-wal":     true,
		".system/index.sqlite-shm":     true,
		".system/index.sqlite-journal": true,
		".system/config.yaml":          false,
		".system/themes/x.json":        false,
		"Work/Inbox.md":                false,
	}
	for rel, want := range cases {
		if got := isIndexArtifact(rel); got != want {
			t.Errorf("isIndexArtifact(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestIsWithin(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		child, parent string
		want          bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/c", "/a/b", true},
		{"/a/b/c/d", "/a/b", true},
		{"/a/bc", "/a/b", false}, // sibling prefix, not nested
		{"/a", "/a/b", false},    // parent is deeper
		{"/a/other", "/a/b", false},
	}
	for _, c := range cases {
		if got := isWithin(c.child, c.parent); got != c.want {
			t.Errorf("isWithin(%q, %q) [sep=%q] = %v, want %v", c.child, c.parent, sep, got, c.want)
		}
	}
}

func TestValidateDestination_RejectsSamePath(t *testing.T) {
	src := t.TempDir()
	if err := validateDestination(src, src); err == nil {
		t.Fatal("expected error for dest == src, got nil")
	}
}

func TestValidateDestination_RejectsDestInsideSrc(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(src, "subfolder")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	err := validateDestination(src, dest)
	if err == nil {
		t.Fatal("expected error for dest inside src, got nil")
	}
	if !errors.Is(err, ErrDestinationRejected) {
		t.Errorf("expected ErrDestinationRejected, got %v", err)
	}
}

func TestValidateDestination_RejectsDestContainingSrc(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "vault")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := validateDestination(src, parent); err == nil {
		t.Fatal("expected error for dest containing src, got nil")
	}
}

func TestValidateDestination_RejectsNonEmptyDest(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "random.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	err := validateDestination(src, dest)
	if err == nil {
		t.Fatal("expected error for non-empty dest, got nil")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("expected 'not empty' message, got %v", err)
	}
}

func TestValidateDestination_RejectsExistingVaultDest(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, ".system"), 0755); err != nil {
		t.Fatal(err)
	}
	err := validateDestination(src, dest)
	if err == nil {
		t.Fatal("expected error for dest that already has .system, got nil")
	}
	if !strings.Contains(err.Error(), "already a Silt vault") {
		t.Errorf("expected 'already a Silt vault' message, got %v", err)
	}
}

func TestValidateDestination_RejectsNetworkFilesystem(t *testing.T) {
	// A real network mount cannot be reproduced in CI, so swap the detector
	// for a stub that simulates one. validateDestination must surface the
	// rejection (the same clear "move to a local folder" message the index
	// opener uses) rather than proceeding to a WAL-incompatible copy.
	orig := networkFSCheck
	networkFSCheck = func(path string) error {
		return errors.New("network filesystem detected: simulated NFS mount")
	}
	t.Cleanup(func() { networkFSCheck = orig })

	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "on-network")
	err := validateDestination(src, dest)
	if err == nil {
		t.Fatal("expected a network-filesystem rejection, got nil")
	}
	if !errors.Is(err, ErrDestinationRejected) {
		t.Errorf("expected ErrDestinationRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "network filesystem") {
		t.Errorf("expected the network-FS reason in the message, got %v", err)
	}
}

func TestValidateDestination_AcceptsEmptyDest(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "new-vault") // does not exist yet
	if err := validateDestination(src, dest); err != nil {
		t.Errorf("expected nil for a fresh empty dest, got %v", err)
	}
}

func TestValidateDestination_RejectsEmptyInputs(t *testing.T) {
	// Empty src or dest must be rejected outright: absClean("") resolves to
	// the working directory, so an unchecked empty path could otherwise point
	// a copy/move at the CWD.
	if err := validateDestination("", "/some/dest"); err == nil {
		t.Fatal("expected error for empty src, got nil")
	}
	if err := validateDestination("/some/src", ""); err == nil {
		t.Fatal("expected error for empty dest, got nil")
	}
}

func TestCopyVaultTree_HappyPath(t *testing.T) {
	src := t.TempDir()
	expected := writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	result, err := CopyVaultTree(src, dest)
	if err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if !result.SkippedIndex {
		t.Error("SkippedIndex should be true (index artifacts were present in src)")
	}
	if result.FilesCopied != len(expected) {
		t.Errorf("FilesCopied = %d, want %d (the non-index regular files)", result.FilesCopied, len(expected))
	}
	if result.BytesCopied <= 0 {
		t.Error("BytesCopied should be > 0")
	}

	// Every expected file exists at the destination.
	for _, rel := range expected {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected file missing at destination: %s (%v)", rel, err)
		}
	}
}

func TestCopyVaultTree_ExcludesIndexArtifacts(t *testing.T) {
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	for _, name := range []string{".system/index.sqlite", ".system/index.sqlite-wal", ".system/index.sqlite-shm"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(name))); err == nil {
			t.Errorf("index artifact should NOT have been copied, but exists: %s", name)
		}
	}
}

func TestCopyVaultTree_DestDoesNotGetIndex(t *testing.T) {
	// A fresh destination has no index; after copy it still has none (the
	// cutover / first open rebuilds it). This is the documented contract.
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".system", "index.sqlite")); err == nil {
		t.Error("destination should not have an index.sqlite after a plain copy")
	}
}

func TestCopyVaultTree_PreservesFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on Windows")
	}
	src := t.TempDir()
	// Make a plugin entry that should keep its executable bit.
	entry := filepath.Join(src, ".system", "plugins", "demo", "index.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("export default {}\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, ".system", "plugins", "demo", "index.js"))
	if err != nil {
		t.Fatalf("stat copied entry: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode not preserved: got %o, want 0755", info.Mode().Perm())
	}
}

func TestCopyVaultTree_LargeFileStreaming(t *testing.T) {
	// A file larger than the buffer io.Copy uses by default (32 KiB) to
	// exercise the streaming hash + copy paths. 100 KiB suffices.
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Work"), 0755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 100*1024)
	for i := range big {
		big[i] = byte(i % 251) // pseudo-random, non-zero
	}
	if err := os.WriteFile(filepath.Join(src, "Work", "big.md"), big, 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "copy")

	result, err := CopyVaultTree(src, dest)
	if err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if result.BytesCopied != int64(len(big)) {
		t.Errorf("BytesCopied = %d, want %d", result.BytesCopied, len(big))
	}
}

func TestCopyVaultTree_CrossVolumeDirs(t *testing.T) {
	// Two independent temp dirs simulate a cross-volume move (the mover never
	// assumes src and dest share a filesystem; it always copies).
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "cross-copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree across two temp roots: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".system", "config.yaml")); err != nil {
		t.Errorf("config.yaml missing after cross-copy: %v", err)
	}
}

func TestCopyVaultTree_CleansUpOnFailure(t *testing.T) {
	// A non-empty destination fails validation; dest is left untouched (no
	// partial tree created).
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := t.TempDir()
	_ = os.WriteFile(filepath.Join(dest, "blocking-file"), []byte("x"), 0644)

	if _, err := CopyVaultTree(src, dest); err == nil {
		t.Fatal("expected validation error, got nil")
	}
	// The blocking file is still there (we did not RemoveAll a pre-existing
	// non-empty dest — that's the user's folder).
	if _, err := os.Stat(filepath.Join(dest, "blocking-file")); err != nil {
		t.Errorf("pre-existing dest content should be untouched, got %v", err)
	}
	// And we did NOT create a .system inside it.
	if _, err := os.Stat(filepath.Join(dest, ".system")); err == nil {
		t.Error("a failed copy must not leave a .system behind in the destination")
	}
}

func TestVerifyCopy_DetectsTamperedDestination(t *testing.T) {
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	// Corrupt a copied file.
	if err := os.WriteFile(filepath.Join(dest, "Work", "Inbox.md"), []byte("TAMPERED"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dest); err == nil {
		t.Error("verifyCopy should detect a tampered destination, got nil")
	}
}

func TestVerifyCopy_DetectsMissingDestinationFile(t *testing.T) {
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if err := os.Remove(filepath.Join(dest, ".system", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dest); err == nil {
		t.Error("verifyCopy should detect a missing destination file, got nil")
	}
}

func TestVerifyCopy_DetectsExtraDestinationFile(t *testing.T) {
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, ".system", "unexpected.txt"), []byte("surprise"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dest); err == nil {
		t.Error("verifyCopy should detect an extra destination file, got nil")
	}
}

func TestVerifyCopy_HappyPath(t *testing.T) {
	src := t.TempDir()
	writeVaultFixture(t, src)
	dest := filepath.Join(t.TempDir(), "copy")

	if _, err := CopyVaultTree(src, dest); err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if err := verifyCopy(src, dest); err != nil {
		t.Errorf("verifyCopy on a clean copy should be nil, got %v", err)
	}
}

func TestRemoveOldVault_RemovesAVault(t *testing.T) {
	old := t.TempDir()
	writeVaultFixture(t, old)

	if err := RemoveOldVault(old); err != nil {
		t.Fatalf("RemoveOldVault: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("expected old vault to be gone, stat err = %v", err)
	}
}

func TestRemoveOldVault_RefusesNonVaultPath(t *testing.T) {
	// A plain folder with no .system must NOT be deleted — guards against a
	// caller passing a stale/wrong path.
	plain := t.TempDir()
	if err := os.WriteFile(filepath.Join(plain, "notes.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOldVault(plain); err == nil {
		t.Fatal("expected refusal for a non-vault path, got nil")
	}
	// And the folder is untouched.
	if _, err := os.Stat(filepath.Join(plain, "notes.md")); err != nil {
		t.Errorf("non-vault folder should be untouched, got %v", err)
	}
}

func TestRemoveOldVault_RefusesEmptyPath(t *testing.T) {
	// An empty path must never reach RemoveAll: absClean("") resolves to the
	// working directory, which could be catastrophic if it held a .system.
	if err := RemoveOldVault(""); err == nil {
		t.Fatal("expected refusal for an empty path, got nil")
	}
}

func TestSourceModifiedAfter(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Work"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Work", "a.md"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}

	// Allow the filesystem clock to advance past the file writes BEFORE
	// capturing the cutoff. SourceModifiedAfter uses !Before (>=) for safety
	// (a borderline file is never deleted from its owner), so the cutoff must
	// be strictly after the writes — otherwise a same-instant mtime triggers
	// a false "modified" on filesystems with coarse timestamp resolution
	// (notably NTFS on Windows).
	time.Sleep(200 * time.Millisecond)
	cutoff := time.Now()
	// Allow the filesystem clock to advance (mtimes have second resolution
	// on some filesystems).
	time.Sleep(1100 * time.Millisecond)

	// No edit since cutoff → not modified.
	modified, err := SourceModifiedAfter(root, cutoff)
	if err != nil {
		t.Fatalf("SourceModifiedAfter: %v", err)
	}
	if modified {
		t.Error("expected not modified when nothing changed after cutoff")
	}

	// Edit a file → modified.
	if err := os.WriteFile(filepath.Join(root, "Work", "a.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	modified, err = SourceModifiedAfter(root, cutoff)
	if err != nil {
		t.Fatalf("SourceModifiedAfter after edit: %v", err)
	}
	if !modified {
		t.Error("expected modified after a post-cutoff edit")
	}

	// Index artifacts are excluded from the check (they're rebuilt anyway, so
	// a WAL rewrite must not block removeOld).
	if err := os.MkdirAll(filepath.Join(root, ".system"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".system", "index.sqlite-wal"), []byte("wal"), 0644); err != nil {
		t.Fatal(err)
	}
	// Fresh cutoff after the markdown edit but before a WAL bump.
	cutoff2 := time.Now()
	time.Sleep(1100 * time.Millisecond)
	_ = os.WriteFile(filepath.Join(root, ".system", "index.sqlite-wal"), []byte("wal2"), 0644)
	modified, err = SourceModifiedAfter(root, cutoff2)
	if err != nil {
		t.Fatalf("SourceModifiedAfter index-exclusion: %v", err)
	}
	if modified {
		t.Error("index artifact change alone must not count as a source modification")
	}

	// Empty root → false, no error.
	if m, err := SourceModifiedAfter("", cutoff); err != nil || m {
		t.Errorf("empty root: modified=%v err=%v", m, err)
	}
}

func TestCopyVaultTree_CountsSkippedSymlinks(t *testing.T) {
	// A symlinked entry is not followed (filepath.WalkDir), so it is absent
	// from the destination. The mover counts it so the UI can warn the user
	// the copy is incomplete.
	if os.Geteuid() == 0 {
		t.Skip("symlink mode assertions are unreliable as root")
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Work"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Work", "real.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// A symlink to a directory outside the vault — must be skipped, not followed.
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "note.md"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(src, "Work", "linked")); err != nil {
		// Some CI filesystems disallow symlinks; skip rather than fail there.
		t.Skipf("cannot create symlink: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "copy")
	res, err := CopyVaultTree(src, dest)
	if err != nil {
		t.Fatalf("CopyVaultTree: %v", err)
	}
	if res.SkippedSymlinks != 1 {
		t.Errorf("SkippedSymlinks = %d, want 1", res.SkippedSymlinks)
	}
	// The symlink target's content must NOT have been copied in.
	if _, err := os.Stat(filepath.Join(dest, "Work", "linked", "note.md")); err == nil {
		t.Error("symlink was followed — target content should be absent from the copy")
	}
}
