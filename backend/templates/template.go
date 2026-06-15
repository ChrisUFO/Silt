// Package templates owns Silt's page-template system: a canonical schema, the
// embedded first-class default library, and (see loader.go/validate.go/
// render.go) the on-disk user-template loader, validator, and placeholder
// renderer.
//
// A template is parameterized Markdown: a title, a category, an icon, an
// optional list of user-declared placeholders, and a Markdown body. Templates
// ship a strong default library (embedded in the binary via //go:embed, read-
// only) and are user-extensible (drop a .md into <vault>/.system/templates/).
// They mirror the two-tier design proven by the theme engine (backend/themes):
// builtin:// embedded defaults + on-disk user copies, on-disk wins the dedup.
//
// Forward-compatibility is structural: schema_version is informational; the
// Source field reserves a "plugin" tier for future plugin-provided templates;
// categories are additive (unknown categories warn, never reject); and unknown
// placeholders warn rather than error. The placeholder grammar is deliberately
// narrow (lowercase identifiers) so smart-graph syntax — {{embed:uuid}} and
// ((uuid)) references (SPECS §5.2) — never collides with template tokens and
// passes through the renderer byte-for-byte.
package templates

// SupportedSchemaVersion is the canonical template schema version this build
// understands. A template carrying a different but well-formed version is
// parsed structurally rather than rejected (forward-compatible, mirroring the
// theme engine's treatment of schema_version).
const SupportedSchemaVersion = "1.0.0"

// Template sources. SourceBuiltin templates are embedded in the binary (read-
// only); SourceDisk templates live in <vault>/.system/templates/ (writable).
// SourcePlugin is reserved for future plugin-provided templates — the loader
// and picker are shaped so adding it is an additive change, not a redesign.
const (
	SourceBuiltin = "builtin"
	SourceDisk    = "disk"
	SourcePlugin  = "plugin"
)

// Default placeholder names. These are always recognized by the renderer and
// are auto-filled from the RenderOptions reference time. Their grammar
// ([a-z][a-z0-9_]*) is shared with user-declared placeholders, which is what
// structurally excludes smart-graph tokens (colons, capitals, parentheses).
const (
	PlaceholderDate    = "date"     // YYYY-MM-DD
	PlaceholderTime    = "time"     // HH:MM
	PlaceholderISODate = "iso_date" // RFC3339 (ISO 8601)
	PlaceholderWeekday = "weekday"  // full weekday name (Monday, …)
)

// KnownCategories is the curated first-class category set. The set is ADDITIVE:
// a template with an unknown-but-non-empty category still loads (the loader
// emits a forward-compat warning) so new categories can ship without an engine
// change. ListTemplates groups by Category for display.
var KnownCategories = []string{
	"notes",
	"meetings",
	"daily",
	"projects",
	"weekly",
	"decisions",
	"reading",
	"retrospectives",
}

// IsKnownCategory reports whether c is in the curated first-class set.
func IsKnownCategory(c string) bool {
	for _, k := range KnownCategories {
		if c == k {
			return true
		}
	}
	return false
}

// Placeholder is one user-declared template variable. The renderer substitutes
// {{Name}} with the caller-supplied value, or Default when the caller omits it.
// Names must match ^[a-z][a-z0-9_]*$ so they never collide with smart-graph
// syntax ({{embed:uuid}}, ((uuid))).
type Placeholder struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
}

// Template is the parsed, in-memory representation of a page template. The
// YAML-tagged fields are the frontmatter (the metadata block at the top of the
// .md file); Body is the Markdown after the frontmatter; Source records where
// the template came from (builtin/disk). Body and Source carry yaml:"-" so a
// yaml.Marshal of a *Template round-trips exactly the frontmatter — this is
// what SerializeTemplate relies on.
type Template struct {
	SchemaVersion string        `json:"schema_version" yaml:"schema_version"`
	ID            string        `json:"id" yaml:"id"`
	Title         string        `json:"title" yaml:"title"`
	Description   string        `json:"description,omitempty" yaml:"description,omitempty"`
	Category      string        `json:"category" yaml:"category"`
	Icon          string        `json:"icon,omitempty" yaml:"icon,omitempty"`
	Placeholders  []Placeholder `json:"placeholders,omitempty" yaml:"placeholders,omitempty"`
	Body          string        `json:"body" yaml:"-"`
	Source        string        `json:"source,omitempty" yaml:"-"`
}

// AsSummary returns the lightweight metadata view used by ListTemplates for the
// picker (the full Body is fetched lazily via GetTemplate only when a template
// is previewed/selected, keeping the listing payload small).
func (t *Template) AsSummary() TemplateSummary {
	// Copy the placeholder slice so callers can't mutate the template's slice
	// through the summary (defensive; the picker treats summaries as read-only).
	var ph []Placeholder
	if len(t.Placeholders) > 0 {
		ph = make([]Placeholder, len(t.Placeholders))
		copy(ph, t.Placeholders)
	}
	return TemplateSummary{
		ID:           t.ID,
		Title:        t.Title,
		Description:  t.Description,
		Category:     t.Category,
		Icon:         t.Icon,
		Source:       t.Source,
		Placeholders: ph,
	}
}

// TemplateSummary is the listing item returned by ListTemplates: everything the
// picker needs to render + group + build the placeholder form EXCEPT the Body
// (which can be large). The frontend fetches the full Template via GetTemplate
// lazily when a row is previewed or selected.
type TemplateSummary struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Description  string        `json:"description,omitempty"`
	Category     string        `json:"category"`
	Icon         string        `json:"icon,omitempty"`
	Source       string        `json:"source,omitempty"`
	Placeholders []Placeholder `json:"placeholders,omitempty"`
}
