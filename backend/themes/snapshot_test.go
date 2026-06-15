package themes

import (
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
