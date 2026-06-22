package vault

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"silt/backend/parser"
)

// MigratePerDayFiles converts old-model per-day files (<page>/<date>.md) into
// the new single-file-per-page model (<page>.md). For each directory that
// contains files matching YYYY-MM-DD.md:
//  1. Read all date files sorted by filename (= by date).
//  2. Parse each file's blocks, tagging each block with the original file's date.
//  3. Concatenate into a single document and render to <parent>/<dirname>.md.
//  4. Remove the old directory.
//
// Idempotent: if no per-date directories exist, this is a no-op. If the target
// <page>.md already exists, that directory is skipped (user may have migrated
// manually). Returns non-fatal warnings for the caller to surface.
func MigratePerDayFiles(vaultPath string, spacesPerTab int) []string {
	var warnings []string

	rootAbs, err := filepath.Abs(vaultPath)
	if err != nil {
		return []string{fmt.Sprintf("migration: cannot resolve vault path: %v", err)}
	}

	// Collect directories that contain date-named .md files.
	type pageDir struct {
		dir       string
		dateFiles []string // sorted filenames
	}
	var pageDirs []pageDir

	_ = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("migration: cannot traverse %q: %v", path, walkErr))
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == rootAbs {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(rootAbs, path)
		if err != nil {
			return nil
		}
		segments := strings.Split(filepath.ToSlash(rel), "/")
		if len(segments) < 2 {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		var dates []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if parser.DateFileRegex.MatchString(e.Name()) {
				dates = append(dates, e.Name())
			}
		}
		if len(dates) > 0 {
			sort.Strings(dates)
			pageDirs = append(pageDirs, pageDir{dir: path, dateFiles: dates})
		}
		return nil
	})

	for _, pd := range pageDirs {
		pageName := filepath.Base(pd.dir)
		targetPath := filepath.Join(filepath.Dir(pd.dir), pageName+".md")

		// Skip if the target already exists (user may have migrated).
		if _, err := os.Stat(targetPath); err == nil {
			warnings = append(warnings, fmt.Sprintf("migration: skipped %q — target %q already exists", pd.dir, targetPath))
			continue
		}

		var allBlocks []parser.ParsedBlock
		var frontmatter string
		for _, dateFile := range pd.dateFiles {
			dateFilePath := filepath.Join(pd.dir, dateFile)
			contentBytes, err := os.ReadFile(dateFilePath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("migration: cannot read %q: %v", dateFilePath, err))
				continue
			}
			dateStr := parser.DateFileRegex.FindStringSubmatch(dateFile)[1]
			notebook, section := "", ""
			relParts := strings.Split(strings.Trim(filepath.ToSlash(strings.TrimPrefix(pd.dir, rootAbs)), "/"), "/")
			if len(relParts) >= 1 {
				notebook = relParts[0]
				if len(relParts) > 2 {
					section = strings.Join(relParts[1:len(relParts)-1], "/")
				}
			}

			blocks, _, _, _, parseErr := parser.ParseFileContent(string(contentBytes), notebook, section, pageName, dateStr, spacesPerTab)
			if parseErr != nil {
				warnings = append(warnings, fmt.Sprintf("migration: parse error in %q: %v", dateFilePath, parseErr))
				continue
			}

			// Stamp each block with the original file's date.
			for i := range blocks {
				if blocks[i].FileDate == "" {
					blocks[i].FileDate = dateStr
				}
			}

			if frontmatter == "" {
				fm, _ := parser.SplitFrontmatter(string(contentBytes))
				if fm != "" {
					frontmatter = fm
				} else {
					frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n",
						strconv.Quote(notebook), strconv.Quote(section), strconv.Quote(pageName), strconv.Quote(dateStr))
				}
			}

			allBlocks = append(allBlocks, blocks...)
		}

		if len(allBlocks) == 0 {
			warnings = append(warnings, fmt.Sprintf("migration: no blocks found in %q, skipping", pd.dir))
			continue
		}

		// Render the merged content and write the new page file.
		mergedContent := parser.RenderFileContent(allBlocks, "", frontmatter, spacesPerTab)
		if err := parser.WriteFileAtomic(targetPath, []byte(mergedContent)); err != nil {
			warnings = append(warnings, fmt.Sprintf("migration: cannot write %q: %v", targetPath, err))
			continue
		}

		// Verify the merged file parses correctly before destroying the
		// originals. A partial/corrupt write must NOT trigger removal.
		verifyBlocks, _, _, _, verifyErr := parser.ParseFileContent(mergedContent, "", "", "", "", spacesPerTab)
		if verifyErr != nil || len(verifyBlocks) != len(allBlocks) {
			warnings = append(warnings, fmt.Sprintf("migration: verification failed for %q (%d/%d blocks) — keeping originals", targetPath, len(verifyBlocks), len(allBlocks)))
			_ = os.Remove(targetPath)
			continue
		}

		// Remove the migrated date files individually (verified safe).
		for _, dateFile := range pd.dateFiles {
			_ = os.Remove(filepath.Join(pd.dir, dateFile))
		}
		// Remove the old directory only if it is now empty.
		if err := os.Remove(pd.dir); err != nil {
			warnings = append(warnings, fmt.Sprintf("migration: wrote %q and removed migrated files, but kept directory %q (may contain other files): %v", targetPath, pd.dir, err))
		}

		warnings = append(warnings, fmt.Sprintf("migration: merged %d date files from %q into %q", len(pd.dateFiles), pd.dir, targetPath))
	}

	return warnings
}
