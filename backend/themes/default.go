// Package themes owns Silt's canonical theme schema, the embedded first-class
// themes, and (see loader.go/validate.go) the on-disk theme loader + validator.
//
// First-class themes are embedded in the binary so they are always selectable:
// they work before a vault exists, when the themes directory has been wiped,
// and when a user-selected theme id is missing or invalid. ScaffoldVault writes
// these same embedded files when it bootstraps a new vault, so there is a single
// source of truth for each first-class theme's content (the JSON under
// backend/themes/themes/). The embedded set today is: the canonical default
// (cyber_forest) plus Terra Noir, Linen, Stark, and Graphite.
package themes

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// embeddedThemesDir is the embed.FS sub-directory holding the first-class
// theme JSON. embed.FS paths are always slash-separated on every platform
// (they are not OS paths), so this package joins them with "/" via path.Join
// rather than filepath.Join.
const embeddedThemesDir = "themes"

//go:embed themes/*.json
var embeddedThemesFS embed.FS

// DefaultThemeID is the id of the bundled default theme. It is the value used
// when AppSettings.ActiveTheme is empty/invalid and the fallback applies, and
// the anchor id for the embedded first-class set.
const DefaultThemeID = "cyber_forest"

// DefaultThemeJSON returns the raw canonical default theme JSON. Callers that
// need the parsed form should use ParseDefault / EmbeddedThemes. embed.FS
// returns a fresh byte slice per ReadFile call, so callers may mutate the
// returned slice freely.
func DefaultThemeJSON() []byte {
	raw, err := embeddedThemesFS.ReadFile(path.Join(embeddedThemesDir, DefaultThemeID+".json"))
	if err != nil {
		// The default file is referenced by a build-time embed directive and
		// its id is a compile-time constant, so a missing read can only mean
		// the embed is misconfigured. Fail loud rather than hand the caller a
		// nil/empty theme to silently render nothing with.
		panic(fmt.Sprintf("themes: embedded default %q missing from embed.FS: %v", DefaultThemeID, err))
	}
	return raw
}

// EmbeddedThemeFiles returns the raw JSON bytes of every embedded first-class
// theme keyed by its filename (e.g. "cyber_forest.json"). ScaffoldVault uses
// this to write editable on-disk copies from the single embedded source of
// truth. The map is the filename→bytes view of the embed; EmbeddedThemes is
// the parsed view.
func EmbeddedThemeFiles() (map[string][]byte, error) {
	entries, err := embeddedThemesFS.ReadDir(embeddedThemesDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded themes dir: %w", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(path.Ext(e.Name()), ".json") {
			continue
		}
		raw, err := embeddedThemesFS.ReadFile(path.Join(embeddedThemesDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded theme %s: %w", e.Name(), err)
		}
		out[e.Name()] = raw
	}
	return out, nil
}

// EmbeddedThemes returns the parsed first-class themes embedded in the binary
// (the default plus every shipped palette). They are the "always-selectable"
// set: ListThemes appends any not already on disk, and ScaffoldVault writes
// editable on-disk copies. Every embedded theme is authored at build time, so
// any validation failure is a release-blocking authoring bug — this function
// returns it rather than silently dropping a theme (fail loud).
func EmbeddedThemes() ([]*Theme, error) {
	files, err := EmbeddedThemeFiles()
	if err != nil {
		return nil, err
	}
	out := make([]*Theme, 0, len(files))
	for name, raw := range files {
		t, err := ParseAndValidate(raw)
		if err != nil {
			return nil, fmt.Errorf("embedded theme %s is invalid: %w", name, err)
		}
		out = append(out, t)
	}
	// Deterministic source order (by id) so ListThemes/ScaffoldVault behave
	// identically across runs; ListThemes re-sorts by Name for display.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ParseEmbeddedByID returns the parsed embedded first-class theme with the
// given id, or ok=false if no embedded theme has that id. Embedded theme
// filenames equal their ids (a convention guaranteed by the build, unlike
// user-renamed on-disk files), so this is a single-file read. Used by
// ResolveActive / CachedThemeByID to resolve a first-class theme that is not
// on disk (wiped themes dir, or an existing vault scaffolded before the theme
// shipped) without falling through to the default.
func ParseEmbeddedByID(id string) (*Theme, bool) {
	if id == "" {
		return nil, false
	}
	raw, err := embeddedThemesFS.ReadFile(path.Join(embeddedThemesDir, id+".json"))
	if err != nil {
		return nil, false
	}
	t, err := ParseAndValidate(raw)
	if err != nil {
		return nil, false
	}
	return t, true
}
