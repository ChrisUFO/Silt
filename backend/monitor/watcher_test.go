package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"silt/backend/core"
	"silt/backend/db"
)

func TestDirectoryWatcher_ReindexFileHoldsFileLock(t *testing.T) {
	// Verifies the fix for the lost-update race: reindexFile must hold
	// the per-file IO lock for the duration of read+parse+write+index
	// so a concurrent UpdateBlockState cannot land between the watcher's
	// read and the watcher's eventual write.
	vaultPath := t.TempDir()

	dm, err := db.NewDatabaseManager()
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })

	coord := core.NewExecutionCoordinator(dm.SQLDB())
	tracker := NewWriteTracker()
	t.Cleanup(tracker.Stop)

	dw, err := NewDirectoryWatcher(vaultPath, dm, tracker, coord, 4)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher: %v", err)
	}

	filePath := filepath.Join(vaultPath, "test.md")
	if err := os.WriteFile(filePath, []byte(
		"# Test <!-- id: aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n"+
			"- [ ] TODO TASK x <!-- id: bbbb2222-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->\n",
	), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Hold the file lock from an outside goroutine for 200ms.
	lockHeld := make(chan struct{})
	lockReleased := make(chan struct{})
	go func() {
		coord.LockFileWrite(filePath, func() {
			close(lockHeld)
			time.Sleep(200 * time.Millisecond)
			close(lockReleased)
		})
	}()
	<-lockHeld

	// Start reindexFile. It must block on the file lock.
	reindexReturned := make(chan struct{})
	go func() {
		dw.reindexFile(filePath)
		close(reindexReturned)
	}()

	select {
	case <-reindexReturned:
		t.Fatalf("reindexFile returned while the per-file lock was held; the lock is not being acquired")
	case <-time.After(50 * time.Millisecond):
		// Good: reindexFile is still blocked. Fall through.
	}

	// Wait for the outer lock to release; reindexFile should then run
	// to completion.
	select {
	case <-reindexReturned:
		// success
	case <-time.After(2 * time.Second):
		t.Fatalf("reindexFile did not return within 2s after the file lock was released")
	}
	<-lockReleased
}

func TestDirectoryWatcher_ReindexFileIndexesFile(t *testing.T) {
	// Smoke test: reindexFile writes block IDs into the file (when
	// missing) and indexes the file's blocks into the database. Verifies
	// the watcher end-to-end contract that the previous lock fix could
	// have broken.
	vaultPath := t.TempDir()

	dm, err := db.NewDatabaseManager()
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })

	coord := core.NewExecutionCoordinator(dm.SQLDB())
	tracker := NewWriteTracker()
	t.Cleanup(tracker.Stop)

	dw, err := NewDirectoryWatcher(vaultPath, dm, tracker, coord, 4)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher: %v", err)
	}

	filePath := filepath.Join(vaultPath, "Work", "Journal", "Daily", "2026-06-13.md")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(
		"# Today <!-- id: 11111111-1111-1111-1111-111111111111 -->\n"+
			"\n"+
			"- [ ] TODO TASK sample <!-- id: 22222222-2222-2222-2222-222222222222 -->\n",
	), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	dw.reindexFile(filePath)

	// File should now have content; the parser may or may not have
	// rewritten it depending on whether the input was already valid.
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read after reindex: %v", err)
	}
	if len(content) == 0 {
		t.Fatalf("file is empty after reindex")
	}

	// Database should now have both blocks.
	for _, id := range []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	} {
		var n int
		if err := dm.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatalf("count block %s: %v", id, err)
		}
		if n != 1 {
			t.Errorf("expected block %s to be indexed, got count %d", id, n)
		}
	}
}

func TestDirectoryWatcher_FocusLockSuppressesReindex(t *testing.T) {
	vaultPath := t.TempDir()

	dm, err := db.NewDatabaseManager()
	if err != nil {
		t.Fatalf("NewDatabaseManager: %v", err)
	}
	t.Cleanup(func() { _ = dm.Close() })

	coord := core.NewExecutionCoordinator(dm.SQLDB())
	tracker := NewWriteTracker()
	t.Cleanup(tracker.Stop)

	dw, err := NewDirectoryWatcher(vaultPath, dm, tracker, coord, 4)
	if err != nil {
		t.Fatalf("NewDirectoryWatcher: %v", err)
	}
	// The file must live under <vault>/<notebook>/<section>/<page>/ to resolve
	// in the 3-level model. Create the dir before Start so the watcher
	// subscribes to it during AddRecursive.
	filePath := filepath.Join(vaultPath, "Work", "Journal", "Daily", "test.md")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := dw.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = dw.Close() })

	// Step 1: write a file and wait for the watcher to index it.
	writeContent := func(text string) {
		if err := os.WriteFile(filePath, []byte(text), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	writeContent("# Initial <!-- id: aaaa0000-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n")

	waitForBlock := func(id string, wantCount int, timeout time.Duration) {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			var n int
			if err := dm.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE id = ?", id).Scan(&n); err == nil && n == wantCount {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		var n int
		_ = dm.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE id = ?", id).Scan(&n)
		t.Fatalf("timed out waiting for block %s count=%d (want %d)", id, n, wantCount)
	}

	waitForBlock("aaaa0000-aaaa-aaaa-aaaa-aaaaaaaaaaaa", 1, 3*time.Second)

	// Step 2: lock the file.
	dw.LockFocus(filePath)

	// Step 3: write new content while locked. The watcher must NOT reindex.
	writeContent("# Updated <!-- id: aaaa0000-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n" +
		"- [ ] TODO TASK locked content <!-- id: bbbb1111-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->\n")

	// Give the watcher time to (incorrectly) process it.
	time.Sleep(1 * time.Second)

	var lockedCount int
	_ = dm.SQLDB().QueryRow("SELECT COUNT(*) FROM blocks WHERE id = ?", "bbbb1111-bbbb-bbbb-bbbb-bbbbbbbbbbbb").Scan(&lockedCount)
	if lockedCount != 0 {
		t.Fatalf("expected locked content to NOT be indexed, but found %d rows for bbbb1111", lockedCount)
	}

	// Step 4: unlock and write again. The watcher must now reindex.
	dw.UnlockFocus(filePath)
	writeContent("# Re-indexed <!-- id: aaaa0000-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n" +
		"- [ ] TODO TASK unlocked content <!-- id: cccc2222-cccc-cccc-cccc-cccccccccccc -->\n")

	waitForBlock("cccc2222-cccc-cccc-cccc-cccccccccccc", 1, 3*time.Second)
}
