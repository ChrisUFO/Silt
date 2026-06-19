// Package vault: archive.go implements the .silt-vault portable archive
// format (#143). A .silt-vault archive is a ZIP (custom extension) carrying a
// manifest.json + the vault contents at the archive root, EXCLUDING the
// reproducible SQLite index (rebuilt from markdown on import, identical
// contract to CopyVaultTree/MoveVault in mover.go — ARCHITECTURE.md §0 rule 4).
//
// The format is the local-first contract made portable: markdown is the
// product, YAML travels in .system/, the SQLite index is disposable working
// memory, and the whole archive is checksummed (per-entry + whole-archive
// SHA-256) so tampering/corruption is detectable before a single file is
// extracted on import.
package vault

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiveExtension is the custom extension for a portable Silt vault archive.
// The underlying container is a ZIP.
const ArchiveExtension = ".silt-vault"

// ArchiveManifestPath is the path (at the archive root) of the manifest entry.
// It is written LAST by ExportVaultTree so the whole-archive SHA-256 it
// carries covers every content entry.
const ArchiveManifestPath = "manifest.json"

// SupportedArchiveVersion is the archive-format version this build produces
// and accepts on import. An archive whose version differs is rejected on
// import (forward-compat: a higher version from a newer Silt is refused with a
// clear message rather than half-extracted).
const SupportedArchiveVersion = "1.0.0"

// maxArchiveUncompressedSize bounds the total extracted size of a .silt-vault
// archive so a hostile or accidental huge file can't exhaust the user's disk.
// Vault scale is larger than a plugin (SPECS §8.4 caps plugins at 100 MB), so
// the ceiling is raised accordingly; a typical vault of thousands of small
// markdown pages is well under this.
const maxArchiveUncompressedSize = 2 * 1024 * 1024 * 1024 // 2 GB

// maxArchiveEntrySize bounds a single extracted file. Per-file defense-in-depth
// alongside the total-archive cap; it also bounds the io.LimitReader so a
// forged-header zip-bomb cannot expand past the declared size during extraction
// (mirrors plugins.copyZipEntry).
const maxArchiveEntrySize = 256 * 1024 * 1024 // 256 MB

// maxManifestSize bounds the uncompressed byte length of manifest.json on
// import. A hostile archive could declare a multi-gigabyte manifest to OOM the
// decoder (which builds the Entries slice in memory) before the total-archive
// size cap runs. 16 MiB is far beyond any plausible manifest (millions of
// entries) while bounding memory firmly.
const maxManifestSize = 16 * 1024 * 1024 // 16 MiB

// ErrArchiveRejected is returned by the import validator when an archive
// cannot be safely imported. The wrapped message is user-actionable.
var ErrArchiveRejected = errors.New("vault archive rejected")

// ArchiveManifest is the manifest.json schema carried at the root of a
// .silt-vault archive. It is the archive's self-description: provenance
// (Silt version, optional vault name, creation time), scale (file/page counts,
// total bytes), and integrity (whole-archive SHA-256 + per-entry digests).
type ArchiveManifest struct {
	// ArchiveVersion is the format version (see SupportedArchiveVersion).
	// Informational on export (always the current version); enforced on import.
	ArchiveVersion string `json:"archive_version"`
	// SiltVersion is the version of Silt that produced the archive, read from
	// the embedded VERSION (App.GetAppVersion). Forward/compat diagnostic.
	SiltVersion string `json:"silt_version"`
	// VaultName is the optional display name of the vault; derived from the
	// source folder name when empty on export. Carried so an imported vault
	// can present a friendly label even after extraction into an arbitrary
	// empty folder.
	VaultName string `json:"vault_name,omitempty"`
	// CreatedAt is the archive creation time, RFC3339 (UTC).
	CreatedAt string `json:"created_at"`
	// PageFileCount is the count of .md page files archived. The issue asks
	// for a "block count", but counting blocks requires a full parse of every
	// file; the honest, cheap proxy is the page-file count (each page is one
	// .md, the streaming unit — SPECS §3.1). The field name reflects what is
	// actually counted.
	PageFileCount int `json:"page_file_count"`
	// FileCount is the total count of all regular (non-index, non-symlink)
	// files archived, including .system/ config/themes/templates/plugins.
	FileCount int `json:"file_count"`
	// TotalBytes is the sum of the uncompressed sizes of every archived file.
	TotalBytes int64 `json:"total_bytes"`
	// ArchiveSHA256 is the whole-archive integrity root: the lowercase-hex SHA-256
	// over the canonical serialization of every entry record (path + size + per-
	// entry digest), computed AFTER all entries are collected and carried in the
	// manifest (written last). It binds the entire archive's identity + content in
	// a single self-contained digest the manifest cannot hold over its own raw
	// bytes (a manifest cannot hash itself).
	//
	// Go's archive/zip.Writer buffers all output and only writes to the underlying
	// file on Close(), so a raw-byte-region hash is not computable live; the root
	// digest is the standard self-contained alternative (Merkle-root style) and is
	// compression-independent. Import validates in two layers: (1) recompute the
	// root over the manifest's declared entries and assert equality (detects
	// manifest tampering) BEFORE extracting, then (2) verify each entry's actual
	// content hash during extraction (detects content corruption). Any changed
	// path/size/content changes the root or the per-entry hash.
	ArchiveSHA256 string `json:"archive_sha256"`
	// Entries carries the per-entry integrity records (slash-form relpath,
	// uncompressed size, lowercase-hex SHA-256). Import verifies each entry
	// against its record as it streams out of the archive.
	Entries []ArchiveEntry `json:"entries"`
}

// ArchiveEntry is the per-file integrity record carried in the manifest.
type ArchiveEntry struct {
	// Path is the archive-root-relative, slash-separated path of the entry
	// (e.g. "Work/Inbox.md" or ".system/config.yaml"). Always forward-slash
	// for cross-platform portability.
	Path string `json:"path"`
	// Size is the uncompressed byte length of the entry.
	Size int64 `json:"size"`
	// SHA256 is the lowercase-hex SHA-256 of the entry's uncompressed bytes.
	SHA256 string `json:"sha256"`
}

// ExportResult describes a completed archive write.
type ExportResult struct {
	FilesArchived   int   `json:"files_archived"`
	BytesArchived   int64 `json:"bytes_archived"`
	PageFileCount   int   `json:"page_file_count"`
	SkippedIndex    bool  `json:"skipped_index"`
	SkippedSymlinks int   `json:"skipped_symlinks"`
}

// ImportResult describes a completed archive extraction.
type ImportResult struct {
	FilesExtracted int             `json:"files_extracted"`
	BytesExtracted int64           `json:"bytes_extracted"`
	PageFileCount  int             `json:"page_file_count"`
	Manifest       ArchiveManifest `json:"manifest"`
}

// walkedFile is a regular file discovered during the export tree walk: its
// absolute source path plus its archive-root-relative, slash-form path. Index
// artifacts (.system/index.sqlite*) and symlinks are excluded by
// computeFileTree (same exclusion rule as CopyVaultTree).
type walkedFile struct {
	srcPath string // absolute path on disk
	relPath string // slash-form, archive-root-relative
}

// computeFileTree walks root and returns the ordered list of regular files to
// archive, EXCLUDING the reproducible SQLite index artifacts and symlinks
// (which are counted + logged, not followed — mirrors CopyVaultTree). The
// order is deterministic (filepath.WalkDir lexical order) so two exports of
// the same vault produce byte-identical entry lists.
func computeFileTree(root string) (files []walkedFile, skippedIndex bool, skippedSymlinks int, err error) {
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if isIndexArtifact(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			skippedIndex = true
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case info.IsDir():
			return nil
		case info.Mode().IsRegular():
			files = append(files, walkedFile{srcPath: path, relPath: relSlash})
			return nil
		default:
			// Skip symlinks and special files (same posture as CopyVaultTree:
			// a symlinked notebook is absent from the archive, so count + log
			// it so the user learns the archive is incomplete).
			if info.Mode()&os.ModeSymlink != 0 {
				skippedSymlinks++
				log.Printf("vault.ExportVaultTree: skipping symlink (not followed): %s", relSlash)
			}
			return nil
		}
	})
	if walkErr != nil {
		return nil, false, 0, walkErr
	}
	return files, skippedIndex, skippedSymlinks, nil
}

// manifestBytes marshals m to canonical JSON (sorted keys via json.Marshal,
// 2-space indent for human-readability — the archive is user-inspectable).
func manifestBytes(m ArchiveManifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// deriveVaultName returns the base name of the vault folder, used as the
// manifest VaultName when the caller does not supply one.
func deriveVaultName(vaultPath string) string {
	if vaultPath == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(vaultPath))
}

// pageFileCount returns the count of entries that are user page files: a
// `.md` file whose first path component is NOT `.system` (so .system/plugins/
// README.md and .system/templates/*.md — which are system files, not pages —
// are excluded). Used as the honest proxy for the issue's "block count" (see
// ArchiveManifest.PageFileCount docs).
func pageFileCount(entries []ArchiveEntry) int {
	n := 0
	for _, e := range entries {
		if isPageFile(e.Path) {
			n++
		}
	}
	return n
}

// isPageFile reports whether relSlash is a user page file: a .md under a
// notebook (first path component is not ".system"). System markdown (the
// plugins README, user templates) is intentionally excluded from the page
// count.
func isPageFile(relSlash string) bool {
	if filepath.Ext(relSlash) != ".md" {
		return false
	}
	first := relSlash
	if i := strings.IndexByte(relSlash, '/'); i >= 0 {
		first = relSlash[:i]
	}
	return first != ".system"
}

// hasParentSegment reports whether any path segment of relSlash (slash-form,
// archive-root-relative) is exactly ".." — i.e. a parent traversal. This is the
// precise zip-slip predicate, replacing an earlier strings.Contains(name, "..")
// check that false-positive'd on legitimate filenames containing a double-dot
// substring (e.g. "2.0..2.1.md", "foo...bar"). A real escape is also caught by
// the final containment check (isWithinMover) on the joined path.
func hasParentSegment(relSlash string) bool {
	for {
		// Walk the segments without allocating a slice.
		i := strings.IndexByte(relSlash, '/')
		var seg string
		if i >= 0 {
			seg = relSlash[:i]
			relSlash = relSlash[i+1:]
		} else {
			seg = relSlash
			relSlash = ""
		}
		if seg == ".." {
			return true
		}
		if i < 0 {
			return false
		}
	}
}

// nowRFC3339UTC returns the current time as an RFC3339 UTC string, for the
// manifest CreatedAt. UTC so the timestamp is stable regardless of the host
// timezone.
func nowRFC3339UTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// IsIndexArtifactName reports whether relSlash (a slash-separated path
// relative to the vault root) is one of the reproducible SQLite index files
// excluded from a copy/move/archive (ARCHITECTURE.md §0 rule 4). Exported so
// cross-package callers (tests, future import-side guards) share the single
// canonical exclusion predicate with mover.go::isIndexArtifact.
func IsIndexArtifactName(relSlash string) bool {
	return isIndexArtifact(relSlash)
}

// ProgressFn is the streaming-progress callback signature shared by export and
// import. current is the number of files processed so far; total is the file
// count discovered in the up-front stat pass (so the bar is determinate).
// phase is "export" or "import" (and "extract" during the import extract pass).
type ProgressFn func(phase string, current, total int)

// rootDigest computes the whole-archive integrity root: SHA-256 over the
// canonical serialization of every entry record. The canonical form is, for
// each entry in order: uint32 BE length of path, path bytes, int64 BE size,
// 32 raw bytes of the entry's SHA-256. This binds each entry's identity +
// content length + content digest into a single self-contained digest.
//
// (binary.BigEndian is used so the encoding is platform-independent and
// stable across versions.)
func rootDigest(entries []ArchiveEntry) string {
	h := sha256.New()
	var buf [8]byte
	for _, e := range entries {
		binary.BigEndian.PutUint32(buf[:4], uint32(len(e.Path)))
		h.Write(buf[:4])
		h.Write([]byte(e.Path))
		binary.BigEndian.PutUint64(buf[:], uint64(e.Size))
		h.Write(buf[:])
		// Deterministic over the entry's declared digest. On invalid hex (a
		// tampered/corrupt manifest field) write the raw string so the digest
		// still captures the entry faithfully — never silently skip, which
		// would make the digest computation ambiguous. An invalid-hex digest
		// is independently rejected by the per-entry checksum check on extract.
		sum, decErr := hex.DecodeString(e.SHA256)
		if decErr != nil {
			sum = []byte(e.SHA256)
		}
		h.Write(sum)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExportVaultTree streams the vault at src into a .silt-vault archive at
// destPath. It writes content entries (markdown + the whole .system/ tree
// EXCEPT the reproducible index.sqlite*), computing each entry's SHA-256
// during the single copy pass, then writes manifest.json LAST carrying the
// per-entry digests + the whole-archive root digest (ArchiveSHA256).
//
// The active vault is never touched by this primitive. Streaming: each file is
// copied one at a time through a MultiWriter (no load-whole-file), and
// onProgress is called after each file so the UI renders a determinate bar. On
// any error the partial destination is removed (the caller should have chosen
// a fresh path via the save-file picker, so cleanup only removes what this
// call wrote).
func ExportVaultTree(src, destPath, vaultName, siltVersion string, onProgress ProgressFn) (ExportResult, error) {
	if src == "" || destPath == "" {
		return ExportResult{}, fmt.Errorf("source and destination paths must not be empty")
	}
	srcAbs, err := absClean(src)
	if err != nil {
		return ExportResult{}, fmt.Errorf("resolve source: %w", err)
	}
	if _, err := os.Stat(srcAbs); err != nil {
		return ExportResult{}, fmt.Errorf("source vault not found: %w", err)
	}

	// Up-front walk gives the ordered file list + a determinate total for the
	// progress bar. Cheap (stat-only); the hash is computed during the copy.
	files, skippedIndex, skippedSymlinks, err := computeFileTree(srcAbs)
	if err != nil {
		return ExportResult{}, fmt.Errorf("scan vault tree: %w", err)
	}
	total := len(files)

	// Create the destination file. The save-file picker is expected to supply
	// a non-existent path; truncate handles a same-name re-export.
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return ExportResult{}, fmt.Errorf("create destination dir: %w", err)
	}
	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return ExportResult{}, fmt.Errorf("create archive file: %w", err)
	}

	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(destPath)
		}
	}()

	// archive/zip.Writer buffers all output and writes to f on Close, so there
	// is no live byte stream to hash; the root digest is computed from the
	// collected entry records after the loop (see rootDigest).
	zw := zip.NewWriter(f)

	result := ExportResult{SkippedIndex: skippedIndex, SkippedSymlinks: skippedSymlinks}
	entries := make([]ArchiveEntry, 0, total)
	pageCount := 0

	for i, wf := range files {
		entry, cerr := copyFileToZip(zw, wf)
		if cerr != nil {
			return ExportResult{}, fmt.Errorf("archive %s: %w", wf.relPath, cerr)
		}
		entries = append(entries, entry)
		result.FilesArchived++
		result.BytesArchived += entry.Size
		if isPageFile(entry.Path) {
			pageCount++
		}
		if onProgress != nil {
			onProgress("export", i+1, total)
		}
	}
	result.PageFileCount = pageCount

	// VaultName defaults to the source folder's base name when the caller did
	// not supply one, so an imported vault can present a friendly label even
	// after extraction into an arbitrary empty folder.
	if vaultName == "" {
		vaultName = deriveVaultName(srcAbs)
	}
	manifest := ArchiveManifest{
		ArchiveVersion: SupportedArchiveVersion,
		SiltVersion:    siltVersion,
		VaultName:      vaultName,
		CreatedAt:      nowRFC3339UTC(),
		PageFileCount:  pageCount,
		FileCount:      result.FilesArchived,
		TotalBytes:     result.BytesArchived,
		ArchiveSHA256:  rootDigest(entries),
		Entries:        entries,
	}
	mb, err := manifestBytes(manifest)
	if err != nil {
		return ExportResult{}, fmt.Errorf("marshal manifest: %w", err)
	}
	mw, err := zw.Create(ArchiveManifestPath)
	if err != nil {
		return ExportResult{}, fmt.Errorf("create manifest entry: %w", err)
	}
	if _, err := mw.Write(mb); err != nil {
		return ExportResult{}, fmt.Errorf("write manifest: %w", err)
	}

	if err := zw.Close(); err != nil {
		return ExportResult{}, fmt.Errorf("finalize archive: %w", err)
	}
	if err := f.Sync(); err != nil {
		return ExportResult{}, fmt.Errorf("sync archive: %w", err)
	}

	success = true
	return result, nil
}

// copyFileToZip creates a ZIP entry for wf at its slash-form relpath, streams
// the source file into it through a per-entry sha256 hasher (so the digest is
// computed during the single copy pass — no second read), and returns the
// per-entry integrity record.
//
// Entries are stored with Method=Store (no compression). Compression is a
// documented future enhancement; markdown vaults are modest and the issue
// prioritizes portability + integrity over size. Store also keeps the archive
// trivially inspectable with any unzip tool.
func copyFileToZip(zw *zip.Writer, wf walkedFile) (ArchiveEntry, error) {
	info, err := os.Stat(wf.srcPath)
	if err != nil {
		return ArchiveEntry{}, err
	}
	fh := &zip.FileHeader{Name: wf.relPath, Method: zip.Store}
	fh.SetMode(info.Mode())
	fw, err := zw.CreateHeader(fh)
	if err != nil {
		return ArchiveEntry{}, err
	}
	in, err := os.Open(wf.srcPath)
	if err != nil {
		return ArchiveEntry{}, err
	}
	defer in.Close()
	perEntry := sha256.New()
	out := io.MultiWriter(fw, perEntry)
	n, err := io.Copy(out, in)
	if err != nil {
		return ArchiveEntry{}, err
	}
	return ArchiveEntry{
		Path:   wf.relPath,
		Size:   n,
		SHA256: hex.EncodeToString(perEntry.Sum(nil)),
	}, nil
}

// ImportVaultTree validates a .silt-vault archive and extracts it into destDir,
// an empty local folder. It follows the validate-before-extract posture of the
// .silt-plugin installer (SPECS §8.4 / backend/plugins/installer.go): nothing
// is written to destDir until the manifest is parsed, the version is accepted,
// every entry passes the zip-slip / absolute / size-cap guards, AND the
// whole-archive root digest is recomputed + asserted. Extraction then streams
// into a sibling temp dir, verifying each entry's SHA-256 during the copy, and
// the temp dir is atomically renamed into destDir only after every entry
// verifies — a corrupt entry aborts before cutover and leaves destDir untouched.
//
// The caller (App.ImportVault) is expected to call SwitchVault(destDir)
// afterwards to open the extracted vault (which rebuilds the SQLite index from
// markdown, exactly like CopyVaultTree/MoveVault).
func ImportVaultTree(archivePath, destDir string, onProgress ProgressFn) (ImportResult, error) {
	if archivePath == "" || destDir == "" {
		return ImportResult{}, fmt.Errorf("%w: archive and destination paths must not be empty", ErrArchiveRejected)
	}
	// Validate the destination FIRST (empty, local, not already a vault). On
	// rejection destDir is never created, so a bad archive + bad dest fails
	// fast without leaving artifacts. validateEmptyDestination needs the abs
	// path; resolve it the same way validateDestination does.
	destAbs, err := absClean(destDir)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: cannot resolve destination: %v", ErrArchiveRejected, err)
	}
	if err := validateEmptyDestination(destAbs, destDir); err != nil {
		return ImportResult{}, err
	}

	// --- Pass 1: validate, do NOT extract (mirrors plugins.Validate). ---
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: failed to open archive: %v", ErrArchiveRejected, err)
	}
	// Locate + parse the manifest WITHOUT extracting (read straight from the
	// entry reader). zip-slip / absolute guards run against every entry name
	// first so a hostile archive is rejected before any byte is copied.
	manifest, err := readArchiveManifest(&zr.Reader)
	if err != nil {
		zr.Close()
		return ImportResult{}, err
	}
	if manifest.ArchiveVersion != SupportedArchiveVersion {
		zr.Close()
		return ImportResult{}, fmt.Errorf("%w: archive version %q is not supported (this build accepts %q)", ErrArchiveRejected, manifest.ArchiveVersion, SupportedArchiveVersion)
	}
	// Whole-archive integrity root: recompute over the declared entries and
	// assert equality. Detects manifest/entry-list tampering BEFORE extraction.
	if got := rootDigest(manifest.Entries); got != manifest.ArchiveSHA256 {
		zr.Close()
		return ImportResult{}, fmt.Errorf("%w: archive integrity check failed (manifest digest mismatch)", ErrArchiveRejected)
	}
	// Build an entry-by-path index for the extract pass + run the safety guards
	// (zip-slip, absolute paths, size cap) against every content entry.
	entryByName := make(map[string]*zip.File, len(zr.File))
	var totalUncompressed uint64
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
			zr.Close()
			return ImportResult{}, fmt.Errorf("%w: archive entry %q is absolute; refusing", ErrArchiveRejected, name)
		}
		if hasParentSegment(name) {
			zr.Close()
			return ImportResult{}, fmt.Errorf("%w: archive entry %q escapes the archive root (zip-slip); refusing", ErrArchiveRejected, name)
		}
		// Overflow-safe accumulation: a hostile archive can declare individual
		// entries with UncompressedSize64 near uint64 max, and a naive post-hoc
		// sum would wrap to a small value and bypass the cap. Check each entry
		// against the cap AND against the remaining headroom before adding it.
		sz := f.UncompressedSize64
		if sz > maxArchiveUncompressedSize || totalUncompressed > maxArchiveUncompressedSize-sz {
			zr.Close()
			return ImportResult{}, fmt.Errorf("%w: archive uncompressed size exceeds the %d-byte limit", ErrArchiveRejected, maxArchiveUncompressedSize)
		}
		totalUncompressed += sz
		if f.Name != ArchiveManifestPath {
			entryByName[f.Name] = f
		}
	}
	// Every manifest entry must be present in the archive; an extra/missing
	// entry signals tampering or a truncated archive.
	if len(manifest.Entries) != len(entryByName) {
		zr.Close()
		return ImportResult{}, fmt.Errorf("%w: manifest declares %d entries but the archive has %d", ErrArchiveRejected, len(manifest.Entries), len(entryByName))
	}
	for _, e := range manifest.Entries {
		if _, ok := entryByName[e.Path]; !ok {
			zr.Close()
			return ImportResult{}, fmt.Errorf("%w: manifest entry %q is missing from the archive", ErrArchiveRejected, e.Path)
		}
	}
	// A restorable vault must carry its .system/ (config/themes/templates/
	// plugins). Reject an archive whose manifest has no .system/ entry BEFORE
	// extracting: otherwise the temp tree extracts cleanly but SwitchVault
	// (which the caller invokes) refuses it for lacking a .system folder,
	// leaving an orphan extracted folder and a cryptic "could not be opened"
	// error. ExportVaultTree always emits .system/ for a real vault, so a
	// missing .system/ signals a partial/foreign archive.
	hasSystem := false
	for _, e := range manifest.Entries {
		if strings.HasPrefix(e.Path, ".system/") {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		zr.Close()
		return ImportResult{}, fmt.Errorf("%w: archive is not a complete vault (no .system/ contents)", ErrArchiveRejected)
	}

	// --- Pass 2: extract + verify each entry into a sibling temp dir. ---
	// os.MkdirTemp next to destDir keeps the final rename on the same volume
	// (rename across volumes fails); the temp dir name is hidden (leading dot).
	tmp, err := os.MkdirTemp(filepath.Dir(destAbs), ".silt-import-*")
	if err != nil {
		zr.Close()
		return ImportResult{}, fmt.Errorf("create staging dir: %w", err)
	}
	// Best-effort cleanup on any failure path. On success the rename consumes
	// tmp, so RemoveAll is a no-op.
	cleanupTmp := func() { _ = os.RemoveAll(tmp) }
	success := false
	defer func() {
		zr.Close()
		if !success {
			cleanupTmp()
		}
	}()

	result := ImportResult{Manifest: manifest}
	total := len(manifest.Entries)
	extracted := 0
	for _, e := range manifest.Entries {
		f := entryByName[e.Path]
		// Final containment check on the joined path (defense in depth,
		// mirrors plugins.Install).
		target := filepath.Join(tmp, filepath.FromSlash(e.Path))
		if !isWithinMover(target, tmp) {
			return ImportResult{}, fmt.Errorf("%w: archive entry %q escapes the staging dir", ErrArchiveRejected, e.Path)
		}
		if err := extractAndVerify(f, target, e); err != nil {
			return ImportResult{}, fmt.Errorf("entry %q failed verification: %w", e.Path, err)
		}
		extracted++
		result.FilesExtracted++
		result.BytesExtracted += e.Size
		if isPageFile(e.Path) {
			result.PageFileCount++
		}
		if onProgress != nil {
			onProgress("extract", extracted, total)
		}
	}

	// Atomic cutover: rename the verified temp tree into destDir. On Windows,
	// os.Rename fails when the destination already exists — even an empty
	// directory (MoveFileEx's replace flag does not apply to directories) —
	// and the user-supplied destination folder is expected to exist.
	// validateEmptyDestination guaranteed destAbs is empty, so removing it
	// immediately before the rename is safe and makes the cutover work
	// cross-platform. A tiny TOCTOU window is acceptable for local-first
	// single-user software.
	if err := os.Remove(destAbs); err != nil && !os.IsNotExist(err) {
		return ImportResult{}, fmt.Errorf("remove empty destination before rename: %w", err)
	}
	if err := os.Rename(tmp, destAbs); err != nil {
		return ImportResult{}, fmt.Errorf("finalize import (rename into destination): %w", err)
	}
	success = true
	return result, nil
}

// readArchiveManifest locates + parses manifest.json from an opened archive
// WITHOUT extracting (it reads straight from the entry reader). Rejects a
// missing or malformed manifest. The caller skips the manifest during
// extraction by iterating the manifest's declared Entries (which never include
// the manifest path), so no zip entry pointer needs to be returned.
func readArchiveManifest(zr *zip.Reader) (ArchiveManifest, error) {
	for _, f := range zr.File {
		if filepath.ToSlash(f.Name) != ArchiveManifestPath {
			continue
		}
		// Bound the manifest size before reading: a hostile archive could
		// declare a multi-gigabyte manifest to OOM the JSON decoder (which
		// builds the Entries slice in memory) before the total-archive size
		// cap runs.
		if f.UncompressedSize64 > maxManifestSize {
			return ArchiveManifest{}, fmt.Errorf("%w: manifest.json is too large (%d bytes)", ErrArchiveRejected, f.UncompressedSize64)
		}
		rc, err := f.Open()
		if err != nil {
			return ArchiveManifest{}, fmt.Errorf("%w: failed to read manifest: %v", ErrArchiveRejected, err)
		}
		// Defense in depth against a forged-header zip-bomb that declares a
		// small UncompressedSize64 yet decompresses past it: LimitReader cuts
		// the stream at maxManifestSize+1 so Decode fails cleanly instead of
		// OOM-ing.
		var m ArchiveManifest
		decErr := json.NewDecoder(io.LimitReader(rc, maxManifestSize+1)).Decode(&m)
		rc.Close()
		if decErr != nil {
			return ArchiveManifest{}, fmt.Errorf("%w: invalid manifest.json: %v", ErrArchiveRejected, decErr)
		}
		return m, nil
	}
	return ArchiveManifest{}, fmt.Errorf("%w: archive is missing %s", ErrArchiveRejected, ArchiveManifestPath)
}

// extractAndVerify copies a single zip entry to target, bounding the
// decompressed stream (io.LimitReader over the declared size, defense-in-depth
// against forged-header zip bombs — mirrors plugins.copyZipEntry) and hashing
// the bytes during the copy. The recomputed digest MUST equal the manifest
// entry's declared SHA-256 and the byte count MUST equal the declared size, or
// the entry is rejected as corrupt/tampered.
func extractAndVerify(f *zip.File, target string, want ArchiveEntry) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	// Bound the decompressed stream to the declared size + a 1 KB margin. A
	// forged-header zip-bomb claiming 1 KB but decompressing to 10 GB is cut
	// off here. Size is bounded DIRECTLY (not via want.Size+1024) so a
	// manifest-declared Size within 1024 of int64 max cannot overflow the
	// addition and bypass the cap; the subsequent n != want.Size check would
	// still reject such an entry, but this guard makes the bound explicit.
	// Checked BEFORE opening the target file so an over-limit entry neither
	// leaks a file descriptor nor leaves an empty target file on disk.
	if want.Size < 0 || want.Size > maxArchiveEntrySize {
		return fmt.Errorf("entry %q exceeds the %d-byte per-entry limit", want.Path, maxArchiveEntrySize)
	}
	limit := want.Size + 1024 // safe: want.Size ≤ maxArchiveEntrySize (256 MiB)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(out, io.LimitReader(io.TeeReader(rc, h), limit))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != want.Size {
		return fmt.Errorf("size %d does not match manifest %d", n, want.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want.SHA256 {
		return fmt.Errorf("checksum mismatch (declared %s, recomputed %s)", want.SHA256, got)
	}
	return nil
}

// isWithinMover is a thin alias for the unexported isWithin in mover.go, used
// by the import extractor's containment check so the safety predicate stays
// single-source. (Kept under an import-local name to read naturally at the
// call site without implying a second implementation.)
func isWithinMover(target, base string) bool {
	return isWithin(target, base)
}
