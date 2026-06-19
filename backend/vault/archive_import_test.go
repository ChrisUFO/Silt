package vault

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportVaultTree_HappyPath verifies export → import produces a
// byte-identical tree (reusing mover.go's collectFileHashes on both sides).
func TestImportVaultTree_HappyPath(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, archive, "V", "0.1.25-test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "imported")
	res, err := ImportVaultTree(archive, dest, nil)
	if err != nil {
		t.Fatalf("ImportVaultTree: %v", err)
	}
	if res.FilesExtracted == 0 {
		t.Error("FilesExtracted should be > 0")
	}
	if res.PageFileCount != 1 {
		t.Errorf("PageFileCount = %d, want 1", res.PageFileCount)
	}

	// The extracted tree is byte-identical to the source (excluding the index
	// artifacts that were never archived — collectFileHashes skips them too).
	srcHashes, err := collectFileHashes(root)
	if err != nil {
		t.Fatalf("collectFileHashes(src): %v", err)
	}
	dstHashes, err := collectFileHashes(dest)
	if err != nil {
		t.Fatalf("collectFileHashes(dest): %v", err)
	}
	if len(srcHashes) != len(dstHashes) {
		t.Fatalf("file count mismatch: src %d, dest %d", len(srcHashes), len(dstHashes))
	}
	for rel, srcH := range srcHashes {
		dstH, ok := dstHashes[rel]
		if !ok {
			t.Errorf("missing in dest: %s", rel)
			continue
		}
		if srcH != dstH {
			t.Errorf("content mismatch for %s", rel)
		}
	}

	// No manifest.json leaked into the extracted vault (it is archive metadata,
	// not vault content).
	if _, err := os.Stat(filepath.Join(dest, ArchiveManifestPath)); err == nil {
		t.Error("manifest.json should NOT be extracted into the vault")
	}
}

// TestImportVaultTree_RejectsMissingManifest refuses a plain ZIP with no
// manifest, before creating destDir.
func TestImportVaultTree_RejectsMissingManifest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "no-manifest.silt-vault")
	buildPlainZip(t, archive, map[string]string{"Work/Inbox.md": "# hi\n"})
	dest := filepath.Join(t.TempDir(), "dest")

	_, err := ImportVaultTree(archive, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing-manifest error, got %v", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("destDir must not be created when validation fails")
	}
}

// TestImportVaultTree_RejectsZipSlip crafts a hostile archive with a ".." entry
// and asserts it is refused and writes nothing outside destDir.
func TestImportVaultTree_RejectsZipSlip(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "hostile.silt-vault")
	// Build an archive that looks legit (manifest + content) PLUS a ".." entry.
	if err := buildHostileArchive(t, archive, root, "../evil.txt", "pwned"); err != nil {
		t.Fatalf("buildHostileArchive: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest")

	_, err := ImportVaultTree(archive, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "zip-slip") {
		t.Fatalf("expected zip-slip rejection, got %v", err)
	}
	// The hostile entry must not have escaped.
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "evil.txt")); statErr == nil {
		t.Error("hostile entry escaped the staging dir")
	}
}

// TestImportVaultTree_RejectsAbsoluteEntry refuses an entry whose name is an
// absolute path.
func TestImportVaultTree_RejectsAbsoluteEntry(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "abs.silt-vault")
	if err := buildHostileArchive(t, archive, root, "/etc/passwd", "pwned"); err != nil {
		t.Fatalf("buildHostileArchive: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest")
	_, err := ImportVaultTree(archive, dest, nil)
	if err == nil {
		t.Fatal("expected absolute-path rejection, got nil")
	}
}

// TestImportVaultTree_RejectsCorruptedEntry flips a byte in a content entry
// after export and asserts the per-entry checksum fails AND destDir was not
// created.
func TestImportVaultTree_RejectsCorruptedEntry(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, archive, "V", "test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	// Corrupt the archive by appending garbage BEFORE re-zipping: simplest
	// reliable corruption is to rewrite one content entry's bytes in place
	// inside the zip. Rebuild the archive from scratch with one tampered file
	// but an UNMATCHED manifest entry (reuse the real manifest).
	tamperedArchive := filepath.Join(t.TempDir(), "corrupt.silt-vault")
	if err := buildArchiveWithTamperedContent(t, archive, tamperedArchive, "Work/Inbox.md"); err != nil {
		t.Fatalf("buildArchiveWithTamperedContent: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "dest")
	_, err := ImportVaultTree(tamperedArchive, dest, nil)
	if err == nil {
		t.Fatal("expected corruption error, got nil")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("destDir must not exist after a corrupt-extract abort")
	}
}

// TestImportVaultTree_RejectsNonEmptyDestination refuses a non-empty dest.
func TestImportVaultTree_RejectsNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, archive, "V", "test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "preexisting.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ImportVaultTree(archive, dest, nil)
	if err == nil {
		t.Fatal("expected non-empty-destination rejection, got nil")
	}
}

// TestImportVaultTree_RejectsUnknownArchiveVersion refuses a manifest whose
// version is not the supported one (forward-compat: refuse rather than
// half-extract).
func TestImportVaultTree_RejectsUnknownArchiveVersion(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	good := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, good, "V", "test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "future.silt-vault")
	if err := rebuildWithManifestVersion(t, good, bad, "999.0.0"); err != nil {
		t.Fatalf("rebuildWithManifestVersion: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest")
	_, err := ImportVaultTree(bad, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected version-rejection, got %v", err)
	}
}

// TestImportVaultTree_RejectsTamperedRootDigest refuses an archive whose entry
// list was edited but whose root digest was not recomputed (manifest self-
// consistency check).
func TestImportVaultTree_RejectsTamperedRootDigest(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	good := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, good, "V", "test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "tampered-root.silt-vault")
	if err := rebuildWithTamperedEntrySize(t, good, bad); err != nil {
		t.Fatalf("rebuildWithTamperedEntrySize: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest")
	_, err := ImportVaultTree(bad, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("expected integrity-root rejection, got %v", err)
	}
}

// TestImportVaultTree_ProgressCallback exercises the extract progress callback.
func TestImportVaultTree_ProgressCallback(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	archive := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree(root, archive, "V", "test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "dest")
	var last struct {
		current, total int
	}
	_, err := ImportVaultTree(archive, dest, func(phase string, current, total int) {
		if phase != "extract" {
			t.Errorf("phase = %q, want extract", phase)
		}
		last.current, last.total = current, total
	})
	if err != nil {
		t.Fatalf("ImportVaultTree: %v", err)
	}
	if last.total == 0 || last.current != last.total {
		t.Errorf("last progress = %d/%d, want current==total>0", last.current, last.total)
	}
}

// --- hostile-archive builders ---

func buildPlainZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// buildHostileArchive exports the legit tree, then rebuilds the archive adding
// one extra entry at evilName with the given body, reusing the REAL manifest
// (so the entry list stays consistent for the legit entries; the hostile entry
// is caught by the zip-slip/absolute name guard, not the entry-count check).
func buildHostileArchive(t *testing.T, archive, src, evilName, evilBody string) error {
	t.Helper()
	// Export legit archive, then read its entries.
	tmpArchive := archive + ".legit"
	if _, err := ExportVaultTree(src, tmpArchive, "V", "test", nil); err != nil {
		return err
	}
	in, err := zip.OpenReader(tmpArchive)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(archive)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	// Copy every legit entry verbatim (bytes + headers).
	for _, f := range in.File {
		fw, err := zw.CreateHeader(&f.FileHeader)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := copyTo(rc, fw); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	// Append the hostile entry.
	hw, err := zw.Create(evilName)
	if err != nil {
		return err
	}
	if _, err := hw.Write([]byte(evilBody)); err != nil {
		return err
	}
	return zw.Close()
}

// buildArchiveWithTamperedContent rebuilds archive with the same manifest but
// one entry's bytes altered, so the per-entry SHA-256 fails on extract.
func buildArchiveWithTamperedContent(t *testing.T, goodArchive, outArchive, tamperEntry string) error {
	t.Helper()
	in, err := zip.OpenReader(goodArchive)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(outArchive)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	for _, f := range in.File {
		fw, err := zw.CreateHeader(&f.FileHeader)
		if err != nil {
			return err
		}
		if filepath.ToSlash(f.Name) == tamperEntry {
			// Write DIFFERENT bytes than the manifest declares.
			if _, err := fw.Write([]byte("# TAMPERED CONTENT — not what was hashed\n")); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := copyTo(rc, fw); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return zw.Close()
}

// rebuildWithManifestVersion rebuilds the archive with the manifest's
// ArchiveVersion field overwritten (every entry byte-for-byte identical, so
// the root digest still matches — isolating the version check).
func rebuildWithManifestVersion(t *testing.T, goodArchive, outArchive, version string) error {
	t.Helper()
	return rebuildWithManifestEdit(t, goodArchive, outArchive, func(m *ArchiveManifest) {
		m.ArchiveVersion = version
	})
}

// rebuildWithTamperedEntrySize rebuilds the archive with one manifest entry's
// Size field mutated, so the root digest no longer matches (manifest self-
// consistency check fires before extraction).
func rebuildWithTamperedEntrySize(t *testing.T, goodArchive, outArchive string) error {
	t.Helper()
	return rebuildWithManifestEdit(t, goodArchive, outArchive, func(m *ArchiveManifest) {
		if len(m.Entries) > 0 {
			m.Entries[0].Size += 9999
		}
	})
}

func rebuildWithManifestEdit(t *testing.T, goodArchive, outArchive string, edit func(*ArchiveManifest)) error {
	t.Helper()
	in, err := zip.OpenReader(goodArchive)
	if err != nil {
		return err
	}
	defer in.Close()
	// Read + edit the manifest.
	var manifest ArchiveManifest
	for _, f := range in.File {
		if filepath.ToSlash(f.Name) == ArchiveManifestPath {
			rc, _ := f.Open()
			_ = json.NewDecoder(rc).Decode(&manifest)
			rc.Close()
		}
	}
	edit(&manifest)
	mb, _ := manifestBytes(manifest)
	// Rewrite the archive with the edited manifest + every other entry verbatim.
	out, err := os.Create(outArchive)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	for _, f := range in.File {
		fw, err := zw.CreateHeader(&f.FileHeader)
		if err != nil {
			return err
		}
		if filepath.ToSlash(f.Name) == ArchiveManifestPath {
			if _, err := fw.Write(mb); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if _, err := copyTo(rc, fw); err != nil {
			rc.Close()
			return err
		}
		rc.Close()
	}
	return zw.Close()
}

func copyTo(src interface{ Read([]byte) (int, error) }, dst interface{ Write([]byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if rerr != nil {
			return total, nil
		}
	}
}

// silence unused imports if a builder path changes.
var _ = sha256.New
var _ = hex.EncodeToString
