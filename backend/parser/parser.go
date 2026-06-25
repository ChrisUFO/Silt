package parser

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// TaskCheckboxRegex matches the GFM task list prefix: optional
// indentation, a checkbox marker (`[ ]`, `[x]`, or `[/]`), and the
// remainder of the line. This is the ONLY structural regex for tasks —
// all metadata (owner, dates, priority, pin, progress) is extracted by
// the [key:: value] token scanner (scanTaskTokens) from the remainder,
// not by positional regex groups.
//
// This drops the legacy `TASK` keyword entirely — any GFM checkbox item
// is a task, matching CommonMark/GFM convention. The token scanner
// makes the metadata order-independent and extensible (new metadata
// type = new key in the switch, no regex change). The token format
// follows the Dataview inline metadata standard ([key:: value]) so
// files are interoperable with the Dataview-compatible ecosystem.
//
// See ARCHITECTURE.md §0 "Storage-of-Truth Tiers" for the design
// rationale: task metadata is file-resident user intent, and the
// [key:: value] format is the de facto standard for per-block metadata
// in markdown.
var TaskCheckboxRegex = regexp.MustCompile(`^([\s]*)-\s\[([ x/])\]\s+(.*)$`)

// TaskTokenRegex captures a single Dataview [key:: value] inline metadata
// token. The double-colon `::` is the signature that distinguishes a
// metadata field from a markdown link `[text](url)` or regular bracketed
// text — no other markdown syntax uses `::`.
//
// Supported keys (see scanTaskTokens for the dispatch table):
//   [due:: DATE]       — due date (YYYY-MM-DD)
//   [start:: DATE]     — start date (YYYY-MM-DD)
//   [owner:: name]     — owner/assignee
//   [priority:: N]     — priority (1=critical, 2=normal, 3=low)
//   [p:: N]            — priority shorthand (alias for [priority:: N])
//   [pin:: true]       — pinned (boolean; presence also implies true)
//   [progress:: N]     — progress (0-100)
//   [prog:: N]         — progress shorthand
//
// The scanner is the single source of truth for token → ParsedBlock
// field mapping; adding a new metadata type is a one-line addition to
// the switch in scanTaskTokens. Keys are case-insensitive.
var TaskTokenRegex = regexp.MustCompile(`\[([\w]+)::\s*([^\]]*)\]`)

// whitespaceRun collapses consecutive whitespace into a single space. Used
// in scanTaskTokens to normalize the description after token stripping.
// Hoisted to package level so the regex is compiled once, not per line.
var whitespaceRun = regexp.MustCompile(`\s+`)

// IDRegex captures the trailing block-identity comment. The format is:
//   <!-- id: uuid -->
// or (with per-block file_date, post per-day-file-model removal):
//   <!-- id: uuid @ YYYY-MM-DD -->
// The date suffix is optional for backward compatibility with notes created
// under the old per-day-file model (it is assigned during migration).
var IDRegex = regexp.MustCompile(`<!-- id: ([a-f0-9\-]{36})(?:\s*@\s*(\d{4}-\d{2}-\d{2}))?\s*-->\s*$`)

// BlockRefRegex matches a global block reference ((uuid)). Read-only detector
// used by the resolver; it never injects IDs (code-fence protection in
// ParseFileContent already prevents ID injection inside ``` blocks).
var BlockRefRegex = regexp.MustCompile(`\(\(([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\)\)`)

// EmbedRegex matches a live block embed {{embed:uuid}}.
var EmbedRegex = regexp.MustCompile(`\{\{embed:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\}\}`)

// NumberedListRegex matches numbered list prefixes like 1. or 1) followed by space.
var NumberedListRegex = regexp.MustCompile(`^(\d+[.)]\s)`)

func generateUUIDv4() string {
	return uuid.New().String()
}

// EnsureBlockID extracts (or assigns) the block identity — both the UUID and
// the per-block file_date — from the trailing comment. Returns:
//   id        — the UUID ("" for empty lines)
//   fileDate  — the date from the comment, or "" if none was embedded
//   newLine   — the line with the comment preserved/assigned
//   modified  — true if a new comment was injected (caller should rewrite)
func EnsureBlockID(line string) (id, fileDate, newLine string, modified bool) {
	clean := strings.TrimSpace(line)
	if clean == "" {
		return "", "", line, false
	}
	matches := IDRegex.FindStringSubmatch(line)
	if len(matches) > 1 {
		id = matches[1]
		if len(matches) > 2 {
			fileDate = matches[2]
		}
		return id, fileDate, line, false
	}

	newID := generateUUIDv4()
	today := time.Now().Format("2006-01-02")
	cleanLine := strings.TrimRight(line, "\r\n")
	newLine = fmt.Sprintf("%s <!-- id: %s @ %s -->", cleanLine, newID, today)
	return newID, today, newLine, true
}

func CleanLineID(line string) string {
	return IDRegex.ReplaceAllString(line, "")
}

func normalizeDate(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}

	// Try standard YYYY-MM-DD
	if _, err := time.Parse("2006-01-02", d); err == nil {
		return d
	}

	// Normalize M/D/YY or MM/DD/YYYY
	parts := strings.Split(d, "/")
	if len(parts) == 3 {
		m := parts[0]
		day := parts[1]
		y := parts[2]

		if len(m) == 1 {
			m = "0" + m
		}
		if len(day) == 1 {
			day = "0" + day
		}
		if len(y) == 2 {
			y = "20" + y
		}
		return fmt.Sprintf("%s-%s-%s", y, m, day)
	}

	return d
}

func parseLeadingIndent(line string, spacesPerTab int) int {
	if spacesPerTab <= 0 {
		spacesPerTab = 4
	}
	tabs := 0
	spaces := 0
	for _, char := range line {
		if char == '\t' {
			tabs++
		} else if char == ' ' {
			spaces++
		} else {
			break
		}
	}
	return tabs + (spaces / spacesPerTab)
}

// scanTaskTokens extracts all Dataview [key:: value] inline metadata
// tokens from a task line's remainder (the text after the checkbox).
// Returns the parsed fields, the description with known tokens stripped,
// and any unrecognised tokens preserved verbatim for forward-compatible
// round-tripping (Dataview-compatible interop — SPECS.md §4.1).
//
// The function is the single source of truth for token → field mapping.
// Adding a new metadata type is a one-line addition to the switch below.
// Unknown keys are preserved in extraTokens so the file round-trips
// without data loss.
func scanTaskTokens(remainder string) (owner, startDate, dueDate string, priority int, pinned *bool, progress int, description string, extraTokens []string) {
	priority = 3 // default; 0 from the regex means "not set"
	progress = 0
	matches := TaskTokenRegex.FindAllStringSubmatch(remainder, -1)
	// Strip all [key:: value] tokens from the remainder to get the
	// description. Do this on the full remainder (not per-match) so the
	// regex's global replace handles overlapping/nested brackets safely.
	description = strings.TrimSpace(TaskTokenRegex.ReplaceAllString(remainder, ""))
	// Collapse multiple spaces left by token removal (e.g. "text  more"
	// after a token between them was stripped).
	description = whitespaceRun.ReplaceAllString(description, " ")

	for _, m := range matches {
		key := strings.ToLower(m[1])
		val := strings.TrimSpace(m[2])
		switch key {
		case "due":
			dueDate = normalizeDate(val)
		case "start":
			startDate = normalizeDate(val)
		case "owner", "o":
			owner = val
		case "priority", "p":
			if val != "" {
				fmt.Sscanf(val, "%d", &priority)
			}
		case "pin", "pinned":
			// Tri-state (#123): the token's PRESENCE is what matters —
			// any [pin:: ...] sets a non-nil pointer so the renderer can
			// distinguish "explicitly unpinned" (&false → [pin:: false])
			// from "no pin token" (nil → omit). Only explicit truthy
			// values ("true"/"yes"/"1") set &true; anything else (false,
			// "no", "0", empty, typos) sets &false. The renderer emits
			// exactly one pin token from the pointer, so toggling via the
			// UI can never produce two competing tokens.
			v := strings.ToLower(val)
			b := v == "true" || v == "yes" || v == "1"
			pinned = &b
		case "progress", "prog":
			if val != "" {
				fmt.Sscanf(val, "%d", &progress)
				if progress < 0 {
					progress = 0
				}
				if progress > 100 {
					progress = 100
				}
			}
		default:
			// Unrecognised key — preserve the full [key:: value] token
			// verbatim so it survives the parse → render round-trip.
			extraTokens = append(extraTokens, m[0])
		}
	}
	return
}

func ParseLine(line string, lineNumber int, spacesPerTab int) (ParsedBlock, string, bool) {
	blockID, blockFileDate, newLine, modified := EnsureBlockID(line)
	if blockID == "" {
		// Empty line, return empty note block
		return ParsedBlock{
			ID:         "",
			Type:       BlockNote,
			RawText:    line,
			CleanText:  "",
			LineNumber: lineNumber,
		}, line, false
	}

	cleanLine := CleanLineID(newLine)
	cleanLineTrimmed := strings.TrimSpace(cleanLine)

	// Check if it matches the GFM task checkbox pattern: `- [ ]`, `- [/]`, `- [x]`.
	// Apply to cleanLine (ID comment stripped) so the remainder fed to
	// scanTaskTokens does not contain the trailing <!-- id: ... --> comment.
	if matches := TaskCheckboxRegex.FindStringSubmatch(cleanLine); matches != nil {
		indent := matches[1]
		checkbox := matches[2]
		remainder := matches[3]

		// Determine status from checkbox state (GFM convention + Silt's [/] for DOING)
		status := "TODO"
		if checkbox == "/" {
			status = "DOING"
		} else if checkbox == "x" {
			status = "DONE"
		}

		// Scan for [key:: value] metadata tokens in the remainder.
		owner, startDate, dueDate, priority, pinned, progress, description, extraTokens := scanTaskTokens(remainder)

		depth := parseLeadingIndent(indent, spacesPerTab)

		return ParsedBlock{
			ID:         blockID,
			Type:       BlockTask,
			Depth:      depth,
			RawText:    newLine,
			CleanText:  description,
			Status:     status,
			Owner:      owner,
			StartDate:  startDate,
			DueDate:    dueDate,
			Priority:   priority,
			Pinned:      pinned,
			Progress:    progress,
			ExtraTokens: extraTokens,
			LineNumber:  lineNumber,
			FileDate:   blockFileDate,
		}, newLine, modified
	}

	// Check if it's a Header
	if strings.HasPrefix(cleanLineTrimmed, "#") {
		// Count header level
		level := 0
		for level < len(cleanLineTrimmed) && cleanLineTrimmed[level] == '#' {
			level++
		}
		// Must be followed by space or end of string
		if level < len(cleanLineTrimmed) && cleanLineTrimmed[level] == ' ' {
			headerText := cleanLineTrimmed[level+1:]
			return ParsedBlock{
				ID:         blockID,
				Type:       BlockHeader,
				Depth:      level,
				RawText:    newLine,
				CleanText:  strings.TrimSpace(headerText),
				LineNumber: lineNumber,
				FileDate:   blockFileDate,
			}, newLine, modified
		}
	}

	// Bullet note check (optional cleaning of bullet markers like "- ", "* ", "+ ", or numbered list prefixes "1. ", "1) ")
	depth := parseLeadingIndent(newLine, spacesPerTab)
	rawCleaned := cleanLineTrimmed
	if strings.HasPrefix(cleanLineTrimmed, "- ") || strings.HasPrefix(cleanLineTrimmed, "* ") || strings.HasPrefix(cleanLineTrimmed, "+ ") {
		rawCleaned = cleanLineTrimmed[2:]
	} else if m := NumberedListRegex.FindString(cleanLineTrimmed); m != "" {
		rawCleaned = cleanLineTrimmed[len(m):]
	}

	return ParsedBlock{
		ID:         blockID,
		Type:       BlockNote,
		Depth:      depth,
		RawText:    newLine,
		CleanText:  strings.TrimSpace(rawCleaned),
		LineNumber: lineNumber,
		FileDate:   blockFileDate,
	}, newLine, modified
}

// codeFenceLen returns the number of leading backticks on a (already
// TrimSpace'd) line, or 0 if it is not a fenced code boundary. GFM requires
// at least three backticks for a fence, and a closing fence must carry at
// least as many backticks as the opening fence — so callers compare
// `codeFenceLen(closer) >= openerLen` to find a matching close (this is what
// lets a code sample that itself contains a ``` line round-trip behind a
// longer outer fence).
func codeFenceLen(trimmedLine string) int {
	n := 0
	for n < len(trimmedLine) && trimmedLine[n] == '`' {
		n++
	}
	if n >= 3 {
		return n
	}
	return 0
}

// isCodeFence reports whether a (already TrimSpace'd) line is a GFM fenced
// code boundary — three or more backticks. Used by both ParseFileContent and
// RenderFileContent so the two paths agree on what counts as a fence.
func isCodeFence(trimmedLine string) bool {
	return codeFenceLen(trimmedLine) >= 3
}

// isClosingFence reports whether a (TrimSpace'd) line is a valid GFM CLOSING
// fence for an opener of openerLen backticks. A closer must have at least
// openerLen backticks AND nothing but whitespace after them — an info string
// (e.g. ```js) is allowed on an OPENER but disqualifies a closer. Without this
// check a 3-backtick block documenting another fence (```js) would close
// prematurely, silently corrupting the file. Openers are still detected with
// plain codeFenceLen (info strings are legal there).
func isClosingFence(trimmed string, openerLen int) bool {
	n := codeFenceLen(trimmed)
	return n >= openerLen && strings.TrimSpace(trimmed[n:]) == ""
}

// accumulateCodeRegion reads a fenced code block starting at lines[openIdx]
// (an opener fence) and returns:
//   - consumedTo: the index of the last consumed input line (the closer, or
//     the trailing id-comment line if one is present). The caller sets its
//     loop variable to this so the next iteration continues after the region.
//   - block: the assembled BlockCode (nil if the fence is unterminated — in
//     which case the opener is emitted verbatim and no block is produced,
//     matching the legacy passthrough for malformed input).
//   - emitLines: the lines to append to outputLines (opener..closer [+ id]).
//   - minted: true if a fresh block id was assigned (caller sets modifiedAny).
//
// The block id lives on its OWN line after the closing fence, never inside
// the code body, so the fence stays strictly GFM and code is never corrupted.
// A closing fence must have at least as many backticks as the opener (GFM),
// so an inner ``` line does not prematurely close a ```` outer fence.
func accumulateCodeRegion(lines []string, openIdx, lineNumber int, meta *FileMetadata) (consumedTo int, block *ParsedBlock, emitLines []string, minted bool) {
	openerTrim := strings.TrimSpace(lines[openIdx])
	openerLen := codeFenceLen(openerTrim)
	lang := strings.TrimSpace(openerTrim[openerLen:])

	// Find the closing fence: the first later line that is a valid GFM closer
	// for this opener (>= openerLen backticks, no info string). A ```js line
	// inside the block is NOT a closer (info strings are opener-only).
	closer := -1
	for j := openIdx + 1; j < len(lines); j++ {
		if isClosingFence(strings.TrimSpace(lines[j]), openerLen) {
			closer = j
			break
		}
	}
	if closer == -1 {
		// Unterminated fence: emit the opener verbatim, produce no block.
		// The caller continues at openIdx (loop i++ moves past it).
		return openIdx, nil, []string{lines[openIdx]}, false
	}

	inner := strings.Join(lines[openIdx+1:closer], "\n")

	// Peek for a DEDICATED trailing block-identity comment line right after
	// the closer. It must be a line that is solely the comment (trimmed starts
	// with "<!-- id:") — otherwise a normal prose block carrying its own id
	// comment would be mis-attributed as this code block's id line.
	idLineIdx := closer + 1
	blockID, blockFileDate := "", ""
	consumedIDLine := false
	if idLineIdx < len(lines) {
		cand := strings.TrimSpace(lines[idLineIdx])
		if strings.HasPrefix(cand, "<!-- id:") {
			if m := IDRegex.FindStringSubmatch(lines[idLineIdx]); len(m) > 1 {
				blockID = m[1]
				if len(m) > 2 {
					blockFileDate = m[2]
				}
				consumedIDLine = true
			}
		}
	}

	if blockID == "" {
		blockID = generateUUIDv4()
		minted = true
	}
	if blockFileDate == "" {
		blockFileDate = meta.Date
	}

	cb := ParsedBlock{
		ID:         blockID,
		Type:       BlockCode,
		Language:   lang,
		CleanText:  inner,
		LineNumber: lineNumber,
		FileDate:   blockFileDate,
	}

	emitLines = append(emitLines, lines[openIdx:closer+1]...)
	if consumedIDLine {
		emitLines = append(emitLines, lines[idLineIdx])
		return idLineIdx, &cb, emitLines, minted
	}
	// No existing id line: emit a minted one so the block has a stable identity.
	emitLines = append(emitLines, fmt.Sprintf("<!-- id: %s @ %s -->", blockID, blockFileDate))
	return closer, &cb, emitLines, minted
}

func ParseFileContent(content string, defaultNotebook, defaultSection, defaultPage, defaultDate string, spacesPerTab int) ([]ParsedBlock, FileMetadata, string, bool, error) {
	if spacesPerTab <= 0 {
		spacesPerTab = 4
	}

	lines := strings.Split(content, "\n")
	var meta FileMetadata
	meta.Notebook = defaultNotebook
	meta.Section = defaultSection
	meta.Page = defaultPage
	meta.Date = defaultDate

	hasFrontmatter := false
	frontmatterEndIdx := -1

	// Check for frontmatter
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		var fmLines []string
		for i := 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "---" {
				hasFrontmatter = true
				frontmatterEndIdx = i
				break
			}
			fmLines = append(fmLines, lines[i])
		}

		if hasFrontmatter {
			fmStr := strings.Join(fmLines, "\n")
			var parsedMeta FileMetadata
			if err := yaml.Unmarshal([]byte(fmStr), &parsedMeta); err == nil {
				if parsedMeta.Notebook != "" {
					meta.Notebook = parsedMeta.Notebook
				}
				if parsedMeta.Section != "" {
					meta.Section = parsedMeta.Section
				}
				if parsedMeta.Page != "" {
					meta.Page = parsedMeta.Page
				}
				if parsedMeta.Date != "" {
					meta.Date = normalizeDate(parsedMeta.Date)
				}
				if len(parsedMeta.Tags) > 0 {
					meta.Tags = parsedMeta.Tags
				}
			} else {
				// Surface the parse failure so the caller can warn the
				// user. Falling through with path-derived defaults would
				// silently lose the user's authored metadata.
				meta.Warnings = append(meta.Warnings, "yaml frontmatter parse error: "+err.Error())
			}
		}
	}

	var blocks []ParsedBlock
	var outputLines []string
	modifiedAny := false

	startIndex := 0
	if hasFrontmatter {
		startIndex = frontmatterEndIdx + 1
		// Add frontmatter lines back to output unmodified
		for i := 0; i <= frontmatterEndIdx; i++ {
			outputLines = append(outputLines, lines[i])
		}
	}

	// activeIDs tracks the most recent block ID at each indent level so we
	// can wire parent_id for nested blocks. We grow it dynamically instead
	// of fixing the size at 100, which previously caused silent parent_id
	// loss for any block past depth 99.
	activeIDs := []string{}

	for i := startIndex; i < len(lines); i++ {
		line := lines[i]
		lineNumber := i + 1

		// If it's the last line and empty, avoid creating a block but keep the line
		if i == len(lines)-1 && strings.TrimSpace(line) == "" {
			outputLines = append(outputLines, line)
			continue
		}

		// Fenced code region (#189): a ``` opener starts a managed BlockCode
		// spanning opener..closer. This generalizes the pre-existing verbatim
		// pass-through (which left code unmanaged and invisible to the editor)
		// so a code block is a first-class editable block. Content is still
		// preserved byte-for-byte — NO id comment is ever injected into the
		// code body. The block identity comment lives on its OWN line after
		// the closing fence so the fence stays strictly GFM (interoperable
		// with Obsidian / GitHub / VS Code).
		if isCodeFence(strings.TrimSpace(line)) {
			consumedTo, codeBlock, emitLines, minted := accumulateCodeRegion(
				lines, i, lineNumber, &meta,
			)
			if codeBlock != nil {
				if codeBlock.FileDate == "" {
					codeBlock.FileDate = meta.Date
				}
				blocks = append(blocks, *codeBlock)
			}
			if minted {
				modifiedAny = true
			}
			outputLines = append(outputLines, emitLines...)
			i = consumedTo
			continue
		}

		block, newLine, modified := ParseLine(line, lineNumber, spacesPerTab)
		if modified {
			modifiedAny = true
		}
		outputLines = append(outputLines, newLine)

		if block.ID != "" {
			// Backward-compat: blocks whose comment predates the per-block
			// file_date format (<!-- id: uuid --> with no @ date) inherit the
			// file-level default date (from frontmatter or path-derived).
			if block.FileDate == "" {
				block.FileDate = meta.Date
			}

			// Resolve Parent ID
			depth := block.Depth
			if depth > 0 && depth-1 < len(activeIDs) {
				block.ParentID = activeIDs[depth-1]
			}

			// Grow the stack so depth is always a valid index.
			if depth >= 0 {
				for len(activeIDs) <= depth {
					activeIDs = append(activeIDs, "")
				}
				activeIDs[depth] = block.ID
				// Clear deeper active IDs
				for d := depth + 1; d < len(activeIDs); d++ {
					activeIDs[d] = ""
				}
			}

			blocks = append(blocks, block)
		}
	}

	newContent := strings.Join(outputLines, "\n")
	return blocks, meta, newContent, modifiedAny, nil
}

// RenderFileContent is the canonical serializer for a Silt note file — the
// single source of truth for turning ParsedBlocks (plus frontmatter and any
// unmanaged prose) back into file content. Every writer (SaveFileBlocks,
// MutateBlock, CreatePage) goes through this function so the on-disk block
// format has exactly one definition and cannot drift between serializers.
//
//   - frontmatter is emitted verbatim. Pass the full frontmatter block
//     including its trailing newline (e.g. "---\n...\n---\n"), or "" for none.
//   - blocks is the authoritative ordered list of managed blocks to write.
//     Blocks without an ID are assigned a fresh UUIDv4 before rendering, so a
//     brand-new editor block reaches disk with a stable identity.
//   - originalBody is the file body with frontmatter already stripped, used
//     to preserve unmanaged lines (fenced code blocks, blank lines, prose
//     that never carried a managed block ID) in their relative position to
//     the managed blocks. Pass "" when there is nothing to preserve (e.g. a
//     brand-new page). Unmanaged lines attach to the managed block that
//     follows them; trailing unmanaged lines are appended after the last
//     block. Managed lines from originalBody whose IDs are no longer in
//     `blocks` are dropped (the block was deleted); lines that merely look
//     like a UUID comment but never parsed as a managed block are preserved.
//
// The per-block line format is produced by the package-internal renderBlock,
// which lives next to ParseLine so a format change has exactly one place to
// update. The round-trip identity tests in parser_test.go guarantee that
// ParseFileContent(RenderFileContent(ParseFileContent(src))) is stable.
func RenderFileContent(blocks []ParsedBlock, originalBody, frontmatter string, spacesPerTab int) string {
	if spacesPerTab <= 0 {
		spacesPerTab = 4
	}

	// Ensure every block reaches disk with a stable ID.
	for i := range blocks {
		if blocks[i].ID == "" {
			blocks[i].ID = generateUUIDv4()
		}
	}

	orderedByID := make(map[string]ParsedBlock, len(blocks))
	for _, b := range blocks {
		orderedByID[b.ID] = b
	}

	// Determine which IDs were managed in the original body so we can tell
	// "this UUID line was a managed block the user deleted" (drop it) from
	// "this UUID-shaped HTML comment is just prose the user typed" (keep it).
	// Without this distinction, quoting a commit hash in a note would silently
	// delete the line on the next save.
	oldManagedIDs := map[string]bool{}
	if originalBody != "" {
		oldBlocks, _, _, _, parseErr := ParseFileContent(originalBody, "", "", "", "", spacesPerTab)
		if parseErr == nil {
			for _, b := range oldBlocks {
				oldManagedIDs[b.ID] = true
			}
		}
	}

	// Walk the original body, bucketing unmanaged lines (code fences, blanks,
	// prose) by the managed block ID that follows them. This mirrors the
	// algorithm SaveFileBlocks used to inline, now centralized here so every
	// writer benefits from preserved user content.
	preservedBefore := make(map[string][]string)
	var pendingPreserved []string
	if originalBody != "" {
		bodyLines0 := strings.Split(originalBody, "\n")
		// Code-fence regions are now managed BlockCode blocks (renderBlock
		// re-emits them from `blocks`), so a fence run in the original body
		// must NOT be preserved — otherwise it would double-emit. We scan a
		// fence opener..closer (+ optional trailing id-comment line) and skip
		// the whole run. Unterminated fences fall back to verbatim preservation.
		for idx := 0; idx < len(bodyLines0); idx++ {
			line := bodyLines0[idx]
			trimmed := strings.TrimSpace(line)
			if isCodeFence(trimmed) {
				openerLen := codeFenceLen(trimmed)
				closer := -1
				for j := idx + 1; j < len(bodyLines0); j++ {
					if isClosingFence(strings.TrimSpace(bodyLines0[j]), openerLen) {
						closer = j
						break
					}
				}
				if closer == -1 {
					// Unterminated: preserve verbatim (legacy behaviour).
					pendingPreserved = append(pendingPreserved, line)
					continue
				}
				consumed := closer + 1
				// If the line right after the closer is this code block's id
				// comment, skip it too (renderBlock emits a fresh one). Mirror
				// accumulateCodeRegion's strict predicate (a dedicated id line)
				// so render and parse agree on the region boundary; a prose line
				// that merely ends in an id-shaped comment must NOT be consumed.
				if consumed < len(bodyLines0) {
					cand := strings.TrimSpace(bodyLines0[consumed])
					if strings.HasPrefix(cand, "<!-- id:") {
						if m := IDRegex.FindStringSubmatch(bodyLines0[consumed]); len(m) > 1 {
							consumed++
						}
					}
				}
				idx = consumed - 1
				continue
			}
			if trimmed == "" {
				pendingPreserved = append(pendingPreserved, line)
				continue
			}
			matches := IDRegex.FindStringSubmatch(line)
			if len(matches) > 1 {
				blockID := matches[1]
				if _, ok := orderedByID[blockID]; ok {
					if _, assigned := preservedBefore[blockID]; !assigned {
						preservedBefore[blockID] = append(preservedBefore[blockID], pendingPreserved...)
						pendingPreserved = nil
						continue
					}
				}
				if oldManagedIDs[blockID] {
					// Deleted managed block: drop it. Its pending unmanaged
					// lines stay pending for the next surviving block.
					continue
				}
			}
			pendingPreserved = append(pendingPreserved, line)
		}
	}

	// Emit frontmatter (verbatim) + woven body (preserved + rendered blocks).
	var bodyLines []string
	for _, b := range blocks {
		if pre, ok := preservedBefore[b.ID]; ok {
			bodyLines = append(bodyLines, pre...)
		}
		bodyLines = append(bodyLines, renderBlock(b, spacesPerTab))
	}
	bodyLines = append(bodyLines, pendingPreserved...)

	return frontmatter + strings.Join(bodyLines, "\n")
}

// renderBlock converts a single ParsedBlock back into its canonical markdown
// line. It is the sole block→line code path in the codebase (the only thing
// that produces on-disk block syntax), kept next to ParseLine so any format
// tweak has one place to update.
//
// Newly created editor blocks arrive with an empty RawText; they are emitted
// as "- " bullet notes so the outliner round-trips. Existing notes preserve
// their original bullet marker ("- ", "* ", "+ ") or plain-text style.
func renderBlock(block ParsedBlock, spacesPerTab int) string {
	if spacesPerTab <= 0 {
		spacesPerTab = 4
	}
	indent := strings.Repeat(" ", block.Depth*spacesPerTab)

	// Build ID suffix — includes per-block file_date if present:
	//   <!-- id: uuid @ YYYY-MM-DD -->
	idSuffix := ""
	if block.ID != "" {
		if block.FileDate != "" {
			idSuffix = fmt.Sprintf(" <!-- id: %s @ %s -->", block.ID, block.FileDate)
		} else {
			idSuffix = fmt.Sprintf(" <!-- id: %s -->", block.ID)
		}
	}

	// BlockCode is multi-line: the code body keeps its internal newlines (it
	// is NOT run through the `\n`→space collapse the prose blocks use). The
	// identity comment goes on its own line after the closing fence so the
	// fence stays strictly GFM (no trailing content) and the block is
	// interoperable with Obsidian / GitHub / VS Code (#189).
	if block.Type == BlockCode {
		// idSuffix for a code block is a leading "\n" + the comment (the
		// comment lives on its own line, not appended to the fence).
		idLine := ""
		if idSuffix != "" {
			idLine = "\n" + strings.TrimSpace(idSuffix)
		}
		// Fence length grows to 4 backticks if the body itself contains a
		// ``` line, so a code sample that includes a triple-backtick fence
		// round-trips without prematurely closing. Rare; correct.
		fence := "```"
		if strings.Contains("\n"+block.CleanText+"\n", "\n"+fence) {
			fence = "````"
			for strings.Contains("\n"+block.CleanText+"\n", "\n"+fence) {
				fence += "`"
			}
		}
		return fmt.Sprintf("%s%s\n%s\n%s%s", fence, block.Language, block.CleanText, fence, idLine)
	}

	if block.Type == BlockTask {
		checkbox := " "
		if block.Status == "DOING" {
			checkbox = "/"
		} else if block.Status == "DONE" {
			checkbox = "x"
		}

		// Build [key:: value] metadata tokens (Dataview inline metadata
		// format — see ARCHITECTURE.md §0 "Storage-of-Truth Tiers").
		// Each metadata field that is set gets its own [key:: value] token
		// appended after the description. The order is fixed: priority,
		// start, due, owner, pin, progress — matching the canonical
		// field order so a parse → render round trip is stable.
		var tokens []string
		if block.Priority > 0 && block.Priority != 3 {
			tokens = append(tokens, fmt.Sprintf("[priority:: %d]", block.Priority))
		}
		if block.StartDate != "" {
			tokens = append(tokens, fmt.Sprintf("[start:: %s]", block.StartDate))
		}
		if block.DueDate != "" {
			tokens = append(tokens, fmt.Sprintf("[due:: %s]", block.DueDate))
		}
		if block.Owner != "" {
			tokens = append(tokens, fmt.Sprintf("[owner:: %s]", block.Owner))
		}
		if block.Pinned != nil {
			if *block.Pinned {
				tokens = append(tokens, "[pin:: true]")
			} else {
				tokens = append(tokens, "[pin:: false]")
			}
		}
		if block.Progress > 0 {
			tokens = append(tokens, fmt.Sprintf("[progress:: %d]", block.Progress))
		}
		// Append unknown Dataview tokens verbatim so they survive the
		// round-trip (Dataview-compatible interop — SPECS.md §4.1).
		tokens = append(tokens, block.ExtraTokens...)

		tokenStr := ""
		if len(tokens) > 0 {
			tokenStr = " " + strings.Join(tokens, " ")
		}

		// - [checkbox] description [key:: value]... <!-- id: id -->
		return fmt.Sprintf("%s- [%s] %s%s%s",
			indent, checkbox,
			strings.ReplaceAll(block.CleanText, "\n", " "),
			tokenStr, idSuffix)
	} else if block.Type == BlockHeader {
		hashes := strings.Repeat("#", block.Depth)
		if hashes == "" {
			hashes = "#"
		}
		return fmt.Sprintf("%s %s%s", hashes, block.CleanText, idSuffix)
	} else {
		// BlockNote. Newly created blocks arrive with an empty RawText, so
		// default to the "- " bullet used by the outliner instead of
		// dropping the marker on every editor-created line.
		prefix := "- "
		trimmedRaw := strings.TrimSpace(block.RawText)
		if trimmedRaw != "" {
			if strings.HasPrefix(trimmedRaw, "- ") {
				prefix = "- "
			} else if strings.HasPrefix(trimmedRaw, "* ") {
				prefix = "* "
			} else if strings.HasPrefix(trimmedRaw, "+ ") {
				prefix = "+ "
			} else if m := NumberedListRegex.FindString(trimmedRaw); m != "" {
				prefix = m
			} else {
				prefix = ""
			}
		}
		return fmt.Sprintf("%s%s%s%s", indent, prefix,
			strings.ReplaceAll(block.CleanText, "\n", " "), idSuffix)
	}
}

