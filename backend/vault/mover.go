package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silt/backend/db"
)

// indexArtifactPrefix is the relpath prefix (slash-separated, relative to the
// vault root) of the SQLite index files. The index is reproducible working
// memory (ARCHITECTURE.md §0 rule 4), so it is NEVER copied by a vault move
// or copy — the destination rebuilds it from markdown on first open. Matching
// on a prefix also catches the WAL (-wal), shared-memory (-shm), and any
// legacy -journal auxiliary files.
const indexArtifactPrefix = ".system/index.sqlite"

// networkFSCheck is the network-filesystem detector used by validateDestination.
// Package-level so tests can swap it to simulate a network mount (which a CI
// temp dir cannot reproduce) without changing the validateDestination
// signature. Defaults to db.IsNetworkFS.
var networkFSCheck = db.IsNetworkFS

// ErrDestinationRejected is returned by validateDestination when the chosen
// destination cannot be used. The wrapped message is user-actionable.
var ErrDestinationRejected = errors.New("vault destination rejected")

// CopyResult describes a completed vault-tree copy (the shared core of both
// Copy and Move). Counts cover regular files only; directories and skipped
// index artifacts are not counted as "files copied".
type CopyResult struct {
	FilesCopied  int   `json:"files_copied"`
	BytesCopied  int64 `json:"bytes_copied"`
	SkippedIndex bool  `json:"skipped_index"`
	// SkippedSymlinks is the count of symlinks encountered and NOT followed
	// (filepath.WalkDir does not follow them). A symlinked notebook is absent
	// from the copy, so this lets the UI warn the user the copy is incomplete.
	SkippedSymlinks int `json:"skipped_symlinks"`
}

// MoveVaultResult is returned by App.MoveVault. It embeds CopyResult (the
// copy + verify stats) and adds the cutover bookkeeping. RemoveOldErr is the
// non-fatal error, if any, from the optional post-cutover removal of the old
// vault — the cutover itself already succeeded when this is set.
type MoveVaultResult struct {
	CopyResult
	From         string `json:"from"`
	To           string `json:"to"`
	RemoveOldErr string `json:"remove_old_err,omitempty"`
}

// validateDestination enforces the move/copy destination rules and returns
// ErrDestinationRejected wrapping a user-readable reason on rejection.
//
//	dest must not equal src, sit inside src, or contain src (recursive copy);
//	dest must not be on a network filesystem (WAL requires shared memory);
//	dest must not already look like a Silt vault (.system/ present) and must
//	otherwise be empty or non-existent (no silent merge with other content).
func validateDestination(src, dest string) error {
	// Reject empty inputs up front: absClean("") resolves to the working
	// directory, which could otherwise lead to copying the vault into / wiping
	// the CWD if a caller passed an unset path.
	if src == "" || dest == "" {
		return fmt.Errorf("%w: source and destination paths must not be empty", ErrDestinationRejected)
	}
	srcAbs, err := absClean(src)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve source: %v", ErrDestinationRejected, err)
	}
	destAbs, err := absClean(dest)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve destination: %v", ErrDestinationRejected, err)
	}

	if srcAbs == destAbs {
		return fmt.Errorf("%w: destination is the same as the current vault", ErrDestinationRejected)
	}
	if isWithin(destAbs, srcAbs) {
		return fmt.Errorf("%w: destination is inside the current vault (would copy recursively)", ErrDestinationRejected)
	}
	if isWithin(srcAbs, destAbs) {
		return fmt.Errorf("%w: destination contains the current vault (would copy recursively)", ErrDestinationRejected)
	}
	return validateEmptyDestination(destAbs, dest)
}

// validateEmptyDestination enforces the destination rules that don't depend on
// a source path: the folder must be local (not a network FS — WAL requires
// shared memory), must not already look like a Silt vault (no .system/), and
// must be empty or non-existent (no silent merge with unrelated content).
// Shared by Move/Copy (via validateDestination) and Import (#143), so the
// import destination meets the identical contract a move destination does.
// destAbs is the resolved absolute path; destDisplay is the original argument
// used in user-facing error messages.
func validateEmptyDestination(destAbs, destDisplay string) error {
	// Network-FS guard reuses the index-opener's per-platform detector so the
	// user sees the same clear "move to a local folder" message (#79, #141).
	if err := networkFSCheck(destAbs); err != nil {
		return fmt.Errorf("%w: %v", ErrDestinationRejected, err)
	}

	// Refuse a destination that already looks like a vault, or any non-empty
	// destination (no silent merge with unrelated content).
	systemDir := filepath.Join(destAbs, ".system")
	if _, err := os.Stat(systemDir); err == nil {
		return fmt.Errorf("%w: %s already contains a .system folder (already a Silt vault); choose an empty folder", ErrDestinationRejected, destDisplay)
	}
	if existing, err := os.ReadDir(destAbs); err == nil && len(existing) > 0 {
		return fmt.Errorf("%w: %s is not empty; choose an empty folder", ErrDestinationRejected, destDisplay)
	}
	return nil
}

// CopyVaultTree copies the entire vault tree at src into dest, EXCLUDING the
// reproducible SQLite index artifacts (.system/index.sqlite*), which the
// destination rebuilds from markdown on first open. It validates the
// destination, performs the copy, then verifies every byte (size + SHA-256).
//
// On any validation/copy/verify failure it best-effort removes dest (which
// validateDestination guarantees was empty or non-existent) so a partial copy
// never lingers. The active vault is never touched by this primitive — the
// cutover (settings + services reinit) is the caller's responsibility.
func CopyVaultTree(src, dest string) (CopyResult, error) {
	if err := validateDestination(src, dest); err != nil {
		return CopyResult{}, err
	}
	srcAbs, _ := absClean(src)
	destAbs, _ := absClean(dest)

	if err := os.MkdirAll(destAbs, 0755); err != nil {
		return CopyResult{}, fmt.Errorf("create destination: %w", err)
	}

	// Cleanup any partial copy on failure. Safe because validateDestination
	// ensured dest was empty or non-existent, so RemoveAll only removes what
	// this call wrote.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(destAbs)
		}
	}()

	var result CopyResult
	walkErr := filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // root already created above
		}
		if isIndexArtifact(filepath.ToSlash(rel)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			result.SkippedIndex = true
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		destPath := filepath.Join(destAbs, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(destPath, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyRegularFile(path, destPath, info.Mode().Perm(), &result)
		default:
			// Skip symlinks and special files. Silt's scanner also skips
			// symlinks; they are not part of the vault content model. Count
			// + log each symlink so a user whose vault has a symlinked
			// notebook learns the copy is incomplete (that notebook is
			// absent from the destination) rather than discovering it later.
			if info.Mode()&os.ModeSymlink != 0 {
				result.SkippedSymlinks++
				log.Printf("CopyVaultTree: skipping symlink (not followed): %s", rel)
			}
			return nil
		}
	})
	if walkErr != nil {
		return CopyResult{}, fmt.Errorf("copy vault tree: %w", walkErr)
	}

	if err := verifyCopy(srcAbs, destAbs); err != nil {
		return CopyResult{}, fmt.Errorf("verify copied vault: %w", err)
	}

	success = true
	return result, nil
}

// verifyCopy walks src and dest in lockstep and asserts every regular,
// non-index file copied 1:1 (same size + SHA-256) and that dest holds no
// unexpected extra files. A mismatch means the copy is not byte-identical and
// the move/copy must abort before any cutover.
func verifyCopy(srcAbs, destAbs string) error {
	expected, err := collectFileHashes(srcAbs)
	if err != nil {
		return fmt.Errorf("hash source: %w", err)
	}
	seen, err := collectFileHashes(destAbs)
	if err != nil {
		return fmt.Errorf("hash destination: %w", err)
	}
	if len(expected) != len(seen) {
		return fmt.Errorf("file count mismatch: source has %d files, destination has %d", len(expected), len(seen))
	}
	for rel, srcHash := range expected {
		destHash, ok := seen[rel]
		if !ok {
			return fmt.Errorf("missing in destination: %s", rel)
		}
		if srcHash.size != destHash.size {
			return fmt.Errorf("size mismatch for %s: source %d bytes, destination %d bytes", rel, srcHash.size, destHash.size)
		}
		if srcHash.sum != destHash.sum {
			return fmt.Errorf("checksum mismatch for %s", rel)
		}
	}
	return nil
}

// RemoveOldVault deletes a vault directory after a successful move. It is
// deliberately narrow: the path must be absolute-izable and must contain a
// `.system` folder (i.e. look like a Silt vault), otherwise it refuses — this
// is a guard against accidental deletion if the caller passes a stale/wrong
// path. The caller is expected to have already confirmed with the user.
func RemoveOldVault(oldPath string) error {
	// Reject an empty path immediately: absClean("") resolves to the current
	// working directory, which — if it happened to contain a .system folder —
	// would be catastrophic (RemoveAll on the CWD). Defense in depth even
	// though the only caller (MoveVault) guards src != "" today.
	if oldPath == "" {
		return fmt.Errorf("empty vault path")
	}
	abs, err := absClean(oldPath)
	if err != nil {
		return fmt.Errorf("resolve old vault path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(abs, ".system")); err != nil {
		return fmt.Errorf("not a Silt vault (no .system folder): %s", oldPath)
	}
	return os.RemoveAll(abs)
}

// SourceModifiedAfter reports whether any regular, non-index file under root
// has an mtime at or after cutoff. MoveVault snapshots the source vault the
// instant its copy+verify completes and calls this before the post-cutover
// removal of the old folder: ARCHITECTURE.md lets external editors 
// write vault files concurrently, and an edit landing in the
// copy→cutover→removeOld window would be silently lost when the source is
// deleted. A "modified since copy" result means the move MUST keep the old
// folder in place rather than delete it (#141 review). Returns false for an
// empty root (nothing to check).
func SourceModifiedAfter(root string, cutoff time.Time) (bool, error) {
	if root == "" {
		return false, nil
	}
	modified := false
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || isIndexArtifact(filepath.ToSlash(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		// !Before (>=) is conservative: treat an edit at the exact cutoff
		// instant as "changed" so a borderline file is never deleted away
		// from its owner.
		if !info.ModTime().Before(cutoff) {
			modified = true
		}
		return nil
	})
	return modified, walkErr
}

// --- helpers ---------------------------------------------------------------

// absClean returns the absolute, cleaned form of p. Used so structural
// comparisons in validateDestination are not fooled by trailing slashes, ".",
// or relative segments.
func absClean(p string) (string, error) {
	return filepath.Abs(filepath.Clean(p))
}

// isWithin reports whether child is path-equal to, or nested anywhere inside,
// parent. Both arguments must be absolute + cleaned.
func isWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true // child == parent
	}
	// If the first segment of rel is "..", child escapes parent.
	first := rel
	if i := strings.Index(rel, string(filepath.Separator)); i >= 0 {
		first = rel[:i]
	}
	return first != ".."
}

// isIndexArtifact reports whether relSlash (a slash-separated path relative to
// the vault root) is one of the reproducible SQLite index files that must be
// excluded from a copy/move.
func isIndexArtifact(relSlash string) bool {
	return strings.HasPrefix(relSlash, indexArtifactPrefix)
}

func copyRegularFile(srcPath, destPath string, mode fs.FileMode, result *CopyResult) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	result.FilesCopied++
	result.BytesCopied += n
	return nil
}

type fileHash struct {
	size int64
	sum  string // lowercase hex sha256
}

// collectFileHashes walks root and returns a map of slash-separated relpath →
// sha256+size for every regular file, skipping index artifacts, directories,
// and symlinks. Used by verifyCopy for both sides of the comparison so the
// skip rules are identical.
func collectFileHashes(root string) (map[string]fileHash, error) {
	out := map[string]fileHash{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || isIndexArtifact(filepath.ToSlash(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		sum, err := sha256OfFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fileHash{size: info.Size(), sum: sum}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
