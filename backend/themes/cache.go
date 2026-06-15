package themes

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// themeCache is a process-local, mtime-aware cache of parsed themes used by
// the webview launch path to resolve the active theme's bg.void without
// re-reading the on-disk file every time. Sprint 5's main.go only
// resolved the embedded default's bg.void; for users on a non-default
// custom theme the pre-CSS flash matched the default palette (the visible
// "first-paint flash for non-default themes" tracked in #73).
//
// The cache is in-process only and is invalidated by ImportTheme so a
// freshly imported theme is picked up on the next launch-resolution
// call. We deliberately do not hook fsnotify: custom themes change
// rarely, and the existing ConfigWatcher already proves the watch path
// is non-trivial to keep race-free. A per-import invalidation is
// sufficient.
type themeCache struct {
	mu      sync.RWMutex
	entries map[string]themeCacheEntry
}

type themeCacheEntry struct {
	t        *Theme
	loadedAt time.Time
	modTime  time.Time // modtime on disk at load time; mismatch → reload
}

// cacheTTL bounds how long a cached entry is considered fresh even if
// the mtime check hasn't triggered a reload. The mtime check is the
// primary freshness gate; this TTL is defense-in-depth against
// filesystems with coarse mtime resolution where a quick edit-then-read
// could see the same mtime.
const cacheTTL = 5 * time.Minute

var globalThemeCache = &themeCache{
	entries: map[string]themeCacheEntry{},
}

// CachedThemeByID returns the parsed theme for the given id, using the
// process-local cache when possible. The embedded default (id ==
// DefaultThemeID or empty) is always served from ParseDefault — the
// canonical embedded copy is authoritative and can never be stale
// relative to the binary.
func CachedThemeByID(themesDir, id string) (*Theme, error) {
	if id == "" || id == DefaultThemeID {
		return ParseDefault()
	}
	// Reject any id that would escape the themes dir if used as a
	// filename component (CWE-22). An invalid id falls through to the
	// embedded default rather than erroring — the active theme should
	// never block the first paint of the app.
	if !IsValidThemeID(id) {
		return ParseDefault()
	}
	if themesDir == "" {
		// No vault open yet: resolve a first-class id from embed so the
		// pre-CSS paint matches the active theme rather than flashing the
		// default; fall back to the default for unknown ids.
		if t, ok := ParseEmbeddedByID(id); ok {
			return t, nil
		}
		return ParseDefault()
	}

	// Fast path: cache hit with a fresh modtime.
	now := time.Now()
	path := filepath.Join(themesDir, id+".json")
	info, err := os.Stat(path)
	if err != nil {
		// The id is not on disk. Before falling back to the default, try
		// the embedded first-class copy: a wiped themes dir (or an existing
		// vault scaffolded before the theme shipped) should still resolve a
		// non-default first-class active theme so the first paint matches.
		// A genuinely unknown id still falls through to the default.
		if t, ok := ParseEmbeddedByID(id); ok {
			return t, nil
		}
		return ParseDefault()
	}

	globalThemeCache.mu.RLock()
	entry, ok := globalThemeCache.entries[id]
	globalThemeCache.mu.RUnlock()
	if ok && entry.t != nil && entry.modTime.Equal(info.ModTime()) && now.Sub(entry.loadedAt) < cacheTTL {
		return entry.t, nil
	}

	// Slow path: parse, then cache.
	t, err := LoadTheme(path)
	if err != nil {
		// Bad file on disk (someone hand-edited it into a broken state);
		// fall back to the embedded default so the app still launches.
		return ParseDefault()
	}
	globalThemeCache.mu.Lock()
	globalThemeCache.entries[id] = themeCacheEntry{
		t:        t,
		loadedAt: now,
		modTime:  info.ModTime(),
	}
	globalThemeCache.mu.Unlock()
	return t, nil
}

// InvalidateThemeCache drops one (or all) entries from the in-process
// cache. Called by App.ImportTheme so a freshly imported theme is
// served on the next launch-resolution call without waiting for the
// 5-minute TTL to expire.
func InvalidateThemeCache(ids ...string) {
	globalThemeCache.mu.Lock()
	defer globalThemeCache.mu.Unlock()
	if len(ids) == 0 {
		globalThemeCache.entries = map[string]themeCacheEntry{}
		return
	}
	for _, id := range ids {
		delete(globalThemeCache.entries, id)
	}
}

// ResetCacheForTests clears the entire cache. Test-only (exported so
// test files in the same package can call it from beforeEach); not
// used by production code. Mirrors the _resetForTests pattern in the
// frontend theme store.
func ResetCacheForTests() {
	globalThemeCache.mu.Lock()
	defer globalThemeCache.mu.Unlock()
	globalThemeCache.entries = map[string]themeCacheEntry{}
}
