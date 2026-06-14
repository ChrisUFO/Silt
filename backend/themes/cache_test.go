package themes

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCachedThemeByID_EmptyIDFallsBackToDefault(t *testing.T) {
	th, err := CachedThemeByID(t.TempDir(), "")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != DefaultThemeID {
		t.Errorf("expected embedded default, got %+v", th)
	}
}

func TestCachedThemeByID_BuiltInIDFallsBackToDefault(t *testing.T) {
	th, err := CachedThemeByID(t.TempDir(), DefaultThemeID)
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != DefaultThemeID {
		t.Errorf("expected embedded default, got %+v", th)
	}
}

func TestCachedThemeByID_UnknownIDFallsBackToDefault(t *testing.T) {
	// No vault loaded, no file on disk — fall back to the embedded
	// default rather than failing the launch. The active id might be
	// stale (file deleted); the first-paint color is then the shipped
	// default, which is always safe.
	th, err := CachedThemeByID(t.TempDir(), "no-such-theme")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != DefaultThemeID {
		t.Errorf("expected embedded default, got %+v", th)
	}
}

func TestCachedThemeByID_EmptyThemesDirFallsBackToDefault(t *testing.T) {
	th, err := CachedThemeByID("", "anything")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != DefaultThemeID {
		t.Errorf("expected embedded default, got %+v", th)
	}
}

func TestCachedThemeByID_LoadsFromDisk(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	th, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != "terra-test" {
		t.Errorf("expected terra-test, got %+v", th)
	}
}

func TestCachedThemeByID_CachesParsedTheme(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	th1, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	th2, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	// Same pointer on cache hit; LoadTheme would re-parse.
	if th1 != th2 {
		t.Errorf("expected cache hit (same pointer), got different objects")
	}
}

func TestCachedThemeByID_InvalidFileFallsBackToDefault(t *testing.T) {
	themesDir := t.TempDir()
	// Write a broken theme file directly (bypass the importer so we can
	// simulate a hand-edited broken file).
	if err := writeBytes(t, filepath.Join(themesDir, "broken.json"), []byte(`{not valid`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	th, err := CachedThemeByID(themesDir, "broken")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	if th == nil || th.ID != DefaultThemeID {
		t.Errorf("expected embedded default fallback, got %+v", th)
	}
}

func TestInvalidateThemeCache_DropsOne(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	th1, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	InvalidateThemeCache("terra-test")
	th2, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	// After invalidation the second call must produce a fresh *Theme
	// pointer (LoadTheme returns a new struct each time).
	if th1 == th2 {
		t.Errorf("expected different pointers after invalidation, got same")
	}
}

func TestInvalidateThemeCache_DropsAll(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	th1, _ := CachedThemeByID(themesDir, "terra-test")
	InvalidateThemeCache()
	th2, _ := CachedThemeByID(themesDir, "terra-test")
	if th1 == th2 {
		t.Errorf("expected different pointers after invalidate-all, got same")
	}
}

func TestInvalidateThemeCache_UnknownIDIsNoOp(t *testing.T) {
	// Just shouldn't panic; cache remains in a usable state.
	InvalidateThemeCache("never-cached", "")
	themesDir := t.TempDir()
	if _, err := CachedThemeByID(themesDir, DefaultThemeID); err != nil {
		t.Errorf("cache still functional: %v", err)
	}
}

func TestCachedThemeByID_PicksUpModTime(t *testing.T) {
	// Mutate the on-disk file between cache calls and assert the
	// mtime check reloads. We bump the mtime explicitly because file
	// systems vary in resolution; the cache key is the mtime itself.
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	th1, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID: %v", err)
	}
	// Bump the mtime by 2s to be safe across FS resolution.
	path := filepath.Join(themesDir, "terra-test.json")
	newTime := time.Now().Add(2 * time.Second)
	if err := touchFile(path, newTime); err != nil {
		t.Fatalf("touch: %v", err)
	}
	th2, err := CachedThemeByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("CachedThemeByID after mtime bump: %v", err)
	}
	if th1 == th2 {
		t.Errorf("expected reload after mtime bump, got cached pointer")
	}
}
