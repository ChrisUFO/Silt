package themes

import "strings"

// Theme is the parsed canonical theme. It mirrors the JSON schema in
// DESIGN.md §2.1 / SPECS.md §6.4 exactly: a modes-based object with
// hue-agnostic semantic accents. See themes/cyber_forest.json for the
// canonical example.
//
// Typography is an optional theme-level section (not per-mode): font
// families rarely change between dark and light variants of the same
// theme. When present, the theme's font choices are injected as CSS
// custom properties (--font-headline, --font-body, --font-mono)
// alongside the color tokens; when absent, the config's editor.*
// values remain in effect (backward compatible).
type Theme struct {
	SchemaVersion string      `json:"schema_version"`
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Author        string      `json:"author"`
	Description   string      `json:"description"`
	Typography    *Typography `json:"typography,omitempty"`
	Modes         Modes       `json:"modes"`
}

// Typography holds the optional font-family choices for a theme. Each
// field is a CSS font-family declaration string (e.g. "'Plus Jakarta
// Sans', sans-serif"). Fields are individually optional — a theme can
// define only a headline font while leaving body/mono to the config.
type Typography struct {
	FontFamily     string `json:"font_family,omitempty"`
	MonoFontFamily string `json:"mono_font_family,omitempty"`
	HeadlineFont   string `json:"headline_font,omitempty"`
}

// Modes holds the per-appearance token sets. Both dark and light are
// required for a theme to be valid.
type Modes struct {
	Dark  Mode `json:"dark"`
	Light Mode `json:"light"`
}

// Mode is one appearance (dark or light) of a theme.
type Mode struct {
	BG      BG      `json:"bg"`
	Border  Border  `json:"border"`
	Text    Text    `json:"text"`
	Accent  Accent  `json:"accent"`
	Status  Status  `json:"status"`
	Texture *Texture `json:"texture,omitempty"`
}

// Texture is an optional per-mode decorative surface overlay (e.g. the woven
// linen + paper grain on the Linen theme). When present, Flatten emits three
// CSS custom properties (--silt-texture-image / -opacity / -blend) that a
// single global ::before overlay renders behind content. It is purely
// decorative and never part of the canonical color-token contract: themes
// without a texture block are unaffected, and the WCAG/contrast harness
// ignores it (it tests token pairs, not the rendered overlay). The image is
// sandboxed by validation (no CSS declaration-breaking characters) so it
// cannot escape the :root{--name:value;} injection context.
type Texture struct {
	Image   string `json:"image"`   // background-image value: url(...) and/or CSS gradient(s), comma-separated
	Opacity string `json:"opacity"` // overall overlay strength, e.g. "0.06"
	Blend   string `json:"blend"`   // mix-blend-mode, e.g. "overlay" or "multiply"
}

// BG is the canvas/background scale.
type BG struct {
	Void    string `json:"void"`
	Surface string `json:"surface"`
	Panel   string `json:"panel"`
	Hover   string `json:"hover"`
	Active  string `json:"active"`
}

// Border is the hairline-isolation scale.
type Border struct {
	Muted  string `json:"muted"`
	Zinc   string `json:"zinc"`
	Active string `json:"active"`
	Focus  string `json:"focus"`
}

// Text is the foreground type scale.
type Text struct {
	Primary  string `json:"primary"`
	Muted    string `json:"muted"`
	Disabled string `json:"disabled"`
}

// Accent holds the two semantic accents (primary = "go/done", secondary =
// "in progress"). Components reference only the semantic names; each theme
// maps its concrete hues onto them.
type Accent struct {
	Primary   AccentTriple `json:"primary"`
	Secondary AccentTriple `json:"secondary"`
}

// AccentTriple is a start/end/glow gradient triple.
type AccentTriple struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Glow  string `json:"glow"`
}

// Status holds warn/danger semantic colors.
type Status struct {
	Warn   string `json:"warn"`
	Danger string `json:"danger"`
}

// Flatten produces the flat map of CSS custom-property names → values for
// the given mode ("dark" or "light"). The keys are exactly the names the
// frontend injects on :root (--bg-void, --accent-primary-start, …). An
// unknown mode falls back to "dark".
func (t *Theme) Flatten(mode string) map[string]string {
	m := t.Modes.Dark
	if mode == "light" {
		m = t.Modes.Light
	}
	out := map[string]string{}
	out["--bg-void"] = m.BG.Void
	out["--bg-surface"] = m.BG.Surface
	out["--bg-panel"] = m.BG.Panel
	out["--bg-hover"] = m.BG.Hover
	out["--bg-active"] = m.BG.Active

	out["--border-muted"] = m.Border.Muted
	out["--border-zinc"] = m.Border.Zinc
	out["--border-active"] = m.Border.Active
	out["--border-focus"] = m.Border.Focus

	out["--text-primary"] = m.Text.Primary
	out["--text-muted"] = m.Text.Muted
	out["--text-disabled"] = m.Text.Disabled

	out["--accent-primary-start"] = m.Accent.Primary.Start
	out["--accent-primary-end"] = m.Accent.Primary.End
	out["--accent-primary-glow"] = m.Accent.Primary.Glow

	out["--accent-secondary-start"] = m.Accent.Secondary.Start
	out["--accent-secondary-end"] = m.Accent.Secondary.End
	out["--accent-secondary-glow"] = m.Accent.Secondary.Glow

	out["--status-warn"] = m.Status.Warn
	out["--status-danger"] = m.Status.Danger

	// Optional decorative surface texture (e.g. Linen's woven paper grain).
	// Emitted only when the mode declares a texture block; absent on the
	// canonical color-token set, so themes without texture are unchanged.
	// The frontend renders a single global ::before overlay from these vars.
	// --silt-texture-display gates the overlay's very existence: the global
	// body::before defaults to display:none, and only a theme that declares
	// a texture flips it to block, so non-textured themes never pay for a
	// full-screen composited layer at all.
	if m.Texture != nil {
		out["--silt-texture-display"] = "block"
		if v := strings.TrimSpace(m.Texture.Image); v != "" {
			out["--silt-texture-image"] = v
		}
		if v := strings.TrimSpace(m.Texture.Opacity); v != "" {
			out["--silt-texture-opacity"] = v
		}
		if v := strings.TrimSpace(m.Texture.Blend); v != "" {
			out["--silt-texture-blend"] = v
		}
	}

	// Typography (theme-level, not per-mode). Emitted only when the
	// theme defines them; the frontend CSS uses these with fallbacks
	// to the config-provided --editor-* variables, so themes without
	// a typography section are backward compatible.
	if t.Typography != nil {
		if v := t.Typography.HeadlineFont; v != "" {
			out["--font-headline"] = v
		}
		if v := t.Typography.FontFamily; v != "" {
			out["--font-body"] = v
		}
		if v := t.Typography.MonoFontFamily; v != "" {
			out["--font-mono"] = v
		}
	}

	return out
}

// BGVoid returns the resolved bg.void for the given mode, used to set the
// native webview BackgroundColour without a full flatten round-trip.
func (t *Theme) BGVoid(mode string) string {
	if mode == "light" {
		return t.Modes.Light.BG.Void
	}
	return t.Modes.Dark.BG.Void
}

// HexToRGB parses a #rgb / #rrggbb / #rrggbbaa hex color into its 8-bit
// components. The 8-digit form (with alpha) is accepted but the alpha
// channel is intentionally dropped: this seeds the native webview
// BackgroundColour, which is an opaque window background where alpha has no
// meaning. This mirrors isValidColor (which accepts #rrggbbaa) so any color
// that passes validation can seed the BackgroundColour; the full alpha-bearing
// value is still injected verbatim into CSS by the frontend injector, where it
// applies correctly. Non-hex inputs return ok=false so the caller can keep a
// safe default.
func HexToRGB(s string) (r, g, b uint8, ok bool) {
	s = strings.TrimSpace(s)
	if len(s) == 0 || s[0] != '#' {
		return 0, 0, 0, false
	}
	hex := s[1:]
	var full string
	switch len(hex) {
	case 3:
		// #rgb → #rrggbb
		full = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
		full = hex
	case 8:
		// #rrggbbaa → drop the alpha channel (webview bg has no alpha).
		full = hex[0:6]
	default:
		return 0, 0, 0, false
	}
	ri, ok1 := parseHexByte(full[0:2])
	gi, ok2 := parseHexByte(full[2:4])
	bi, ok3 := parseHexByte(full[4:6])
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false
	}
	return ri, gi, bi, true
}

func parseHexByte(s string) (uint8, bool) {
	hi, ok1 := hexDigit(s[0])
	lo, ok2 := hexDigit(s[1])
	if !ok1 || !ok2 {
		return 0, false
	}
	return hi*16 + lo, true
}

func hexDigit(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// ThemeInfo is the lightweight metadata returned by ListThemes for the
// picker UI (#47) and the active-theme summary.
type ThemeInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Swatches    []string `json:"swatches"` // preview color hexes (primary/secondary start)
	Source      string   `json:"source"`   // "disk" (on-disk), "default" (embedded primary default), or "bundled" (an embedded non-default first-class theme)
}

// AsInfo converts a parsed Theme into the lightweight ThemeInfo, deriving
// preview swatches from the dark-mode accent starts.
func (t *Theme) AsInfo(source string) ThemeInfo {
	return ThemeInfo{
		ID:          t.ID,
		Name:        t.Name,
		Author:      t.Author,
		Description: t.Description,
		Swatches:    []string{t.Modes.Dark.Accent.Primary.Start, t.Modes.Dark.Accent.Secondary.Start},
		Source:      source,
	}
}
