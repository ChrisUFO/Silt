package parser

import (
	"strings"
	"testing"
)

// Sprint 14 (#189) — multi-line managed code blocks. The single-line block
// model (`renderBlock` collapses `\n`→space for prose) must NOT touch code:
// blank lines, leading indentation, tabs, a literal `|`, and the info-string
// language all survive a parse → render → parse round trip byte-for-byte.
// These cases complement the broader TestRenderFileContent_RoundTripIdentity
// suite by exercising the content shapes that previously distinguished
// "unmanaged verbatim pass-through" from "managed block".

func TestCodeBlock_MultilineRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		lang string
		body string
	}{
		{
			name: "blank lines preserved",
			lang: "text",
			body: "first\n\nthird",
		},
		{
			name: "leading indentation preserved",
			lang: "go",
			body: "func main() {\n\tinner := func() {\n\t\tx := 1\n\t}\n\t_ = inner\n}",
		},
		{
			name: "literal pipe survives",
			lang: "text",
			body: "a | b\n| c",
		},
		{
			name: "empty body",
			lang: "",
			body: "",
		},
		{
			name: "single line",
			lang: "ts",
			body: "const x: number = 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := ParsedBlock{
				ID:        "dddddddd-1111-1111-1111-111111111111",
				Type:      BlockCode,
				Language:  tc.lang,
				CleanText: tc.body,
				FileDate:  "2026-06-14",
			}
			rendered := RenderFileContent([]ParsedBlock{block}, "", "", 4)
			reparsed, _, _, _, err := ParseFileContent(rendered, "NB", "", "PG", "2026-06-14", 4)
			if err != nil {
				t.Fatalf("reparse: %v\nrendered:\n%s", err, rendered)
			}
			if len(reparsed) != 1 || reparsed[0].Type != BlockCode {
				t.Fatalf("expected one BlockCode, got %+v", reparsed)
			}
			got := reparsed[0]
			if got.CleanText != tc.body {
				t.Errorf("CleanText drifted\nwant: %q\n got: %q", tc.body, got.CleanText)
			}
			if got.Language != tc.lang {
				t.Errorf("Language drifted: want %q got %q", tc.lang, got.Language)
			}
			// Byte-stable across a second render pass.
			rendered2 := RenderFileContent(reparsed, "", "", 4)
			if rendered != rendered2 {
				t.Errorf("not byte-stable\n--- pass1 ---\n%s\n--- pass2 ---\n%s", rendered, rendered2)
			}
		})
	}
}

func TestCodeBlock_ExternalFileGetsIdOnFirstParse(t *testing.T) {
	// A code block authored externally (Obsidian/VS Code) carries no id.
	// First parse mints one on its own line after the closing fence; the
	// code body is untouched. Second parse is stable (no further change).
	src := "# H <!-- id: aaaaaaaa-1111-1111-1111-111111111111 -->\n" +
		"```js\nconst x = 1\n```\n" +
		"- note <!-- id: aaaaaaaa-2222-2222-2222-111111111111 -->\n"

	blocks, _, _, modified, err := ParseFileContent(src, "NB", "", "PG", "2026-06-14", 4)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !modified {
		t.Errorf("expected the external code block to be assigned an id on first parse")
	}
	var code *ParsedBlock
	for i := range blocks {
		if blocks[i].Type == BlockCode {
			code = &blocks[i]
		}
	}
	if code == nil {
		t.Fatalf("expected a BlockCode, got %+v", blocks)
	}
	if code.CleanText != "const x = 1" {
		t.Errorf("code body corrupted: %q", code.CleanText)
	}
	if code.Language != "js" {
		t.Errorf("language lost: %q", code.Language)
	}
}

func TestCodeBlock_NestedBacktickFence(t *testing.T) {
	// A code sample that itself contains a ``` line must round-trip. The
	// renderer grows the outer fence to four backticks so the inner fence
	// does not prematurely close the block.
	inner := "outer line\n```\nstill code\n```"
	block := ParsedBlock{
		ID:        "eeeeeeee-1111-1111-1111-111111111111",
		Type:      BlockCode,
		Language:  "markdown",
		CleanText: inner,
		FileDate:  "2026-06-14",
	}
	rendered := RenderFileContent([]ParsedBlock{block}, "", "", 4)
	if !strings.Contains(rendered, "````") {
		t.Errorf("expected a 4-backtick outer fence for nested content, got:\n%s", rendered)
	}
	reparsed, _, _, _, err := ParseFileContent(rendered, "NB", "", "PG", "2026-06-14", 4)
	if err != nil {
		t.Fatalf("reparse: %v\nrendered:\n%s", err, rendered)
	}
	if len(reparsed) != 1 || reparsed[0].Type != BlockCode {
		t.Fatalf("expected one BlockCode, got %+v", reparsed)
	}
	if reparsed[0].CleanText != inner {
		t.Errorf("nested-fence body drifted\nwant: %q\n got: %q", inner, reparsed[0].CleanText)
	}
}

// A closing fence must be backticks-only (GFM): a ```js line inside the block
// is an inner fence WITH an info string, NOT a closer. Without this rule a
// 3-backtick block that documents another fence closes prematurely and the
// spill becomes prose — silent corruption that contradicts the GFM/Obsidian/
// GitHub interop guarantee. Must render as ONE block matching GitHub.
func TestCodeBlock_InfoStringLineIsNotACloser(t *testing.T) {
	src := "```markdown\n" +
		"Example:\n" +
		"```js\n" +
		"foo()\n" +
		"```\n" +
		"done\n"
	first, _, _, _, err := ParseFileContent(src, "NB", "", "PG", "2026-06-14", 4)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var code *ParsedBlock
	for i := range first {
		if first[i].Type == BlockCode {
			code = &first[i]
		}
	}
	if code == nil {
		t.Fatalf("expected one BlockCode, got %+v", first)
	}
	wantBody := "Example:\n```js\nfoo()"
	if code.CleanText != wantBody {
		t.Errorf("info-string-fence body drifted (block split prematurely?)\nwant: %q\n got: %q",
			wantBody, code.CleanText)
	}
	// Round-trip is byte-stable.
	rendered := RenderFileContent(first, "", "", 4)
	second, _, _, _, err := ParseFileContent(rendered, "NB", "", "PG", "2026-06-14", 4)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	var code2 *ParsedBlock
	for i := range second {
		if second[i].Type == BlockCode {
			code2 = &second[i]
		}
	}
	if code2 == nil || code2.CleanText != wantBody {
		t.Errorf("round-trip lost the info-string-fence body\nrendered:\n%s", rendered)
	}
}
