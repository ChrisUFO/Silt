package main

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"silt/backend/config"
	"silt/backend/parser"
	"strconv"
	"strings"
	"time"
)

// --- Rename / Delete lifecycle (#62, #83) ---------------------------------

// trashBase returns the .system/trash directory path.
func (a *App) trashBase() string {
	return filepath.Join(a.vaultPath, ".system", "trash")
}

// moveToTrash moves a file or directory to .system/trash/<timestamp>/<relPath>,
// preserving the relative structure so the user can recover it. Returns the
// trash destination path. The caller MUST guard with isPathWithinRoot.
func (a *App) moveToTrash(source string) (string, error) {
	rel, err := filepath.Rel(a.vaultPath, source)
	if err != nil {
		return "", fmt.Errorf("cannot compute relative path: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	dest := filepath.Join(a.trashBase(), ts, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("failed to create trash directory: %w", err)
	}
	if err := os.Rename(source, dest); err != nil {
		return "", fmt.Errorf("failed to move to trash: %w", err)
	}
	return dest, nil
}

// reindexFile reads, parses, and indexes a single .md file at the given path.
// Used by rename operations where the file content changed (frontmatter) or
// the path changed (folder rename). The caller MUST hold the file lock.
func (a *App) reindexFile(filePath, notebook, section, page string) {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("reindexFile: failed to read %s: %v", filePath, err)
		return
	}
	content := string(contentBytes)
	blocks, meta, _, _, parseErr := parser.ParseFileContent(
		content, notebook, section, page,
		fileOrDefaultDate(filePath), a.spacesPerTab,
	)
	if parseErr != nil {
		log.Printf("reindexFile: parse failed for %s: %v", filePath, parseErr)
		return
	}
	var idxErr error
	reidxSource := a.resolveSourceByName(notebook)
	a.coordinator.WithDBWrite(func() {
		idxErr = a.db.IndexFileBlocks(reidxSource, meta.Notebook, meta.Section, meta.Page, blocks, meta.Tags, meta.Warnings...)
	})
	if idxErr != nil {
		log.Printf("reindexFile: index failed for %s/%s/%s: %v", meta.Notebook, meta.Section, meta.Page, idxErr)
	}
	// Emit block:changed so live embeds/references refresh.
	for _, b := range blocks {
		if b.ID != "" {
			a.emitBlockChanged(b.ID, meta.Notebook, meta.Section, meta.Page, b.FileDate)
		}
	}
}

// updateFrontmatterField rewrites a single YAML key in the frontmatter block.
// It performs a simple line-based replacement of `key: "old"` → `key: "new"`.
// The caller MUST hold the file lock and call tracker.RegisterWrite.
func updateFrontmatterField(content, key, newVal string) string {
	lines := strings.Split(content, "\n")
	inFM := false
	closeIdx := -1
	found := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if !inFM {
				inFM = true
				continue
			}
			closeIdx = i
			break // closing ---
		}
		if inFM {
			prefix := key + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", key, strconv.Quote(newVal))
				found = true
				break
			}
		}
	}
	// If the frontmatter exists but the key was absent, insert it before
	// the closing --- so externally-authored files (external editors) that
	// lack the key gain it on rename rather than silently no-oping.
	if inFM && !found && closeIdx >= 0 {
		newLine := fmt.Sprintf("%s: %s", key, strconv.Quote(newVal))
		lines = append(lines[:closeIdx], append([]string{newLine}, lines[closeIdx:]...)...)
	}
	return strings.Join(lines, "\n")
}

// RenamePage renames a single page file. Updates the page: frontmatter value,
// moves the file, and re-indexes. Block UUIDs are preserved so references
// and embeds keep resolving (#62, #83).
func (a *App) RenamePage(notebook, section, oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safeOldPage := sanitizePathSegment(oldName)
	safeNewPage := sanitizePathSegment(newName)
	if safeNotebook == "" || safeOldPage == "" || safeNewPage == "" {
		return fmt.Errorf("notebook and page names are required")
	}
	if safeOldPage == safeNewPage {
		return nil
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	oldFile := filepath.Join(notebookDir, safeSection, safeOldPage+".md")
	newFile := filepath.Join(notebookDir, safeSection, safeNewPage+".md")
	if !isPathWithinRoot(oldFile, notebookDir) || !isPathWithinRoot(newFile, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(newFile); err == nil {
		return fmt.Errorf("a page named %q already exists", safeNewPage)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	// Lock the notebook root to prevent interleaving with the scanner.
	nbRoot := notebookDir
	a.coordinator.LockFileWrite(nbRoot, func() {
		// 1. Read the file content before renaming.
		contentBytes, err := os.ReadFile(oldFile)
		if err != nil {
			runErr = err
			return
		}

		// 2. Rename old → new FIRST. If this fails, nothing was modified
		// (clean state). This avoids the stale-frontmatter-at-old-path
		// inconsistency that would occur if we wrote frontmatter first.
		a.tracker.RegisterWrite(oldFile)
		a.tracker.RegisterWrite(newFile)
		if err := os.Rename(oldFile, newFile); err != nil {
			runErr = err
			return
		}

		// 3. Update frontmatter at the new path. If this fails, the file
		// is at the correct new path with stale frontmatter — the scanner
		// will use the path-derived page name, which matches the sidebar.
		content := updateFrontmatterField(string(contentBytes), "page", safeNewPage)
		a.tracker.RegisterWrite(newFile)
		if err := parser.WriteFileAtomic(newFile, []byte(content)); err != nil {
			runErr = err
			return
		}

		// 4. Clear old index entries + re-index at new path.
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, safeOldPage)
		})
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ForgetFile(oldFile)
		})
		a.reindexFile(newFile, safeNotebook, safeSection, safeNewPage)
	})

	return runErr
}

// MovePage moves a page from one section to another (or to the notebook root
// when toSection == "") within the same notebook (#177). The .md file is
// renamed on disk, its `section:` frontmatter is rewritten, the block index
// is rebuilt at the new path, and nav_order is adjusted for both the source
// and target sectionKeys. Returns an error on name collision. Cross-notebook
// moves are out of scope — the page stays within `notebook`. Block UUIDs are
// preserved so references and embeds keep resolving.
func (a *App) MovePage(notebook, fromSection, toSection, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeFrom := sanitizeSectionPath(fromSection)
	safeTo := sanitizeSectionPath(toSection)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("notebook and page names are required")
	}
	if safeFrom == safeTo {
		return nil // already in the target section
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	oldFile := filepath.Join(notebookDir, safeFrom, safePage+".md")
	newFile := filepath.Join(notebookDir, safeTo, safePage+".md")
	if !isPathWithinRoot(oldFile, notebookDir) || !isPathWithinRoot(newFile, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(oldFile); os.IsNotExist(err) {
		return fmt.Errorf("page %q not found in section %q", safePage, safeFrom)
	}
	if _, err := os.Stat(newFile); err == nil {
		return fmt.Errorf("a page named %q already exists in that section", safePage)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	nbRoot := notebookDir
	a.coordinator.LockFileWrite(nbRoot, func() {
		// 1. Ensure the target section directory exists (handles nested
		// sections like "Projects/Active" and the section-less root, which
		// is the notebook dir itself).
		targetDir := filepath.Dir(newFile)
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			runErr = fmt.Errorf("failed to create target section directory: %w", err)
			return
		}

		// 2. Read the file content before moving.
		contentBytes, err := os.ReadFile(oldFile)
		if err != nil {
			runErr = err
			return
		}

		// 3. Rename old → new. If this fails, nothing was modified.
		a.tracker.RegisterWrite(oldFile)
		a.tracker.RegisterWrite(newFile)
		if err := os.Rename(oldFile, newFile); err != nil {
			runErr = err
			return
		}

		// 4. Rewrite the section: frontmatter at the new path. An empty
		// safeTo produces `section: ""` (section-less), matching the
		// parser's section-less convention. If this fails, the file is at
		// the new path with stale frontmatter — we log the error and
		// continue through the index cleanup (step 5) so the index doesn't
		// dangle at the old path. The scanner will reconcile on next pass.
		content := updateFrontmatterField(string(contentBytes), "section", safeTo)
		a.tracker.RegisterWrite(newFile)
		if err := parser.WriteFileAtomic(newFile, []byte(content)); err != nil {
			log.Printf("MovePage: WriteFileAtomic failed at %s (file already moved): %v", newFile, err)
		}

		// 5. Clear old index entries + re-index at the new path. These run
		// unconditionally — even if the frontmatter write failed, the file
		// has already moved and the old index entries must be cleaned up.
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeFrom, safePage)
		})
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ForgetFile(oldFile)
		})
		a.reindexFile(newFile, safeNotebook, safeTo, safePage)
		// If frontmatter write failed, the file has already moved — do not
		// surface the error to the user. The scanner reconciles stale
		// frontmatter on the next pass (comment at line 3380).
	})
	if runErr != nil {
		return runErr
	}

	// 6. Update nav_order: remove the page from the old section's ordering
	// and append it to the new section's ordering. RenamePage omits this
	// step — MovePage must keep nav_order consistent with the new on-disk
	// layout so the sidebar doesn't fall back to alphabetical for a moved
	// page (#177). Note: this runs outside the LockFileWrite lambda, so
	// there is a microsecond window where a concurrent SetNavOrder could
	// interleave — the consequence is stale ordering (not data loss).
	// If the config save fails, the file move has already succeeded, so
	// we log but don't return an error — the in-memory cfg is correct and
	// the next config save (any UI action) will flush it.
	if err := a.updateNavOrderForMove(safeNotebook, safeFrom, safeTo, safePage); err != nil {
		log.Printf("MovePage: nav_order persist failed (file move succeeded): %v", err)
	}
	return nil
}

// updateNavOrderForMove adjusts NavOrder.Pages after a page moves between
// sections. The page is removed from the old sectionKey's ordering and
// appended to the new sectionKey's ordering (idempotent — skips if already
// present). The sectionKey format mirrors the frontend: `${notebook}/${section}`
// (empty section for root pages). Persisted atomically with self-write
// suppression so the config watcher doesn't double-fire.
func (a *App) updateNavOrderForMove(notebook, fromSection, toSection, page string) error {
	oldKey := notebook + "/" + fromSection
	newKey := notebook + "/" + toSection

	a.configMu.Lock()
	pages := a.cfg.UI.NavOrder.Pages
	if pages == nil {
		pages = map[string][]string{}
	}
	// Remove from old section.
	if oldList, ok := pages[oldKey]; ok {
		filtered := make([]string, 0, len(oldList))
		for _, p := range oldList {
			if p != page {
				filtered = append(filtered, p)
			}
		}
		pages[oldKey] = filtered
	}
	// Append to new section (idempotent).
	newList := pages[newKey]
	already := false
	for _, p := range newList {
		if p == page {
			already = true
			break
		}
	}
	if !already {
		pages[newKey] = append(newList, page)
	}
	a.cfg.UI.NavOrder.Pages = pages
	cfg := a.cfg
	a.configMu.Unlock()

	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, cfg)
}

// RenameSection renames a section folder and updates the section: frontmatter
// in every .md file it contains. All affected blocks are re-indexed (#62).
func (a *App) RenameSection(notebook, oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeOldSection := sanitizePathSegment(oldName)
	safeNewSection := sanitizePathSegment(newName)
	if safeNotebook == "" || safeOldSection == "" || safeNewSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}
	if safeOldSection == safeNewSection {
		return nil
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	oldDir := filepath.Join(notebookDir, safeOldSection)
	newDir := filepath.Join(notebookDir, safeNewSection)
	if !isPathWithinRoot(oldDir, notebookDir) || !isPathWithinRoot(newDir, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("a section named %q already exists", safeNewSection)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	nbRoot := notebookDir
	a.coordinator.LockFileWrite(nbRoot, func() {
		// 1. Read all .md files from the old section BEFORE renaming.
		entries, err := os.ReadDir(oldDir)
		if err != nil {
			runErr = err
			return
		}
		type fileContent struct {
			name    string
			content []byte
		}
		var files []fileContent
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			oldPath := filepath.Join(oldDir, entry.Name())
			b, err := os.ReadFile(oldPath)
			if err != nil {
				runErr = fmt.Errorf("RenameSection: read %s: %w", entry.Name(), err)
				return
			}
			files = append(files, fileContent{name: entry.Name(), content: b})
		}

		// 2. Rename the section folder FIRST. If this fails, nothing was
		// modified (clean state — avoids stale frontmatter at old paths).
		a.tracker.RegisterWrite(oldDir)
		a.tracker.RegisterWrite(newDir)
		if err := os.Rename(oldDir, newDir); err != nil {
			runErr = err
			return
		}

		// 3. Update section: frontmatter in each file at the new path.
		// If any write fails, the folder is at the correct new path;
		// the scanner will derive section from the path (which matches
		// the sidebar), and stale frontmatter self-heals on next rename.
		var writeErrs []string
		for _, fc := range files {
			newPath := filepath.Join(newDir, fc.name)
			updated := updateFrontmatterField(string(fc.content), "section", safeNewSection)
			a.tracker.RegisterWrite(newPath)
			if err := parser.WriteFileAtomic(newPath, []byte(updated)); err != nil {
				writeErrs = append(writeErrs, fmt.Sprintf("write %s: %v", fc.name, err))
			}
		}
		if len(writeErrs) > 0 {
			runErr = fmt.Errorf("RenameSection: %d file(s) failed frontmatter update at new path: %s", len(writeErrs), strings.Join(writeErrs, "; "))
			return
		}

		// 4. Clear old index entries + re-index all pages at new paths.
		var pageFiles []string
		for _, fc := range files {
			pageFiles = append(pageFiles, fc.name)
		}
		a.coordinator.WithDBWrite(func() {
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeOldSection, "")
		})
		for _, pageFile := range pageFiles {
			oldPath := filepath.Join(oldDir, pageFile)
			newPath := filepath.Join(newDir, pageFile)
			pageName := strings.TrimSuffix(pageFile, ".md")
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ForgetFile(oldPath)
			})
			a.reindexFile(newPath, safeNotebook, safeNewSection, pageName)
		}
	})

	return runErr
}

// RenameNotebook renames a notebook folder and updates the notebook: frontmatter
// in every .md file it contains. All affected blocks are re-indexed (#62).
func (a *App) RenameNotebook(oldName, newName string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeOldNotebook := sanitizePathSegment(oldName)
	safeNewNotebook := sanitizePathSegment(newName)
	if safeOldNotebook == "" || safeNewNotebook == "" {
		return fmt.Errorf("notebook names are required")
	}
	if safeOldNotebook == safeNewNotebook {
		return nil
	}

	// A linked notebook's name is its external folder basename + registry
	// identity; renaming it is unlink + re-link, not a folder rename on the
	// external source of truth. Refuse here so the vault-only folder rename
	// below never misroutes (#100).
	if src := a.resolveSourceByName(safeOldNotebook); strings.HasPrefix(src, "linked:") {
		return fmt.Errorf("linked notebooks cannot be renamed in place — unlink and re-link the folder under the new name")
	}

	oldDir := filepath.Join(a.vaultPath, safeOldNotebook)
	newDir := filepath.Join(a.vaultPath, safeNewNotebook)
	if !isPathWithinRoot(oldDir, a.vaultPath) || !isPathWithinRoot(newDir, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("a notebook named %q already exists", safeNewNotebook)
	}
	if a.nameCollidesWithLink(safeNewNotebook, "") {
		return fmt.Errorf("a linked notebook named %q already exists; unlink or rename it first", safeNewNotebook)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	a.coordinator.LockFileWrite(oldDir, func() {
		// 1. Walk all .md files under the old notebook recursively and
		// read their content BEFORE renaming.
		type fileContent struct {
			oldPath string
			relPath string
			content []byte
		}
		var files []fileContent
		_ = filepath.WalkDir(oldDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				b, readErr := os.ReadFile(path)
				if readErr != nil {
					runErr = fmt.Errorf("RenameNotebook: read %s: %w", path, readErr)
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(oldDir, path)
				files = append(files, fileContent{oldPath: path, relPath: rel, content: b})
			}
			return nil
		})
		if runErr != nil {
			return
		}

		// 2. Rename the notebook folder FIRST. If this fails, nothing
		// was modified (clean state).
		a.tracker.RegisterWrite(oldDir)
		a.tracker.RegisterWrite(newDir)
		if err := os.Rename(oldDir, newDir); err != nil {
			runErr = err
			return
		}

		// 3. Update notebook: frontmatter in each file at the new path.
		var writeErrs []string
		for _, fc := range files {
			newMdPath := filepath.Join(newDir, fc.relPath)
			updated := updateFrontmatterField(string(fc.content), "notebook", safeNewNotebook)
			a.tracker.RegisterWrite(newMdPath)
			if err := parser.WriteFileAtomic(newMdPath, []byte(updated)); err != nil {
				writeErrs = append(writeErrs, fmt.Sprintf("write %s: %v", fc.relPath, err))
			}
		}
		if len(writeErrs) > 0 {
			runErr = fmt.Errorf("RenameNotebook: %d file(s) failed frontmatter update at new path: %s", len(writeErrs), strings.Join(writeErrs, "; "))
			return
		}

		// 4. Clear old index entries + re-index all files at new paths.
		for _, fc := range files {
			rel, err := filepath.Rel(oldDir, fc.oldPath)
			if err != nil {
				continue
			}
			// Derive section/page from the relative path for ClearFileBlocks.
			relParts := strings.Split(filepath.ToSlash(rel), "/")
			var section, page string
			if len(relParts) == 1 {
				page = strings.TrimSuffix(relParts[0], ".md")
			} else {
				section = relParts[0]
				page = strings.TrimSuffix(relParts[len(relParts)-1], ".md")
			}
			// Clear old index entries via the typed API (not raw SQL) so
			// the files mtime cache is also cleaned via ForgetFile.
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ClearFileBlocks(nil, "vault", safeOldNotebook, section, page)
				_ = a.db.ForgetFile(fc.oldPath)
			})
			newMdPath := filepath.Join(newDir, rel)
			a.reindexFile(newMdPath, safeNewNotebook, section, page)
		}
	})

	return runErr
}

// DeletePage moves a single page file to .system/trash/ and clears its index
// entries. The file is recoverable from the trash folder (#62).
func (a *App) DeletePage(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return fmt.Errorf("notebook and page names are required")
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	filePath := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(filePath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("page %q not found", safePage)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	linked := strings.HasPrefix(source, "linked:")
	var runErr error
	a.coordinator.LockFileWrite(filePath, func() {
		a.tracker.RegisterWrite(filePath)
		if linked {
			// External folder is the source of truth — delete in place. Silt
			// never copies linked content into the vault trash (#100).
			if err := os.Remove(filePath); err != nil {
				runErr = err
				return
			}
		} else {
			if _, err := a.moveToTrash(filePath); err != nil {
				runErr = err
				return
			}
		}
		var blockIDs []string
		a.coordinator.WithDBWrite(func() {
			blockIDs, _ = a.db.BlockIDsForPage(source, safeNotebook, safeSection, safePage)
			_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, safePage)
			_ = a.db.ForgetFile(filePath)
		})
		// Release the deleted blocks' per-block mutex entries (#122).
		a.coordinator.ReleaseBlockMutexes(blockIDs)
	})

	return runErr
}

// DeleteSection moves a section folder (all pages) to .system/trash/ and clears
// their index entries (#62).
func (a *App) DeleteSection(notebook, section string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	if safeNotebook == "" || safeSection == "" {
		return fmt.Errorf("notebook and section names are required")
	}

	source := a.resolveSourceByName(safeNotebook)
	notebookDir, err := a.resolveNotebookDir(safeNotebook, source)
	if err != nil {
		return err
	}
	secPath := filepath.Join(notebookDir, safeSection)
	if !isPathWithinRoot(secPath, notebookDir) {
		return fmt.Errorf("path escapes notebook root")
	}
	if _, err := os.Stat(secPath); os.IsNotExist(err) {
		return fmt.Errorf("section %q not found", safeSection)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	linked := strings.HasPrefix(source, "linked:")
	var runErr error
	a.coordinator.LockFileWrite(secPath, func() {
		// Collect page files before deletion for index cleanup.
		entries, _ := os.ReadDir(secPath)
		var pageNames []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				pageNames = append(pageNames, strings.TrimSuffix(entry.Name(), ".md"))
			}
		}

		a.tracker.RegisterWrite(secPath)
		if linked {
			// External folder is the source of truth — remove in place (#100).
			if err := os.RemoveAll(secPath); err != nil {
				runErr = err
				return
			}
		} else {
			if _, err := a.moveToTrash(secPath); err != nil {
				runErr = err
				return
			}
		}

		a.coordinator.WithDBWrite(func() {
			for _, pg := range pageNames {
				_ = a.db.ClearFileBlocks(nil, source, safeNotebook, safeSection, pg)
			}
		})
	})

	return runErr
}

// DeleteNotebook moves a notebook folder (all sections + pages) to
// .system/trash/ and clears their index entries (#62).
func (a *App) DeleteNotebook(notebook string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	safeNotebook := sanitizePathSegment(notebook)
	if safeNotebook == "" {
		return fmt.Errorf("notebook name is required")
	}

	nbPath := filepath.Join(a.vaultPath, safeNotebook)
	if !isPathWithinRoot(nbPath, a.vaultPath) {
		return fmt.Errorf("path escapes vault")
	}
	if _, err := os.Stat(nbPath); os.IsNotExist(err) {
		return fmt.Errorf("notebook %q not found", safeNotebook)
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var runErr error
	a.coordinator.LockFileWrite(nbPath, func() {
		// Walk the subtree BEFORE trashing to collect file paths and their
		// (section, page) for per-page index cleanup via the typed API.
		type pageInfo struct {
			path    string
			section string
			page    string
		}
		var pages []pageInfo
		_ = filepath.WalkDir(nbPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				rel, _ := filepath.Rel(nbPath, path)
				relParts := strings.Split(filepath.ToSlash(rel), "/")
				var section, page string
				if len(relParts) == 1 {
					page = strings.TrimSuffix(relParts[0], ".md")
				} else {
					section = relParts[0]
					page = strings.TrimSuffix(relParts[len(relParts)-1], ".md")
				}
				pages = append(pages, pageInfo{path: path, section: section, page: page})
			}
			return nil
		})

		a.tracker.RegisterWrite(nbPath)
		if _, err := a.moveToTrash(nbPath); err != nil {
			runErr = err
			return
		}
		// Clear blocks + files-cache entries per page via the typed API.
		for _, pg := range pages {
			a.coordinator.WithDBWrite(func() {
				_ = a.db.ClearFileBlocks(nil, "vault", safeNotebook, pg.section, pg.page)
				_ = a.db.ForgetFile(pg.path)
			})
		}
	})

	return runErr
}
