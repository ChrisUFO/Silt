package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMigrationInvariant_NoOldHueTokens (#50) is a CI-grade guard that
// the old hue-named tokens — color-teal-*, --accent-teal-*,
// --color-teal-*, --accent-indigo-* — do NOT reappear in LIVE SOURCE
// (code, CSS, theme JSON, configs). The #43 migration canonicalized
// them to hue-agnostic semantic accents (--accent-primary-* /
// --accent-secondary-*); this test fails loudly the moment a stale
// reference creeps back into a stylesheet, a theme file, a component,
// or Go code.
//
// Scope: SOURCE files only (.go/.ts/.svelte/.css/.json/.js/.html/.yaml).
// Markdown (.md) is intentionally EXCLUDED — documentation that
// describes the migration (this very comment, TESTING.md, ARCHITECTURE
// notes) legitimately names the old tokens in prose, and markdown is
// never rendered as CSS so a literal there has no runtime effect. The
// risk surface for stale tokens is code/CSS/themes, which IS scanned.
//
// It runs under the existing `go test -race -count=1 ./...` CI step and
// the local pre-push hook, so no separate grep/lint step is required.
// It enumerates files via `git ls-files` (tracked only — build artifacts
// like node_modules / frontend/dist are never listed) and scans the
// text-ish extensions. Banned substrings are concatenated at runtime so
// this test file's own source is never a false positive.
func TestMigrationInvariant_NoOldHueTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("migration invariant walks the whole tree; skip under -short")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not found in PATH, skipping migration invariant check")
	}
	repoRoot := findRepoRoot(t)

	// Banned substrings, built by concatenation so the literal does not
	// appear in this file (otherwise the test would match its own
	// source). "color-teal" covers both `color-teal-*` and
	// `--color-teal-*`; the two accent forms are listed explicitly.
	banned := []string{
		"color" + "-teal",
		"--accent" + "-teal",
		"--accent" + "-indigo",
	}

	files, err := trackedTextFiles(repoRoot)
	if err != nil {
		t.Fatalf("enumerate tracked files: %v", err)
	}

	// allowlist is the explicit set of source files permitted to name
	// the banned tokens in their own definition — currently just this
	// test, which constructs the banned substrings to scan for. It is
	// the ONLY escape hatch: if a future source file legitimately
	// references the migration (e.g. a dedicated migration test), add
	// its tracked basename here. (Documentation prose is excluded
	// wholesale by the extension filter in trackedTextFiles.) Keyed by
	// basename and matched exactly so the intent is obvious; if a file
	// is renamed without updating this set, the test fails loudly on
	// its own tokens rather than silently skipping.
	allowlist := map[string]bool{
		"theme_migration_invariant_test.go": true,
	}
	skip := func(path string) bool { return allowlist[filepath.Base(path)] }

	var hits []string
	for _, rel := range files {
		if skip(rel) {
			continue
		}
		full := filepath.Join(repoRoot, rel)
		data, err := os.ReadFile(full)
		if err != nil {
			// A vanished tracked file (mid-rebase, etc.) is not an
			// invariant failure; report and continue.
			t.Logf("skip unreadable %s: %v", rel, err)
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, b := range banned {
				if strings.Contains(line, b) {
					hits = append(hits, formatHit(rel, i+1, b, line))
				}
			}
		}
	}

	if len(hits) > 0 {
		t.Fatalf("migration invariant violated: old hue-named tokens found "+
			"(%d occurrence(s)). The #43 migration canonicalized these to "+
			"semantic accents (--accent-primary-* / --accent-secondary-*); "+
			"replace any remaining reference:\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}

func formatHit(file string, line int, needle, src string) string {
	trim := strings.TrimSpace(src)
	if len(trim) > 120 {
		trim = trim[:120] + "…"
	}
	return "  • " + file + ":" + strconv.Itoa(line) + " contains \"" + needle + "\": " + trim
}

// findRepoRoot locates the directory containing go.mod (the module root
// = repo root for this project).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	// Start from the test file's directory (the package dir) and walk up.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repo root (no go.mod found walking up from CWD)")
	return ""
}

// trackedTextFiles returns repo-root-relative paths of tracked files
// whose extension looks like text, via `git ls-files`. Build artifacts
// (node_modules, frontend/dist, frontend/wailsjs) are gitignored and
// therefore never listed by ls-files, so no manual exclusion is needed.
func trackedTextFiles(repoRoot string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Source extensions only — see the doc comment for why .md is
	// excluded (documentation prose legitimately names the migration).
	textExt := map[string]bool{
		".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".svelte": true, ".vue": true, ".css": true, ".scss": true,
		".json": true, ".jsonc": true, ".html": true,
		".yaml": true, ".yml": true, ".toml": true, ".svg": true,
	}
	var files []string
	for _, line := range strings.Split(string(out), "\x00") {
		if line == "" {
			continue
		}
		if textExt[filepath.Ext(line)] {
			files = append(files, line)
		}
	}
	return files, nil
}


