package parser

import (
	"os"
	"path/filepath"
	"time"
)

// WriteFileAtomic writes content to a temporary file, flushes it to disk,
// and atomically renames it to the target path. The temp file is created
// via os.CreateTemp so that two concurrent writers of the same target do
// not clobber each other's temp files; the rename is what makes the
// visible swap atomic at the filesystem level.
func WriteFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// os.CreateTemp guarantees a unique filename in dir. The pattern
	// "<basename>.*.tmp" is enough to keep related temps grouped when a
	// developer inspects the directory.
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	// success gates the cleanup so a successful rename leaves the new
	// file in place rather than deleting it as a "failed temp".
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		_ = tmpFile.Close()
		return err
	}

	// Flush to storage hardware so the rename below is durable across
	// power loss.
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}

	// Close before rename on all platforms.
	if err := tmpFile.Close(); err != nil {
		return err
	}

	// On Windows, concurrent rename/replace operations on the same file can
	// cause transient sharing violations or access denied errors.
	// We retry with a brief backoff to make it robust.
	for i := 0; ; i++ {
		err = os.Rename(tmpPath, path)
		if err == nil {
			break
		}
		if i >= 10 {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}

	success = true
	return nil
}
