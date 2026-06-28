package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"silt/backend/db"
	"silt/backend/parser"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// errBlockBeingEdited is returned by MutateBlock when the target file is
// focus-locked (a user is editing it in another view). Callers retry rather
// than silently overwriting the in-flight edit.
var errBlockBeingEdited = fmt.Errorf("block is being edited in another view")

// FetchPageBlocks returns a flat list of all blocks for a page, ordered by
// line_number. A page is a single file; each block carries its own file_date.
// The notebook's source is resolved server-side from its (globally-unique)
// name so a linked notebook sharing a display name with a vault notebook
// returns its own page (#100).
func (a *App) FetchPageBlocks(notebook, section, page string) ([]parser.ParsedBlock, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	source := a.resolveSourceByName(sanitizePathSegment(notebook))
	var res []parser.ParsedBlock
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.FetchPageBlocks(source, notebook, section, page)
	})

	return res, err
}

// DistinctOwners returns the sorted, de-duplicated set of task owners in the
// vault — the source for the @-mention typeahead (#184). Read-only projection
// of the tasks index; no mention state is persisted to SQLite.
func (a *App) DistinctOwners() ([]string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []string
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.DistinctOwners()
	})

	return res, err
}

// UpdateBlockState changes task status and updates the file and cache.
//
// To avoid TOCTOU races between the DB read and the file write, we look up the
// block's UUID, file metadata, and the lock by file path, then re-locate the
// target line inside the file write lock by scanning for the UUID comment. The
// UUID is the source of truth for the target line, not the cached line number.
func (a *App) UpdateBlockState(blockID string, newState string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	// Guard against a meaningless no-op that the frontend might interpret
	// as an error. The only valid task status values are TODO, DOING, DONE.
	switch newState {
	case "TODO", "DOING", "DONE":
	default:
		return fmt.Errorf("invalid target status: %s (valid: TODO, DOING, DONE)", newState)
	}

	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var loc db.BlockLocation
	err := a.coordinator.WithDBReadResult(func() error {
		var e error
		loc, e = a.db.GetBlockLocation(blockID)
		return e
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}
	notebook, section, page, blockType := loc.Notebook, loc.Section, loc.Page, loc.BlockType

	if blockType != string(parser.BlockTask) {
		return fmt.Errorf("block %s is not a task", blockID)
	}

	// Defense-in-depth against path traversal: notebook/section/page originate
	// from user-editable YAML frontmatter. Section may be empty (a page living
	// directly under its notebook).
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid file metadata for block %s: notebook=%q section=%q page=%q", blockID, notebook, section, page)
	}
	// Resolve the notebook content dir from the block's source (#100): vault
	// blocks live under <vault>/<notebook>, linked blocks under their root.
	notebookDir, err := a.resolveNotebookDir(safeNotebook, loc.Source)
	if err != nil {
		return fmt.Errorf("resolve notebook dir for block %s: %w", blockID, err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("resolved file path %q escapes notebook root %q", filePath, notebookDir)
	}

	var writeErr error
	a.coordinator.LockBlockWrite(blockID, func() {
		a.coordinator.LockFileWrite(filePath, func() {
			contentBytes, err := os.ReadFile(filePath)
			if err != nil {
				writeErr = err
				return
			}

			// Parse the whole file, flip the target task's status in the parsed
			// slice, then re-render through the single serializer. This keeps
			// UpdateBlockState on the same write path as every other writer
			// (one on-disk format definition) and preserves unmanaged lines via
			// the original body.
			// Use the file's modification time as the default date for blocks
			// whose comment lacks a @ date suffix — matches the scanner's behavior.
			// Using time.Now() here would silently shift old blocks' dates to today.
			fileDate := fileOrDefaultDate(filePath)
			parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, fileDate, a.spacesPerTab)
			if parseErr != nil {
				writeErr = fmt.Errorf("failed to parse file for state update: %w", parseErr)
				return
			}
			found := false
			for i := range parsedBlocks {
				if parsedBlocks[i].ID == blockID {
					if parsedBlocks[i].Type != parser.BlockTask {
						writeErr = fmt.Errorf("block %s is not a task", blockID)
						return
					}
					parsedBlocks[i].Status = newState
					found = true
					break
				}
			}
			if !found {
				writeErr = fmt.Errorf("block %s not found in file %s", blockID, filePath)
				return
			}

			frontmatter, body := parser.SplitFrontmatter(string(contentBytes))
			if frontmatter == "" {
				today := time.Now().Format("2006-01-02")
				frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(today))
				body = string(contentBytes)
			}

			newContent := parser.RenderFileContent(parsedBlocks, body, frontmatter, a.spacesPerTab)

			a.tracker.RegisterWrite(filePath)

			if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
				writeErr = err
				return
			}

			// Re-parse with the sanitized metadata so the re-indexed row
			// uses the same cleaned values that went into the file path.
			blocks, remeta, _, _, err := parser.ParseFileContent(newContent, meta.Notebook, meta.Section, meta.Page, meta.Date, a.spacesPerTab)
			if err == nil {
				var idxErr error
				a.coordinator.WithDBWrite(func() {
					idxErr = a.db.IndexFileBlocks(loc.Source, remeta.Notebook, remeta.Section, remeta.Page, blocks, remeta.Tags, remeta.Warnings...)
				})
				if idxErr != nil {
					log.Printf("UpdateBlockState: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
				}
			}
		})
	}) // LockBlockWrite

	if writeErr != nil {
		return writeErr
	}
	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, "")
	return nil
}

// QueryTasks retrieves indexed items matching the active filters.
func (a *App) QueryTasks(filter parser.TaskQueryFilter) ([]parser.TaskResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res []parser.TaskResult
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.QueryTasksWithFilters(filter)
	})

	return res, err
}

// emitBlockChanged broadcasts a block:changed event so live embeds/references
// refresh whenever a block is mutated through any code path.
func (a *App) emitBlockChanged(id, notebook, section, page, fileDate string) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "block:changed", parser.BlockChangedEvent{
		ID: id, Notebook: notebook, Section: section, Page: page, FileDate: fileDate,
	})
}

// ResolveBlockReference looks up a ((uuid)) reference, returning its content
// and location for hover previews and scroll-to-source navigation. Missing
// UUIDs return Exists=false (no error) so the UI can render a broken-link chip.
func (a *App) ResolveBlockReference(blockID string) (parser.BlockReference, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	ref := parser.BlockReference{ID: blockID}
	if a.db == nil {
		return ref, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()

	err := a.coordinator.WithDBReadResult(func() error {
		row := a.db.SQLDB().QueryRow(
			"SELECT type, raw_content, clean_content, notebook, section, page, file_date, line_number FROM blocks WHERE id = ?",
			blockID,
		)
		var bType, raw, clean, notebook, section, page, fileDate string
		var ln int
		if err := row.Scan(&bType, &raw, &clean, &notebook, &section, &page, &fileDate, &ln); err != nil {
			return nil // not found → Exists stays false
		}
		ref.Exists = true
		ref.Type = bType
		ref.RawText = raw
		ref.CleanText = clean
		ref.Notebook = notebook
		ref.Section = section
		ref.Page = page
		ref.FileDate = fileDate
		ref.LineNumber = ln
		return nil
	})
	return ref, err
}

// MutateBlock rewrites the body text of a block (identified by UUID) in its
// source file, preserving the leading task/header/bullet syntax and the
// trailing <!-- id --> comment. It re-indexes the file and emits block:changed
// so live embeds/references stay in sync.
func (a *App) MutateBlock(blockID, newText string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	// Block text is single-line; collapse any newlines to spaces.
	cleanText := strings.ReplaceAll(newText, "\n", " ")

	a.wg.Add(1)
	defer a.wg.Done()

	var loc db.BlockLocation
	err := a.coordinator.WithDBReadResult(func() error {
		var e error
		loc, e = a.db.GetBlockLocation(blockID)
		return e
	})
	if err != nil {
		return fmt.Errorf("block %s not found in SQLite: %w", blockID, err)
	}
	notebook, section, page := loc.Notebook, loc.Section, loc.Page

	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("invalid file metadata for block %s", blockID)
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, loc.Source)
	if err != nil {
		return fmt.Errorf("resolve notebook dir for block %s: %w", blockID, err)
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("resolved file path %q escapes notebook root %q", filePath, notebookDir)
	}

	var writeErr error
	a.coordinator.LockBlockWrite(blockID, func() {
		a.coordinator.LockFileWrite(filePath, func() {
			// Don't clobber a block the user is actively editing in another view
			// (the timeline editor holds a focus lock on the file while focused).
			// Refuse rather than silently overwrite; callers (e.g. EmbedPortal)
			// retry once the editor releases the lock.
			if a.watcher != nil && a.watcher.IsFocusLocked(filePath) {
				writeErr = errBlockBeingEdited
				return
			}
			contentBytes, err := os.ReadFile(filePath)
			if err != nil {
				writeErr = err
				return
			}

			// Parse the whole file, mutate the target block in the slice, then
			// re-render through the single serializer (RenderFileContent). This
			// preserves unmanaged lines (code fences, prose) via the original
			// body and keeps MutateBlock on the same write path as every other
			// writer, so there is one on-disk format definition.
			// Use the file's modification time as the default date for blocks
			// whose comment lacks a @ date suffix — matches the scanner's behavior.
			fileDate := fileOrDefaultDate(filePath)
			parsedBlocks, meta, _, _, parseErr := parser.ParseFileContent(string(contentBytes), safeNotebook, safeSection, safePage, fileDate, a.spacesPerTab)
			if parseErr != nil {
				writeErr = fmt.Errorf("failed to parse file for mutation: %w", parseErr)
				return
			}
			found := false
			for i := range parsedBlocks {
				if parsedBlocks[i].ID == blockID {
					parsedBlocks[i].CleanText = cleanText
					found = true
					break
				}
			}
			if !found {
				writeErr = fmt.Errorf("block %s not found in file %s", blockID, filePath)
				return
			}

			frontmatter, body := parser.SplitFrontmatter(string(contentBytes))
			if frontmatter == "" {
				frontmatter = fmt.Sprintf("---\nnotebook: %s\nsection: %s\npage: %s\ndate: %s\ntags: []\n---\n", strconv.Quote(safeNotebook), strconv.Quote(safeSection), strconv.Quote(safePage), strconv.Quote(time.Now().Format("2006-01-02")))
			}

			newContent := parser.RenderFileContent(parsedBlocks, body, frontmatter, a.spacesPerTab)

			a.tracker.RegisterWrite(filePath)
			if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
				writeErr = err
				return
			}

			// Re-parse the rendered output and reindex so the cache reflects the
			// canonical on-disk state (RenderFileContent may have normalized the
			// mutated line's format).
			reblocks, remeta, _, _, err := parser.ParseFileContent(newContent, meta.Notebook, meta.Section, meta.Page, meta.Date, a.spacesPerTab)
			if err == nil {
				var idxErr error
				a.coordinator.WithDBWrite(func() {
					idxErr = a.db.IndexFileBlocks(loc.Source, remeta.Notebook, remeta.Section, remeta.Page, reblocks, remeta.Tags, remeta.Warnings...)
				})
				if idxErr != nil {
					log.Printf("MutateBlock: IndexFileBlocks failed for %s/%s/%s/%s: %v", remeta.Notebook, remeta.Section, remeta.Page, remeta.Date, idxErr)
				}
			}
		})
	}) // LockBlockWrite
	if writeErr != nil {
		return writeErr
	}

	a.emitBlockChanged(blockID, safeNotebook, safeSection, safePage, "")
	return nil
}
