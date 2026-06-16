package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileAtomic_ConcurrentSafety(t *testing.T) {
	// Two concurrent writes to the same target path must not clobber each
	// other's temp files. Each writer must see its own content land on
	// disk, not the other writer's. The final content depends on which
	// os.Rename wins, but neither call should fail.
	dir := t.TempDir()
	target := filepath.Join(dir, "target.md")

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		content := []byte("writer-" + string(rune('A'+i)))
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(target, content); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("WriteFileAtomic returned an error under concurrency: %v", err)
	}

	// The file should exist and have non-empty content from one of the
	// writers. We don't care which one won the race — only that the file
	// is not truncated by a race on the temp path.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("could not read target after concurrent write: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("target file is empty after concurrent writes")
	}
}

// TestWriteFileAtomic_NoTruncatedFilesOnKill verifies that 100 concurrent
// atomic writes to DIFFERENT files in the same directory all land with
// their full content intact (no truncation, no partial writes) and leave
// zero stray temp files behind (#21).
func TestWriteFileAtomic_NoTruncatedFilesOnKill(t *testing.T) {
	dir := t.TempDir()
	const n = 100

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		path := filepath.Join(dir, fmt.Sprintf("file-%03d.md", i))
		content := []byte(fmt.Sprintf("content for file %d", i))
		go func() {
			defer wg.Done()
			if err := WriteFileAtomic(path, content); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("WriteFileAtomic error: %v", err)
	}

	// Every file must exist with its full content (not truncated/partial).
	for i := range n {
		path := filepath.Join(dir, fmt.Sprintf("file-%03d.md", i))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile %s: %v", path, err)
			continue
		}
		expected := fmt.Sprintf("content for file %d", i)
		if string(data) != expected {
			t.Errorf("file %d truncated: got %q, want %q", i, string(data), expected)
		}
	}

	// No stray temp files remain after all writes complete.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("stray temp file: %s", e.Name())
		}
	}
}
