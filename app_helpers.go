package main

import (
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"time"
)

var updateLineIDRegex = regexp.MustCompile(`<!-- id: ([a-f0-9\-]{36}) -->`)

// fileOrDefaultDate returns the file's modification date (YYYY-MM-DD), falling
// back to today if the stat fails. Used consistently by SaveFileBlocks,
// MutateBlock, and UpdateBlockState as the defaultDate passed to
// ParseFileContent — ensures old blocks without a @ date suffix inherit the
// file's actual mtime rather than silently shifting to today.
func fileOrDefaultDate(filePath string) string {
	if fi, err := os.Stat(filePath); err == nil {
		return fi.ModTime().Format("2006-01-02")
	}
	return time.Now().Format("2006-01-02")
}

// findLineByBlockID returns the 0-based index of the line in `lines` whose
// trailing `<!-- id: UUID -->` comment matches blockID, or -1 if no such line
// exists.
func findLineByBlockID(lines []string, blockID string) int {
	for i, line := range lines {
		matches := updateLineIDRegex.FindStringSubmatch(line)
		if len(matches) >= 2 && matches[1] == blockID {
			return i
		}
	}
	return -1
}

// sanitizePathSegment strips path-traversal indicators from a single path
// component: directory separators, NUL, control chars, and a LEADING `..`
// (or run of leading `..`s) which is the path-traversal signal. Internal `..`
// substrings (e.g. `2.0..2.1`, `a..b..c`) are preserved verbatim — they are
// legitimate filename characters, not traversal (#89). The contract is
// "single segment": `/` and `\` are stripped so the join can never produce
// a multi-segment path.
func sanitizePathSegment(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return -1
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	for strings.HasPrefix(cleaned, "..") {
		cleaned = strings.TrimSpace(strings.TrimPrefix(cleaned, ".."))
	}
	if cleaned == "." {
		cleaned = ""
	}
	return cleaned
}

// sanitizeSectionPath sanitizes a multi-segment section path (e.g.
// "Projects/Active"). Each segment is sanitized independently via
// sanitizePathSegment, preserving the `/` separator so deeply-nested
// section paths survive the sanitize pass (#88, #97). An empty input
// (or all-empty segments) returns "".
func sanitizeSectionPath(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if c := sanitizePathSegment(p); c != "" {
			out = append(out, c)
		}
	}
	return strings.Join(out, "/")
}

// splitFrontmatter separates a leading YAML frontmatter block (--- ... ---)
// from the body. It returns the frontmatter exactly as it appears in content
// (including the trailing newline after the closing ---), and the body with
// the frontmatter stripped. If content has no frontmatter, frontmatter is ""
// and body is the full content. Callers pair this with parser.RenderFileContent
// so every writer extracts frontmatter the same way.
func splitFrontmatter(content string) (frontmatter, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			fm := strings.Join(lines[:i+1], "\n") + "\n"
			body := strings.Join(lines[i+1:], "\n")
			return fm, body
		}
	}
	// Opening --- with no closing ---: treat the whole thing as body so we
	// don't silently drop user content.
	return "", content
}

// isPathWithinRoot reports whether target is the same as or a descendant of
// root. Generalized from the vault-only check for #100: callers pass the
// resolved notebook root (vault root, an in-vault notebook dir, or a linked
// notebook root) so the same traversal guard covers external notebooks.
//
// Both paths are cleaned, made absolute, and resolved through EvalSymlinks
// (mirroring backend/plugins/installer.go:isWithin) so a symlink planted
// inside a notebook that points outside it cannot mask an escape. The
// comparison is case-insensitive on Windows where the filesystem itself is
// case-insensitive. EvalSymlinks errors (e.g. non-existent target during
// construction) fall back to the lexical form.
func isPathWithinRoot(target, root string) bool {
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	absTarget = filepath.Clean(absTarget)
	absRoot = filepath.Clean(absRoot)
	if absTarget == absRoot {
		return true
	}
	prefix := absRoot + string(os.PathSeparator)
	if goruntime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(absTarget), strings.ToLower(prefix))
	}
	return strings.HasPrefix(absTarget, prefix)
}
