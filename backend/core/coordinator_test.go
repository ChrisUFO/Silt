package core

import (
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestCoordinator(t *testing.T) *ExecutionCoordinator {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return NewExecutionCoordinator(sqlDB)
}

func TestExecutionCoordinator_DBSerializesWrites(t *testing.T) {
	ec := newTestCoordinator(t)

	const goroutines = 8
	var inFlight int32
	var maxInFlight int32
	var wg sync.WaitGroup

	ec.WithDBWrite(func() {
		// Hold the write lock while we measure concurrency.
	})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ec.WithDBWrite(func() {
				cur := atomic.AddInt32(&inFlight, 1)
				for {
					m := atomic.LoadInt32(&maxInFlight)
					if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
			})
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Errorf("expected write lock to serialize, observed max in-flight = %d", got)
	}
}

func TestExecutionCoordinator_DBReadAllowsConcurrency(t *testing.T) {
	ec := newTestCoordinator(t)

	const goroutines = 8
	var inFlight int32
	var maxInFlight int32
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ec.WithDBRead(func() {
				cur := atomic.AddInt32(&inFlight, 1)
				for {
					m := atomic.LoadInt32(&maxInFlight)
					if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
			})
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInFlight); got < 2 {
		t.Errorf("expected read lock to allow concurrency, observed max in-flight = %d", got)
	}
}

func TestExecutionCoordinator_FileWriteLocksArePerFile(t *testing.T) {
	ec := newTestCoordinator(t)
	fileA := filepath.Join(t.TempDir(), "a.md")
	fileB := filepath.Join(t.TempDir(), "b.md")

	var overlap int32
	var maxOverlap int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	hold := func() {
		<-start
		cur := atomic.AddInt32(&overlap, 1)
		for {
			m := atomic.LoadInt32(&maxOverlap)
			if cur <= m || atomic.CompareAndSwapInt32(&maxOverlap, m, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&overlap, -1)
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		ec.LockFileWrite(fileA, hold)
	}()
	go func() {
		defer wg.Done()
		ec.LockFileWrite(fileB, hold)
	}()

	close(start)
	wg.Wait()

	// Different files should run in parallel; if the locks were shared, the
	// max in-flight would be 1.
	if got := atomic.LoadInt32(&maxOverlap); got < 2 {
		t.Errorf("expected per-file locks to allow concurrency, observed max overlap = %d", got)
	}
}

func TestExecutionCoordinator_SameFileWritesAreSerialized(t *testing.T) {
	ec := newTestCoordinator(t)
	file := filepath.Join(t.TempDir(), "shared.md")

	var overlap int32
	var maxOverlap int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	hold := func() {
		<-start
		cur := atomic.AddInt32(&overlap, 1)
		for {
			m := atomic.LoadInt32(&maxOverlap)
			if cur <= m || atomic.CompareAndSwapInt32(&maxOverlap, m, cur) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&overlap, -1)
	}

	const goroutines = 4
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ec.LockFileWrite(file, hold)
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&maxOverlap); got != 1 {
		t.Errorf("expected same-file writes to be serialized, observed max overlap = %d", got)
	}
}

func TestExecutionCoordinator_WithDBReadResultReturnsError(t *testing.T) {
	ec := newTestCoordinator(t)

	sentinel := errSentinel("boom")
	got := ec.WithDBReadResult(func() error {
		return sentinel
	})
	if got != sentinel {
		t.Errorf("expected sentinel error to propagate, got %v", got)
	}

	if err := ec.WithDBReadResult(func() error { return nil }); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// --- Phase 5a: per-file mutex eviction (#30) ---

// TestReleaseFileMutex_EntryDeleted verifies that after ReleaseFileMutex the
// ioMu map no longer holds an entry for the path (the eviction actually ran).
func TestReleaseFileMutex_EntryDeleted(t *testing.T) {
	ec := newTestCoordinator(t)
	path := "/vault/file-a.md"

	done := make(chan struct{})
	ec.LockFileWrite(path, func() { close(done) })
	<-done

	if _, ok := ec.ioMu.Load(path); !ok {
		t.Fatal("expected ioMu entry to exist after a LockFileWrite")
	}

	ec.ReleaseFileMutex(path)

	if _, ok := ec.ioMu.Load(path); ok {
		t.Fatal("expected ioMu entry to be deleted after ReleaseFileMutex")
	}
}

// TestReleaseFileMutex_NextAcquireGetsFreshEntry proves a post-release caller
// lands on a brand-new entry (a fresh mutex generation), not the stale one.
func TestReleaseFileMutex_NextAcquireGetsFreshEntry(t *testing.T) {
	ec := newTestCoordinator(t)
	path := "/vault/file-b.md"

	ec.LockFileWrite(path, func() {})
	first, _ := ec.ioMu.Load(path)
	ec.ReleaseFileMutex(path)

	ec.LockFileWrite(path, func() {})
	second, _ := ec.ioMu.Load(path)

	if first.(*fileMutexEntry).mu == second.(*fileMutexEntry).mu {
		t.Error("post-release LockFileWrite reused the evicted mutex instead of creating a fresh one")
	}
}

// TestReleaseFileMutex_NoDeadlockWithInFlightHolder runs a LockFileWrite that
// holds the lock while another goroutine calls ReleaseFileMutex, then a third
// goroutine calls LockFileWrite. The third must complete (no deadlock) and
// serialize correctly against the released-then-recreated entry. Repeated
// under -race to catch any data race in the generation handoff.
func TestReleaseFileMutex_NoDeadlockWithInFlightHolder(t *testing.T) {
	ec := newTestCoordinator(t)
	path := "/vault/file-c.md"

	holderReleased := make(chan struct{})
	holderDone := make(chan struct{})
	// 1. Acquire and hold.
	go func() {
		ec.LockFileWrite(path, func() {
			// 2. While held, a watcher-style release fires (eviction on a
			//    Remove/Rename event). This must not panic or deadlock.
			ec.ReleaseFileMutex(path)
			close(holderReleased)
			// Hold a bit longer so the third caller has to wait on the
			// released (orphaned) entry first, then retry.
			time.Sleep(40 * time.Millisecond)
		})
		close(holderDone)
	}()

	<-holderReleased

	// 3. A new caller arrives while the holder is still in its critical
	//    section. It should block on the orphaned lock, detect the stale
	//    generation after the holder unlocks, retry against the fresh entry,
	//    and complete.
	ran := make(chan struct{})
	go func() {
		ec.LockFileWrite(path, func() { close(ran) })
	}()

	select {
	case <-ran:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("LockFileWrite deadlocked after a concurrent ReleaseFileMutex")
	}
	<-holderDone
}

// TestReleaseFileMutex_ConcurrentCallersSerialize runs many concurrent
// LockFileWrite callers against a path that is repeatedly released mid-flight,
// confirming none are lost (every task runs) and the race detector stays clean.
func TestReleaseFileMutex_ConcurrentCallersSerialize(t *testing.T) {
	ec := newTestCoordinator(t)
	path := "/vault/file-d.md"

	var ran int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// A "watcher" goroutine repeatedly evicts the mutex.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				ec.ReleaseFileMutex(path)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	const n = 100
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ec.LockFileWrite(path, func() {
				atomic.AddInt64(&ran, 1)
			})
		}()
	}
	wg.Wait()
	close(stop)

	if got := atomic.LoadInt64(&ran); got != n {
		t.Errorf("expected all %d critical sections to run, got %d (some were lost)", n, got)
	}
}
