package parser

import "strings"

// SplitFrontmatter separates a leading YAML frontmatter block (--- ... ---)
// from the body. It returns the frontmatter exactly as it appears in content
// (including the trailing newline after the closing ---), and the body with
// the frontmatter stripped. If content has no frontmatter, frontmatter is ""
// and body is the full content. Callers pair this with RenderFileContent so
// every writer extracts frontmatter the same way.
func SplitFrontmatter(content string) (frontmatter, body string) {
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
