package monitor

import (
	"sync"
	"time"
)

type WriteTracker struct {
	mu           sync.Mutex
	activeWrites map[string]time.Time
}

func NewWriteTracker() *WriteTracker {
	return &WriteTracker{
		activeWrites: make(map[string]time.Time),
	}
}

func (wt *WriteTracker) RegisterWrite(filepath string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	wt.activeWrites[filepath] = time.Now()
}

func (wt *WriteTracker) IsSelfGenerated(filepath string) bool {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	t, exists := wt.activeWrites[filepath]
	if !exists {
		return false
	}
	// If write event fired within 300ms of our atomic write, ignore it
	if time.Since(t) < 300*time.Millisecond {
		delete(wt.activeWrites, filepath)
		return true
	}
	return false
}
