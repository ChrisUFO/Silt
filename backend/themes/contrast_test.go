package themes

import (
	"math"
	"testing"
)

// approxRatio computes the WCAG contrast ratio between two hex/rgb color
// strings using the same math as the production harness, rounded to 2 dp
// for legible failure messages.
func approxRatio(t *testing.T, a, b string) float64 {
	t.Helper()
	r, ok := ContrastRatio(a, b)
	if !ok {
		t.Fatalf("ContrastRatio(%q,%q) not parseable", a, b)
	}
	return math.Round(r*100) / 100
}

// TestContrastRatio_ReferencePairs pins the WCAG formula against known
// reference values so a future refactor of the math is caught.
func TestContrastRatio_ReferencePairs(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want float64 // exact WCAG ratio
		tol  float64
	}{
		{"black on white", "#ffffff", "#000000", 21.0, 0.05},
		{"white on black", "#000000", "#ffffff", 21.0, 0.05},
		{"black on black", "#000000", "#000000", 1.0, 0.001},
		{"#777 on #fff (WCAG sample)", "#777777", "#ffffff", 4.48, 0.05},
	}
	for _, c := range cases {
		got, ok := ContrastRatio(c.a, c.b)
		if !ok {
			t.Fatalf("%s: not parseable", c.name)
		}
		if math.Abs(got-c.want) > c.tol {
			t.Errorf("%s: ContrastRatio = %.3f, want %.2f (±%.2f)", c.name, got, c.want, c.tol)
		}
	}
}

// TestContrastRatio_AcceptedColorForms ensures the harness handles every
// color grammar the validator permits (#hex variants + rgb()/rgba()).
func TestContrastRatio_AcceptedColorForms(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"#rrggbb", "#0c0c0e", "#ffffff"},
		{"#rgb", "#fff", "#000"},
		{"#rrggbbaa (alpha dropped)", "#0c0c0eff", "#ffffffff"},
		{"rgb()", "rgb(12,12,14)", "rgb(255,255,255)"},
		{"rgba()", "rgba(12,12,14,1)", "rgba(255,255,255,1)"},
		{"rgb() percent", "rgb(5%,5%,5%)", "rgb(100%,100%,100%)"},
	}
	for _, c := range cases {
		if _, ok := ContrastRatio(c.a, c.b); !ok {
			t.Errorf("%s: expected ok, got false (%q vs %q)", c.name, c.a, c.b)
		}
	}
	// Unparseable forms are rejected.
	for _, bad := range []string{"red", "hsl(0,0%,0%)", "url(x)", "", "not-a-color"} {
		if _, ok := ContrastRatio(bad, "#fff"); ok {
			t.Errorf("expected %q to be rejected, got ok", bad)
		}
	}
	// Malformed alpha values are rejected.
	for _, bad := range []string{"rgba(12,12,14,bad)", "rgba(12,12,14,2)", "rgba(12,12,14,-1)"} {
		if _, ok := ContrastRatio(bad, "#fff"); ok {
			t.Errorf("expected %q to be rejected (bad alpha), got ok", bad)
		}
	}
	// NaN/Inf in an RGB component are rejected (strconv.ParseFloat
	// accepts them with nil error; without the non-finite guard the
	// harness would coerce NaN->0 and return a bogus ratio). Note: a
	// NaN in the ALPHA channel (e.g. rgba(12,12,14,NaN)) is NOT rejected
	// here — the harness intentionally drops alpha (luminance is over
	// opaque colors), so alpha-NaN never reaches a range check. The
	// validator (isValidColor) rejects alpha-NaN because it validates
	// the full color spec; see TestIsValidColor.
	for _, bad := range []string{"rgba(NaN,0,0,0.5)", "rgb(Inf,0,0)", "rgb(-Inf,0,0)"} {
		if _, ok := ContrastRatio(bad, "#fff"); ok {
			t.Errorf("expected %q to be rejected (non-finite RGB component), got ok", bad)
		}
	}
}

// contrastPairs enumerates the text/background pairs the shipped default
// theme is measured against, per the WCAG targets documented in
// DESIGN.md §8 and docs/THEMING.md §5.
type contrastPair struct {
	label  string
	fg, bg string
}

func themePairs(t *Theme) map[string][]contrastPair {
	pairs := map[string][]contrastPair{}
	for _, mode := range []string{"dark", "light"} {
		flat := t.Flatten(mode)
		// All five backgrounds a token can render on. The earlier
		// 3-background matrix missed bg.hover/bg.active, where a
		// medium-gray muted text can dip below AA on the lighter
		// active/hover surfaces — exactly the gap the audit caught.
		bgs := []string{"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active"}
		textFgs := []string{"--text-primary", "--text-muted"}
		var ps []contrastPair
		for _, fg := range textFgs {
			for _, bg := range bgs {
				ps = append(ps, contrastPair{fg + " on " + bg, flat[fg], flat[bg]})
			}
		}
		// Accents are non-text UI (focus rings, swatches, icons): AA
		// non-text threshold is 3:1, measured against the canvas.
		for _, fg := range []string{"--accent-primary-start", "--accent-secondary-start"} {
			ps = append(ps, contrastPair{fg + " on --bg-void", flat[fg], flat["--bg-void"]})
		}
		pairs[mode] = ps
	}
	return pairs
}

// TestWCAG_DefaultTheme_ReportsAllRatios logs every measured ratio for
// the embedded default so the assertion thresholds below are auditable
// and a future palette regression is obvious from the log.
func TestWCAG_DefaultTheme_ReportsAllRatios(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	for mode, ps := range themePairs(th) {
		for _, p := range ps {
			t.Logf("[%-5s] %-32s %s / %s = %.2f:1", mode, p.label, p.fg, p.bg, approxRatio(t, p.fg, p.bg))
		}
	}
}

// TestWCAG_DefaultTheme_PrimaryTextAAA asserts primary text meets AAA
// (>=7:1) against every background it is rendered on (all five, both
// modes). Primary text is body copy — the highest-contrast requirement.
func TestWCAG_DefaultTheme_PrimaryTextAAA(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	const min = 7.0
	for _, mode := range []string{"dark", "light"} {
		flat := th.Flatten(mode)
		for _, bg := range []string{"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active"} {
			r := approxRatio(t, flat["--text-primary"], flat[bg])
			if r < min {
				t.Errorf("%s: text.primary on %s = %.2f:1, want >= %.1f:1 (AAA)", mode, bg, r, min)
			}
		}
	}
}

// TestWCAG_DefaultTheme_AccentsNonTextAA asserts the two semantic accent
// starts meet the WCAG AA non-text threshold (>=3:1) on the canvas, so
// focus rings, icons, and swatches stay discernible.
func TestWCAG_DefaultTheme_AccentsNonTextAA(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	const min = 3.0
	for _, mode := range []string{"dark", "light"} {
		flat := th.Flatten(mode)
		for _, fg := range []string{"--accent-primary-start", "--accent-secondary-start"} {
			r := approxRatio(t, flat[fg], flat["--bg-void"])
			if r < min {
				t.Errorf("%s: %s on bg.void = %.2f:1, want >= %.1f:1 (AA non-text)", mode, fg, r, min)
			}
		}
	}
}

// TestWCAG_DefaultTheme_MutedTextAA asserts muted text (labels,
// metadata, secondary text) meets AA (>=4.5:1) against ALL FIVE
// backgrounds in both modes — including bg.hover/bg.active, the lighter
// surfaces where a medium-gray muted token is most at risk. This is the
// documented DESIGN.md §8 target for secondary text. (An earlier
// 3-background version passed while light-muted actually failed on
// bg.active; the full matrix closes that gap.)
func TestWCAG_DefaultTheme_MutedTextAA(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("ParseDefault: %v", err)
	}
	const min = 4.5
	for _, mode := range []string{"dark", "light"} {
		flat := th.Flatten(mode)
		for _, bg := range []string{"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active"} {
			r := approxRatio(t, flat["--text-muted"], flat[bg])
			if r < min {
				t.Errorf("%s: text.muted on %s = %.2f:1, want >= %.1f:1 (AA). "+
					"Muted/metadata text is below the documented 4.5:1 target; "+
					"bump modes.%s.text.muted lighter (dark) / darker (light).",
					mode, bg, r, min, mode)
			}
		}
	}
}

// --- First-class theme WCAG coverage (Sprint 8) ---------------------------
//
// The four new first-class themes (Terra Noir, Linen, Stark, Graphite) are
// measured against the SAME matrix as the default: primary text >= 7:1 (AAA)
// and muted text >= 4.5:1 (AA) on all five backgrounds in both modes, plus the
// two accent starts >= 3:1 (AA non-text) on the canvas. The Sprint 7 harness
// was designed so Sprint 8 "adds rows, not code" — these tests are those rows.
//
// text.disabled and the glow tokens are decorative/non-essential per WCAG and
// are intentionally not asserted here (documented in DESIGN.md); only tokens
// that carry meaning (body text, metadata text, focus/selection accents) are
// guarded. Starter palettes from the issues were tuned where the 5-background
// matrix caught a failure (the same lesson as the default's light-muted).

// assertWCAG runs the full primary/muted/accent matrix for one theme across
// both modes. Failure messages name the theme, mode, token, and the fix
// direction so a failing palette is actionable.
func assertWCAG(t *testing.T, th *Theme) {
	t.Helper()
	backgrounds := []string{"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active"}
	for _, mode := range []string{"dark", "light"} {
		flat := th.Flatten(mode)
		for _, bg := range backgrounds {
			if r := approxRatio(t, flat["--text-primary"], flat[bg]); r < 7.0 {
				t.Errorf("%s [%s]: text.primary on %s = %.2f:1, want >= 7.1 (AAA)",
					th.ID, mode, bg, r)
			}
			if r := approxRatio(t, flat["--text-muted"], flat[bg]); r < 4.5 {
				t.Errorf("%s [%s]: text.muted on %s = %.2f:1, want >= 4.5 (AA). "+
					"Bump modes.%s.text.muted lighter (dark) / darker (light).",
					th.ID, mode, bg, r, mode)
			}
		}
		for _, fg := range []string{"--accent-primary-start", "--accent-secondary-start"} {
			if r := approxRatio(t, flat[fg], flat["--bg-void"]); r < 3.0 {
				t.Errorf("%s [%s]: %s on bg.void = %.2f:1, want >= 3.0 (AA non-text)",
					th.ID, mode, fg, r)
			}
		}
	}
}

// TestWCAG_FirstClassThemes_AllMeetsTargets asserts every embedded first-class
// theme meets the WCAG matrix. It also logs every measured ratio so a future
// palette regression is obvious from the test output (auditable, like the
// default's ReportsAllRatios test).
func TestWCAG_FirstClassThemes_AllMeetsTargets(t *testing.T) {
	all, err := EmbeddedThemes()
	if err != nil {
		t.Fatalf("EmbeddedThemes: %v", err)
	}
	for _, th := range all {
		// Skip the default — it has its own dedicated assertions above (and
		// its own golden snapshot). This test covers the Sprint 8 additions.
		if th.ID == DefaultThemeID {
			continue
		}
		for mode, ps := range themePairs(th) {
			for _, p := range ps {
				t.Logf("[%-14s %-5s] %-32s = %.2f:1", th.ID, mode, p.label, approxRatio(t, p.fg, p.bg))
			}
		}
		assertWCAG(t, th)
	}
}

// TestWCAG_Stark_FocusStatesUnmistakable: Stark's design (#51) relies on
// border-led structure because its near-uniform backgrounds can't separate
// panels by fill alone. WCAG 2.4.11 (Focus Visible) / 1.4.11 (Focus Notable)
// require focus indicators to meet ≥3:1 against adjacent colors. Assert
// border.focus clears that bar on every background in both modes — the
// specific acceptance criterion that makes Stark's focus rings unmistakable.
func TestWCAG_Stark_FocusStatesUnmistakable(t *testing.T) {
	th, ok := ParseEmbeddedByID("silt-stark")
	if !ok {
		t.Fatal("silt-stark not embedded")
	}
	const min = 3.0
	backgrounds := []string{"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active"}
	for _, mode := range []string{"dark", "light"} {
		flat := th.Flatten(mode)
		focus := flat["--border-focus"]
		for _, bg := range backgrounds {
			r := approxRatio(t, focus, flat[bg])
			if r < min {
				t.Errorf("stark [%s]: border.focus on %s = %.2f:1, want >= %.1f:1 (WCAG 2.4.11/1.4.11)",
					mode, bg, r, min)
			}
		}
	}
}

// TestAccentDistinctness_AllFirstClassThemes guards the docs/THEMING.md §4 rule
// that primary and secondary must be visually distinct so the "go/done" and
// "in-progress" states never blur together. We assert a minimum sRGB Euclidean
// distance between accent.primary.start and accent.secondary.start for every
// first-class theme in both modes. The threshold (30) is conservative: Linen
// and Graphite are the closest pairs by design (calm, low-chroma), yet still
// clear it. A future palette that collapses the two accents fails here.
func TestAccentDistinctness_AllFirstClassThemes(t *testing.T) {
	const minDist = 30.0
	all, err := EmbeddedThemes()
	if err != nil {
		t.Fatalf("EmbeddedThemes: %v", err)
	}
	for _, th := range all {
		for _, mode := range []string{"dark", "light"} {
			flat := th.Flatten(mode)
			d := rgbDistance(t, flat["--accent-primary-start"], flat["--accent-secondary-start"])
			if d < minDist {
				t.Errorf("%s [%s]: primary/secondary accent distance = %.1f, want >= %.1f (accents must stay distinct)",
					th.ID, mode, d, minDist)
			}
		}
	}
}

// TestTextPrimaryDistinctFromDefault_AllFirstClassThemes guards the #138
// regression: every first-class theme's body-text color must be perceptibly
// distinct from the default's (Cyber Forest), so switching themes produces a
// visibly different result. The issue tracked a state where Graphite and Linen
// shipped Cyber-Forest-adjacent cool blue-grays (#e6e8eb / #e6e7ea, ~10 sRGB
// units from the default's #dee3e6) and thus read near-identical.
//
// The guard is anchored to the DEFAULT theme rather than asserting all-pairs
// distance because two themes are *intentionally* close: Linen (#e8e3d8) and
// Terra Noir (#ece3d5) are both warm oatmeal/cream whites (~5 units apart) by
// design. A global all-pairs threshold high enough to catch the cool-blue-gray
// collapse would false-trip that legitimate warm-warm pair. Anchoring to the
// default isolates the actual #138 concern (everything reading like Cyber
// Forest) without coupling to the intentional warmth similarity.
//
// The threshold (13.0) cleanly separates the bug from intent: the buggy
// Evidence B values sat at ~9.8-10.7 from the default, while every shipped
// theme clears 16. The headroom keeps the guard from flapping on a future
// minor palette retune.
func TestTextPrimaryDistinctFromDefault_AllFirstClassThemes(t *testing.T) {
	const minDist = 13.0
	all, err := EmbeddedThemes()
	if err != nil {
		t.Fatalf("EmbeddedThemes: %v", err)
	}
	defaults, ok := findByID(all, DefaultThemeID)
	if !ok {
		t.Fatalf("default theme %q not in EmbeddedThemes", DefaultThemeID)
	}
	for _, mode := range []string{"dark", "light"} {
		anchor := defaults.Flatten(mode)["--text-primary"]
		for _, th := range all {
			if th.ID == DefaultThemeID {
				continue
			}
			got := th.Flatten(mode)["--text-primary"]
			d := rgbDistance(t, anchor, got)
			if d < minDist {
				t.Errorf("%s [%s]: --text-primary %s is only %.1f sRGB units from default %s (%s), want >= %.1f (themes must read visibly distinct from Cyber Forest per #138)",
					th.ID, mode, got, d, defaults.ID, anchor, minDist)
			}
		}
	}
}

// findByID returns the theme with the given id from the slice, or ok=false.
func findByID(all []*Theme, id string) (*Theme, bool) {
	for _, th := range all {
		if th.ID == id {
			return th, true
		}
	}
	return nil, false
}

// rgbDistance is the sRGB Euclidean distance between two colors. It is a crude
// but adequate proxy for "perceptually different enough to distinguish" for the
// accent-distinctness guard; a full ΔE is overkill for catching an accidental
// palette collapse.
func rgbDistance(t *testing.T, a, b string) float64 {
	t.Helper()
	ar, ag, ab, ok := parseColorAny(a)
	if !ok {
		t.Fatalf("parseColorAny(%q) failed", a)
	}
	br, bg, bb, ok := parseColorAny(b)
	if !ok {
		t.Fatalf("parseColorAny(%q) failed", b)
	}
	dr := float64(ar) - float64(br)
	dg := float64(ag) - float64(bg)
	db := float64(ab) - float64(bb)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}
