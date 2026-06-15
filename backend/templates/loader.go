package templates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	return res, nil
}

// GetTemplate resolves a single template by id: on-disk first, then the
// embedded set. Returns the full Template (including Body) so the caller can
// render it. ErrTemplateNotFound (wrapped) is returned when the id is on
// neither tier; genuine I/O errors propagate.
//
// The on-disk tier is an O(1) direct file lookup (<id>.md) rather than a
// directory scan — this mirrors CachedGetTemplate and SaveTemplate, both of
// which enforce the filename-matches-id convention (<id>.md). An id that does
// not correspond to an existing file falls through to the embedded set.
func GetTemplate(templatesDir, id string) (*Template, error) {
	if id == "" {
		return nil, ErrTemplateNotFound
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
