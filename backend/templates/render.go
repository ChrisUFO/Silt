package templates

import (
	"regexp"
	"time"
)

// placeholderToken matches {{name}} where name is a valid placeholder
// identifier: a lowercase letter followed by lowercase letters, digits, and
// underscores (^[a-z][a-z0-9_]*$). Optional inner whitespace is tolerated
// ({{name}} and {{ name }} both work).
//
// This grammar DELIBERATELY EXCLUDES smart-graph syntax, which is the spec-
// compatibility guarantee (SPECS §5.2, ARCHITECTURE §7.3):
//   - {{embed:uuid}} — the colon means the capture group stops at "embed" and
//     the trailing "}} does not line up, so the whole token is a non-match and
//     passes through byte-for-byte.
//   - {{Anything}} with a capital — capitals are outside [a-z], non-match.
//   - ((uuid)) references — different delimiters entirely, non-match.
//
// So the passthrough rule is a structural consequence of the narrow grammar,
// not a special case the renderer has to detect.
var placeholderToken = regexp.MustCompile(`{{\s*([a-z][a-z0-9_]*)\s*}}`)

// RenderOptions controls template rendering. The zero value renders against
// time.Now() in the local timezone, which is the production default. Tests pass
// a frozen Now (and optionally a Timezone) so snapshot output is deterministic.
type RenderOptions struct {
	// Now is the reference time for the default placeholders (date/time/
	// iso_date/weekday). Zero value means time.Now().
	Now time.Time
	// Timezone overrides the timezone used to format the default placeholders.
	// Nil means time.Local. User vars and declared defaults are timezone-
	// independent (they are substituted verbatim).
	Timezone *time.Location
}

// Render substitutes placeholders in t.Body and returns the rendered Markdown
// plus any non-fatal warnings. Recognized names are, in priority order:
//  1. caller-supplied vars (highest priority — the user's picker input wins);
//  2. the four built-in defaults (date/time/iso_date/weekday) computed from
//     opts.Now in opts.Timezone;
//  3. any declared placeholder's Default.
//
// Every other {{...}} token (including smart-graph {{embed:uuid}}) and all
// ((uuid)) references are left byte-for-byte untouched and the unrecognized
// name is reported once in warnings. Rendering never returns an error — a
// template with unknown placeholders is forward-compatible, not broken.
func Render(t *Template, vars map[string]string, opts RenderOptions) (string, []string) {
	if t == nil {
		return "", nil
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if loc := opts.Timezone; loc != nil {
		now = now.In(loc)
	} else {
		now = now.In(time.Local)
	}

	// Build the recognized-value map. Built-in defaults first, then declared
	// placeholder defaults, then caller vars (vars win over defaults so the
	// picker's form input overrides a placeholder's Default).
	values := map[string]string{
		PlaceholderDate:    now.Format("2006-01-02"),
		PlaceholderTime:    now.Format("15:04"),
		PlaceholderISODate: now.Format(time.RFC3339),
		PlaceholderWeekday: now.Weekday().String(),
	}
	// recognized is the set of names that are KNOWN (declared or default) even
	// when they have no value. A declared-but-unprovided placeholder stays
	// literal in the output (so the user sees {{meeting_title}} and knows what
	// to fill) WITHOUT raising a warning — it is a known, intentional token,
	// not an unknown one. Only truly-unknown tokens warn (forward-compat).
	recognized := map[string]bool{
		PlaceholderDate:    true,
		PlaceholderTime:    true,
		PlaceholderISODate: true,
		PlaceholderWeekday: true,
	}
	for _, p := range t.Placeholders {
		recognized[p.Name] = true
		if p.Name != "" && p.Default != "" {
			if _, ok := values[p.Name]; !ok {
				values[p.Name] = p.Default
			}
		}
	}
	for k, v := range vars {
		values[k] = v
		recognized[k] = true
	}

	var warnings []string
	warned := map[string]bool{}
	rendered := placeholderToken.ReplaceAllStringFunc(t.Body, func(match string) string {
		sub := placeholderToken.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		name := sub[1]
		if v, ok := values[name]; ok {
			return v
		}
		// Declared/default but no value: leave literal, no warning (the token
		// is known — the user just hasn't filled it in yet).
		if recognized[name] {
			return match
		}
		// Unknown token: leave it verbatim (forward-compat + smart-graph
		// passthrough) and warn once per name.
		if !warned[name] {
			warned[name] = true
			warnings = append(warnings, "unknown placeholder: {{"+name+"}}")
		}
		return match
	})
	return rendered, warnings
}
