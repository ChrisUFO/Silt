package themes

import (
	"os"
	"testing"
	"time"
)

// writeBytes is a tiny helper for writing raw bytes (used by cache tests
// that need to write broken JSON to test the invalid-file fallback).
func writeBytes(t *testing.T, path string, b []byte) error {
	t.Helper()
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func dirOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
}

// touchFile sets the mtime of path to t.
func touchFile(path string, t time.Time) error {
	return os.Chtimes(path, t, t)
}
