//go:build !windows

package config

import (
	"fmt"
	"os"
	"syscall"
)

// computeFingerprint returns a "dev:ino:mtime" fingerprint for POSIX systems.
// A rename within the same filesystem preserves the inode; a cross-filesystem
// move (or a delete+recreate) changes it, which trips the quarantine and
// surfaces the re-link prompt.
func computeFingerprint(absPath string, info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Should never happen on a POSIX system for a local path; if it does,
		// fail closed (return an error so LinkNotebook refuses to proceed
		// without a fingerprint rather than storing an empty one).
		return "", fmt.Errorf("fingerprint: unsupported stat type %T for %q", info.Sys(), absPath)
	}
	return fmt.Sprintf("%x:%x:%x", stat.Dev, stat.Ino, stat.Mtim.Sec), nil
}
