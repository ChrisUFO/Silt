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
	// Source is the `blocks.source` discriminator ('vault' for the vault
	// startup scan, or 'linked:<id>' for a batched linked-tree scan, #134).
	// Empty defaults to 'vault' in IndexScanResults so the vault startup scan
	// path (which does not set this field) is unchanged.
	Source   string
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

// WalkMarkdown recursively enumerates the .md files under root, with explicit
// symlink and hidden-directory handling shared by the scanner (#32).
//
// filepath.WalkDir does not follow symlinks, so a symlink loop (e.g.
// Work -> ../Work) cannot cause infinite recursion on its own. We make the
// skip explicit AND surface it as a warning so the user knows the symlinked
// directory's contents are not indexed (and so a symlink pointing outside the
// vault is skipped deliberately rather than silently). System/hidden
// directories (leading ".") are pruned.
//
// Returns the markdown file list, non-fatal warnings (per symlink/perms
// errors), and a fatal error only if the root cannot be walked at all.
func WalkMarkdown(root string) (files []string, warnings []string, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Surface walker errors (permission denied, removed during
			// traversal, etc.) as warnings rather than aborting the whole
			// scan — one unreadable subtree shouldn't block startup.
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, walkErr))
			return nil
		}
		// Explicit symlink handling: WalkDir already does not follow them,
		// but skipping silently leaves the user wondering why a symlinked
		// folder's notes never appear. Warn so it's discoverable.
		if d.Type()&fs.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf("%s: symlink not followed", path))
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip system and hidden directories (but keep the root itself
			// when its name legitimately starts with "." — the root is the
			// entry point, not a child to prune).
			if path != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	return files, warnings, err
}

// ScanWorkspace scans the vault directory recursively and returns all parsed
// file blocks and metadata, plus walk-level warnings (symlink skips,
// permission errors) for the caller to surface as init-warnings (#32).
func ScanWorkspace(vaultPath string, spacesPerTab int) (results []ScanResult, walkWarnings []string, err error) {
	// 1. Gather all markdown files via the shared symlink-aware walker.
	files, walkWarnings, err := WalkMarkdown(vaultPath)
	if err != nil {
		return nil, walkWarnings, fmt.Errorf("failed to scan directories: %w", err)
	}

	if len(files) == 0 {
		return nil, walkWarnings, nil
	}

	// 2. Parse files in parallel using a worker pool
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	if numWorkers > len(files) {
		numWorkers = len(files)
	}
	if numWorkers < 1 {
		numWorkers = 1
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
	collected := make([]ScanResult, 0, len(files))
	for res := range resultsChan {
		collected = append(collected, res)
	}

	return collected, walkWarnings, nil
}

func parseSingleFile(path string, vaultPath string, spacesPerTab int) ScanResult {
	res := ScanResult{Path: path}

	// 1. Resolve default notebook, section, page from file path.
	//
	// New file model (post per-day removal): a page IS a file, not a directory.
	//   <vault>/<notebook>/[<section>/...]<page>.md
	//   - notebook = the top-level folder under the vault
	//   - page     = the filename without .md
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

	// Strip the .md extension to get the page name.
	pageName := filename
	if strings.HasSuffix(strings.ToLower(pageName), ".md") {
		pageName = pageName[:len(pageName)-3]
	}

	var notebook, section, page string
	// ancestors are the path segments excluding the filename itself.
	ancestors := parts[:len(parts)-1]
	if len(ancestors) >= 1 {
		notebook = ancestors[0]
		page = pageName
		if len(ancestors) > 1 {
			section = strings.Join(ancestors[1:], "/")
		}
	} else {
		// Files directly in the vault root (no notebook) are a layout error.
		res.Warnings = append(res.Warnings, fmt.Sprintf("skipped %q: expected to live under <vault>/<notebook>/[<section>/]", relPathClean))
		return res
	}

	// Date: no longer extracted from the filename. Each block carries its own
	// file_date in the trailing comment. The file-level default (used when a
	// block's comment has no date) falls back to the file's modification time,
	// which is a reasonable proxy for "when was this content written."
	var info os.FileInfo
	info, err = os.Stat(path)
	if err == nil {
		res.MTime = info.ModTime()
		res.Size = info.Size()
	} else {
		info = nil
	}

	dateStr := ""
	if info != nil {
		dateStr = info.ModTime().Format("2006-01-02")
	} else {
		dateStr = time.Now().Format("2006-01-02")
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
