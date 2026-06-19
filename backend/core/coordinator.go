package core

import (
	"database/sql"
	"sort"
	"sync"
)

// fileMutexEntry is the value stored in ExecutionCoordinator.ioMu for each
// path. It carries only the mutex; staleness is detected by re-checking the
// live ioMu map AFTER locking (see LockFileWrite) rather than with a generation
// counter. The previous generation approach had a TOCTOU window because gen
// was read BEFORE the mutex was acquired, so a concurrently-released entry
// could hand a caller the already-bumped gen and let it proceed on an orphaned
// entry (#131). The map-lookup check closes that window without per-entry
// state and keeps a single source of truth for the eviction idiom across both
// ioMu (per-path) and blockMu (per-block).
type fileMutexEntry struct {
	mu *sync.Mutex
}

// blockMutexEntry is the per-block analog of fileMutexEntry (#122). The same
// map-lookup staleness check (see LockBlockWrite) applies: a concurrent
// ReleaseBlockMutex can delete (and force a future LoadOrStore to replace) the
// entry, so a waiter re-checks the live map value after locking.
type blockMutexEntry struct {
	mu *sync.Mutex
}

type ExecutionCoordinator struct {
	dbMu sync.RWMutex
	// ioMu maps filepath -> *fileMutexEntry. Entries are added on first use
	// and removed by ReleaseFileMutex (driven by the watcher on Remove/Rename
	// events) so the working set stays proportional to the active vault
	// rather than to the cumulative history of distinct paths (#30).
	ioMu sync.Map
	// blockMu maps block UUID -> *blockMutexEntry for per-block write-intent
	// locking (#64). Prevents a full-page SaveFileBlocks from clobbering a
	// concurrent single-block MutateBlock when both target the same block.
	// Entries are evicted by ReleaseBlockMutex on block deletion / file
	// eviction so the map does not grow with the cumulative history of every
	// block UUID ever locked (#122).
	blockMu sync.Map
	db      *sql.DB
}

func NewExecutionCoordinator(db *sql.DB) *ExecutionCoordinator {
	return &ExecutionCoordinator{
		db: db,
	}
}

// getFileEntry returns the current fileMutexEntry for path, creating it on
// first use. Callers MUST re-check that this entry is still the live ioMu
// value AFTER acquiring entry.mu (see LockFileWrite): a concurrent
// ReleaseFileMutex can delete (and force a future LoadOrStore to replace) it.
func (ec *ExecutionCoordinator) getFileEntry(path string) *fileMutexEntry {
	iface, _ := ec.ioMu.LoadOrStore(path, &fileMutexEntry{mu: &sync.Mutex{}})
	return iface.(*fileMutexEntry)
}

// LockFileWrite runs task while holding the per-file write mutex for path,
// serializing app-driven and watcher-driven file mutations. It tolerates
// concurrent ReleaseFileMutex: after acquiring entry.mu it re-checks that
// entry is still the live ioMu value; if ReleaseFileMutex deleted (and a later
// caller replaced) it while we waited, we drop the orphaned lock and retry
// against the fresh entry. No in-flight holder is ever invalidated — release
// only prevents NEW callers from serializing against the deleted entry.
func (ec *ExecutionCoordinator) LockFileWrite(path string, task func()) {
	for {
		entry := ec.getFileEntry(path)
		entry.mu.Lock()
		if current, ok := ec.ioMu.Load(path); ok && current == entry {
			defer entry.mu.Unlock()
			task()
			return
		}
		entry.mu.Unlock()
	}
}

// ReleaseFileMutex evicts the per-file mutex for path, bounding ioMu growth.
// Safe to call concurrently with LockFileWrite: it simply deletes the map
// entry, so any waiter that later re-checks the map (after acquiring the
// orphaned mutex) sees the entry is gone or replaced and retries against the
// fresh one. A caller that already holds the lock keeps it until its own
// Unlock — this never invalidates a holder. Idempotent: a no-op if there is
// no entry for path.
func (ec *ExecutionCoordinator) ReleaseFileMutex(path string) {
	ec.ioMu.Delete(path)
}

// getBlockEntry returns the current blockMutexEntry for blockID, creating it on
// first use. Callers MUST re-check that this entry is still the live blockMu
// entry AFTER acquiring entry.mu (see LockBlockWrite): a concurrent
// ReleaseBlockMutex can delete (and force a future LoadOrStore to replace) it.
func (ec *ExecutionCoordinator) getBlockEntry(blockID string) *blockMutexEntry {
	iface, _ := ec.blockMu.LoadOrStore(blockID, &blockMutexEntry{mu: &sync.Mutex{}})
	return iface.(*blockMutexEntry)
}

// LockBlockWrite runs task while holding the per-block write-intent lock for
// blockID (#64). This serializes MutateBlock (single-block) against
// SaveFileBlocks (full-page) so the last writer never silently clobbers the
// other when both target the same block. The block lock is acquired OUTSIDE
// the per-file lock so they compose without deadlock. It tolerates concurrent
// ReleaseBlockMutex: after acquiring entry.mu it re-checks that entry is still
// the live blockMu value; if ReleaseBlockMutex deleted (and a later caller
// replaced) it while we waited, we drop the orphaned lock and retry against the
// fresh entry. A caller that already passed the check owns the mutex until it
// unlocks — release never invalidates an in-flight holder.
func (ec *ExecutionCoordinator) LockBlockWrite(blockID string, task func()) {
	for {
		entry := ec.getBlockEntry(blockID)
		entry.mu.Lock()
		if current, ok := ec.blockMu.Load(blockID); ok && current == entry {
			defer entry.mu.Unlock()
			task()
			return
		}
		entry.mu.Unlock()
	}
}

// LockBlocksWrite acquires per-block locks for ALL given blockIDs (sorted +
// deduped to prevent deadlock) before running task. Used by SaveFileBlocks so
// a concurrent MutateBlock for any block in the page waits until the full-page
// save completes. Tolerates concurrent ReleaseBlockMutex: after acquiring each
// entry.mu it re-checks that the entry is still the live blockMu value; if any
// entry was released (and replaced) while we waited, we release everything
// acquired so far and retry against fresh entries. No in-flight holder is ever
// invalidated.
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

	for {
		acquired := make([]*blockMutexEntry, 0, len(sorted))
		stale := false
		for _, id := range sorted {
			entry := ec.getBlockEntry(id)
			entry.mu.Lock()
			if current, ok := ec.blockMu.Load(id); !ok || current != entry {
				// This entry was released while we waited. Drop it and the
				// partial set, then retry the whole acquisition.
				entry.mu.Unlock()
				stale = true
				break
			}
			acquired = append(acquired, entry)
		}
		if stale {
			for i := len(acquired) - 1; i >= 0; i-- {
				acquired[i].mu.Unlock()
			}
			continue
		}
		// All live-entry locks held. Run the critical section and release in
		// reverse acquisition order (incl. on panic).
		func() {
			defer unlockBlockEntries(acquired)
			task()
		}()
		return
	}
}

func unlockBlockEntries(entries []*blockMutexEntry) {
	for i := len(entries) - 1; i >= 0; i-- {
		entries[i].mu.Unlock()
	}
}

// ReleaseBlockMutex evicts the per-block mutex for blockID, bounding blockMu
// growth (#122). Safe to call concurrently with LockBlockWrite/LockBlocksWrite:
// it simply deletes the map entry, so any waiter that later re-checks the map
// (after acquiring the orphaned mutex) sees the entry is gone or replaced and
// retries against the fresh one. A caller that already holds the lock keeps it
// until its own Unlock — this never invalidates a holder. Idempotent: a no-op
// if there is no entry for blockID.
func (ec *ExecutionCoordinator) ReleaseBlockMutex(blockID string) {
	ec.blockMu.Delete(blockID)
}

// ReleaseBlockMutexes evicts the per-block mutex for each ID. See
// ReleaseBlockMutex. Used by batch eviction paths (page delete, file eviction).
func (ec *ExecutionCoordinator) ReleaseBlockMutexes(blockIDs []string) {
	for _, id := range blockIDs {
		ec.ReleaseBlockMutex(id)
	}
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
