package plugins

import (
	"fmt"
	"sort"
	"strings"
)

// This file defines the v2 SDK capability & permission model (#113). A
// capability is a privileged operation a plugin may request (file I/O, network,
// OS integration, editor-schema mutation, rendered-UI surfaces). A grant is the
// user's affirmative, per-vault permission for a specific plugin to use one.
//
// Enforcement is server-side: every privileged App binding calls requireGrant
// before doing its work and returns a structured CapabilityDeniedError on
// denial (never a panic). The frontend SDK methods are thin pass-throughs —
// there is exactly one source of truth for grants (config.yaml plugins.grants).
//
// "exec" is intentionally absent from the v2 set. It is deferred until the
// trust/signing model matures (#111), so a manifest declaring "exec" is
// rejected at install time rather than silently ignored.

// Capability is the identifier of a privileged operation class.
type Capability string

const (
	// CapReadFiles — read non-markdown files within a notebook (attachments,
	// assets). Gated by the read-files capability.
	CapReadFiles Capability = "read-files"
	// CapWriteFiles — write/delete files within a notebook + plugin scratch
	// space. Scoped by a qualifier (notebook | vault).
	CapWriteFiles Capability = "write-files"
	// CapNetwork — HTTP fetch through the Go-side proxy. Whole-scope.
	CapNetwork Capability = "network"
	// CapOSOpen — open a file/URL in the OS native handler. Whole-scope
	// (URLs are scheme-allowlisted independently).
	CapOSOpen Capability = "os-open"
	// CapOSClipboard — read/write the system clipboard (text only).
	CapOSClipboard Capability = "os-clipboard"
	// CapOSNotify — show a desktop notification.
	CapOSNotify Capability = "os-notify"
	// CapUISurface — render a third-party UI surface (sidebar panel, modal,
	// status-bar item, settings panel) in the sandboxed iframe renderer.
	CapUISurface Capability = "ui-surface"
	// CapEditorSchema — contribute slash commands, custom embed-block views,
	// and decorations to the TipTap editor.
	CapEditorSchema Capability = "editor-schema"
	// CapContentMutate — create / delete / move blocks across the vault via the
	// plugin content CRUD API. Mirrors the implicit right the core editor has
	// always had, but gates it for plugins so a zero-capability third-party
	// plugin cannot mutate content (#156).
	CapContentMutate Capability = "content-mutate"
	// CapPluginDB — open and use the per-plugin SQLite store
	// (<vault>/.system/plugins/<id>/data/plugin.db) via ctx.pluginDb.exec /
	// query / migrate. The connection is distinct from the core index and never
	// ATTACH-able to it (#213).
	CapPluginDB Capability = "plugin-db"
)

// KnownCapabilities is the set of capabilities recognized by this version of
// Silt. A manifest declaring an unknown capability is rejected at install so a
// plugin cannot silently claim a capability the host does not understand (nor
// enlarge its rights via typos or future names). "exec" is intentionally
// absent — deferred until signing/trust lands (#111).
var KnownCapabilities = map[Capability]bool{
	CapReadFiles:     true,
	CapWriteFiles:    true,
	CapNetwork:       true,
	CapOSOpen:        true,
	CapOSClipboard:   true,
	CapOSNotify:      true,
	CapUISurface:     true,
	CapEditorSchema:  true,
	CapContentMutate: true,
	CapPluginDB:      true,
}

// Qualifier refines a capability grant's scope. The default/whole-scope
// qualifier is QualGranted. File-write capabilities may be narrowed to the
// active/declared notebook (QualNotebook) or the whole vault (QualVault).
const (
	QualGranted  = "granted"
	QualNotebook = "notebook"
	QualVault    = "vault"
)

// validQualifiers is the set of accepted scope qualifier strings.
var validQualifiers = map[string]bool{
	QualGranted:  true,
	QualNotebook: true,
	QualVault:    true,
}

// qualifierScopeFor reports which qualifiers are meaningful for a capability.
// File-write ops honor notebook/vault narrowing; everything else uses
// QualGranted (a non-default qualifier is accepted but has no extra effect, so
// a manifest can be uniform). Unknown capabilities are rejected upstream.
func qualifierScopeFor(c Capability) string {
	switch c {
	case CapWriteFiles, CapReadFiles:
		return "granted|notebook|vault"
	default:
		return "granted"
	}
}

// NormalizeCapabilities converts a raw manifest capabilities declaration
// (values may be the bool true, a string qualifier, or null/absent) into a
// normalized capability→qualifier map. It rejects unknown capabilities and
// malformed values so an install never silently grants a right the host does
// not understand. An empty/nil input yields an empty (non-nil) map.
func NormalizeCapabilities(raw map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		cap := Capability(k)
		if !KnownCapabilities[cap] {
			return nil, fmt.Errorf("unknown capability %q (recognized: %s)", k, ListCapabilities())
		}
		qual, err := normalizeQualifier(cap, v)
		if err != nil {
			return nil, fmt.Errorf("capability %q: %w", k, err)
		}
		out[k] = qual
	}
	return out, nil
}

// normalizeQualifier interprets a single capability value. `true` (or null)
// means "granted at default scope"; a string must be a recognized qualifier. c
// is the capability the value is bound to, so an invalid qualifier can report
// the scopes that are actually meaningful for that capability.
func normalizeQualifier(c Capability, v any) (string, error) {
	switch x := v.(type) {
	case bool:
		if x {
			return QualGranted, nil
		}
		return "", fmt.Errorf("capability value false is not meaningful (omit the capability instead)")
	case nil:
		return QualGranted, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return QualGranted, nil
		}
		if !validQualifiers[s] {
			return "", fmt.Errorf("invalid scope %q (expected %s)", s, qualifierScopeFor(c))
		}
		return s, nil
	default:
		return "", fmt.Errorf("capability value must be true or a scope string (got %T)", v)
	}
}

// ListCapabilities returns a sorted, comma-separated list of recognized
// capability ids, for error messages.
func ListCapabilities() string {
	caps := make([]string, 0, len(KnownCapabilities))
	for c := range KnownCapabilities {
		caps = append(caps, string(c))
	}
	sort.Strings(caps)
	return strings.Join(caps, ", ")
}

// CapabilityDeniedError is the structured error returned by requireGrant when a
// plugin attempts a privileged operation it has not been granted. It is
// JSON-serializable so the frontend SDK can surface a specific, actionable
// message (and a re-prompt) rather than a generic string.
//
// Disabled (omitempty, zero-value false) discriminates the binding-layer
// disabled-plugin denial (#359) from a plain ungranted capability so callers
// can show the right message ("plugin is disabled, re-enable it" vs. "grant
// this capability?") without parsing the error string.
type CapabilityDeniedError struct {
	Plugin     string `json:"plugin"`
	Capability string `json:"capability"`
	Requested  string `json:"requested"` // qualifier the plugin asked for ("" if N/A)
	Granted    string `json:"granted"`   // qualifier currently granted ("" if none)
	Disabled   bool   `json:"disabled,omitempty"`
}

func (e *CapabilityDeniedError) Error() string {
	if e.Disabled {
		return fmt.Sprintf("plugin %q is disabled; capability %q denied at the binding layer", e.Plugin, e.Capability)
	}
	if e.Granted == "" {
		return fmt.Sprintf("plugin %q has not been granted %q", e.Plugin, e.Capability)
	}
	return fmt.Sprintf("plugin %q needs %q scope for %q but is granted %q", e.Plugin, e.Requested, e.Capability, e.Granted)
}

// validSettingTypes is the set of recognized settings-schema field types (#103).
var validSettingTypes = map[string]bool{
	"string": true,
	"number": true,
	"bool":   true,
	"select": true,
	"color":  true,
	"keymap": true,
	"list":   true,
}

// validateSettingsSchema checks that each entry in a manifest's declarative
// settings schema has the required fields (key, label, type), a recognized
// type, AND — when a default value is present — that the default matches the
// declared type (#155). Malformed entries are rejected at install so the
// generated settings form never renders a broken field or silently ships a
// default that is not a valid value.
func validateSettingsSchema(settings []map[string]any) error {
	keys := make(map[string]bool, len(settings))
	for i, field := range settings {
		key, _ := field["key"].(string)
		if key == "" {
			return fmt.Errorf("settings[%d]: missing or empty key", i)
		}
		if keys[key] {
			return fmt.Errorf("settings[%d]: duplicate key %q", i, key)
		}
		keys[key] = true
		label, _ := field["label"].(string)
		if label == "" {
			return fmt.Errorf("settings[%d]: missing or empty label for key %q", i, key)
		}
		ftype, _ := field["type"].(string)
		if !validSettingTypes[ftype] {
			return fmt.Errorf("settings[%d]: key %q has invalid type %q (recognized: string, number, bool, select, color, keymap, list)", i, key, ftype)
		}
		// Type-check the default value if present (#155).
		if defaultVal, hasDefault := field["default"]; hasDefault && defaultVal != nil {
			if err := validateSettingDefault(ftype, defaultVal, field, key); err != nil {
				return fmt.Errorf("settings[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// validateSettingDefault type-checks a single default value against the
// declared field type (#155). Returns a descriptive error naming the field +
// expected type.
func validateSettingDefault(ftype string, defaultVal any, field map[string]any, key string) error {
	switch ftype {
	case "string", "keymap":
		if _, ok := defaultVal.(string); !ok {
			return fmt.Errorf("key %q: default for type %q must be a string, got %T", key, ftype, defaultVal)
		}
	case "number":
		if _, ok := defaultVal.(float64); !ok {
			// JSON numbers come through as float64 in Go's encoding/json.
			// An int assertion covers hand-built maps in tests.
			if _, ok := defaultVal.(int); ok {
				return nil
			}
			return fmt.Errorf("key %q: default for type \"number\" must be a number, got %T", key, defaultVal)
		}
	case "bool":
		if _, ok := defaultVal.(bool); !ok {
			return fmt.Errorf("key %q: default for type \"bool\" must be a boolean, got %T", key, defaultVal)
		}
	case "select":
		str, ok := defaultVal.(string)
		if !ok {
			return fmt.Errorf("key %q: default for type \"select\" must be a string, got %T", key, defaultVal)
		}
		// Verify the default is in the options array.
		optionsRaw, _ := field["options"].([]any)
		if len(optionsRaw) == 0 {
			return fmt.Errorf("key %q: type \"select\" requires a non-empty \"options\" array when a default is set", key)
		}
		found := false
		for _, opt := range optionsRaw {
			if optStr, ok := opt.(string); ok && optStr == str {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("key %q: default %q is not in the options array", key, str)
		}
	case "color":
		str, ok := defaultVal.(string)
		if !ok {
			return fmt.Errorf("key %q: default for type \"color\" must be a string, got %T", key, defaultVal)
		}
		if !isValidSettingColor(str) {
			return fmt.Errorf("key %q: default %q is not a valid color (expected #hex or rgb()/rgba())", key, str)
		}
	case "list":
		if _, ok := defaultVal.([]any); !ok {
			return fmt.Errorf("key %q: default for type \"list\" must be an array, got %T", key, defaultVal)
		}
	}
	return nil
}

// isValidSettingColor checks whether a string is a valid CSS color for the
// settings form color type. Accepts #hex (3–8 digits) and rgb()/rgba(). Named
// colors, hsl(), url(), and expressions are rejected — the same sandbox-by-
// validation approach used by the theme validator.
func isValidSettingColor(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == '#' {
		hex := s[1:]
		for _, c := range hex {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return len(hex) == 3 || len(hex) == 4 || len(hex) == 6 || len(hex) == 8
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(s, ")") {
		return true
	}
	if strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(s, ")") {
		return true
	}
	return false
}
