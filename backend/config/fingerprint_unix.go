//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// computeFingerprint returns a "dev:ino" fingerprint for POSIX systems.
// A rename within the same filesystem preserves the inode; a cross-filesystem
// move (or a delete+recreate) changes it, which trips the quarantine and
// surfaces the re-link prompt. Only the device + inode are used: the directory
// mtime is intentionally excluded because adding/removing a page inside the
// linked root legitimately mutates the directory's mtime, which would
// otherwise invalidate the fingerprint on every CRUD op (and mtime adds no
// tamper-detection value — `touch -r` defeats it).
func computeFingerprint(absPath string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Should never happen on a POSIX system for a local path; if it does,
		// fail closed (return an error so LinkNotebook refuses to proceed
		// without a fingerprint rather than storing an empty one).
		return "", fmt.Errorf("fingerprint: unsupported stat type %T for %q", info.Sys(), absPath)
	}
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
