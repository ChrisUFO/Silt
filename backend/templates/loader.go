package templates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrTemplateNotFound is returned (wrapped) by GetTemplate when no template
// with the requested id is available on disk or in the embedded set. Callers
// use errors.Is to distinguish "not found" from genuine I/O errors.
var ErrTemplateNotFound = errors.New("template not found")

// TemplateLoadError records a single template file that could not be loaded,
// so ListTemplates can surface broken files to the UI without dropping them
// silently or aborting the whole enumeration. Mirrors themes.ThemeLoadError.
type TemplateLoadError struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// ListTemplatesResult is returned by ListTemplates: the valid templates
// (always including the embedded first-class set, deduped by id with on-disk
// winning) plus any per-file load errors and per-file forward-compat warnings
// (e.g. an unknown category).
type ListTemplatesResult struct {
	Templates []TemplateSummary  `json:"templates"`
	Errors    []TemplateLoadError `json:"errors"`
	Warnings  []TemplateLoadError `json:"warnings"`
}

// pluginRegistry holds templates registered at runtime by plugins (#96).
// The outer map is keyed by pluginID; the inner map by template id. A
// plugin provides its templates via the RegisterPluginTemplates IPC
// method; the picker sees them under a `Plugins / <pluginID>` group
// header (the URI scheme plugin://<plugin-id>/<template-id> is the
// canonical way to refer to one). The registry is in-memory only —
// plugin templates are NOT written to disk (the .md <vault>/.system/templates/
// tier is unchanged).
var pluginRegistryMu sync.RWMutex
var pluginRegistry = map[string]map[string]*Template{}

// RegisterPluginTemplates adds a plugin's templates to the runtime registry.
// The caller is responsible for setting each template's Source = SourcePlugin
// and PluginID = pluginID; this function validates and stores them keyed by
// id. Re-registering for the same plugin id replaces the previous set
// atomically. Returns an error if pluginID is empty or the slice is nil.
func RegisterPluginTemplates(pluginID string, tpls []*Template) error {
	if pluginID == "" {
		return fmt.Errorf("plugin id is required")
	}
	if tpls == nil {
		return fmt.Errorf("plugin template slice is nil")
	}
	if len(tpls) > 100 {
		return fmt.Errorf("plugin %q registered %d templates, max is 100", pluginID, len(tpls))
	}
	store := make(map[string]*Template, len(tpls))
	for i, t := range tpls {
		if t == nil {
			return fmt.Errorf("plugin template at index %d is nil", i)
		}
		if t.ID == "" {
			return fmt.Errorf("plugin template at index %d has empty id", i)
		}
		if t.Source != SourcePlugin {
			return fmt.Errorf(
				"plugin template %q has source %q, expected %q",
				t.ID, t.Source, SourcePlugin,
			)
		}
		if t.PluginID != pluginID {
			return fmt.Errorf(
				"plugin template %q has plugin_id %q, expected %q",
				t.ID, t.PluginID, pluginID,
			)
		}
		store[t.ID] = t
	}
	pluginRegistryMu.Lock()
	pluginRegistry[pluginID] = store
	pluginRegistryMu.Unlock()
	return nil
}

// UnregisterPluginTemplates removes a plugin's templates from the runtime
// registry. Idempotent: no error if the plugin wasn't registered.
func UnregisterPluginTemplates(pluginID string) {
	pluginRegistryMu.Lock()
	delete(pluginRegistry, pluginID)
	pluginRegistryMu.Unlock()
}

// ListPluginTemplates returns a flat slice of every plugin-provided
// template across all registered plugins. The slice is a fresh copy of
// the summaries (the picker treats summaries as read-only).
func ListPluginTemplates() []TemplateSummary {
	pluginRegistryMu.RLock()
	defer pluginRegistryMu.RUnlock()
	var out []TemplateSummary
	for _, store := range pluginRegistry {
		for _, t := range store {
			out = append(out, t.AsSummary())
		}
	}
	return out
}

// GetPluginTemplate resolves a `plugin://<plugin-id>/<template-id>` URI
// to a registered plugin template. Returns ErrTemplateNotFound (wrapped)
// if the plugin is not registered, or the plugin doesn't have a
// template with that id.
func GetPluginTemplate(uri string) (*Template, error) {
	pluginID, tplID, err := parsePluginTemplateURI(uri)
	if err != nil {
		return nil, err
	}
	pluginRegistryMu.RLock()
	defer pluginRegistryMu.RUnlock()
	store, ok := pluginRegistry[pluginID]
	if !ok {
		return nil, fmt.Errorf("%w: plugin %q not registered", ErrTemplateNotFound, pluginID)
	}
	t, ok := store[tplID]
	if !ok {
		return nil, fmt.Errorf(
			"%w: plugin %q has no template %q",
			ErrTemplateNotFound, pluginID, tplID,
		)
	}
	cp := *t
	if len(cp.Placeholders) > 0 {
		ph := make([]Placeholder, len(cp.Placeholders))
		copy(ph, cp.Placeholders)
		cp.Placeholders = ph
	}
	return &cp, nil
}

// parsePluginTemplateURI parses `plugin://<plugin-id>/<template-id>` into
// (pluginID, templateID). The plugin-id must be non-empty; the template-id
// may not contain `/` (we split on the first `/` after `plugin://`).
func parsePluginTemplateURI(uri string) (pluginID, templateID string, err error) {
	const prefix = "plugin://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", fmt.Errorf("not a plugin template uri: %q", uri)
	}
	rest := strings.TrimPrefix(uri, prefix)
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return "", "", fmt.Errorf("plugin template uri missing template id: %q", uri)
	}
	pluginID = rest[:idx]
	templateID = rest[idx+1:]
	if pluginID == "" {
		return "", "", fmt.Errorf("plugin template uri has empty plugin id: %q", uri)
	}
	if templateID == "" {
		return "", "", fmt.Errorf("plugin template uri has empty template id: %q", uri)
	}
	if strings.Contains(templateID, "/") {
		return "", "", fmt.Errorf("plugin template id must not contain '/': %q", uri)
	}
	return pluginID, templateID, nil
}

// ResetPluginRegistry removes every registered plugin template. Called by
// App.teardownVaultServices on vault close/switch so stale plugin templates
// from the previous vault don't leak into the next (#128).
func ResetPluginRegistry() {
	pluginRegistryMu.Lock()
	pluginRegistry = map[string]map[string]*Template{}
	pluginRegistryMu.Unlock()
}

// splitFrontmatter separates a leading YAML frontmatter block (bounded by ---
// lines) from the Markdown body. A file with no frontmatter returns hasFM=
// false and body=raw. The frontmatter is the text between the opening and
// closing --- lines (exclusive); the body is everything after the closing ---
// line, with a single leading newline trimmed. CR (Windows line endings) is
// tolerated on the delimiter lines.
func splitFrontmatter(raw string) (fm string, body string, hasFM bool) {
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", raw, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			fm = strings.Join(lines[1:i], "\n")
			body = strings.Join(lines[i+1:], "\n")
			body = strings.TrimPrefix(body, "\r\n")
			body = strings.TrimPrefix(body, "\n")
			return fm, body, true
		}
	}
	// Opening --- with no closing ---: treat the whole file as body (no
	// frontmatter) rather than swallowing the content as metadata.
	return "", raw, false
}

// titleFromBodyOrName derives a human Title when the frontmatter omits one:
// the first "# heading" line wins, otherwise the filename stem (title-cased
// naively). A derived title is a fallback only — authored frontmatter always
// wins because ParseTemplateBytes only calls this when Title is empty.
func titleFromBodyOrName(body, filename string) string {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
		}
	}
	return filename
}

// ParseTemplateBytes parses raw template bytes (filename is used for id/title
// fallbacks and error messages; source labels the origin). It splits the YAML
// frontmatter from the Markdown body, defaults omitted metadata fields, and
// validates the result. A template with no frontmatter at all is still valid:
// the id is derived from the filename, the title from the first heading (or
// filename), and schema_version defaults to the supported version.
func ParseTemplateBytes(raw []byte, filename, source string) (*Template, error) {
	var t Template
	fmStr, body, hasFM := splitFrontmatter(string(raw))
	t.Body = body

	// Reject plugin_id: in user-authored frontmatter (#96). Plugin templates
	// are registered programmatically; an on-disk file claiming to be from
	// a plugin is a corruption indicator.
	if err := rejectPluginIDInFrontmatter([]byte(fmStr)); err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}

	if hasFM {
		if err := yaml.Unmarshal([]byte(fmStr), &t); err != nil {
			return nil, fmt.Errorf("invalid frontmatter in %s: %w", filename, err)
		}
	}

	// Derive id/title from the filename when the frontmatter omits them. The
	// filename stem (minus extension) is the id; the title falls back to the
	// first heading, then the filename stem.
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if t.ID == "" {
		t.ID = base
	}
	if t.Title == "" {
		t.Title = titleFromBodyOrName(t.Body, base)
	}
	if t.SchemaVersion == "" {
		t.SchemaVersion = SupportedSchemaVersion
	}
	if t.Category == "" {
		t.Category = "notes"
	}
	if source != "" {
		t.Source = source
	}

	if err := Validate(&t); err != nil {
		return nil, fmt.Errorf("template %s is invalid: %w", filename, err)
	}
	return &t, nil
}

// loadOne reads and parses a single on-disk template file. A malformed file
// returns a structured error (ValidationErrors) so ListTemplates can surface
// "file X is missing field Y" without aborting the enumeration.
func loadOne(path string) (*Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read template %s: %w", filepath.Base(path), err)
	}
	return ParseTemplateBytes(raw, filepath.Base(path), SourceDisk)
}

// ListTemplates enumerates <templatesDir>/*.md, returning a summary for every
// valid template, then appends every embedded first-class template not already
// on disk (on-disk wins the dedup, preserving the existing contract). Invalid
// files are collected into Errors (never panic); a missing templatesDir is not
// an error — it yields just the embedded set. Forward-compat warnings (e.g. an
// unknown category) are collected into Warnings. The result is sorted by
// (Category, Title) for stable picker grouping.
func ListTemplates(templatesDir string) (*ListTemplatesResult, error) {
	res := &ListTemplatesResult{
		Templates: []TemplateSummary{},
		Errors:    []TemplateLoadError{},
		Warnings:  []TemplateLoadError{},
	}
	seenIDs := map[string]bool{}

	if templatesDir != "" {
		entries, err := os.ReadDir(templatesDir)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
					continue
				}
				full := filepath.Join(templatesDir, e.Name())
				t, loadErr := loadOne(full)
				if loadErr != nil {
					res.Errors = append(res.Errors, TemplateLoadError{
						File:    e.Name(),
						Message: loadErr.Error(),
					})
					continue
				}
				if seenIDs[t.ID] {
					continue // first valid definition of an id wins
				}
				seenIDs[t.ID] = true
				if !IsKnownCategory(t.Category) {
					res.Warnings = append(res.Warnings, TemplateLoadError{
						File:    e.Name(),
						Message: fmt.Sprintf("template %q uses unknown category %q (loaded; add it to KnownCategories to silence)", t.ID, t.Category),
					})
				}
				res.Templates = append(res.Templates, t.AsSummary())
			}
		} else if !os.IsNotExist(err) {
			// A real I/O error (permissions, etc.) — surface it. A missing dir
			// is expected (fresh/empty vault) and is not an error.
			return nil, fmt.Errorf("failed to read templates directory %s: %w", templatesDir, err)
		}
	}

	// Always guarantee the embedded first-class templates are selectable. A
	// template whose id is already on disk won the dedup above (on-disk wins);
	// otherwise append the embedded copy so the picker shows the full default
	// library even on an empty vault.
	embedded, embedErr := EmbeddedTemplates()
	if embedErr != nil {
		// An embed corruption is a release-blocking build bug (caught by the
		// embed tests in CI). Surface it rather than silently dropping the
		// default library.
		res.Errors = append(res.Errors, TemplateLoadError{
			File:    "(embedded)",
			Message: fmt.Sprintf("failed to load embedded templates: %v", embedErr),
		})
	} else {
		for _, t := range embedded {
			if seenIDs[t.ID] {
				continue
			}
			seenIDs[t.ID] = true
			res.Templates = append(res.Templates, t.AsSummary())
		}
	}

	sort.Slice(res.Templates, func(i, j int) bool {
		if res.Templates[i].Category != res.Templates[j].Category {
			return res.Templates[i].Category < res.Templates[j].Category
		}
		return res.Templates[i].Title < res.Templates[j].Title
	})

	// Append plugin-provided templates (#96) AFTER on-disk + embedded so
	// they don't shadow any user-defined or first-class template with the
	// same id (the picker shows them under a "Plugins / <plugin-id>" group
	// header derived from the PluginID field). Dedup by id: a plugin
	// template whose id already appeared in the on-disk or embedded set is
	// skipped (on-disk > embedded > plugin).
	pluginSummaries := ListPluginTemplates()
	for _, ps := range pluginSummaries {
		if seenIDs[ps.ID] {
			continue
		}
		seenIDs[ps.ID] = true
		res.Templates = append(res.Templates, ps)
	}
	return res, nil
}

// GetTemplate resolves a single template by id. Resolution order:
//   1. `plugin://<plugin-id>/<template-id>` URI → registered plugin template.
//   2. On-disk <id>.md (user-authored, wins the dedup over builtins).
//   3. Embedded builtin by id.
//
// Returns the full Template (including Body) so the caller can render it.
// ErrTemplateNotFound (wrapped) is returned when the id is on no tier;
// genuine I/O errors propagate. The on-disk tier is an O(1) direct file
// lookup (<id>.md) rather than a directory scan — this mirrors
// CachedGetTemplate and SaveTemplate, both of which enforce the
// filename-matches-id convention (<id>.md). An id that does not
// correspond to an existing file falls through to the embedded set.
func GetTemplate(templatesDir, id string) (*Template, error) {
	if id == "" {
		return nil, ErrTemplateNotFound
	}
	// Plugin URI: bypass the on-disk + embedded tiers and go straight to
	// the runtime registry. Plugin templates are in-memory only.
	if strings.HasPrefix(id, "plugin://") {
		return GetPluginTemplate(id)
	}
	if templatesDir != "" {
		path := filepath.Join(templatesDir, id+".md")
		if t, err := loadOne(path); err == nil {
			return t, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if t, ok := ParseEmbeddedByID(id); ok {
		return t, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrTemplateNotFound, id)
}
