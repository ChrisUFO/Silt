package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// templateCache is a process-local, mtime-aware cache of parsed on-disk user
// templates. It is the direct port of backend/themes/cache.go, scoped to
// templates: the live-preview path calls GetTemplate repeatedly (once per
// keystroke in the placeholder form), and re-reading + re-parsing the .md file
// each time is wasteful. Embedded first-class templates are served from the
// embed (already in memory) and are never cached here — they are cheap and
// authoritative from the binary.
//
// The cache is in-process only. InvalidateTemplateCache is called after a
// SaveTemplate/DeleteTemplate so the next GetTemplate re-reads the new file.
// The file watcher's reload also invalidates the whole cache when an external
// edit lands. We deliberately do not hook fsnotify inside the cache — the
// dedicated TemplateWatcher (watcher.go) owns observation and calls
// InvalidateTemplateCache on change.
type templateCache struct {
	mu      sync.RWMutex
	entries map[string]templateCacheEntry
}

type templateCacheEntry struct {
	t        *Template
	loadedAt time.Time
	modTime  time.Time // on-disk modtime at load; mismatch → reload
}

// cacheTTL bounds how long an entry is considered fresh even if the mtime
// check has not fired. The mtime check is the primary freshness gate; this TTL
// is defense-in-depth against filesystems with coarse mtime resolution.
const cacheTTL = 5 * time.Minute

var globalTemplateCache = &templateCache{
	entries: map[string]templateCacheEntry{},
}

// CachedGetTemplate returns the parsed on-disk template for id, using the
// cache when the file is unchanged. Embedded ids are served from the embed
// (never cached). A cache miss with an unreadable/invalid file returns the
// underlying error; callers fall back to the embedded lookup or fail loudly.
func CachedGetTemplate(templatesDir, id string) (*Template, error) {
	if id == "" {
		return nil, ErrTemplateNotFound
	}
	// Embedded first-class templates are authoritative from the binary; serve
	// them directly (cheap, never stale relative to the build).
	if IsBuiltinID(id) {
		if t, ok := ParseEmbeddedByID(id); ok {
			return t, nil
		}
	}
	if templatesDir == "" {
		return nil, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}

	path := filepath.Join(templatesDir, id+".md")
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
	}

	now := time.Now()
	globalTemplateCache.mu.RLock()
	entry, ok := globalTemplateCache.entries[id]
	globalTemplateCache.mu.RUnlock()
	if ok && entry.t != nil && entry.modTime.Equal(info.ModTime()) && now.Sub(entry.loadedAt) < cacheTTL {
		return entry.t, nil
	}

	t, err := loadOne(path)
	if err != nil {
		return nil, err
	}
	globalTemplateCache.mu.Lock()
	globalTemplateCache.entries[id] = templateCacheEntry{
		t:        t,
		loadedAt: now,
		modTime:  info.ModTime(),
	}
	globalTemplateCache.mu.Unlock()
	return t, nil
}

// InvalidateTemplateCache drops one (or all) entries. Called by
// App.SaveUserTemplate/DeleteUserTemplate (so the next GetTemplate re-reads the
// new file) and by the watcher's reload (so an external edit is picked up).
func InvalidateTemplateCache(ids ...string) {
	globalTemplateCache.mu.Lock()
	defer globalTemplateCache.mu.Unlock()
	if len(ids) == 0 {
		globalTemplateCache.entries = map[string]templateCacheEntry{}
		return
	}
	for _, id := range ids {
		delete(globalTemplateCache.entries, id)
	}
}

// ResetCacheForTests clears the entire cache. Test-only (exported so test files
// in the same package can call it from setup); not used by production code.
// Mirrors themes.ResetCacheForTests.
func ResetCacheForTests() {
	globalTemplateCache.mu.Lock()
	defer globalTemplateCache.mu.Unlock()
	globalTemplateCache.entries = map[string]templateCacheEntry{}
}
