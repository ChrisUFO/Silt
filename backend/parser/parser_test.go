package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureBlockID(t *testing.T) {
	// Line without ID
	line1 := "- [ ] TODO TASK Draft README definition file"
	id1, newLine1, modified1 := EnsureBlockID(line1)
	if !modified1 {
		t.Errorf("Expected line to be modified")
	}
	if id1 == "" {
		t.Errorf("Expected an ID to be generated")
	}
	if !strings.Contains(newLine1, "<!-- id: "+id1+" -->") {
		t.Errorf("Expected new line to contain ID comment")
	}

	// Line with ID
	line2 := "- [ ] TODO TASK Draft README <!-- id: 8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->"
	id2, newLine2, modified2 := EnsureBlockID(line2)
	if modified2 {
		t.Errorf("Expected line not to be modified")
	}
	if id2 != "8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a" {
		t.Errorf("Expected matched ID, got: %s", id2)
	}
	if newLine2 != line2 {
		t.Errorf("Expected output line to equal input line")
	}
}

func TestNormalizeDate(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-06-13", "2026-06-13"},
		{"6/13/26", "2026-06-13"},
		{"06/13/2026", "2026-06-13"},
		{" 6/13/2026 ", "2026-06-13"},
		{"", ""},
	}

	for _, tc := range tests {
		actual := normalizeDate(tc.input)
		if actual != tc.expected {
			t.Errorf("For %q expected %q, got %q", tc.input, tc.expected, actual)
		}
	}
}

func TestParseLine(t *testing.T) {
	// Test task line
	taskLine := "- [ ] TODO TASK [Chris](2026-06-13, 2026-08-03)#1 Draft README <!-- id: 8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->"
	block, _, _ := ParseLine(taskLine, 1, 4)

	if block.Type != BlockTask {
		t.Errorf("Expected BlockTask, got %s", block.Type)
	}
	if block.Owner != "Chris" {
		t.Errorf("Expected owner Chris, got %s", block.Owner)
	}
	if block.StartDate != "2026-06-13" || block.DueDate != "2026-08-03" {
		t.Errorf("Expected start 2026-06-13 and due 2026-08-03, got start: %s, due: %s", block.StartDate, block.DueDate)
	}
	if block.Priority != 1 {
		t.Errorf("Expected priority 1, got %d", block.Priority)
	}
	if block.CleanText != "Draft README" {
		t.Errorf("Expected clean text 'Draft README', got '%s'", block.CleanText)
	}

	// Test header line
	headerLine := "## General Info <!-- id: 2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->"
	block2, _, _ := ParseLine(headerLine, 2, 4)
	if block2.Type != BlockHeader {
		t.Errorf("Expected BlockHeader, got %s", block2.Type)
	}
	if block2.Depth != 2 {
		t.Errorf("Expected header depth 2, got %d", block2.Depth)
	}
	if block2.CleanText != "General Info" {
		t.Errorf("Expected clean text 'General Info', got '%s'", block2.CleanText)
	}

	// Test note line
	noteLine := "    - An bullet list note <!-- id: 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8b -->"
	block3, _, _ := ParseLine(noteLine, 3, 4)
	if block3.Type != BlockNote {
		t.Errorf("Expected BlockNote, got %s", block3.Type)
	}
	if block3.Depth != 1 {
		t.Errorf("Expected depth 1, got %d", block3.Depth)
	}
	if block3.CleanText != "An bullet list note" {
		t.Errorf("Expected clean text 'An bullet list note', got '%s'", block3.CleanText)
	}
}

func TestParseFileContent(t *testing.T) {
	doc := `---
notebook: Engineering
section: Architecture
page: DailyLog
date: 2026-06-13
tags: [work/sogav, systems/specs]
---
# Saturday, June 13, 2026 <!-- id: 0a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->

## Stream Logging <!-- id: 1a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8b -->
- [ ] TODO TASK [Chris](2026-06-13)#1 Draft README <!-- id: 2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c -->
    - [/] DOING TASK [Jenny]#2 Research subtasks <!-- id: 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8d -->
- A general note <!-- id: 4a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8e -->`

	blocks, meta, newContent, modified, err := ParseFileContent(doc, "DefaultNB", "DefaultSec", "DefaultPage", "2026-06-01", 4)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if meta.Notebook != "Engineering" || meta.Section != "Architecture" || meta.Page != "DailyLog" || meta.Date != "2026-06-13" {
		t.Errorf("Metadata mismatch: %+v", meta)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "work/sogav" {
		t.Errorf("Tags mismatch: %+v", meta.Tags)
	}

	if modified {
		t.Errorf("Expected no modification since all blocks have IDs")
	}
	if len(blocks) != 5 {
		t.Errorf("Expected 5 blocks, got %d", len(blocks))
	}

	// Verify parent-child
	// Check header-id-1 (depth 1)
	if blocks[0].ID != "0a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a" || blocks[0].Type != BlockHeader {
		t.Errorf("Expected block 0 to be 0a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a")
	}

	// Check task-id-1 (depth 0)
	if blocks[2].ID != "2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c" || blocks[2].ParentID != "" {
		t.Errorf("Expected block 2 2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c to have no parent, got: %s", blocks[2].ParentID)
	}

	// Check task-id-2 (depth 1, child of task-id-1)
	if blocks[3].ID != "3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8d" {
		t.Fatalf("Expected block 3 to be 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8d")
	}
	if blocks[3].ParentID != "2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c" {
		t.Errorf("Expected parent to be 2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c, got: %s", blocks[3].ParentID)
	}

	// Verify that the rewritten content remains identical since no modifications were needed
	if newContent != doc {
		t.Errorf("Content mismatch. Expected:\n%s\nGot:\n%s", doc, newContent)
	}
}

func TestParseFileContent_SkipsCodeBlockIDInjection(t *testing.T) {
	// Lines inside fenced code blocks must NOT receive block ID comments;
	// doing so would corrupt code samples in the user's notes.
	doc := "# Example <!-- id: 4a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->\n" +
		"\n" +
		"```go\n" +
		"func hello() string { return \"hi\" }\n" +
		"// A code comment that must not be touched\n" +
		"```\n" +
		"\n" +
		"- A normal note line <!-- id: 5a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8b -->\n"

	_, _, newContent, modified, err := ParseFileContent(doc, "NB", "Sec", "P", "2026-06-13", 4)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if modified {
		t.Errorf("Did not expect modifications since both blocks already have IDs")
	}
	if strings.Contains(newContent, "func hello() string { return \"hi\" } <!-- id:") {
		t.Errorf("Code block line was corrupted with an ID comment:\n%s", newContent)
	}
	if !strings.Contains(newContent, "// A code comment that must not be touched") {
		t.Errorf("Comment line was modified:\n%s", newContent)
	}

	// And the full content should be unchanged.
	if newContent != doc {
		t.Errorf("Content changed unexpectedly. Got:\n%s", newContent)
	}
}

func TestParseFileContent_HandlesMultipleFencedCodeBlocks(t *testing.T) {
	// Verify that nesting-style toggles (back-to-back fenced blocks) don't
	// accidentally leave us in a stuck "in code block" state.
	doc := "open <!-- id: aaaa1111-aaaa-aaaa-aaaa-aaaaaaaaaaaa -->\n" +
		"```\n" +
		"x <!-- would be corrupted without fix -->\n" +
		"```\n" +
		"middle <!-- id: bbbb2222-bbbb-bbbb-bbbb-bbbbbbbbbbbb -->\n" +
		"```python\n" +
		"y = 2 <!-- would be corrupted without fix -->\n" +
		"```\n" +
		"end <!-- id: cccc3333-cccc-cccc-cccc-cccccccccccc -->\n"

	_, _, newContent, _, err := ParseFileContent(doc, "NB", "Sec", "P", "2026-06-13", 4)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if strings.Contains(newContent, "x <!-- would be corrupted without fix --> <!-- id:") {
		t.Errorf("First code block content was corrupted")
	}
	if strings.Contains(newContent, "y = 2 <!-- would be corrupted without fix --> <!-- id:") {
		t.Errorf("Second code block content was corrupted")
	}
	if !strings.Contains(newContent, "end <!-- id: cccc3333-cccc-cccc-cccc-cccccccccccc -->") {
		t.Errorf("Post-code-block content lost its ID")
	}
}

func TestParseFileContent_SurfacesYAMLErrors(t *testing.T) {
	// Malformed frontmatter must produce a Warning on FileMetadata so the
	// caller can tell the user their YAML is broken. Falling through
	// silently to path-derived defaults would lose user-authored metadata.
	doc := `---
notebook: Engineering:
  - broken yaml
  - indent level inconsistent
---
# Header <!-- id: 11111111-1111-1111-1111-111111111111 -->`

	_, meta, _, _, err := ParseFileContent(doc, "DefaultNB", "DefaultSec", "DefaultPage", "2026-06-01", 4)
	if err != nil {
		t.Fatalf("ParseFileContent: %v", err)
	}
	if len(meta.Warnings) != 1 {
		t.Errorf("expected 1 warning for broken YAML frontmatter, got %d: %v", len(meta.Warnings), meta.Warnings)
	}
	if meta.Warnings[0] == "" {
		t.Errorf("warning should carry the parse error detail")
	}
	// Also confirm that the defaults are used (the buggy YAML didn't
	// silently promote a partial parse).
	if meta.Notebook != "DefaultNB" {
		t.Errorf("expected default notebook, got %q", meta.Notebook)
	}
}

func TestFormatBlockToLine_DefaultsBulletForNewBlockNote(t *testing.T) {
	// Newly created editor blocks arrive with empty RawText. The serializer
	// must emit a "- " bullet so the outliner round-trips correctly.
	block := ParsedBlock{
		ID:        "new-block-id",
		Type:      BlockNote,
		RawText:   "",
		CleanText: "fresh content",
	}
	line := FormatBlockToLine(block, 4)
	if !strings.HasPrefix(strings.TrimSpace(line), "- ") {
		t.Errorf("expected '- ' bullet for empty-RawText BlockNote, got: %s", line)
	}
	if !strings.Contains(line, "fresh content") {
		t.Errorf("expected clean text in output, got: %s", line)
	}

	// An existing plain-text note (no bullet marker in RawText) must
	// serialize without a bullet to preserve the original style.
	block.RawText = "just plain text <!-- id: new-block-id -->"
	line = FormatBlockToLine(block, 4)
	if strings.HasPrefix(strings.TrimSpace(line), "- ") {
		t.Errorf("expected no bullet for plain-text note, got: %s", line)
	}

	// An existing bullet note must preserve its specific marker.
	block.RawText = "* starred note <!-- id: new-block-id -->"
	line = FormatBlockToLine(block, 4)
	if !strings.HasPrefix(strings.TrimSpace(line), "* ") {
		t.Errorf("expected '* ' bullet to be preserved, got: %s", line)
	}
}

func BenchmarkScanWorkspace_1000Files(b *testing.B) {
	for range b.N {
		dir := b.TempDir()
		writeBenchVault(b, dir, 1000)
		_, err := ScanWorkspace(dir, 4)
		if err != nil {
			b.Fatalf("ScanWorkspace: %v", err)
		}
	}
}

// Helper shared by the benchmark — writes N small daily-note files under
// Work/Journal/Daily/ (notebook/section/page) so the scanner has realistic
// 3-level structure to walk.
func writeBenchVault(tb interface{ Fatal(args ...any) }, root string, n int) {
	for i := range n {
		dateStr := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
		dir := filepath.Join(root, "Work", "Journal", "Daily")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		day := fmt.Sprintf("---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: %s\ntags: [bench]\n---\n# Day %d <!-- id: %08x-1111-1111-1111-111111111111 -->\n\n- [ ] TODO TASK [Bench]#1 Item for day %d <!-- id: %08x-2222-2222-2222-222222222222 -->\n- A note for day %d <!-- id: %08x-3333-3333-3333-333333333333 -->\n", dateStr, i+1, i, i+1, i, i+1, i)
		path := filepath.Join(dir, dateStr+".md")
		if err := os.WriteFile(path, []byte(day), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}
