package vault

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// openArchiveForRead opens a .silt-vault archive for inspection without
// extracting. Used by export tests to assert the on-disk contract.
func openArchiveForRead(t *testing.T, path string) *zip.ReadCloser {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("zip.OpenReader(%s): %v", path, err)
	}
	return r
}

func TestExportVaultTree_HappyPath(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	dest := filepath.Join(t.TempDir(), "out.silt-vault")

	res, err := ExportVaultTree(root, dest, "MyVault", "0.1.25-test", nil)
	if err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	if res.FilesArchived != 3 {
		t.Errorf("FilesArchived = %d, want 3 (Inbox.md + config.yaml + theme)", res.FilesArchived)
	}
	if res.PageFileCount != 1 {
		t.Errorf("PageFileCount = %d, want 1", res.PageFileCount)
	}
	if !res.SkippedIndex {
		t.Error("SkippedIndex should be true")
	}

	// The archive exists and is a readable ZIP.
	zr := openArchiveForRead(t, dest)
	defer zr.Close()

	// manifest.json present and parses.
	var manifest ArchiveManifest
	foundManifest := false
	entryIndex := map[string]*zip.File{}
	for _, f := range zr.File {
		entryIndex[f.Name] = f
		if f.Name != ArchiveManifestPath {
			continue
		}
		foundManifest = true
		rc, rerr := f.Open()
		if rerr != nil {
			t.Fatalf("open manifest: %v", rerr)
		}
		if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
			rc.Close()
			t.Fatalf("decode manifest: %v", err)
		}
		rc.Close()
	}
	if !foundManifest {
		t.Fatal("manifest.json missing from archive")
	}

	// Version + provenance recorded.
	if manifest.ArchiveVersion != SupportedArchiveVersion {
		t.Errorf("manifest.ArchiveVersion = %q, want %q", manifest.ArchiveVersion, SupportedArchiveVersion)
	}
	if manifest.SiltVersion != "0.1.25-test" {
		t.Errorf("manifest.SiltVersion = %q", manifest.SiltVersion)
	}
	if manifest.VaultName != "MyVault" {
		t.Errorf("manifest.VaultName = %q, want MyVault", manifest.VaultName)
	}
	if manifest.FileCount != 3 || manifest.PageFileCount != 1 {
		t.Errorf("manifest counts = file %d / page %d, want 3 / 1", manifest.FileCount, manifest.PageFileCount)
	}
	if len(manifest.Entries) != 3 {
		t.Errorf("manifest has %d entries, want 3", len(manifest.Entries))
	}

	// Index artifacts are NOT in the archive.
	for _, f := range zr.File {
		if isIndexArtifact(f.Name) {
			t.Errorf("index artifact present in archive: %q", f.Name)
		}
	}

	// Per-entry SHA-256 matches an independent recompute over the entry's
	// uncompressed bytes (read straight from the zip).
	for _, e := range manifest.Entries {
		zf, ok := entryIndex[e.Path]
		if !ok {
			t.Errorf("manifest entry %q not in archive", e.Path)
			continue
		}
		rc, rerr := zf.Open()
		if rerr != nil {
			t.Fatalf("open %s: %v", e.Path, rerr)
		}
		h := sha256.New()
		n, _ := copyAndCount(rc, h)
		rc.Close()
		if n != e.Size {
			t.Errorf("entry %s size = %d, manifest says %d", e.Path, n, e.Size)
		}
		if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
			t.Errorf("entry %s sha256 mismatch", e.Path)
		}
	}
}

func TestExportVaultTree_ManifestWrittenLastWithRootDigest(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	dest := filepath.Join(t.TempDir(), "out.silt-vault")

	if _, err := ExportVaultTree(root, dest, "", "0.1.25-test", nil); err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}

	zr := openArchiveForRead(t, dest)
	defer zr.Close()
	manifest := readManifest(t, zr)
	if manifest.ArchiveSHA256 == "" {
		t.Fatal("manifest.ArchiveSHA256 empty")
	}

	// The manifest entry must be the LAST content stream in the central
	// directory ordering produced by the writer (it was written after every
	// content entry). zip.OpenReader surfaces entries in write order.
	manifestIdx := -1
	for i, f := range zr.File {
		if f.Name == ArchiveManifestPath {
			manifestIdx = i
		}
	}
	if manifestIdx != len(zr.File)-1 {
		t.Errorf("manifest at index %d, want last (%d)", manifestIdx, len(zr.File)-1)
	}

	// The root digest must equal an independent recompute over the entry list.
	if got := rootDigest(manifest.Entries); got != manifest.ArchiveSHA256 {
		t.Errorf("root digest mismatch: manifest %q vs recomputed %q", manifest.ArchiveSHA256, got)
	}
	// The root is content-sensitive: flipping any entry's sha256 changes it.
	if len(manifest.Entries) == 0 {
		t.Fatal("need entries to mutate")
	}
	tampered := make([]ArchiveEntry, len(manifest.Entries))
	copy(tampered, manifest.Entries)
	tampered[0].SHA256 = "deadbeef" + manifest.Entries[0].SHA256[8:]
	if rootDigest(tampered) == manifest.ArchiveSHA256 {
		t.Error("root digest should change when an entry's sha256 changes")
	}

	// VaultName was empty on input → derived from the source folder name.
	if manifest.VaultName != filepath.Base(filepath.Clean(root)) {
		t.Errorf("VaultName = %q, want derived %q", manifest.VaultName, filepath.Base(filepath.Clean(root)))
	}
}

func TestExportVaultTree_ProgressCallback(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	dest := filepath.Join(t.TempDir(), "out.silt-vault")

	var calls []struct {
		phase   string
		current int
		total   int
	}
	_, err := ExportVaultTree(root, dest, "V", "0.1.25-test", func(phase string, current, total int) {
		calls = append(calls, struct {
			phase   string
			current int
			total   int
		}{phase, current, total})
	})
	if err != nil {
		t.Fatalf("ExportVaultTree: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("progress calls = %d, want 3 (one per file)", len(calls))
	}
	if calls[0].phase != "export" {
		t.Errorf("first call phase = %q, want export", calls[0].phase)
	}
	last := calls[len(calls)-1]
	if last.current != last.total || last.total != 3 {
		t.Errorf("last call = %d/%d, want 3/3", last.current, last.total)
	}
}

func TestExportVaultTree_CleansUpOnFailure(t *testing.T) {
	root := t.TempDir()
	scaffoldArchiveTree(t, root)
	// Destination directory cannot be created → open fails → partial archive
	// must be removed (and ExportVaultTree returns an error).
	dest := filepath.Join(root, "subdir-deep", "nested", "out.silt-vault")
	// Make a path component a regular file so MkdirAll/OpenFile fails.
	if err := os.WriteFile(filepath.Join(root, "subdir-deep"), []byte("blocker"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ExportVaultTree(root, dest, "", "test", nil)
	if err == nil {
		t.Fatal("expected error when destination cannot be created")
	}
	// The archive file must not exist as a readable artifact after failure.
	// (A parent path component being a file can surface as "not a directory"
	// rather than IsNotExist, so assert by absence of a successful stat.)
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Errorf("expected no archive file on failure, but stat succeeded")
	}
}

func TestExportVaultTree_RejectsEmptyOrMissingSource(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.silt-vault")
	if _, err := ExportVaultTree("", dest, "", "test", nil); err == nil {
		t.Error("expected error for empty source")
	}
	if _, err := ExportVaultTree("/does/not/exist/vault", dest, "", "test", nil); err == nil {
		t.Error("expected error for missing source")
	}
}

// --- helpers used by export + import tests ---

func copyAndCount(r interface{ Read([]byte) (int, error) }, w interface{ Write([]byte) (int, error) }) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if rerr != nil {
			if rerr.Error() == "EOF" {
				return total, nil
			}
			return total, nil // io.EOF from zip rc: treat as done
		}
	}
}

func readManifest(t *testing.T, zr *zip.ReadCloser) ArchiveManifest {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != ArchiveManifestPath {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open manifest: %v", err)
		}
		defer rc.Close()
		var m ArchiveManifest
		if err := json.NewDecoder(rc).Decode(&m); err != nil {
			t.Fatalf("decode manifest: %v", err)
		}
		return m
	}
	t.Fatal("manifest.json not found in archive")
	return ArchiveManifest{}
}
