package parser

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var DateFileRegex = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.md$`)

type ScanResult struct {
	Path     string
	Notebook string
	Section  string
	Page     string
	Date     string
	Blocks   []ParsedBlock
	Tags     []string
	Warnings []string
	Err      error

	// MTime and Size are the file's modification time and byte size at scan
	// time. The DB's files table records them so a warm restart can skip
	// re-parsing any file whose mtime+size match the last successful index
	// (#29). Both are zero when the file could not be stat'd.
	MTime time.Time
	Size  int64
}

// ScanWorkspace scans the vault directory recursively and returns all parsed file blocks and metadata.
func ScanWorkspace(vaultPath string, spacesPerTab int) ([]ScanResult, error) {
	// 1. Gather all markdown files
	var files []string
	err := filepath.WalkDir(vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip system and hidden directories
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only parse markdown files
		if strings.ToLower(filepath.Ext(path)) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan directories: %w", err)
	}

	if len(files) == 0 {
		return nil, nil
	}

	// 2. Parse files in parallel using a worker pool
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	if numWorkers > len(files) {
		numWorkers = len(files)
	}

	pathsChan := make(chan string, len(files))
	for _, f := range files {
		pathsChan <- f
	}
	close(pathsChan)

	resultsChan := make(chan ScanResult, len(files))
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range pathsChan {
				res := parseSingleFile(path, vaultPath, spacesPerTab)
				resultsChan <- res
			}
		}()
	}

	wg.Wait()
	close(resultsChan)

	// Collect results
	var results []ScanResult
	for res := range resultsChan {
		results = append(results, res)
	}

	return results, nil
}

func parseSingleFile(path string, vaultPath string, spacesPerTab int) ScanResult {
	res := ScanResult{Path: path}

	// 1. Resolve default notebook, section, page, and date from file path.
	//
	// Hierarchy (Section is optional, nesting can be deeper than one level):
	//   <vault>/<notebook>/[<section>/...]<page>/<file>.md
	//   - notebook = the top-level folder under the vault
	//   - page     = the folder directly containing the file (streaming unit)
	//   - section  = the path between notebook and page ("" when the page
	//                lives directly under the notebook; one or more segments
	//                otherwise, joined by "/")
	relPath, err := filepath.Rel(vaultPath, path)
	if err != nil {
		res.Err = err
		return res
	}

	relPathClean := filepath.ToSlash(relPath)
	parts := strings.Split(relPathClean, "/")
	filename := parts[len(parts)-1]

	var notebook, section, page string
	// ancestors are the path segments excluding the filename itself.
	ancestors := parts[:len(parts)-1]
	if len(ancestors) >= 2 {
		notebook = ancestors[0]
		page = ancestors[len(ancestors)-1]
		if len(ancestors) > 2 {
			section = strings.Join(ancestors[1:len(ancestors)-1], "/")
		}
	} else {
		// Files must live at least two levels beneath the vault root
		// (vault/notebook/page/file.md); anything shallower (e.g. a stray
		// .md directly inside a notebook folder) is a layout error we
		// surface rather than silently mis-bucket.
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipped %q: expected to live under <vault>/<notebook>/[<section>/]<page>/", relPathClean))
		return res
	}

	// Extract date from filename if possible, otherwise use modification date
	dateStr := ""
	// info captures the file's stat for both the date fallback and the
	// MTime/Size fields used by the incremental re-indexer (#29).
	var info os.FileInfo
	if matches := DateFileRegex.FindStringSubmatch(filename); len(matches) > 1 {
		dateStr = matches[1]
	} else {
		// Use modification time
		var err error
		info, err = os.Stat(path)
		if err == nil {
			dateStr = info.ModTime().Format("2006-01-02")
		} else {
			dateStr = time.Now().Format("2006-01-02")
		}
	}

	// Stat the file (if not already) to populate MTime/Size for the
	// incremental re-indexer. A failure here is non-fatal: the file is still
	// parsed, it just won't be skippable on the next restart.
	if info == nil {
		if st, err := os.Stat(path); err == nil {
			info = st
		}
	}
	if info != nil {
		res.MTime = info.ModTime()
		res.Size = info.Size()
	}

	// 2. Read and parse file content
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		res.Err = err
		return res
	}

	blocks, meta, newContent, modified, err := ParseFileContent(string(contentBytes), notebook, section, page, dateStr, spacesPerTab)
	if err != nil {
		res.Err = err
		return res
	}
	res.Warnings = meta.Warnings

	// 3. Write back atomically if modified (i.e. UUIDs injected)
	if modified {
		if err := WriteFileAtomic(path, []byte(newContent)); err != nil {
			res.Err = fmt.Errorf("failed to write file atomically: %w", err)
			return res
		}
	}

	res.Notebook = meta.Notebook
	res.Section = meta.Section
	res.Page = meta.Page
	res.Date = meta.Date
	res.Blocks = blocks
	res.Tags = meta.Tags

	return res
}
