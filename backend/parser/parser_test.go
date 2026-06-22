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
	line1 := "- [ ] Draft README definition file"
	id1, _, newLine1, modified1 := EnsureBlockID(line1)
	if !modified1 {
		t.Errorf("Expected line to be modified")
	}
	if id1 == "" {
		t.Errorf("Expected an ID to be generated")
	}
	if !strings.Contains(newLine1, "<!-- id: "+id1) {
		t.Errorf("Expected new line to contain ID comment")
	}

	// Line with ID (old format, no date)
	line2 := "- [ ] Draft README <!-- id: 8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->"
	id2, fileDate2, newLine2, modified2 := EnsureBlockID(line2)
	if modified2 {
		t.Errorf("Expected line not to be modified")
	}
	if id2 != "8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a" {
		t.Errorf("Expected matched ID, got: %s", id2)
	}
	if fileDate2 != "" {
		t.Errorf("Expected empty file_date for old-format comment, got: %s", fileDate2)
	}
	if newLine2 != line2 {
		t.Errorf("Expected output line to equal input line")
	}

	// Line with ID + date (new format)
	line3 := "- note with date <!-- id: 8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a @ 2026-06-14 -->"
	id3, fileDate3, _, modified3 := EnsureBlockID(line3)
	if modified3 {
		t.Errorf("Expected line not to be modified")
	}
	if id3 != "8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a" {
		t.Errorf("Expected matched ID, got: %s", id3)
	}
	if fileDate3 != "2026-06-14" {
		t.Errorf("Expected file_date 2026-06-14, got: %s", fileDate3)
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
	taskLine := "- [ ] Draft README [owner:: Chris] [start:: 2026-06-13] [due:: 2026-08-03] [priority:: 1] <!-- id: 8fa72c3b-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->"
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

	// Test numbered list note line 1.
	numberedLine1 := "    1. A numbered list note <!-- id: 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c -->"
	block4, _, _ := ParseLine(numberedLine1, 4, 4)
	if block4.Type != BlockNote {
		t.Errorf("Expected BlockNote, got %s", block4.Type)
	}
	if block4.Depth != 1 {
		t.Errorf("Expected depth 1, got %d", block4.Depth)
	}
	if block4.CleanText != "A numbered list note" {
		t.Errorf("Expected clean text 'A numbered list note', got '%s'", block4.CleanText)
	}

	// Test numbered list note line 1)
	numberedLine2 := "1) Another numbered note <!-- id: 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8d -->"
	block5, _, _ := ParseLine(numberedLine2, 5, 4)
	if block5.Type != BlockNote {
		t.Errorf("Expected BlockNote, got %s", block5.Type)
	}
	if block5.Depth != 0 {
		t.Errorf("Expected depth 0, got %d", block5.Depth)
	}
	if block5.CleanText != "Another numbered note" {
		t.Errorf("Expected clean text 'Another numbered note', got '%s'", block5.CleanText)
	}
}

// TestParseLine_PinAndProgress covers the [pin:: true] and [progress:: N]
// Dataview inline metadata tokens in the task syntax. Both are file-
// resident user intent (ARCHITECTURE §0) so the parser is the canonical
// reader — a round trip (parse → render → parse) must be stable.
//
// Syntax convention (matches the renderer output):
//   - [x] description [priority:: N] [start:: DATE] [due:: DATE] [owner:: name] [pin:: true] [progress:: N]
// Metadata tokens follow the description in Dataview [key:: value] format.
func TestParseLine_PinAndProgress(t *testing.T) {
	// pinState collapses a *bool into a comparable label for assertions.
	pinState := func(p *bool) string {
		if p == nil {
			return "nil"
		}
		if *p {
			return "true"
		}
		return "false"
	}
	t.Run("pin flag parsed from [pin:: true] token", func(t *testing.T) {
		line := "- [ ] Draft release notes [pin:: true] <!-- id: 11111111-1111-1111-1111-111111111111 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if got := pinState(block.Pinned); got != "true" {
			t.Errorf("expected Pinned=true for [pin:: true] token, got %s", got)
		}
		if block.Progress != 0 {
			t.Errorf("expected Progress=0 when no [progress:: N] present, got %d", block.Progress)
		}
	})
	t.Run("progress parsed from [progress:: N] marker", func(t *testing.T) {
		line := "- [/] Refine UI kernel [owner:: Alice] [priority:: 2] [progress:: 50] <!-- id: 22222222-2222-2222-2222-222222222222 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Progress != 50 {
			t.Errorf("expected Progress=50 from [progress:: 50], got %d", block.Progress)
		}
		if block.Pinned != nil {
			t.Errorf("expected Pinned=nil when no [pin:: ...] token present, got %s", pinState(block.Pinned))
		}
	})
	t.Run("pin + progress coexist with full metadata", func(t *testing.T) {
		line := "- [/] Critical workstream [priority:: 1] [start:: 2026-06-13] [due:: 2026-08-03] [owner:: Bob] [pin:: true] [progress:: 75] <!-- id: 33333333-3333-3333-3333-333333333333 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if got := pinState(block.Pinned); got != "true" {
			t.Errorf("expected Pinned=true with [pin:: true] present, got %s", got)
		}
		if block.Progress != 75 {
			t.Errorf("expected Progress=75, got %d", block.Progress)
		}
		if block.Owner != "Bob" {
			t.Errorf("expected Owner=Bob, got %q", block.Owner)
		}
		if block.Priority != 1 {
			t.Errorf("expected Priority=1, got %d", block.Priority)
		}
		if block.CleanText != "Critical workstream" {
			t.Errorf("expected CleanText='Critical workstream', got %q", block.CleanText)
		}
	})
	t.Run("bare word `pin` in description does not count as pinned", func(t *testing.T) {
		// Defensive: a copy-paste of "pin this" or a sentence containing
		// the word should never flip Pinned. Only the [pin:: ...]
		// Dataview token sets the pointer (to &true or &false).
		line := "- [ ] pin this later <!-- id: 44444444-4444-4444-4444-444444444444 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Pinned != nil {
			t.Errorf("bare word `pin` must not set Pinned, got %s", pinState(block.Pinned))
		}
	})
	t.Run("truthy [pin::] -> &true, falsy/garbage [pin::] -> &false, absent -> nil", func(t *testing.T) {
		// Tri-state (#123): the token's PRESENCE sets a non-nil pointer.
		// Only explicit truthy values are &true; everything else with a
		// present token is &false. No token at all is nil.
		truthy := map[string]bool{
			"[pin:: true]": true, "[pin:: yes]": true, "[pin:: 1]": true,
		}
		falsy := []string{
			"[pin:: false]", "[pin:: no]", "[pin:: 0]", "[pin:: ]",
			"[pin:: maybe]", "[pin:: foo]", "[pin:: 2]", // typos/garbage
		}
		for token, want := range truthy {
			line := "- [ ] test " + token + " <!-- id: 12345678-1234-1234-1234-123456789012 -->"
			block, _, _ := ParseLine(line, 1, 4)
			if block.Pinned == nil {
				t.Errorf("%s: expected non-nil &true, got nil", token)
			} else if *block.Pinned != want {
				t.Errorf("%s: expected *Pinned=%v, got %v", token, want, *block.Pinned)
			}
		}
		for _, token := range falsy {
			line := "- [ ] test " + token + " <!-- id: 12345678-1234-1234-1234-123456789012 -->"
			block, _, _ := ParseLine(line, 1, 4)
			if block.Pinned == nil {
				t.Errorf("%s: expected non-nil &false (token present), got nil", token)
			} else if *block.Pinned {
				t.Errorf("%s: expected *Pinned=false, got true", token)
			}
		}
		// Absent token -> nil.
		block, _, _ := ParseLine("- [ ] no token here <!-- id: 12345678-1234-1234-1234-123456789012 -->", 1, 4)
		if block.Pinned != nil {
			t.Errorf("expected Pinned=nil when token absent, got %s", pinState(block.Pinned))
		}
	})
	t.Run("progress > 100 clamps to 100", func(t *testing.T) {
		line := "- [ ] Overflow [progress:: 999] <!-- id: 55555555-5555-5555-5555-555555555555 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Progress != 100 {
			t.Errorf("expected Progress clamped to 100, got %d", block.Progress)
		}
	})
	t.Run("round trip — parse then render preserves pin + progress", func(t *testing.T) {
		line := "- [x] Ship release [priority:: 2] [owner:: Eve] [pin:: true] [progress:: 100] <!-- id: 66666666-6666-6666-6666-666666666666 -->"
		parsed, _, _ := ParseLine(line, 1, 4)
		rendered := renderBlock(parsed, 4)
		// Re-parse the rendered line to confirm stability.
		parsed2, _, _ := ParseLine(rendered, 1, 4)
		if pinState(parsed2.Pinned) != pinState(parsed.Pinned) {
			t.Errorf("round-trip Pinned drift: %s → %s", pinState(parsed.Pinned), pinState(parsed2.Pinned))
		}
		if parsed2.Progress != parsed.Progress {
			t.Errorf("round-trip Progress drift: %d → %d", parsed.Progress, parsed2.Progress)
		}
		if parsed2.CleanText != parsed.CleanText {
			t.Errorf("round-trip CleanText drift: %q → %q", parsed.CleanText, parsed2.CleanText)
		}
	})

	// --- #123 tri-state round-trip + toggle-safety regression tests ---

	t.Run("[pin:: false] round-trips (not silently dropped)", func(t *testing.T) {
		line := "- [ ] Task [pin:: false] <!-- id: 77777777-7777-7777-7777-777777777777 -->"
		parsed, _, _ := ParseLine(line, 1, 4)
		if got := pinState(parsed.Pinned); got != "false" {
			t.Fatalf("expected &false, got %s", got)
		}
		rendered := renderBlock(parsed, 4)
		if !strings.Contains(rendered, "[pin:: false]") {
			t.Errorf("expected rendered line to keep [pin:: false], got: %s", rendered)
		}
		// Re-parse: still &false, no drift to nil.
		parsed2, _, _ := ParseLine(rendered, 1, 4)
		if got := pinState(parsed2.Pinned); got != "false" {
			t.Errorf("re-parse drift: expected false, got %s", got)
		}
	})

	t.Run("no pin token renders nothing (nil omits the token)", func(t *testing.T) {
		line := "- [ ] Task <!-- id: 88888888-8888-8888-8888-888888888888 -->"
		parsed, _, _ := ParseLine(line, 1, 4)
		if parsed.Pinned != nil {
			t.Fatalf("expected nil, got %s", pinState(parsed.Pinned))
		}
		rendered := renderBlock(parsed, 4)
		if strings.Contains(rendered, "[pin::") {
			t.Errorf("expected no [pin:: token when Pinned is nil, got: %s", rendered)
		}
	})

	t.Run("toggle pin on→off→on never produces two tokens or drifts (#123)", func(t *testing.T) {
		// Start with an explicit [pin:: false] plus an unknown token, which
		// is the issue's ExtraTokens-conflict regression scenario.
		line := "- [ ] Task [pin:: false] [project:: alpha] <!-- id: 99999999-9999-9999-9999-999999999999 -->"
		parsed, _, _ := ParseLine(line, 1, 4)
		if got := pinState(parsed.Pinned); got != "false" {
			t.Fatalf("initial parse: expected false, got %s", got)
		}

		// Toggle ON (UI sets &true).
		on := true
		parsed.Pinned = &on
		renderedOn := renderBlock(parsed, 4)
		if cnt := strings.Count(renderedOn, "[pin::"); cnt != 1 {
			t.Errorf("after pin ON: expected exactly 1 [pin:: token, got %d in %q", cnt, renderedOn)
		}
		if !strings.Contains(renderedOn, "[pin:: true]") {
			t.Errorf("after pin ON: expected [pin:: true], got: %s", renderedOn)
		}
		// Unknown token must survive the toggle.
		if !strings.Contains(renderedOn, "[project:: alpha]") {
			t.Errorf("after pin ON: unknown token dropped, got: %s", renderedOn)
		}
		// Re-parse: should be &true (no reversion to false).
		reparsed, _, _ := ParseLine(renderedOn, 1, 4)
		if got := pinState(reparsed.Pinned); got != "true" {
			t.Errorf("after pin ON re-parse: expected true, got %s (silent revert bug)", got)
		}

		// Toggle OFF (UI sets &false).
		off := false
		reparsed.Pinned = &off
		renderedOff := renderBlock(reparsed, 4)
		if cnt := strings.Count(renderedOff, "[pin::"); cnt != 1 {
			t.Errorf("after pin OFF: expected exactly 1 [pin:: token, got %d in %q", cnt, renderedOff)
		}
		if !strings.Contains(renderedOff, "[pin:: false]") {
			t.Errorf("after pin OFF: expected [pin:: false], got: %s", renderedOff)
		}
		// Final re-parse: still &false.
		final, _, _ := ParseLine(renderedOff, 1, 4)
		if got := pinState(final.Pinned); got != "false" {
			t.Errorf("after pin OFF re-parse: expected false, got %s", got)
		}
	})
}

// TestParseLine_UnknownTokens verifies that unrecognised [key:: value]
// Dataview tokens (e.g. third-party fields like [project:: alpha]) survive
// the parse → render round-trip so files stay interoperable with
// Dataview-compatible (SPECS.md §4.1).
func TestParseLine_UnknownTokens(t *testing.T) {
	t.Run("unknown token collected into ExtraTokens", func(t *testing.T) {
		line := "- [ ] Build feature [due:: 2026-08-03] [project:: alpha] <!-- id: 77777777-7777-7777-7777-777777777777 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if len(block.ExtraTokens) != 1 {
			t.Fatalf("expected 1 extra token, got %d: %v", len(block.ExtraTokens), block.ExtraTokens)
		}
		if block.ExtraTokens[0] != "[project:: alpha]" {
			t.Errorf("expected '[project:: alpha]', got %q", block.ExtraTokens[0])
		}
		// Known token still parsed correctly.
		if block.DueDate != "2026-08-03" {
			t.Errorf("expected DueDate=2026-08-03, got %q", block.DueDate)
		}
		// Unknown token stripped from the description.
		if block.CleanText != "Build feature" {
			t.Errorf("expected CleanText='Build feature', got %q", block.CleanText)
		}
	})
	t.Run("multiple unknown tokens round-trip through render", func(t *testing.T) {
		line := "- [ ] Task [project:: alpha] [estimate:: 3h] [priority:: 1] <!-- id: 88888888-8888-8888-8888-888888888888 -->"
		parsed, _, _ := ParseLine(line, 1, 4)
		rendered := renderBlock(parsed, 4)
		// Re-parse the rendered output.
		parsed2, _, _ := ParseLine(rendered, 1, 4)
		if len(parsed2.ExtraTokens) != 2 {
			t.Fatalf("expected 2 extra tokens after round-trip, got %d: %v", len(parsed2.ExtraTokens), parsed2.ExtraTokens)
		}
		// Both unknown tokens preserved verbatim.
		expectExtra := map[string]bool{"[project:: alpha]": false, "[estimate:: 3h]": false}
		for _, tok := range parsed2.ExtraTokens {
			if _, ok := expectExtra[tok]; ok {
				expectExtra[tok] = true
			}
		}
		for tok, found := range expectExtra {
			if !found {
				t.Errorf("extra token %q missing after round-trip; got %v", tok, parsed2.ExtraTokens)
			}
		}
		// Known token still parsed correctly.
		if parsed2.Priority != 1 {
			t.Errorf("expected Priority=1 after round-trip, got %d", parsed2.Priority)
		}
	})
}

// TestParseLine_EdgeCases covers the task-shorthand branches the
// all-metadata TestParseLine case does not exercise: minimal task (no
// metadata at all), DOING/DONE checkbox states, and partial metadata
// (owner without dates, priority without owner).
func TestParseLine_EdgeCases(t *testing.T) {
	t.Run("minimal task — no owner/dates/priority", func(t *testing.T) {
		line := "- [ ] Just a description <!-- id: 11111111-1111-1111-1111-111111111111 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Type != BlockTask {
			t.Fatalf("expected BlockTask, got %s", block.Type)
		}
		if block.Owner != "" {
			t.Errorf("expected empty owner, got %q", block.Owner)
		}
		if block.StartDate != "" || block.DueDate != "" {
			t.Errorf("expected empty dates, got start=%q due=%q", block.StartDate, block.DueDate)
		}
		if block.Priority != 3 {
			t.Errorf("expected default priority 3, got %d", block.Priority)
		}
		if block.CleanText != "Just a description" {
			t.Errorf("expected 'Just a description', got %q", block.CleanText)
		}
		if block.Status != "TODO" {
			t.Errorf("expected status TODO, got %q", block.Status)
		}
	})

	t.Run("DOING state", func(t *testing.T) {
		line := "- [/] In progress task [owner:: Bob] <!-- id: 22222222-2222-2222-2222-222222222222 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Status != "DOING" {
			t.Errorf("expected status DOING, got %q", block.Status)
		}
		if block.Owner != "Bob" {
			t.Errorf("expected owner Bob, got %q", block.Owner)
		}
	})

	t.Run("DONE state", func(t *testing.T) {
		line := "- [x] Completed task [start:: 2026-01-01] [due:: 2026-06-01] [owner:: Carol] [priority:: 2] <!-- id: 33333333-3333-3333-3333-333333333333 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Status != "DONE" {
			t.Errorf("expected status DONE, got %q", block.Status)
		}
		if block.Priority != 2 {
			t.Errorf("expected priority 2, got %d", block.Priority)
		}
		if block.StartDate != "2026-01-01" || block.DueDate != "2026-06-01" {
			t.Errorf("unexpected dates: start=%q due=%q", block.StartDate, block.DueDate)
		}
	})

	t.Run("priority without owner or dates", func(t *testing.T) {
		line := "- [ ] Urgent task no owner [priority:: 1] <!-- id: 44444444-4444-4444-4444-444444444444 -->"
		block, _, _ := ParseLine(line, 1, 4)
		if block.Priority != 1 {
			t.Errorf("expected priority 1, got %d", block.Priority)
		}
		if block.Owner != "" {
			t.Errorf("expected empty owner, got %q", block.Owner)
		}
		if block.CleanText != "Urgent task no owner" {
			t.Errorf("expected 'Urgent task no owner', got %q", block.CleanText)
		}
	})
}

func TestParseFileContent(t *testing.T) {
	doc := `---
notebook: Engineering
section: Architecture
page: DailyLog
date: 2026-06-13
tags: [work/project, systems/specs]
---
# Saturday, June 13, 2026 <!-- id: 0a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8a -->

## Stream Logging <!-- id: 1a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8b -->
- [ ] Draft README [due:: 2026-06-13] [owner:: Chris] [priority:: 1] <!-- id: 2a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8c -->
    - [/] Research subtasks [owner:: Jenny] [priority:: 2] <!-- id: 3a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8d -->
- A general note <!-- id: 4a10b1a0-d1e5-4b0d-8ea2-bfcfd2ee7f8e -->`

	blocks, meta, newContent, modified, err := ParseFileContent(doc, "DefaultNB", "DefaultSec", "DefaultPage", "2026-06-01", 4)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if meta.Notebook != "Engineering" || meta.Section != "Architecture" || meta.Page != "DailyLog" || meta.Date != "2026-06-13" {
		t.Errorf("Metadata mismatch: %+v", meta)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "work/project" {
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

func TestRenderFileContent_DefaultsBulletForNewBlockNote(t *testing.T) {
	// Newly created editor blocks arrive with empty RawText. The serializer
	// must emit a "- " bullet so the outliner round-trips correctly.
	block := ParsedBlock{
		ID:        "new-block-id",
		Type:      BlockNote,
		RawText:   "",
		CleanText: "fresh content",
	}
	content := RenderFileContent([]ParsedBlock{block}, "", "", 4)
	if !strings.HasPrefix(strings.TrimSpace(content), "- ") {
		t.Errorf("expected '- ' bullet for empty-RawText BlockNote, got: %s", content)
	}
	if !strings.Contains(content, "fresh content") {
		t.Errorf("expected clean text in output, got: %s", content)
	}

	// An existing plain-text note (no bullet marker in RawText) must
	// serialize without a bullet to preserve the original style.
	block.RawText = "just plain text <!-- id: new-block-id -->"
	content = RenderFileContent([]ParsedBlock{block}, "", "", 4)
	if strings.HasPrefix(strings.TrimSpace(content), "- ") {
		t.Errorf("expected no bullet for plain-text note, got: %s", content)
	}

	// An existing bullet note must preserve its specific marker.
	block.RawText = "* starred note <!-- id: new-block-id -->"
	content = RenderFileContent([]ParsedBlock{block}, "", "", 4)
	if !strings.HasPrefix(strings.TrimSpace(content), "* ") {
		t.Errorf("expected '* ' bullet to be preserved, got: %s", content)
	}

	// An existing numbered note must preserve its specific prefix.
	block.RawText = "3) numbered note <!-- id: new-block-id -->"
	content = RenderFileContent([]ParsedBlock{block}, "", "", 4)
	if !strings.HasPrefix(strings.TrimSpace(content), "3) ") {
		t.Errorf("expected '3) ' prefix to be preserved, got: %s", content)
	}
}

// blocksEqual compares the semantic fields of two ParsedBlock slices — the
// fields that must survive a render→parse round trip. LineNumber/RawText can
// shift (e.g. when preserved unmanaged lines move) so they are not compared.
func blocksEqual(a, b []ParsedBlock) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || x.ParentID != y.ParentID ||
			x.Type != y.Type || x.Depth != y.Depth ||
			x.CleanText != y.CleanText || x.Status != y.Status ||
			x.Owner != y.Owner || x.StartDate != y.StartDate ||
			x.DueDate != y.DueDate || x.Priority != y.Priority {
			return false
		}
	}
	return true
}

// TestRenderFileContent_RoundTripIdentity guarantees the single serializer
// produces output the parser reads back as the same blocks — the core #40
// invariant. If this fails, renderBlock and ParseLine have drifted apart.
//
// Note: ParseFileContent injects IDs into every non-empty, non-code line, so
// after the first parse ALL prose is managed. The round trip therefore passes
// body="" (nothing extra to preserve) and checks both semantic equality of
// the blocks and byte-stability of the render across two passes. Preservation
// of genuinely unmanaged lines (code fences / blanks) is covered separately
// by the code_fence_preserved case below.
func TestRenderFileContent_RoundTripIdentity(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "task_note_header",
			src: "---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: 2026-06-14\ntags: []\n---\n" +
				"# Sprint Plan <!-- id: 11111111-1111-1111-1111-111111111111 -->\n" +
				"- [ ] Ship the feature [priority:: 1] [owner:: Chris] <!-- id: 22222222-2222-2222-2222-222222222222 -->\n" +
				"- A plain note <!-- id: 33333333-3333-3333-3333-333333333333 -->\n",
		},
		{
			name: "nested_depths_and_states",
			src: "---\nnotebook: NB\nsection: \npage: PG\ndate: 2026-06-14\ntags: []\n---\n" +
				"# Top <!-- id: aaaaaaaa-1111-1111-1111-111111111111 -->\n" +
				"- [ ] Parent <!-- id: aaaaaaaa-2222-2222-2222-111111111111 -->\n" +
				"    - [/] Child [priority:: 1] [start:: 2026-06-14] [due:: 2026-06-20] [owner:: Sam] <!-- id: aaaaaaaa-3333-3333-3333-111111111111 -->\n" +
				"        - [x] Grandchild <!-- id: aaaaaaaa-4444-4444-4444-111111111111 -->\n",
		},
		{
			name: "code_fence_preserved",
			src: "---\nnotebook: NB\nsection: \npage: PG\ndate: 2026-06-14\ntags: []\n---\n" +
				"# Notes <!-- id: bbbbbbbb-1111-1111-1111-111111111111 -->\n" +
				"```go\n" +
				"// code block content - no IDs injected here\n" +
				"func main() {}\n" +
				"```\n" +
				"- After code <!-- id: bbbbbbbb-2222-2222-2222-111111111111 -->\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, meta, _, _, err := ParseFileContent(tc.src, "NB", "", "PG", "2026-06-14", 4)
			if err != nil {
				t.Fatalf("first parse: %v", err)
			}
			// First render: no body to preserve (all content is in `first`).
			fm, _ := SplitFrontmatter(tc.src)
			rendered := RenderFileContent(first, "", fm, 4)
			second, _, _, _, err := ParseFileContent(rendered, meta.Notebook, meta.Section, meta.Page, meta.Date, 4)
			if err != nil {
				t.Fatalf("second parse: %v", err)
			}
			if !blocksEqual(first, second) {
				t.Errorf("round trip changed the blocks\nfirst:  %+v\nsecond: %+v", first, second)
			}
			// The second render must be byte-stable (canonical form reached).
			rendered2 := RenderFileContent(second, "", fm, 4)
			if rendered != rendered2 {
				t.Errorf("render is not byte-stable across two passes\n--- pass1 ---\n%s\n--- pass2 ---\n%s", rendered, rendered2)
			}
		})
	}

	// Sub-test: code-fence preservation requires the body (the fence is not
	// in the parsed blocks, so it must come from originalBody).
	t.Run("code_fence_preserved_via_body", func(t *testing.T) {
		src := "---\nnotebook: NB\nsection: \npage: PG\ndate: 2026-06-14\ntags: []\n---\n" +
			"# Notes <!-- id: bbbbbbbb-1111-1111-1111-111111111111 -->\n" +
			"```go\nfunc main() {}\n```\n" +
			"- After code <!-- id: bbbbbbbb-2222-2222-2222-111111111111 -->\n"
		first, _, _, _, _ := ParseFileContent(src, "NB", "", "PG", "2026-06-14", 4)
		fm, body := SplitFrontmatter(src)
		rendered := RenderFileContent(first, body, fm, 4)
		if !strings.Contains(rendered, "```go") || !strings.Contains(rendered, "func main()") {
			t.Errorf("code fence was dropped from rendered output:\n%s", rendered)
		}
	})
}

// TestRenderFileContent_DeletedBlockDropped verifies that removing a block
// from the input slice deletes its line on save (the block was deleted in the
// editor). A line carrying a trailing <!-- id --> comment IS a managed block
// to the parser, so dropping it from the slice must drop it from the output.
func TestRenderFileContent_DeletedBlockDropped(t *testing.T) {
	src := "---\nnotebook: NB\nsection: \npage: PG\ndate: 2026-06-14\ntags: []\n---\n" +
		"# Keep <!-- id: dddddddd-1111-1111-1111-111111111111 -->\n" +
		"- Drop me <!-- id: dddddddd-2222-2222-2222-111111111111 -->\n"
	first, _, _, _, _ := ParseFileContent(src, "NB", "", "PG", "2026-06-14", 4)
	var kept []ParsedBlock
	for _, b := range first {
		if b.CleanText == "Keep" {
			kept = append(kept, b)
		}
	}
	fm, body := SplitFrontmatter(src)
	out := RenderFileContent(kept, body, fm, 4)
	if strings.Contains(out, "Drop me") {
		t.Errorf("deleted managed block was kept:\n%s", out)
	}
	if !strings.Contains(out, "Keep") {
		t.Errorf("surviving block was dropped:\n%s", out)
	}
}

// TestRenderFileContent_ScaffoldSnapshot pins the canonical output of the
// CreatePage scaffold so a silent format change is caught immediately.
func TestRenderFileContent_ScaffoldSnapshot(t *testing.T) {
	blocks := []ParsedBlock{
		{Type: BlockHeader, Depth: 1, CleanText: "Sunday, June 14, 2026"},
		{Type: BlockTask, Status: "TODO", Owner: "Chris", Priority: 3, CleanText: "Start writing in Daily"},
	}
	fm := "---\nnotebook: \"Work\"\nsection: \"Journal\"\npage: \"Daily\"\ndate: \"2026-06-14\"\ntags: []\n---\n"
	got := RenderFileContent(blocks, "", fm, 4)
	// Two managed lines, each with an injected UUID; header uses '#', task
	// uses the TODO checkbox syntax with default priority (#3 is omitted).
	if strings.Count(got, "<!-- id:") != 2 {
		t.Errorf("expected 2 injected IDs, got %d in:\n%s", strings.Count(got, "<!-- id:"), got)
	}
	if !strings.Contains(got, "# Sunday, June 14, 2026") {
		t.Errorf("header line missing/wrong:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] Start writing in Daily [owner:: Chris]") {
		t.Errorf("task line missing/wrong:\n%s", got)
	}
	// The scaffolded output must parse cleanly (round trip back to blocks).
	reparsed, _, _, _, err := ParseFileContent(got, "Work", "Journal", "Daily", "2026-06-14", 4)
	if err != nil {
		t.Fatalf("scaffold did not re-parse: %v", err)
	}
	if len(reparsed) != 2 {
		t.Fatalf("expected 2 blocks after reparse, got %d", len(reparsed))
	}
	if reparsed[0].Type != BlockHeader || reparsed[1].Type != BlockTask {
		t.Fatalf("scaffold block types wrong: %+v", reparsed)
	}
	if reparsed[1].Status != "TODO" || reparsed[1].Owner != "Chris" {
		t.Errorf("task fields not preserved: %+v", reparsed[1])
	}
}

// --- Phase 5c: symlink loop handling (#32) ---

// writeFile is a tiny helper for the symlink tests.
func writeMdFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkMarkdown_SelfReferencingSymlinkDoesNotLoop(t *testing.T) {
	dir := t.TempDir()
	writeMdFile(t, filepath.Join(dir, "NB", "PG", "2026-06-14.md"), "real note")
	// Self-referencing symlink: NB/loop -> NB/loop (a degenerate cycle).
	loopDir := filepath.Join(dir, "NB", "loop")
	if err := os.Symlink(loopDir, loopDir); err != nil {
		// Some platforms / CI runners disable symlink creation; skip gracefully.
		t.Skipf("cannot create symlink: %v", err)
	}
	files, warnings, err := WalkMarkdown(dir)
	if err != nil {
		t.Fatalf("WalkMarkdown: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 real file (symlink not followed), got %d: %v", len(files), files)
	}
	if len(warnings) == 0 {
		t.Error("expected a symlink warning, got none")
	}
}

func TestWalkMarkdown_MutualSymlinkCycleIsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeMdFile(t, filepath.Join(dir, "NB", "PG", "2026-06-14.md"), "real note")
	// Mutual cycle: NB/a -> NB/b, NB/b -> NB/a.
	a := filepath.Join(dir, "NB", "a")
	b := filepath.Join(dir, "NB", "b")
	if err := os.Symlink(b, a); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	files, warnings, err := WalkMarkdown(dir)
	if err != nil {
		t.Fatalf("WalkMarkdown: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected only the 1 real file, got %d: %v", len(files), files)
	}
	if len(warnings) < 2 {
		t.Errorf("expected >=2 symlink warnings (one per symlink), got %d", len(warnings))
	}
}

func TestWalkMarkdown_OneHopSymlinkIsSkippedWithWarning(t *testing.T) {
	dir := t.TempDir()
	// A real subdirectory with a note, plus a symlink pointing at it.
	target := filepath.Join(dir, "Real", "PG")
	writeMdFile(t, filepath.Join(target, "2026-06-14.md"), "via target")
	link := filepath.Join(dir, "Shortcut")
	if err := os.Symlink(filepath.Join(dir, "Real"), link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	files, warnings, err := WalkMarkdown(dir)
	if err != nil {
		t.Fatalf("WalkMarkdown: %v", err)
	}
	// The real target's note is indexed; the symlink is skipped (not followed).
	if len(files) != 1 {
		t.Errorf("expected 1 file (real target only), got %d: %v", len(files), files)
	}
	foundSymlinkWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "symlink not followed") {
			foundSymlinkWarn = true
			break
		}
	}
	if !foundSymlinkWarn {
		t.Errorf("expected a 'symlink not followed' warning, got %v", warnings)
	}
}

func TestScanWorkspace_NoCrashOnSymlinkLoop(t *testing.T) {
	// Integration: ScanWorkspace must not hang or crash on a symlink loop,
	// and must still return the real file's blocks.
	dir := t.TempDir()
	writeMdFile(t, filepath.Join(dir, "NB", "PG", "2026-06-14.md"),
		"# Real <!-- id: 11111111-1111-1111-1111-111111111111 -->\n")
	loopDir := filepath.Join(dir, "NB", "loop")
	if err := os.Symlink(filepath.Join(dir, "NB"), loopDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	done := make(chan struct{})
	var results []ScanResult
	var warnings []string
	var scanErr error
	go func() {
		defer close(done)
		results, warnings, scanErr = ScanWorkspace(dir, 4)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ScanWorkspace hung on a symlink loop")
	}
	if scanErr != nil {
		t.Fatalf("ScanWorkspace error: %v", scanErr)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if len(warnings) == 0 {
		t.Error("expected symlink warnings, got none")
	}
}

func BenchmarkScanWorkspace_1000Files(b *testing.B) {
	for range b.N {
		dir := b.TempDir()
		writeBenchVault(b, dir, 1000)
		_, _, err := ScanWorkspace(dir, 4)
		if err != nil {
			b.Fatalf("ScanWorkspace: %v", err)
		}
	}
}

// TestScanWorkspace_BudgetRegression asserts the boot scanner stays within
// the 450ms / 1,000-file budget (Sprint 1 spec, TESTING.md). The benchmark
// above measures the same workload but only runs under -bench; this test
// runs under the normal -race CI gate so a regression is caught immediately.
// Skipped under -short for quick test runs.
func TestScanWorkspace_BudgetRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping budget regression in short mode")
	}
	dir := t.TempDir()
	writeBenchVault(t, dir, 1000)

	start := time.Now()
	_, _, err := ScanWorkspace(dir, 4)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ScanWorkspace: %v", err)
	}
	// Budget: <450ms for 1,000 files (baseline ~280ms on Ryzen AI MAX+ /
	// Go 1.25 / Windows per TESTING.md; 450ms allows headroom for slower
	// CI runners and the race detector). Under -race the detector adds
	// ~2x overhead to the I/O+parse workload, so scanBudgetRegressionLimit
	// returns a scaled threshold (900ms) via a build tag — the test still
	// runs in the normal `go test -race ./...` CI gate and stays sensitive
	// to a real regression in both modes.
	limit := scanBudgetRegressionLimit()
	if elapsed > limit {
		t.Fatalf("ScanWorkspace regressed: %v > %v/1k files", elapsed, limit)
	}
	t.Logf("ScanWorkspace 1k files: %v (budget %v)", elapsed, limit)
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
		day := fmt.Sprintf("---\nnotebook: Work\nsection: Journal\npage: Daily\ndate: %s\ntags: [bench]\n---\n# Day %d <!-- id: %08x-1111-1111-1111-111111111111 -->\n\n- [ ] Item for day %d [priority:: 1] [owner:: Bench] <!-- id: %08x-2222-2222-2222-222222222222 -->\n- A note for day %d <!-- id: %08x-3333-3333-3333-333333333333 -->\n", dateStr, i+1, i, i+1, i, i+1, i)
		path := filepath.Join(dir, dateStr+".md")
		if err := os.WriteFile(path, []byte(day), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}
