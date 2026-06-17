package templates

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// idRe mirrors the theme-engine id grammar ([a-z0-9_-]+). The id flows
	// into filepath.Join(templatesDir, id+".md") on the write path, so it must
	// not contain path separators, parent dirs, or NUL — this narrow set is
	// filename-safe on every platform we ship and CWE-22-safe.
	idRe = regexp.MustCompile(`^[a-z0-9_-]+$`)
	// placeholderNameRe enforces ^[a-z][a-z0-9_]*$ so placeholder names can
	// never collide with smart-graph syntax (colons, capitals, parentheses).
	placeholderNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// schemaVersionRe accepts dotted numeric versions (1.0.0, 2, 1.2). It is
	// informational: a forward-versioned template whose shape still matches v1
	// keeps loading, exactly like the theme engine.
	schemaVersionRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// IsValidID reports whether id is a safe template id: non-empty and matching
// ^[a-z0-9_-]+$. Used by the write path to reject path-escape attempts before
// the id ever reaches filepath.Join.
func IsValidID(id string) bool {
	return idRe.MatchString(id)
}

// ValidationError describes a single template-validation problem in machine-
// readable form so the UI can surface "template X is missing field Y" without
// the app crashing on a bad file. Mirrors themes.ValidationError.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (v ValidationError) Error() string {
	return fmt.Sprintf("template validation error at %s: %s", v.Field, v.Message)
}

// ValidationErrors aggregates per-field issues so a caller gets every problem
// in one pass instead of fixing them one at a time. The loader wraps these
// into a single error; SaveUserTemplate propagates them over IPC so the picker
// can name the offending field.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	msgs := make([]string, 0, len(ve))
	for _, e := range ve {
		msgs = append(msgs, e.Error())
	}
	return strings.Join(msgs, "; ")
}

// rejectPluginIDInFrontmatter rejects `plugin_id:` lines in YAML frontmatter.
// Plugin templates are registered programmatically via RegisterPluginTemplates
// (#96); a user-authored .md file claiming to be from a plugin would be a
// corruption indicator. The field is also yaml:"-" on the struct so it never
// deserializes, but we belt-and-suspenders this with an explicit check on the
// raw frontmatter text.
func rejectPluginIDInFrontmatter(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	lower := string(raw)
	if !strings.Contains(lower, "plugin_id") {
		return nil
	}
	for _, line := range strings.Split(lower, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "plugin_id:") {
			return fmt.Errorf(
				"plugin_id is reserved for plugin-registered templates; " +
					"set Source = \"plugin\" via the plugin registry instead",
			)
		}
	}
	return nil
}

// Validate checks a parsed template against the canonical schema. It returns
// nil if the template is well-formed, or a ValidationErrors slice listing every
// structural problem (missing identity, empty body, malformed id/schema_version,
// bad placeholder names, duplicates). schema_version is informational: a
// missing one is defaulted by the parser, a malformed one is rejected, and a
// higher-but-well-formed one is accepted (forward-compatible).
//
// Categories are intentionally ADDITIVE: an unknown-but-non-empty category is
// valid here (the loader emits a forward-compat warning); only an empty
// category is rejected. This lets new categories ship without an engine change.
func Validate(t *Template) error {
	var errs ValidationErrors

	if t == nil {
		return ValidationErrors{{Field: "$", Message: "template is nil"}}
	}

	if strings.TrimSpace(t.ID) == "" {
		errs = append(errs, ValidationError{Field: "id", Message: "id is required"})
	} else if !idRe.MatchString(t.ID) {
		errs = append(errs, ValidationError{
			Field:   "id",
			Message: fmt.Sprintf("id %q must be lowercase [a-z0-9_-]", t.ID),
		})
	}

	if strings.TrimSpace(t.Title) == "" {
		errs = append(errs, ValidationError{Field: "title", Message: "title is required"})
	}

	if strings.TrimSpace(t.SchemaVersion) == "" {
		errs = append(errs, ValidationError{Field: "schema_version", Message: "schema_version is required"})
	} else if !schemaVersionRe.MatchString(t.SchemaVersion) {
		errs = append(errs, ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("schema_version %q is not a valid dotted-numeric version (e.g. 1.0.0)", t.SchemaVersion),
		})
	}

	if strings.TrimSpace(t.Body) == "" {
		errs = append(errs, ValidationError{Field: "body", Message: "body is required (a template must contain Markdown)"})
	}

	if strings.TrimSpace(t.Category) == "" {
		errs = append(errs, ValidationError{Field: "category", Message: "category is required"})
	}

	seen := map[string]bool{}
	for i, p := range t.Placeholders {
		prefix := fmt.Sprintf("placeholders[%d]", i)
		switch {
		case p.Name == "":
			errs = append(errs, ValidationError{Field: prefix + ".name", Message: "placeholder name is required"})
		case !placeholderNameRe.MatchString(p.Name):
			errs = append(errs, ValidationError{
				Field:   prefix + ".name",
				Message: fmt.Sprintf("placeholder name %q must be lowercase (^[a-z][a-z0-9_]*$) so it never collides with smart-graph syntax", p.Name),
			})
		case seen[p.Name]:
			errs = append(errs, ValidationError{
				Field:   prefix + ".name",
				Message: fmt.Sprintf("duplicate placeholder name %q", p.Name),
			})
		default:
			seen[p.Name] = true
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}
