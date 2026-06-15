package themes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustWriteTheme writes a JSON theme file into dir.
func mustWriteTheme(t *testing.T, dir, name, json string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(json), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// minimalValidJSON is a structurally-valid canonical theme (two modes, full
// token set) used as the base for mutation in tests.
const minimalValidJSON = `{
  "schema_version": "1.0.0",
  "id": "test-theme",
  "name": "Test Theme",
  "author": "Tester",
  "description": "test",
  "modes": {
    "dark": {
      "bg": {"void":"#000000","surface":"#111111","panel":"#161616","hover":"#1c1c1c","active":"#222222"},
      "border": {"muted":"#1e1e1e","zinc":"#272727","active":"#3f3f3f","focus":"#525252"},
      "text": {"primary":"#e4e4e4","muted":"#71717a","disabled":"#4b5563"},
      "accent": {
        "primary": {"start":"#2dd4bf","end":"#0d9488","glow":"rgba(20,184,166,0.15)"},
        "secondary": {"start":"#6366f1","end":"#a855f7","glow":"rgba(168,85,247,0.12)"}
      },
      "status": {"warn":"#fbbf24","danger":"#f43f5e"}
    },
    "light": {
      "bg": {"void":"#ffffff","surface":"#f8fafc","panel":"#f1f5f9","hover":"#e2e8f0","active":"#cbd5e1"},
      "border": {"muted":"#e2e8f0","zinc":"#cbd5e1","active":"#94a3b8","focus":"#64748b"},
      "text": {"primary":"#0f172a","muted":"#64748b","disabled":"#94a3b8"},
      "accent": {
        "primary": {"start":"#0d9488","end":"#115e59","glow":"rgba(13,148,136,0.10)"},
        "secondary": {"start":"#4f46e5","end":"#7c3aed","glow":"rgba(79,70,229,0.08)"}
      },
      "status": {"warn":"#d97706","danger":"#e11d48"}
    }
  }
}`

func TestValidate_ValidTheme(t *testing.T) {
	th, err := ParseAndValidate([]byte(minimalValidJSON))
	if err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
	if th.ID != "test-theme" {
		t.Errorf("id mismatch: %q", th.ID)
	}
}

func TestValidate_MissingToken(t *testing.T) {
	bad := strings.Replace(minimalValidJSON, `"#2dd4bf"`, `""`, 1)
	_, err := ParseAndValidate([]byte(bad))
	if err == nil {
		t.Fatalf("expected validation error for missing token, got nil")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	found := false
	for _, e := range verrs {
		if strings.Contains(e.Field, "accent.primary.start") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error on accent.primary.start, got %v", verrs)
	}
}

func TestValidate_BadColor(t *testing.T) {
	bad := strings.Replace(minimalValidJSON, `"#2dd4bf"`, `"not-a-color"`, 1)
	_, err := ParseAndValidate([]byte(bad))
	if err == nil {
		t.Fatalf("expected validation error for bad color, got nil")
	}
}

func TestValidate_MissingIdentity(t *testing.T) {
	bad := strings.Replace(minimalValidJSON, `"id": "test-theme"`, `"id": ""`, 1)
	_, err := ParseAndValidate([]byte(bad))
	if err == nil {
		t.Fatalf("expected validation error for missing id, got nil")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("expected id-required error, got %v", err)
	}
}

func TestValidate_UnparseableJSON(t *testing.T) {
	_, err := ParseAndValidate([]byte("{not json"))
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
}

// TestValidate_SchemaVersionForwardCompat pins the documented forward-
// compatibility contract: schema_version is informational. A theme
// whose token set still matches v1 but carries a higher version number
// keeps loading rather than being rejected outright (validate.go checks
// only that schema_version is non-empty, not that it equals
// SupportedSchemaVersion).
func TestValidate_SchemaVersionForwardCompat(t *testing.T) {
	future := strings.Replace(minimalValidJSON, `"schema_version": "1.0.0"`, `"schema_version": "9.9.9"`, 1)
	th, err := ParseAndValidate([]byte(future))
	if err != nil {
		t.Fatalf("a higher schema_version should still validate (informational): %v", err)
	}
	if th.SchemaVersion != "9.9.9" {
		t.Errorf("schema_version = %q, want 9.9.9 preserved", th.SchemaVersion)
	}
}

// TestValidate_UnknownSchemaVersionStillRequiresField: a missing
// schema_version is reported (the field is required even though its
// value is informational), so a forward-versioned theme can never be
// confused with a theme that omits the field entirely.
func TestValidate_UnknownSchemaVersionStillRequiresField(t *testing.T) {
	bad := strings.Replace(minimalValidJSON, `"schema_version": "1.0.0",`, ``, 1)
	_, err := ParseAndValidate([]byte(bad))
	if err == nil {
		t.Fatal("expected validation error for missing schema_version")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected schema_version in error, got: %v", err)
	}
}

// darkOnlyJSON is a structurally-valid dark theme with NO light mode
// object. The validator must report every required token under
// modes.light as missing (a zero-valued Mode struct has empty token
// fields, each of which fails the required-token check).
const darkOnlyJSON = `{
  "schema_version": "1.0.0",
  "id": "test-theme",
  "name": "Test Theme",
  "modes": {
    "dark": {
      "bg": {"void":"#000000","surface":"#111111","panel":"#161616","hover":"#1c1c1c","active":"#222222"},
      "border": {"muted":"#1e1e1e","zinc":"#272727","active":"#3f3f3f","focus":"#525252"},
      "text": {"primary":"#e4e4e4","muted":"#8b8b94","disabled":"#4b5563"},
      "accent": {
        "primary": {"start":"#2dd4bf","end":"#0d9488","glow":"rgba(20,184,166,0.15)"},
        "secondary": {"start":"#6366f1","end":"#a855f7","glow":"rgba(168,85,247,0.12)"}
      },
      "status": {"warn":"#fbbf24","danger":"#f43f5e"}
    }
  }
}`

// TestValidate_MissingLightMode: a theme that defines only modes.dark
// must be rejected with every required modes.light token reported as
// missing. This is the explicit "missing modes" case from #50.
func TestValidate_MissingLightMode(t *testing.T) {
	_, err := ParseAndValidate([]byte(darkOnlyJSON))
	if err == nil {
		t.Fatal("expected validation error for a theme missing modes.light")
	}
	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	// Every required dark token is present, so all reported errors must
	// be under modes.light. Count them and confirm the prefix.
	lightErrs := 0
	for _, e := range verrs {
		if strings.HasPrefix(e.Field, "modes.light.") {
			lightErrs++
		} else {
			t.Errorf("unexpected non-light error: %+v", e)
		}
	}
	if lightErrs != len(requiredTokens) {
		t.Errorf("expected all %d required light tokens flagged, got %d", len(requiredTokens), lightErrs)
	}
}

func TestIsValidColor(t *testing.T) {
	good := []string{
		"#fff", "#ffffff", "#ffffffff",
		"rgba(0,0,0,0.5)", "rgba(0, 0, 0, 0)", "rgba(255,255,255,1)",
		"rgb(1,2,3)", "rgb(100%, 0%, 0%)",
	}
	for _, c := range good {
		if !isValidColor(c) {
			t.Errorf("isValidColor(%q) = false, want true", c)
		}
	}
	bad := []string{
		"", "white", "#ff", "#gggggg", "hsl(0,0%,0%)",
		"rgba(0,0,0)",       // missing alpha
		"rgba(999,0,0,0.5)", // rgb component out of range
		"rgba(0,0,0,2)",     // alpha > 1
		"rgba(0,0,0,-1)",    // alpha < 0
		"rgb(1,2,3,4)",      // too many components
		"rgb(300,0,0)",      // out of range
		"rgba(a,b,c,d)",     // non-numeric
		// NaN/Inf: strconv.ParseFloat accepts them with a nil error, and
		// NaN range comparisons (v < 0 || v > 255) are both false, so
		// without an explicit non-finite guard these slip through the
		// schema sandbox (#48).
		"rgba(NaN,0,0,0.5)",  // NaN rgb component
		"rgba(12,12,14,NaN)", // NaN alpha channel
		"rgb(Inf,0,0)",       // +Inf component
		"rgb(-Inf,0,0)",      // -Inf component
	}
	for _, c := range bad {
		if isValidColor(c) {
			t.Errorf("isValidColor(%q) = true, want false", c)
		}
	}
}

func TestParseDefault_IsValid(t *testing.T) {
	th, err := ParseDefault()
	if err != nil {
		t.Fatalf("embedded default is invalid: %v", err)
	}
	if th.ID != DefaultThemeID {
		t.Errorf("default id = %q, want %q", th.ID, DefaultThemeID)
	}
	// Flatten must produce all 19 canonical CSS tokens.
	tokens := th.Flatten("dark")
	expected := []string{
		"--bg-void", "--bg-surface", "--bg-panel", "--bg-hover", "--bg-active",
		"--border-muted", "--border-zinc", "--border-active", "--border-focus",
		"--text-primary", "--text-muted", "--text-disabled",
		"--accent-primary-start", "--accent-primary-end", "--accent-primary-glow",
		"--accent-secondary-start", "--accent-secondary-end", "--accent-secondary-glow",
		"--status-warn", "--status-danger",
	}
	for _, k := range expected {
		if _, ok := tokens[k]; !ok {
			t.Errorf("Flatten missing %s", k)
		}
	}
}

func TestFlatten_DarkLightDiffer(t *testing.T) {
	th, _ := ParseDefault()
	dark := th.Flatten("dark")
	light := th.Flatten("light")
	if dark["--bg-void"] == light["--bg-void"] {
		t.Errorf("dark/light bg.void should differ (dark=%s light=%s)", dark["--bg-void"], light["--bg-void"])
	}
	if dark["--bg-void"] != "#0c0c0e" {
		t.Errorf("dark bg.void = %s, want #0c0c0e (pixel-identity)", dark["--bg-void"])
	}
}

func TestBGVoid(t *testing.T) {
	th, _ := ParseDefault()
	if th.BGVoid("dark") != "#0c0c0e" {
		t.Errorf("BGVoid dark = %s", th.BGVoid("dark"))
	}
	if th.BGVoid("light") != "#f8fafc" {
		t.Errorf("BGVoid light = %s", th.BGVoid("light"))
	}
	if th.BGVoid("system") != "#0c0c0e" { // system→dark first paint
		t.Errorf("BGVoid system should resolve to dark: %s", th.BGVoid("system"))
	}
}

func TestListThemes_EmptyDir(t *testing.T) {
	dir := t.TempDir() // exists but empty
	res, err := ListThemes(dir)
	if err != nil {
		t.Fatalf("ListThemes empty dir: %v", err)
	}
	// Empty dir → only the embedded default.
	if len(res.Themes) != 1 {
		t.Fatalf("expected 1 theme (default), got %d", len(res.Themes))
	}
	if res.Themes[0].ID != DefaultThemeID || res.Themes[0].Source != "default" {
		t.Errorf("expected embedded default, got %+v", res.Themes[0])
	}
}

func TestListThemes_MissingDir(t *testing.T) {
	// A nonexistent themes dir (fresh vault before scaffold) is not an
	// error and yields the embedded default.
	res, err := ListThemes(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("ListThemes missing dir: %v", err)
	}
	if len(res.Themes) != 1 || res.Themes[0].ID != DefaultThemeID {
		t.Fatalf("expected embedded default only, got %+v", res.Themes)
	}
}

func TestListThemes_EmptyPath(t *testing.T) {
	// An empty themesDir (no vault open yet) must not call os.ReadDir("") and
	// must still yield the embedded default rather than erroring.
	res, err := ListThemes("")
	if err != nil {
		t.Fatalf("ListThemes empty path: %v", err)
	}
	if len(res.Themes) != 1 || res.Themes[0].ID != DefaultThemeID {
		t.Fatalf("expected embedded default only for empty path, got %+v", res.Themes)
	}
	if res.Themes[0].Source != "default" {
		t.Errorf("expected source=default, got %q", res.Themes[0].Source)
	}
}

func TestListThemes_OnDiskPlusMalformed(t *testing.T) {
	dir := t.TempDir()
	mustWriteTheme(t, dir, "custom.json", minimalValidJSON)
	mustWriteTheme(t, dir, "broken.json", "{not json")

	res, err := ListThemes(dir)
	if err != nil {
		t.Fatalf("ListThemes: %v", err)
	}
	// custom + embedded default = 2 themes; broken.json surfaces in Errors.
	ids := map[string]bool{}
	for _, ti := range res.Themes {
		ids[ti.ID] = true
	}
	if !ids["test-theme"] || !ids[DefaultThemeID] {
		t.Fatalf("expected test-theme + default, got %v", ids)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].File, "broken.json") {
		t.Fatalf("expected 1 load error for broken.json, got %+v", res.Errors)
	}
}

func TestResolveActive_KnownID(t *testing.T) {
	dir := t.TempDir()
	mustWriteTheme(t, dir, "custom.json", minimalValidJSON)
	t1, err := ResolveActive(dir, "test-theme", "dark")
	if err != nil {
		t.Fatalf("ResolveActive known id: %v", err)
	}
	if t1.ID != "test-theme" {
		t.Errorf("resolved id = %q, want test-theme", t1.ID)
	}
}

func TestResolveActive_UnknownID_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	t1, err := ResolveActive(dir, "no-such-id", "dark")
	if err != nil {
		t.Fatalf("ResolveActive unknown id: %v", err)
	}
	if t1.ID != DefaultThemeID {
		t.Errorf("expected fallback to default, got %q", t1.ID)
	}
}

func TestResolveActive_EmptyID_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	t1, err := ResolveActive(dir, "", "dark")
	if err != nil {
		t.Fatalf("ResolveActive empty id: %v", err)
	}
	if t1.ID != DefaultThemeID {
		t.Errorf("expected default, got %q", t1.ID)
	}
}

func TestHexToRGB(t *testing.T) {
	cases := []struct {
		in           string
		r, g, b      uint8
		ok           bool
	}{
		{"#0c0c0e", 12, 12, 14, true},
		{"#ffffff", 255, 255, 255, true},
		{"#ffffffff", 255, 255, 255, true}, // 8-digit (alpha ignored)
		{"#0c0c0eff", 12, 12, 14, true},    // 8-digit w/ alpha → matches #0c0c0e
		{"#fff", 255, 255, 255, true},
		{"#000", 0, 0, 0, true},
		{" #0c0c0e ", 12, 12, 14, true},
		{"nope", 0, 0, 0, false},
		{"#ff", 0, 0, 0, false},
		{"#gggggg", 0, 0, 0, false},
	}
	for _, c := range cases {
		r, g, b, ok := HexToRGB(c.in)
		if ok != c.ok || r != c.r || g != c.g || b != c.b {
			t.Errorf("HexToRGB(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
				c.in, r, g, b, ok, c.r, c.g, c.b, c.ok)
		}
	}
}

// --- Typography tests (Sprint 6 extension) ---

func TestValidate_TypographyOptional(t *testing.T) {
	// A theme without a typography section must still validate (backward compat).
	th, err := ParseAndValidate([]byte(minimalValidJSON))
	if err != nil {
		t.Fatalf("theme without typography should validate: %v", err)
	}
	if th.Typography != nil {
		t.Errorf("expected nil Typography, got %+v", th.Typography)
	}
}

func TestValidate_TypographyValid(t *testing.T) {
	withTypo := strings.Replace(
		minimalValidJSON,
		`"modes": {`,
		`"typography": {
      "font_family": "'Inter', sans-serif",
      "mono_font_family": "'JetBrains Mono', monospace",
      "headline_font": "'Hanken Grotesk', sans-serif"
    },
    "modes": {`,
		1,
	)
	th, err := ParseAndValidate([]byte(withTypo))
	if err != nil {
		t.Fatalf("valid typography should pass: %v", err)
	}
	if th.Typography == nil {
		t.Fatal("expected non-nil Typography")
	}
	if th.Typography.FontFamily != "'Inter', sans-serif" {
		t.Errorf("FontFamily = %q", th.Typography.FontFamily)
	}
}

func TestValidate_TypographyRejectsCSSInjection(t *testing.T) {
	bad := []string{
		"'Inter'; body { background: red",
		"'Inter'} body{",
		"'Inter'<script>alert(1)</script>",
		"'Inter'>bad",
	}
	for _, v := range bad {
		withBad := strings.Replace(
			minimalValidJSON,
			`"modes": {`,
			`"typography": { "font_family": "`+v+`" },
    "modes": {`,
			1,
		)
		_, err := ParseAndValidate([]byte(withBad))
		if err == nil {
			t.Errorf("expected validation error for font_family %q", v)
		}
	}
}

func TestValidate_TypographyPartial(t *testing.T) {
	// Only headline_font defined — other fields are optional.
	partial := strings.Replace(
		minimalValidJSON,
		`"modes": {`,
		`"typography": { "headline_font": "'Playfair Display', serif" },
    "modes": {`,
		1,
	)
	th, err := ParseAndValidate([]byte(partial))
	if err != nil {
		t.Fatalf("partial typography should pass: %v", err)
	}
	if th.Typography.HeadlineFont != "'Playfair Display', serif" {
		t.Errorf("HeadlineFont = %q", th.Typography.HeadlineFont)
	}
	if th.Typography.FontFamily != "" {
		t.Errorf("FontFamily should be empty, got %q", th.Typography.FontFamily)
	}
}

func TestFlatten_TypographyEmittedWhenPresent(t *testing.T) {
	withTypo := strings.Replace(
		minimalValidJSON,
		`"modes": {`,
		`"typography": {
      "font_family": "'Inter', sans-serif",
      "headline_font": "'Hanken Grotesk', sans-serif"
    },
    "modes": {`,
		1,
	)
	th, err := ParseAndValidate([]byte(withTypo))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dark := th.Flatten("dark")
	if dark["--font-body"] != "'Inter', sans-serif" {
		t.Errorf("--font-body = %q", dark["--font-body"])
	}
	if dark["--font-headline"] != "'Hanken Grotesk', sans-serif" {
		t.Errorf("--font-headline = %q", dark["--font-headline"])
	}
	if _, ok := dark["--font-mono"]; ok {
		t.Errorf("--font-mono should be absent (mono_font_family not set)")
	}
}

func TestFlatten_TypographyAbsentWhenNoSection(t *testing.T) {
	th, err := ParseAndValidate([]byte(minimalValidJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dark := th.Flatten("dark")
	for _, key := range []string{"--font-body", "--font-mono", "--font-headline"} {
		if _, ok := dark[key]; ok {
			t.Errorf("%s should be absent when theme has no typography section", key)
		}
	}
}

func TestIsValidFontFamily(t *testing.T) {
	good := []string{
		"'Inter', sans-serif",
		"'JetBrains Mono', monospace",
		"serif",
		"Georgia, 'Times New Roman', serif",
		"system-ui",
	}
	bad := []string{
		"'Inter'; body{",
		"'Inter'} div{",
		"'><script>",
		// CSS escape-sequence bypass: \3B resolves to ; at CSS-parse time.
		"'Inter'\\3B background:red;/*",
		"'Inter'\\7D body{",
	}
	for _, v := range good {
		if !isValidFontFamily(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	for _, v := range bad {
		if isValidFontFamily(v) {
			t.Errorf("expected %q to be rejected", v)
		}
	}
}
