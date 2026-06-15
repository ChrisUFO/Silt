package templates

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// embeddedBuiltinDir is the embed.FS sub-directory holding the first-class
// default templates. embed.FS paths are always slash-separated on every
// platform, so this package joins them with "/" via path.Join rather than
// filepath.Join (mirrors backend/themes/default.go).
const embeddedBuiltinDir = "builtin"

//go:embed builtin/*.md
var embeddedBuiltinFS embed.FS

// EmbeddedBuiltinFiles returns the raw bytes of every embedded first-class
// template keyed by its filename (e.g. "daily-note.md"). Each embedded
// template's filename equals its id by convention (enforced by the embed
// tests), so BuiltinIDs derives ids from these filenames without parsing.
func EmbeddedBuiltinFiles() (map[string][]byte, error) {
	entries, err := embeddedBuiltinFS.ReadDir(embeddedBuiltinDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded templates dir: %w", err)
	}
	out := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(path.Ext(e.Name()), ".md") {
			continue
		}
		raw, err := embeddedBuiltinFS.ReadFile(path.Join(embeddedBuiltinDir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded template %s: %w", e.Name(), err)
		}
		out[e.Name()] = raw
	}
	return out, nil
}

// EmbeddedTemplates returns the parsed first-class templates embedded in the
// binary. They are the always-available set: ListTemplates appends any not
// already on disk. Every embedded template is authored at build time, so any
// validation failure is a release-blocking authoring bug — this function
// returns it rather than silently dropping a template (fail loud, mirroring
// themes.EmbeddedThemes).
func EmbeddedTemplates() ([]*Template, error) {
	files, err := EmbeddedBuiltinFiles()
	if err != nil {
		return nil, err
	}
	out := make([]*Template, 0, len(files))
	for name, raw := range files {
		t, err := ParseTemplateBytes(raw, name, SourceBuiltin)
		if err != nil {
			return nil, fmt.Errorf("embedded template %s is invalid: %w", name, err)
		}
		out = append(out, t)
	}
	// Deterministic source order (by id) so ListTemplates behaves identically
	// across runs; ListTemplates re-sorts by (Category, Title) for display.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ParseEmbeddedByID returns the parsed embedded first-class template with the
// given id, or ok=false if no embedded template has that id. Embedded template
// filenames equal their ids (a convention guaranteed by the build and enforced
// by the embed tests), so this is a single-file read.
func ParseEmbeddedByID(id string) (*Template, bool) {
	if id == "" {
		return nil, false
	}
	raw, err := embeddedBuiltinFS.ReadFile(path.Join(embeddedBuiltinDir, id+".md"))
	if err != nil {
		return nil, false
	}
	t, err := ParseTemplateBytes(raw, id+".md", SourceBuiltin)
	if err != nil {
		return nil, false
	}
	return t, true
}

// BuiltinIDs returns the ids of the embedded first-class templates, derived
// from filenames (convention: <id>.md). Used by the write path's read-only
// guard (SaveTemplate/DeleteTemplate reject builtin:// ids) without parsing
// every file. The filename==id convention is enforced by the embed tests.
func BuiltinIDs() (map[string]bool, error) {
	entries, err := embeddedBuiltinFS.ReadDir(embeddedBuiltinDir)
	if err != nil {
		return nil, fmt.Errorf("read embedded templates dir: %w", err)
	}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(path.Ext(e.Name()), ".md") {
			continue
		}
		ids[strings.TrimSuffix(e.Name(), path.Ext(e.Name()))] = true
	}
	return ids, nil
}

// IsBuiltinID reports whether id names an embedded first-class template. A
// failure to read the embed dir (which can only mean a misconfigured build) is
// treated as "not builtin" so the write path never blocks on an embed fault —
// the worst case is a user file that shadows an embedded id, which the loader's
// on-disk-wins dedup already handles correctly.
func IsBuiltinID(id string) bool {
	ids, err := BuiltinIDs()
	if err != nil {
		return false
	}
	return ids[id]
}
