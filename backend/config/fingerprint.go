package config

// fingerprint.go provides the cross-platform RootFingerprint helper used by
// the F3 linked-notebook trust anchor. ComputeRootFingerprint returns a
// stable, opaque string that identifies the on-disk entity at absPath at the
// moment of the call. If the entity changes identity (a synced edit to
// config.yaml redirects root_path to a different folder, or the folder is
// moved to a new inode), the fingerprint changes, and the verification in
// resolveNotebookDir quarantines the link.
//
// Platform implementations:
//   - POSIX (Linux, darwin, freebsd): device id + inode from
//     syscall.Stat_t (the directory mtime is deliberately excluded — adding or
//     removing a page inside the root mutates it, which would invalidate the
//     fingerprint on every CRUD op). A rename WITHIN the same filesystem
//     preserves the inode; a cross-filesystem move changes it (benign — the
//     re-link prompt handles this).
//   - Windows: volume serial number + file index from
//     GetFileInformationByHandle (the NTFS stable identity).
//
// The fingerprint is opaque to YAML consumers — it round-trips through
// config.yaml as a short hex string, and no consumer interprets it except the
// verifier.

import (
	"fmt"
	"os"
)

// ComputeRootFingerprint returns a stable fingerprint for the directory at
// absPath. Returns an error if the path does not exist or is not accessible.
// The caller (LinkNotebook) captures this at link time; resolveNotebookDir
// recomputes and compares on every access.
func ComputeRootFingerprint(absPath string) (string, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("fingerprint: stat %q: %w", absPath, err)
	}
	return computeFingerprint(absPath, info)
}
