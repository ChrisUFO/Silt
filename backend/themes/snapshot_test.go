package themes

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// goldenDefaultDark / goldenDefaultLight pin the EXACT flattened CSS
// token map produced by the embedded default theme (cyber_forest.json).
// This is the default-theme regression snapshot (#50): any drift in the
// shipped default — an accidental token edit, a migration regression, a
// palette change without intent — fails here with a precise diff.
//
// The text.muted values were corrected during #50 to bring
// muted/metadata text up to the WCAG AA 4.5:1 target documented in
// DESIGN.md §8 (see contrast_test.go), across ALL FIVE backgrounds
// (void/surface/panel/hover/active): dark #71717a → #8b8b94, light
// #64748b → #4d5667. Update these only if the muted token is
// intentionally re-tuned, and re-run the WCAG assertions to confirm AA
// is still met on every background.
var goldenDefaultDark = map[string]string{
	"--bg-void":               "#0c0c0e",
	"--bg-surface":            "#121215",
	"--bg-panel":              "#161619",
	"--bg-hover":              "#1c1c21",
	"--bg-active":             "#222226",
	"--border-muted":          "#1e1e23",
	"--border-zinc":           "#27272a",
	"--border-active":         "#3f3f46",
	"--border-focus":          "#52525b",
	"--text-primary":          "#dee3e6",
	"--text-muted":            "#8b8b94",
	"--text-disabled":         "#4b5563",
	"--accent-primary-start":  "#2dd4bf",
	"--accent-primary-end":    "#0d9488",
	"--accent-primary-glow":   "rgba(20, 184, 166, 0.15)",
	"--accent-secondary-start": "#6366f1",
	"--accent-secondary-end":  "#a855f7",
	"--accent-secondary-glow": "rgba(168, 85, 247, 0.12)",
	"--status-warn":           "#fbbf24",
	"--status-danger":         "#f43f5e",
	"--font-body":             "'Plus Jakarta Sans', sans-serif",
	"--font-mono":             "'JetBrains Mono', monospace",
	"--font-headline":         "'Hanken Grotesk', sans-serif",
}

var goldenDefaultLight = map[string]string{
	"--bg-void":               "#f8fafc",
	"--bg-surface":            "#ffffff",
	"--bg-panel":              "#f1f5f9",
	"--bg-hover":              "#e2e8f0",
	"--bg-active":             "#cbd5e1",
	"--border-muted":          "#e2e8f0",
	"--border-zinc":           "#cbd5e1",
	"--border-active":         "#94a3b8",
	"--border-focus":          "#64748b",
	"--text-primary":          "#0f172a",
	"--text-muted":            "#4d5667",
	"--text-disabled":         "#94a3b8",
	"--accent-primary-start":  "#0d9488",
	"--accent-primary-end":    "#115e59",
	"--accent-primary-glow":   "rgba(13, 148, 136, 0.10)",
	"--accent-secondary-start": "#4f46e5",
	"--accent-secondary-end":  "#7c3aed",
	"--accent-secondary-glow": "rgba(79, 70, 229, 0.08)",
	"--status-warn":           "#d97706",
	"--status-danger":         "#e11d48",
	"--font-body":             "'Plus Jakarta Sans', sans-serif",
	"--font-mono":             "'JetBrains Mono', monospace",
	"--font-headline":         "'Hanken Grotesk', sans-serif",
}

// TestDefaultTheme_GoldenSnapshot asserts the embedded default theme's
// flattened dark + light token maps are byte-identical to the golden
// maps above. On drift the failure names every mismatched token and its
// expected vs actual value so a reviewer sees exactly what changed.
func TestDefaultTheme_GoldenSnapshot(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("embedded default is invalid: %v", err)
	}
	for _, c := range []struct {
		mode   string
		golden map[string]string
	}{
		{"dark", goldenDefaultDark},
		{"light", goldenDefaultLight},
	} {
		got := th.Flatten(c.mode)
		assertTokenMap(t, c.mode, c.golden, got)
	}
}

func assertTokenMap(t *testing.T, mode string, want, got map[string]string) {
	t.Helper()
	keys := make(map[string]struct{}, len(want))
	for k := range want {
		keys[k] = struct{}{}
	}
	for k := range got {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var b strings.Builder
	mismatch := 0
	for _, k := range sorted {
		w, wantOK := want[k]
		g, gotOK := got[k]
		if !wantOK {
			mismatch++
			b.WriteString("\n  + " + k + " = " + g + " (unexpected token in theme)")
			continue
		}
		if !gotOK {
			mismatch++
			b.WriteString("\n  - " + k + " (missing from theme)")
			continue
		}
		if w != g {
			mismatch++
			b.WriteString("\n  ~ " + k + ": want " + w + ", got " + g)
		}
	}
	if mismatch > 0 {
		t.Errorf("%s mode: embedded default theme drifted from the golden snapshot (%d token(s) changed):%s\n"+
			"If the change is intentional, update goldenDefault%s in snapshot_test.go.",
			mode, mismatch, b.String(), titleCase(mode))
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// expectedFlattenKeys is the complete set of CSS custom properties a theme
// with the default typography block produces when flattened: 20 color tokens
// + 3 typography tokens (--font-body/mono/headline). The default golden
// snapshot above pins every VALUE of cyber_forest against this set; the
// first-class shape guard below pins the SET (and the tuned tokens) for the
// Sprint 8 additions.
var expectedFlattenKeys = []string{
	"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active",
	"--border-muted", "--border-zinc", "--border-active", "--border-focus",
	"--text-primary", "--text-muted", "--text-disabled",
	"--accent-primary-start", "--accent-primary-end", "--accent-primary-glow",
	"--accent-secondary-start", "--accent-secondary-end", "--accent-secondary-glow",
	"--status-warn", "--status-danger",
	"--font-body", "--font-mono", "--font-headline",
}

// TestFirstClassThemes_FlattenShape pins the structural contract for every
// non-default first-class theme: both modes flatten to exactly the canonical
// color-token key set (no missing/extra color tokens), the typography block is
// present, and the specifically WCAG-tuned tokens hold their known-good
// values. The default has a full value-level golden snapshot above; the new
// themes get shape + tuned-token guards (the contrast harness covers WCAG drift
// on the rest).
//
// The optional decorative texture overlay (--silt-texture-image/opacity/blend,
// used by Linen's woven-paper surface) is an opt-in superset on top of the
// canonical token set: it is allowed here but never required, so a theme
// without a texture block still passes.
func TestFirstClassThemes_FlattenShape(t *testing.T) {
	all, err := EmbeddedThemes()
	if err != nil {
		t.Fatalf("EmbeddedThemes: %v", err)
	}
	// Tuned tokens: values changed during Sprint 8 to clear the WCAG
	// 5-background AA matrix. Pinning them here catches a regression of the
	// fix with a precise message (the contrast test would also fail, but
	// less specifically).
	tuned := map[string]map[string]string{
		"silt-terra-noir": {"dark|--text-muted": "#a89478"},
		// #138: pin Graphite/Linen dark text-primary at the distinct values
		// #136 anchored (neutral-white / warm-oatmeal). The issue tracked a
		// state where these collapsed to Cyber-Forest-adjacent cool blue-grays
		// (#e6e8eb / #e6e7ea) and themes stopped reading differently. The
		// distinctness guard in contrast_test.go covers the perceptual side;
		// this pin catches an exact-value revert with a precise diff message.
		"silt-graphite": {"dark|--text-primary": "#ebebeb"},
		"silt-linen": {
			"dark|--text-muted":   "#b9b0a1",
			"dark|--text-primary": "#e8e3d8",
		},
	}
	// Opt-in decorative texture overlay keys (superset; allowed, not required).
	textureKeys := map[string]bool{
		"--silt-texture-display": true,
		"--silt-texture-image":   true,
		"--silt-texture-opacity": true,
		"--silt-texture-blend":   true,
	}
	for _, th := range all {
		if th.ID == DefaultThemeID {
			continue
		}
		if th.Typography == nil {
			t.Errorf("%s: expected a typography block", th.ID)
		}
		for _, mode := range []string{"dark", "light"} {
			flat := th.Flatten(mode)
			// Every canonical color token must be present.
			for _, k := range expectedFlattenKeys {
				if _, ok := flat[k]; !ok {
					t.Errorf("%s [%s]: missing token %s", th.ID, mode, k)
				}
			}
			// Any extra key must be the opt-in texture overlay, not a stray token.
			for k := range flat {
				if !slices.Contains(expectedFlattenKeys, k) && !textureKeys[k] {
					t.Errorf("%s [%s]: unexpected token %s (only --silt-texture-* overlays are allowed)",
						th.ID, mode, k)
				}
			}
			// Tuned-token pin.
			if tt, ok := tuned[th.ID]; ok {
				for key, want := range tt {
					// key is "<mode>|<token>"
					if strings.HasPrefix(key, mode+"|") {
						tok := strings.TrimPrefix(key, mode+"|")
						if got := flat[tok]; got != want {
							t.Errorf("%s [%s]: tuned %s = %s, want %s (WCAG tuning regressed)",
								th.ID, mode, tok, got, want)
						}
					}
				}
			}
		}
	}
}

// TestTextureDisplay_GatedByTextureBlock pins the --silt-texture-display
// contract introduced for the render-gating optimization: a mode that
// declares a texture block must flip the global overlay on (display:block),
// and a mode without one must emit nothing (so the frontend's body::before
// stays display:none and never instantiates a full-screen layer). This is
// the exact behavior the overlay's display:var(--silt-texture-display,none)
// relies on; a regression here would either hide Linen's texture or force a
// composited layer onto every non-textured theme.
func TestTextureDisplay_GatedByTextureBlock(t *testing.T) {
	all, err := EmbeddedThemes()
	if err != nil {
		t.Fatalf("EmbeddedThemes: %v", err)
	}
	wantTextured := map[string]bool{"silt-linen": true} // the only first-class textured theme
	for _, th := range all {
		for _, mode := range []string{"dark", "light"} {
			flat := th.Flatten(mode)
			_, hasDisplay := flat["--silt-texture-display"]
			if wantTextured[th.ID] {
				if !hasDisplay {
					t.Errorf("%s [%s]: textured theme must emit --silt-texture-display", th.ID, mode)
				} else if flat["--silt-texture-display"] != "block" {
					t.Errorf("%s [%s]: --silt-texture-display = %q, want \"block\"",
						th.ID, mode, flat["--silt-texture-display"])
				}
			} else {
				if hasDisplay {
					t.Errorf("%s [%s]: non-textured theme must NOT emit --silt-texture-display (got %q)",
						th.ID, mode, flat["--silt-texture-display"])
				}
			}
		}
	}
}
