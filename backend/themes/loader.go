package themes

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrThemeNotFound is returned (wrapped) by loadThemeByID when no theme
// with the requested id lives on disk. Callers use errors.Is to
// distinguish "not found" (benign — fall back to default) from genuine
// I/O errors (permission denied, etc.) which should propagate.
var ErrThemeNotFound = errors.New("theme not found")

// ParseDefault parses the embedded canonical default theme. It is the
// guaranteed fallback used when no vault/themes exist or the active id is
// invalid. Because the JSON is compiled into the binary, this only errors
// if the embedded file is itself corrupt (a build-time authoring bug).
func ParseDefault() (*Theme, error) {
	t, err := ParseAndValidate(DefaultThemeJSON())
	if err != nil {
		return nil, fmt.Errorf("embedded default theme is invalid: %w", err)
	}
	return t, nil
}

// LoadTheme reads and validates a single theme file from disk. A malformed
// or schema-invalid file returns a structured error (ValidationErrors) so
// the UI can show which token is wrong without crashing the app.
func LoadTheme(path string) (*Theme, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read theme %s: %w", filepath.Base(path), err)
	}
	t, err := ParseAndValidate(raw)
	if err != nil {
		return nil, fmt.Errorf("theme %s is invalid: %w", filepath.Base(path), err)
	}
	return t, nil
}

// ThemeLoadError records a single theme file that could not be loaded, so
// ListThemes can surface broken files to the UI without dropping them
// silently or aborting the whole enumeration.
type ThemeLoadError struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// FlatTokensPerMode is the per-theme flat CSS-token map for each mode.
// Used by the picker to render live previews on hover without an
// extra roundtrip per preview. The picker receives one pair per theme
// (one dark + one light map) so it can preview in the user's current
// mode without a second IPC call when the mode changes.
type FlatTokensPerMode struct {
	Dark  map[string]string `json:"dark"`
	Light map[string]string `json:"light"`
}

// ListThemesResult is returned by ListThemes: the valid themes (always
// including the embedded default, deduped by id) plus any per-file load
// errors. FlatTokens (Sprint 6, #47) carries the per-mode CSS custom-
// property map keyed by ThemeInfo.ID so the picker can render live
// previews without a second IPC call per hover.
type ListThemesResult struct {
	Themes     []ThemeInfo                  `json:"themes"`
	Errors     []ThemeLoadError             `json:"errors"`
	FlatTokens map[string]FlatTokensPerMode `json:"flat_tokens,omitempty"`
}

// ListThemes enumerates <themesDir>/*.json, returning metadata for every
// valid theme. Invalid files are collected into Errors (never panic). The
// embedded default theme is always present (deduped by id) so the picker
// always has at least one selectable theme even on an empty/wiped vault.
// A missing themesDir is not an error — it yields just the default.
//
// Since Sprint 6 (#47) the result also carries FlatTokens: the per-mode
// CSS-token map keyed by ThemeInfo.ID, used by the picker for live
// previews. The cost is one extra Flatten call per parsed theme (cheap;
// the theme is already in memory after ParseAndValidate).
func ListThemes(themesDir string) (*ListThemesResult, error) {
	res := &ListThemesResult{
		Themes:     []ThemeInfo{},
		Errors:     []ThemeLoadError{},
		FlatTokens: map[string]FlatTokensPerMode{},
	}

	seenIDs := map[string]bool{}

	// An empty themesDir means no vault is open yet. Skip the directory read
	// entirely rather than relying on platform-dependent os.ReadDir("")
	// behavior; the embedded-default append below still guarantees a result.
	if themesDir != "" {
		entries, err := os.ReadDir(themesDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
					continue
				}
				full := filepath.Join(themesDir, e.Name())
				t, loadErr := LoadTheme(full)
				if loadErr != nil {
					res.Errors = append(res.Errors, ThemeLoadError{
						File:    e.Name(),
						Message: loadErr.Error(),
					})
					continue
				}
				if seenIDs[t.ID] {
					continue // first valid definition of an id wins
				}
				seenIDs[t.ID] = true
				res.Themes = append(res.Themes, t.AsInfo("disk"))
				res.FlatTokens[t.ID] = FlatTokensPerMode{
					Dark:  t.Flatten("dark"),
					Light: t.Flatten("light"),
				}
			}
		} else if !os.IsNotExist(err) {
			// A real I/O error (permissions, etc.) — surface it. A missing dir
			// is expected (fresh/empty vault) and is not an error.
			return nil, fmt.Errorf("failed to read themes directory %s: %w", themesDir, err)
		}
	}

	// Always guarantee the embedded first-class themes are selectable. A
	// theme whose id is already on disk won the dedup above (on-disk wins,
	// preserving the existing contract); otherwise append the embedded copy
	// so the picker shows the full first-party set even on an empty/wiped
	// vault or an existing vault scaffolded before a theme shipped.
	embedded, embedErr := EmbeddedThemes()
	if embedErr != nil {
		// An embed corruption is a release-blocking build bug (caught by
		// EmbeddedThemes tests in CI). Keep the picker usable by falling
		// back to just the default rather than bricking the UI — the same
		// defensive posture the original single-default path used.
		if dt, derr := ParseDefault(); derr == nil && !seenIDs[dt.ID] {
			res.Themes = append(res.Themes, dt.AsInfo("default"))
			res.FlatTokens[dt.ID] = FlatTokensPerMode{
				Dark:  dt.Flatten("dark"),
				Light: dt.Flatten("light"),
			}
		}
	} else {
		for _, t := range embedded {
			if seenIDs[t.ID] {
				continue
			}
			seenIDs[t.ID] = true
			// The primary default is labeled "default" (unchanged); the
			// other embedded first-class themes are "bundled" so the UI
			// can tell a shipped palette from a user's on-disk copy.
			source := "bundled"
			if t.ID == DefaultThemeID {
				source = "default"
			}
			res.Themes = append(res.Themes, t.AsInfo(source))
			res.FlatTokens[t.ID] = FlatTokensPerMode{
				Dark:  t.Flatten("dark"),
				Light: t.Flatten("light"),
			}
		}
	}

	sort.Slice(res.Themes, func(i, j int) bool {
		return res.Themes[i].Name < res.Themes[j].Name
	})
	return res, nil
}

// ResolveActive loads the theme the user selected (activeID) and falls back
// to the embedded default when it is missing or invalid. mode is returned
// normalized to a concrete "dark"/"light" (a "system" value is resolved to
// "dark" here as the first-paint default; the frontend re-resolves "system"
// via prefers-color-scheme using both mode token maps). The concrete theme
// is always non-nil on success because the embedded default is the final
// fallback.
func ResolveActive(themesDir, activeID, mode string) (*Theme, error) {
	// 1. Try the user's selected id on disk.
	if activeID != "" {
		t, err := loadThemeByID(themesDir, activeID)
		if err == nil {
			return t, nil
		}
		// 2. Not on disk — try the embedded first-class copy so a
		// non-default first-class theme resolves even when the themes dir
		// is wiped or the vault predates the theme shipping. Without this
		// step the active theme would flash the default palette on launch.
		if t, ok := ParseEmbeddedByID(activeID); ok {
			return t, nil
		}
		// Surface why the selected theme didn't load so theme-file issues
		// aren't invisible; still fall back to the default (never brick the
		// app). Skipped pre-vault (themesDir=="") because the empty-dir
		// "error" there is the normal first-run state, not a fault.
		if themesDir != "" {
			log.Printf("themes: active theme %q unavailable (%v); using default", activeID, err)
		}
	}

	// 3. Final fallback: the embedded default. The concrete theme is always
	// non-nil on success because the embedded default is the final fallback.
	if t, err := ParseDefault(); err == nil {
		return t, nil
	}
	return nil, fmt.Errorf("no usable theme: active id %q not found and the embedded default is unavailable", activeID)
}

// loadThemeByID scans themesDir for the first valid theme whose id matches.
// It intentionally does not assume the filename equals the id. Returns
// ErrThemeNotFound (wrapped) when no matching id is on disk; returns the
// raw os.ReadDir error on genuine I/O failures so callers don't confuse
// a permission fault with a missing theme.
func loadThemeByID(themesDir, id string) (*Theme, error) {
	if themesDir == "" {
		return nil, ErrThemeNotFound
	}
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		t, err := LoadTheme(filepath.Join(themesDir, e.Name()))
		if err != nil {
			continue // skip invalid files while hunting for the id
		}
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("%w: %q in %s", ErrThemeNotFound, id, themesDir)
}

// LoadByID is the public version of loadThemeByID: a single os.ReadDir scan
// that returns the parsed theme for the given id (or false if absent). Used
// by ApplyTheme to validate the requested id and obtain the theme in one
// directory read (the previous implementation called ListThemes — which
// reads + parses every file — and then ResolveActive — which read the
// directory a second time — making ApplyTheme double the file system work
// for every switch).
func LoadByID(themesDir, id string) (*Theme, bool, error) {
	if themesDir == "" {
		return nil, false, nil
	}
	t, err := loadThemeByID(themesDir, id)
	if err != nil {
		if errors.Is(err, ErrThemeNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return t, true, nil
}
