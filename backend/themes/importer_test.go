package themes

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validCustomThemeJSON mirrors the constant in app_themes_test.go: a
// structurally-valid canonical theme with id "terra-test" used as the
// base for import tests. Defining a local copy here keeps the
// themes-package tests self-contained.
const validCustomThemeJSON = `{
  "schema_version": "1.0.0",
  "id": "terra-test",
  "name": "Terra Test",
  "author": "Tester",
  "description": "a second theme",
  "modes": {
    "dark": {
      "bg": {"void":"#1a0f0a","surface":"#2a1a12","panel":"#33221a","hover":"#3d2a20","active":"#4a3328"},
      "border": {"muted":"#2a1a12","zinc":"#3d2a20","active":"#5a3d30","focus":"#7a5238"},
      "text": {"primary":"#f0e6dc","muted":"#a08878","disabled":"#5a4a40"},
      "accent": {
        "primary": {"start":"#c2410c","end":"#7c2d12","glow":"rgba(194,65,12,0.15)"},
        "secondary": {"start":"#4d7c0f","end":"#365314","glow":"rgba(77,124,15,0.12)"}
      },
      "status": {"warn":"#fbbf24","danger":"#f43f5e"}
    },
    "light": {
      "bg": {"void":"#faf6f2","surface":"#ffffff","panel":"#f1ebe4","hover":"#e5dccf","active":"#d6c7b4"},
      "border": {"muted":"#e5dccf","zinc":"#d6c7b4","active":"#a8907a","focus":"#7a6452"},
      "text": {"primary":"#2a1a12","muted":"#7a6452","disabled":"#a8907a"},
      "accent": {
        "primary": {"start":"#9a3412","end":"#7c2d12","glow":"rgba(154,52,18,0.10)"},
        "secondary": {"start":"#3f6212","end":"#365314","glow":"rgba(63,98,18,0.08)"}
      },
      "status": {"warn":"#b45309","danger":"#be123c"}
    }
  }
}`

func TestImportThemeFromPath_HappyPath(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)

	res, err := ImportThemeFromPath(themesDir, src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Info.ID != "terra-test" {
		t.Errorf("expected id terra-test, got %q", res.Info.ID)
	}
	if res.Renamed {
		t.Errorf("unexpected rename: %+v", res)
	}
	// File was written under <themesDir>/<id>.json
	dst := filepath.Join(themesDir, "terra-test.json")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected import to write %s: %v", dst, err)
	}
	// And the on-disk content is re-parseable
	parsed, err := LoadTheme(dst)
	if err != nil {
		t.Errorf("re-parse imported file: %v", err)
	}
	if parsed.ID != "terra-test" || parsed.Name != "Terra Test" {
		t.Errorf("round-trip content drift: id=%q name=%q", parsed.ID, parsed.Name)
	}
}

func TestImportThemeFromPath_NoSourcePath(t *testing.T) {
	if _, err := ImportThemeFromPath(t.TempDir(), ""); err == nil {
		t.Fatal("expected error for empty source path")
	}
}

func TestImportThemeFromPath_NoThemesDir(t *testing.T) {
	if _, err := ImportThemeFromPath("", "/some/file.json"); err == nil {
		t.Fatal("expected error for empty themes dir")
	}
}

func TestImportThemeFromPath_MissingSource(t *testing.T) {
	if _, err := ImportThemeFromPath(t.TempDir(), "/no/such/file.json"); err == nil {
		t.Fatal("expected error for missing source file")
	}
}

func TestImportThemeFromPath_UnparseableJSON(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), `{"not":"valid`)

	_, err := ImportThemeFromPath(t.TempDir(), src)
	if err == nil {
		t.Fatal("expected parse error")
	}
	if !strings.Contains(err.Error(), "not parseable") {
		t.Errorf("expected 'not parseable' in error, got: %v", err)
	}
}

func TestImportThemeFromPath_SchemaError(t *testing.T) {
	// Valid JSON, missing an inner token (replace accent primary start with "")
	bad := strings.Replace(validCustomThemeJSON, `"#c2410c"`, `""`, 1)
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), bad)

	themesDir := t.TempDir()
	res, err := ImportThemeFromPath(themesDir, src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.ValidationErrors) == 0 {
		t.Fatal("expected validation errors in result")
	}
	found := false
	for _, e := range res.ValidationErrors {
		if strings.Contains(e.Field, "accent.primary.start") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on accent.primary.start, got: %+v", res.ValidationErrors)
	}
	// Critical: the import must NOT have written a file.
	entries, _ := os.ReadDir(themesDir)
	if len(entries) != 0 {
		t.Errorf("expected no file written on validation failure, got: %+v", entries)
	}
}

func TestImportThemeFromPath_SandboxRejectsNonColor(t *testing.T) {
	// A theme that smuggles a url() or expression() at a color slot. The
	// canonical validator's isValidColor must reject every form.
	cases := []string{
		`url(http://evil.example/x)`,
		`expression(alert(1))`,
		`<script>alert(1)</script>`,
		`red`,         // named color — explicitly not accepted by isValidColor
		`hsl(0,0%,0%)`, // also not accepted
		`not-a-color`,
	}
	for _, bad := range cases {
		mutated := strings.Replace(validCustomThemeJSON, `"#c2410c"`, `"`+bad+`"`, 1)
		src := filepath.Join(t.TempDir(), "src.json")
		mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), mutated)
		res, err := ImportThemeFromPath(t.TempDir(), src)
		if err != nil {
			t.Errorf("unexpected error for %q: %v", bad, err)
		}
		if len(res.ValidationErrors) == 0 {
			t.Errorf("expected validator to reject %q as a color value", bad)
		}
	}
}

func TestImportThemeFromPath_NamespacesBuiltInID(t *testing.T) {
	themesDir := t.TempDir()
	// An import whose id collides with the bundled default. The on-disk
	// default file is also present (themesDir just scaffolded by the test,
	// but DefaultThemeID is what we collide with — namespace step kicks in
	// before the on-disk check).
	mustWriteTheme(t, themesDir, DefaultThemeID+".json", validCustomThemeJSON)

	src := filepath.Join(t.TempDir(), "src.json")
	clone := strings.Replace(validCustomThemeJSON, `"terra-test"`, `"`+DefaultThemeID+`"`, 1)
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), clone)

	res, err := ImportThemeFromPath(themesDir, src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := userPrefix + DefaultThemeID
	if res.Info.ID != want {
		t.Errorf("expected renamed id %q, got %q", want, res.Info.ID)
	}
	if !res.Renamed {
		t.Errorf("expected Renamed=true")
	}
	if res.RenamedFromID != DefaultThemeID {
		t.Errorf("RenamedFromID = %q, want %q", res.RenamedFromID, DefaultThemeID)
	}
	// The built-in file on disk is untouched.
	canonPath := filepath.Join(themesDir, DefaultThemeID+".json")
	orig, _ := os.ReadFile(canonPath)
	clone2, _ := os.ReadFile(canonPath)
	if string(orig) != string(clone2) {
		t.Errorf("built-in file mutated on import")
	}
	// The namespaced file lives next to it.
	nsPath := filepath.Join(themesDir, want+".json")
	if _, err := os.Stat(nsPath); err != nil {
		t.Errorf("expected namespaced file %s: %v", nsPath, err)
	}
}

func TestImportThemeFromPath_RejectsDuplicateImportID(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)

	// First import lands terra-test on disk.
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Second import of the same id (no rename) must be refused so the
	// user doesn't silently overwrite a different theme with the same id.
	_, err := ImportThemeFromPath(themesDir, src)
	if err == nil {
		t.Fatal("expected duplicate-import error")
	}
	if !errors.Is(err, ErrImportDuplicate) {
		t.Errorf("expected ErrImportDuplicate, got: %v", err)
	}
}

func TestImportThemeFromPath_SanitizesID(t *testing.T) {
	themesDir := t.TempDir()
	// An id with mixed case, spaces, and other invalid chars must be
	// sanitized to [a-z0-9_-] and the result is what's used for the
	// filename and the on-disk id.
	weirdID := "My Theme v2 (final)!"
	src := filepath.Join(t.TempDir(), "src.json")
	clone := strings.Replace(validCustomThemeJSON, `"terra-test"`, `"`+weirdID+`"`, 1)
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), clone)

	res, err := ImportThemeFromPath(themesDir, src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := "my-theme-v2-final"
	if res.Info.ID != want {
		t.Errorf("expected sanitized id %q, got %q", want, res.Info.ID)
	}
	if !res.Renamed {
		t.Errorf("expected Renamed=true for sanitized id")
	}
	if res.RenamedFromID != weirdID {
		t.Errorf("RenamedFromID = %q, want %q", res.RenamedFromID, weirdID)
	}
	if _, err := os.Stat(filepath.Join(themesDir, want+".json")); err != nil {
		t.Errorf("expected sanitized filename: %v", err)
	}
}

func TestImportThemeFromPath_RejectsAllInvalidID(t *testing.T) {
	// A theme whose id consists entirely of invalid characters (e.g.
	// punctuation) sanitizes to "". Before the fix this slipped past
	// Validate (non-empty id) and produced a ".json" file with an empty
	// id. Now the importer must reject it explicitly.
	themesDir := t.TempDir()
	badID := "!@#$"
	clone := strings.Replace(validCustomThemeJSON, `"terra-test"`, `"`+badID+`"`, 1)
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), clone)

	_, err := ImportThemeFromPath(themesDir, src)
	if err == nil {
		t.Fatal("expected error for all-invalid theme ID")
	}
	if !strings.Contains(err.Error(), "invalid after sanitization") {
		t.Errorf("expected sanitization error, got: %v", err)
	}
	// No file written.
	entries, _ := os.ReadDir(themesDir)
	if len(entries) != 0 {
		t.Errorf("expected no file written, got: %+v", entries)
	}
}

func TestImportThemeFromPath_AtomicWrite_NoTempLeftovers(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)

	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	entries, _ := os.ReadDir(themesDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("atomic write left temp file behind: %s", e.Name())
		}
	}
}

func TestExportThemeToPath_RoundTrip(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "out.json")
	if err := ExportThemeToPath(themesDir, "terra-test", dst); err != nil {
		t.Fatalf("export: %v", err)
	}
	// Re-parse the exported file with the canonical validator.
	if _, err := ParseAndValidate(mustReadFile(t, dst)); err != nil {
		t.Errorf("exported file fails canonical validation: %v", err)
	}
}

func TestExportThemeToPath_EmbeddedDefault(t *testing.T) {
	// Exporting the embedded default works even with an empty themes dir.
	dst := filepath.Join(t.TempDir(), "default.json")
	if err := ExportThemeToPath(t.TempDir(), DefaultThemeID, dst); err != nil {
		t.Fatalf("export default: %v", err)
	}
	if _, err := ParseAndValidate(mustReadFile(t, dst)); err != nil {
		t.Errorf("exported default fails canonical validation: %v", err)
	}
}

func TestExportThemeToPath_MissingCustomIDErrors(t *testing.T) {
	// A custom theme id that's not on disk must error — the user
	// expects to export their actual theme, not a surprise fallback
	// to the embedded default. (Old behavior was to silently write
	// the default; changed because it was misleading.)
	dst := filepath.Join(t.TempDir(), "out.json")
	err := ExportThemeToPath(t.TempDir(), "no-such-theme", dst)
	if err == nil {
		t.Fatal("expected error for missing custom theme id")
	}
}

func TestExportThemeToPath_EmptyDest(t *testing.T) {
	if err := ExportThemeToPath(t.TempDir(), DefaultThemeID, ""); err == nil {
		t.Fatal("expected error for empty dest path")
	}
}

func TestExportThemeToPath_NoThemesDir(t *testing.T) {
	if err := ExportThemeToPath("", DefaultThemeID, filepath.Join(t.TempDir(), "out.json")); err == nil {
		t.Fatal("expected error for empty themes dir")
	}
}

// TestExportThemeToPath_MissingCustomID: exporting a custom theme whose
// file is absent must error, not silently fall back to the embedded default.
func TestExportThemeToPath_MissingCustomID(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.json")
	if err := ExportThemeToPath(t.TempDir(), "ghost-theme", dst); err == nil {
		t.Fatal("expected error for missing custom theme id")
	}
}

// TestExportThemeToPath_DefaultIDStillWorks: exporting the embedded
// default (DefaultThemeID) always works even when the on-disk copy is absent.
func TestExportThemeToPath_DefaultIDStillWorks(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.json")
	if err := ExportThemeToPath(t.TempDir(), DefaultThemeID, dst); err != nil {
		t.Fatalf("export default should work: %v", err)
	}
	if _, err := ParseAndValidate(mustReadFile(t, dst)); err != nil {
		t.Errorf("exported default fails validation: %v", err)
	}
}

func TestSanitizeThemeID(t *testing.T) {
	// Underscores are preserved (DefaultThemeID = "cyber_forest" relies
	// on them). All other invalid chars collapse to single hyphens, and
	// double-hyphens are collapsed.
	cases := map[string]string{
		"My Theme v2 (final)!":  "my-theme-v2-final",
		"hello---world":          "hello-world",
		"-leading-and-trailing-": "leading-and-trailing",
		"already-clean":          "already-clean",
		"already_clean":          "already_clean",
		"":                       "",
		"   ":                    "",
		"123!@#abc":              "123-abc",
		"FOO_BAR":                "foo_bar",
	}
	for in, want := range cases {
		if got := sanitizeThemeID(in); got != want {
			t.Errorf("sanitizeThemeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadByID_Found(t *testing.T) {
	themesDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.json")
	mustWriteTheme(t, filepath.Dir(src), filepath.Base(src), validCustomThemeJSON)
	if _, err := ImportThemeFromPath(themesDir, src); err != nil {
		t.Fatalf("import: %v", err)
	}
	t1, found, err := LoadByID(themesDir, "terra-test")
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if t1 == nil || t1.ID != "terra-test" {
		t.Errorf("got %+v", t1)
	}
}

func TestLoadByID_NotFound(t *testing.T) {
	t1, found, err := LoadByID(t.TempDir(), "nope")
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	if found {
		t.Errorf("expected found=false, got theme %+v", t1)
	}
}

func TestLoadByID_EmptyThemesDir(t *testing.T) {
	t1, found, err := LoadByID("", "anything")
	if err != nil {
		t.Fatalf("LoadByID: %v", err)
	}
	if found || t1 != nil {
		t.Errorf("expected empty result, got %+v found=%v", t1, found)
	}
}

// TestLoadByID_PropagatesIOError: a permission-denied directory must
// surface as an error, not be swallowed as "not found".
func TestLoadByID_PropagatesIOError(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory, remove its read permission.
	subdir := filepath.Join(dir, "noperm")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a dummy file inside, then strip read permission from the dir.
	if err := os.WriteFile(filepath.Join(subdir, "x.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(subdir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o755) })

	// On some platforms (root user, or certain CI runners) the chmod is
	// not effective. Skip rather than fail in those environments.
	if _, err := os.ReadDir(subdir); err == nil {
		t.Skip("os.Chmod 0o000 not enforced in this environment")
	}

	_, _, err := LoadByID(subdir, "anything")
	if err == nil {
		t.Fatal("expected LoadByID to propagate I/O error, got nil")
	}
}

func TestExistingOnDiskIDs(t *testing.T) {
	dir := t.TempDir()
	mustWriteTheme(t, dir, "a.json", validCustomThemeJSON)
	mustWriteTheme(t, dir, "b.json", validCustomThemeJSON)
	// A non-JSON file is skipped.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ids, err := ExistingOnDiskIDs(dir)
	if err != nil {
		t.Fatalf("ExistingOnDiskIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("got %+v", ids)
	}
}

func TestExistingOnDiskIDs_NoDir(t *testing.T) {
	ids, err := ExistingOnDiskIDs(t.TempDir() + "/nope")
	if err != nil {
		t.Fatalf("ExpectedOnDiskIDs missing-dir: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected no ids, got %+v", ids)
	}
}

func TestNamespaceThemeID_BuiltIn(t *testing.T) {
	got, err := namespaceThemeID(t.TempDir(), DefaultThemeID, DefaultThemeID)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if got != userPrefix+DefaultThemeID {
		t.Errorf("got %q, want %q", got, userPrefix+DefaultThemeID)
	}
}

func TestNamespaceThemeID_DuplicateBuiltInSuffixed(t *testing.T) {
	dir := t.TempDir()
	// Pre-create user-cyber_forest.json so the next namespace step
	// needs to fall back to a counter.
	if err := os.WriteFile(filepath.Join(dir, userPrefix+DefaultThemeID+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := namespaceThemeID(dir, userPrefix+DefaultThemeID, DefaultThemeID)
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	if got != userPrefix+DefaultThemeID+"-2" {
		t.Errorf("got %q, want %q", got, userPrefix+DefaultThemeID+"-2")
	}
}

func TestNamespaceThemeID_DuplicateUserImportRejected(t *testing.T) {
	dir := t.TempDir()
	// Pre-create a user-... file; an import whose *original* id (not
	// sanitized/renamed) is a different theme with the same user- id
	// must be rejected so the user can rename in the source.
	originalID := "other-theme"
	if err := os.WriteFile(filepath.Join(dir, originalID+".json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := namespaceThemeID(dir, originalID, originalID)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if !errors.Is(err, ErrImportDuplicate) {
		t.Errorf("expected ErrImportDuplicate, got: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
