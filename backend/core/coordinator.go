package core

import (
	"database/sql"
	"sort"
	"sync"
	"sync/atomic"
)

// fileMutexEntry is the value stored in ExecutionCoordinator.ioMu for each
// path. The generation counter lets ReleaseFileMutex invalidate a stale entry
// WITHOUT invalidating an in-flight lock holder: a holder that already passed
// its generation check owns the mutex until it unlocks, while a waiter that
// loaded the about-to-be-released entry detects the bumped generation after
// acquiring and retries against the fresh entry. See LockFileWrite.
type fileMutexEntry struct {
	mu  *sync.Mutex
	gen int64 // bumped on release so waiters detect staleness
}

type ExecutionCoordinator struct {
	dbMu sync.RWMutex
	// ioMu maps filepath -> *fileMutexEntry. Entries are added on first use
	// and removed by ReleaseFileMutex (driven by the watcher on Remove/Rename
	// events) so the working set stays proportional to the active vault
	// rather than to the cumulative history of distinct paths (#30).
	ioMu sync.Map
	// blockMu maps block UUID -> *sync.Mutex for per-block write-intent
	// locking (#64). Prevents a full-page SaveFileBlocks from clobbering a
	// concurrent single-block MutateBlock when both target the same block.
	blockMu sync.Map
	db      *sql.DB
}

func NewExecutionCoordinator(db *sql.DB) *ExecutionCoordinator {
	return &ExecutionCoordinator{
		db: db,
	}
}

// getFileEntry returns the current fileMutexEntry for path (creating it on
// first use) and the generation to check against after locking.
func (ec *ExecutionCoordinator) getFileEntry(path string) (*fileMutexEntry, int64) {
	iface, _ := ec.ioMu.LoadOrStore(path, &fileMutexEntry{mu: &sync.Mutex{}})
	e := iface.(*fileMutexEntry)
	return e, atomic.LoadInt64(&e.gen)
}

// LockFileWrite runs task while holding the per-file write mutex for path,
// serializing app-driven and watcher-driven file mutations. It tolerates
// concurrent ReleaseFileMutex: if the entry was released (generation bumped
// + deleted) while a caller was waiting on the lock, the caller detects the
// stale generation after acquiring, drops the orphaned lock, and re-acquires
// the fresh entry. No in-flight holder is ever invalidated — release only
// prevents NEW callers from serializing against the deleted entry.
func (ec *ExecutionCoordinator) LockFileWrite(path string, task func()) {
	entry, gen := ec.getFileEntry(path)
	for {
		entry.mu.Lock()
		if atomic.LoadInt64(&entry.gen) == gen {
			// Current-generation lock acquired; run the critical section.
			defer entry.mu.Unlock()
			task()
			return
		}
		// Stale: the entry was released while we waited. Drop the orphaned
		// lock and re-acquire the fresh entry so we serialize correctly.
		entry.mu.Unlock()
		entry, gen = ec.getFileEntry(path)
	}
}

// ReleaseFileMutex evicts the per-file mutex for path, bounding ioMu growth.
// Safe to call concurrently with LockFileWrite: it bumps the entry's
// generation (so any waiter holding a pointer to this entry retries against
// the fresh one) and then deletes the map entry. A caller that already holds
// the lock keeps it until its own Unlock — this never invalidates a holder.
// Idempotent: a no-op if there is no entry for path.
func (ec *ExecutionCoordinator) ReleaseFileMutex(path string) {
	iface, ok := ec.ioMu.Load(path)
	if !ok {
		return
	}
	entry := iface.(*fileMutexEntry)
	atomic.AddInt64(&entry.gen, 1)
	ec.ioMu.Delete(path)
}

// getBlockMutex returns the per-block mutex for blockID (creating on first use).
func (ec *ExecutionCoordinator) getBlockMutex(blockID string) *sync.Mutex {
	mu, _ := ec.blockMu.LoadOrStore(blockID, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// LockBlockWrite runs task while holding the per-block write-intent lock for
// blockID (#64). This serializes MutateBlock (single-block) against
// SaveFileBlocks (full-page) so the last writer never silently clobbers the
// other when both target the same block. The block lock is acquired OUTSIDE
// the per-file lock so they compose without deadlock.
func (ec *ExecutionCoordinator) LockBlockWrite(blockID string, task func()) {
	mu := ec.getBlockMutex(blockID)
	mu.Lock()
	defer mu.Unlock()
	task()
}

// LockBlocksWrite acquires per-block locks for ALL given blockIDs (sorted +
// deduped to prevent deadlock) before running task. Used by SaveFileBlocks so
// a concurrent MutateBlock for any block in the page waits until the full-page
// save completes.
func (ec *ExecutionCoordinator) LockBlocksWrite(blockIDs []string, task func()) {
	sorted := make([]string, 0, len(blockIDs))
	seen := make(map[string]bool, len(blockIDs))
	for _, id := range blockIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	for _, id := range sorted {
		mu := ec.getBlockMutex(id)
		mu.Lock()
		defer mu.Unlock()
	}
	task()
}

func (ec *ExecutionCoordinator) WithDBRead(fn func()) {
	ec.dbMu.RLock()
	defer ec.dbMu.RUnlock()
	fn()
}

func (ec *ExecutionCoordinator) WithDBWrite(fn func()) {
	ec.dbMu.Lock()
	defer ec.dbMu.Unlock()
	fn()
}

func (ec *ExecutionCoordinator) WithDBReadResult(fn func() error) error {
	ec.dbMu.RLock()
	defer ec.dbMu.RUnlock()
	return fn()
}

func (ec *ExecutionCoordinator) WithDBWriteResult(fn func() error) error {
	ec.dbMu.Lock()
	defer ec.dbMu.Unlock()
	return fn()
}

func (ec *ExecutionCoordinator) LockDBWrite(task func()) {
	ec.dbMu.Lock()
	defer ec.dbMu.Unlock()
	task()
}

func (ec *ExecutionCoordinator) LockDBRead(task func()) {
	ec.dbMu.RLock()
	defer ec.dbMu.RUnlock()
	task()
}
