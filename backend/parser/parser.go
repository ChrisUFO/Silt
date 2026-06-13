package parser

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// TaskRegex captures:
// 1: Indentation
// 2: Checkbox state marker
// 3: Task status keyword (TODO|DOING|DONE)
// 4: Owner (optional, inside [owner])
// 5: Dates (optional, inside (start, due) or (due))
// 6: Priority (optional, after #)
// 7: Remainder description (which may contain the UUID comment at the end)
var TaskRegex = regexp.MustCompile(`^([\s]*)-\s\[([ x/])\]\s(TODO|DOING|DONE)\sTASK(?:\s\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$`)

var IDRegex = regexp.MustCompile(`<!-- id: ([a-f0-9\-]{36}) -->\s*$`)

func generateUUIDv4() string {
	return uuid.New().String()
}

func EnsureBlockID(line string) (string, string, bool) {
	clean := strings.TrimSpace(line)
	if clean == "" {
		return "", line, false
	}
	matches := IDRegex.FindStringSubmatch(line)
	if len(matches) > 1 {
		return matches[1], line, false
	}

	newID := generateUUIDv4()
	cleanLine := strings.TrimRight(line, "\r\n")
	newLine := fmt.Sprintf("%s <!-- id: %s -->", cleanLine, newID)
	return newID, newLine, true
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

func ParseLine(line string, lineNumber int, spacesPerTab int) (ParsedBlock, string, bool) {
	blockID, newLine, modified := EnsureBlockID(line)
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

	// Check if it matches TaskRegex
	if matches := TaskRegex.FindStringSubmatch(newLine); matches != nil {
		indent := matches[1]
		checkbox := matches[2]
		// keyword := matches[3] // e.g. TODO, DOING, DONE
		owner := matches[4]
		dates := matches[5]
		priorityStr := matches[6]
		description := matches[7]

		// Determine status from checkbox state
		status := "TODO"
		if checkbox == "/" {
			status = "DOING"
		} else if checkbox == "x" {
			status = "DONE"
		}

		// Parse dates
		var startDate, dueDate string
		if dates != "" {
			dateParts := strings.Split(dates, ",")
			if len(dateParts) == 2 {
				startDate = normalizeDate(dateParts[0])
				dueDate = normalizeDate(dateParts[1])
			} else if len(dateParts) == 1 {
				dueDate = normalizeDate(dateParts[0])
			}
		}

		// Parse priority
		priority := 3 // default
		if priorityStr != "" {
			fmt.Sscanf(priorityStr, "%d", &priority)
		}

		depth := parseLeadingIndent(indent, spacesPerTab)

		return ParsedBlock{
			ID:         blockID,
			Type:       BlockTask,
			Depth:      depth,
			RawText:    newLine,
			CleanText:  strings.TrimSpace(CleanLineID(description)),
			Status:     status,
			Owner:      strings.TrimSpace(owner),
			StartDate:  startDate,
			DueDate:    dueDate,
			Priority:   priority,
			LineNumber: lineNumber,
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
			}, newLine, modified
		}
	}

	// Bullet note check (optional cleaning of bullet markers like "- ", "* ", "+ ")
	depth := parseLeadingIndent(newLine, spacesPerTab)
	rawCleaned := cleanLineTrimmed
	if strings.HasPrefix(cleanLineTrimmed, "- ") || strings.HasPrefix(cleanLineTrimmed, "* ") || strings.HasPrefix(cleanLineTrimmed, "+ ") {
		rawCleaned = cleanLineTrimmed[2:]
	}

	return ParsedBlock{
		ID:         blockID,
		Type:       BlockNote,
		Depth:      depth,
		RawText:    newLine,
		CleanText:  strings.TrimSpace(rawCleaned),
		LineNumber: lineNumber,
	}, newLine, modified
}

func ParseFileContent(content string, defaultNotebook, defaultSection, defaultDate string, spacesPerTab int) ([]ParsedBlock, FileMetadata, string, bool, error) {
	if spacesPerTab <= 0 {
		spacesPerTab = 4
	}

	lines := strings.Split(content, "\n")
	var meta FileMetadata
	meta.Notebook = defaultNotebook
	meta.Section = defaultSection
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
				if parsedMeta.Date != "" {
					meta.Date = normalizeDate(parsedMeta.Date)
				}
				if len(parsedMeta.Tags) > 0 {
					meta.Tags = parsedMeta.Tags
				}
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
	inCodeBlock := false

	for i := startIndex; i < len(lines); i++ {
		line := lines[i]
		lineNumber := i + 1

		// If it's the last line and empty, avoid creating a block but keep the line
		if i == len(lines)-1 && strings.TrimSpace(line) == "" {
			outputLines = append(outputLines, line)
			continue
		}

		// Track fenced code block state. Lines inside ``` blocks are passed
		// through verbatim — we must not inject block IDs into code samples,
		// HTML, or other preformatted content.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
			outputLines = append(outputLines, line)
			continue
		}
		if inCodeBlock {
			outputLines = append(outputLines, line)
			continue
		}

		block, newLine, modified := ParseLine(line, lineNumber, spacesPerTab)
		if modified {
			modifiedAny = true
		}
		outputLines = append(outputLines, newLine)

		if block.ID != "" {
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
