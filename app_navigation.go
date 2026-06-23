package main

import (
	"fmt"
	"os"
	"path/filepath"
	"silt/backend/config"
	"silt/backend/parser"
	"sort"
	"strings"
)

// resolveNotebookDir returns the content directory for a notebook under the
// given source (#100): the folder whose direct children are the notebook's
// sections and section-less pages. For an in-vault notebook ('vault') that is
// <vaultPath>/<notebookName>; for a linked notebook ('linked:<id>') it is the
// linked root itself (sections/pages live directly under the external root).
// The caller MUST still guard any path built from this dir with
// isPathWithinRoot. Returns an error if the vault is not loaded or a linked
// source references an unregistered id.
func (a *App) resolveNotebookDir(notebookName, source string) (string, error) {
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	if source == "" || source == config.LinkedNotebooksVaultSource {
		return filepath.Join(a.vaultPath, notebookName), nil
	}
	if strings.HasPrefix(source, "linked:") {
		id := strings.TrimPrefix(source, "linked:")
		// F3: check quarantine + fingerprint under RLock; the fingerprint
		// comparison is fast (a stat + struct comparison). A mismatch
		// quarantines the link so every downstream op fails closed until the
		// user re-links.
		a.configMu.RLock()
		if a.quarantinedLinks != nil {
			if _, q := a.quarantinedLinks[id]; q {
				a.configMu.RUnlock()
				return "", fmt.Errorf("linked notebook %q is quarantined (root moved or tampered); re-link it via Settings → Linked notebooks", id)
			}
		}
		var ln config.LinkedNotebook
		found := false
		for _, entry := range a.cfg.LinkedNotebooks {
			if entry.ID == id {
				ln = entry
				found = true
				break
			}
		}
		a.configMu.RUnlock()
		if !found {
			return "", fmt.Errorf("linked notebook %q is not registered", id)
		}
		// F3: re-verify the root fingerprint. A synced edit to config.yaml's
		// root_path redirects the link to an attacker-chosen folder; the
		// fingerprint comparison catches this on the next access and
		// quarantines the link.
		if ln.RootFingerprint != "" {
			currentFP, fpErr := config.ComputeRootFingerprint(ln.RootPath)
			if fpErr != nil {
				return "", fmt.Errorf("linked notebook %q root is inaccessible: %w", id, fpErr)
			}
			if currentFP != ln.RootFingerprint {
				a.quarantineLink(id, "fingerprint_mismatch")
				return "", fmt.Errorf("linked notebook %q root fingerprint mismatch (root moved or tampered); re-link it via Settings → Linked notebooks", id)
			}
		}
		return ln.RootPath, nil
	}
	return "", fmt.Errorf("unknown notebook source %q", source)
}

// nspKey is the source-aware (source, notebook, section, page) lookup key for
// the per-page block count map used by ListNavigation. Source leads so a
// linked notebook sharing a display name with a vault notebook gets its own
// counts (#100).
type nspKey struct{ src, n, s, p string }

// resolveSourceByName maps a notebook display name to its index source
// ("vault" or "linked:<id>"). It acquires configMu in read mode for the
// standalone callers below. Notebook display names are globally unique
// (LinkNotebook rejects collisions), so the name unambiguously resolves the
// source (#100).
func (a *App) resolveSourceByName(notebookName string) string {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	return a.resolveSourceByNameLocked(notebookName)
}

// resolveSourceByNameLocked is the lock-free inner form. The caller MUST hold
// configMu (read or write). Needed so GetPluginSettingsForNotebook — which
// holds configMu in WRITE mode (linkedConfigLocked mutates the cache map) —
// can resolve the source without self-deadlocking on a re-entrant RLock
// (sync.RWMutex blocks RLock while a writer holds the lock).
func (a *App) resolveSourceByNameLocked(notebookName string) string {
	for _, ln := range a.cfg.LinkedNotebooks {
		if ln.DisplayName == notebookName {
			return ln.Source()
		}
	}
	return config.LinkedNotebooksVaultSource
}

// ListNavigation returns the Notebook > Section > Page tree for the sidebar.
//
// The directory structure on disk is the single source of truth. Each
// directory is classified by what it DIRECTLY contains:
//   - A `.md` file directly under a folder is a PAGE belonging to that folder's
//     section (a page belongs to the folder it's in; the folder's own path
//     is the section path, multi-segment joined with `/`).
//   - A sub-directory of a folder is a nested SECTION. We recurse into it to
//     collect its own pages + its own nested sections. Empty sections are
//     preserved so a freshly-created section appears in the sidebar (#88).
//   - A `.md` file directly under a Notebook's root belongs to the section-less
//     group (Name = "").
//
// Block counts are merged from the index for per-page badges. The returned
// tree is a true tree: each section may carry `Children []NavigationSection`
// for arbitrarily-deep nesting.
func (a *App) ListNavigation() (parser.NavigationTree, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return parser.NavigationTree{}, fmt.Errorf("vault not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	// 1. Block counts per (source, notebook, section, page) from the index.
	// Source is part of the key so a linked notebook sharing a display name
	// with a vault notebook gets its own counts (#100).
	counts := map[nspKey]int{}
	if a.db != nil {
		a.coordinator.WithDBRead(func() {
			rows, err := a.db.SQLDB().Query("SELECT COALESCE(source, 'vault'), notebook, section, page, COUNT(*) FROM blocks GROUP BY COALESCE(source, 'vault'), notebook, section, page")
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var src, n, s, p string
				var c int
				if err := rows.Scan(&src, &n, &s, &p, &c); err == nil {
					counts[nspKey{src, n, s, p}] = c
				}
			}
		})
	}

	tree := parser.NavigationTree{Notebooks: []parser.NavigationNotebook{}}
	nbEntries, err := os.ReadDir(a.vaultPath)
	if err != nil {
		return tree, fmt.Errorf("failed to read vault: %w", err)
	}

	for _, nbE := range nbEntries {
		nbName := nbE.Name()
		if !nbE.IsDir() || strings.HasPrefix(nbName, ".") {
			continue
		}
		nbPath := filepath.Join(a.vaultPath, nbName)
		rootPages, childSections := a.walkSections(nbPath, nbName, "", counts)
		var sections []parser.NavigationSection
		// Direct .md files at the notebook root form the section-less
		// group (Name = ""), surfaced first in the sidebar.
		if len(rootPages) > 0 {
			sections = append(sections, parser.NavigationSection{
				Name:  "",
				Pages: rootPages,
			})
		}
		sections = append(sections, childSections...)
		tree.Notebooks = append(tree.Notebooks, parser.NavigationNotebook{
			Name:     nbName,
			Sections: sections,
			Source:   "vault",
		})
	}

	// 2. Linked (external) notebooks (#100). Their sections/pages come from
	// the index counts (the root may be momentarily offline; the last-synced
	// rows still show). Each link is one notebook. Section-less pages ("")
	// surface first, matching the vault ordering above.
	a.configMu.RLock()
	links := append([]config.LinkedNotebook(nil), a.cfg.LinkedNotebooks...)
	a.configMu.RUnlock()
	for _, ln := range links {
		src := ln.Source()
		pagesBySection := map[string][]parser.NavigationPage{}
		for k, c := range counts {
			if k.src == src && k.n == ln.DisplayName {
				pagesBySection[k.s] = append(pagesBySection[k.s], parser.NavigationPage{Name: k.p, Count: c})
			}
		}
		_, statErr := os.Stat(ln.RootPath)

		var sections []parser.NavigationSection
		if pages, ok := pagesBySection[""]; ok {
			sortNavPages(pages)
			sections = append(sections, parser.NavigationSection{Name: "", Pages: pages})
		}
		named := make([]string, 0, len(pagesBySection))
		for s := range pagesBySection {
			if s != "" {
				named = append(named, s)
			}
		}
		sortStrings(named)
		for _, s := range named {
			pages := pagesBySection[s]
			sortNavPages(pages)
			sections = append(sections, parser.NavigationSection{Name: s, Pages: pages})
		}

		tree.Notebooks = append(tree.Notebooks, parser.NavigationNotebook{
			Name:         ln.DisplayName,
			Source:       src,
			RootPath:     ln.RootPath,
			Disconnected: statErr != nil,
			Sections:     sections,
		})
	}

	// Mix vault + linked notebooks alphabetically by name for a unified tree.
	sort.Slice(tree.Notebooks, func(i, j int) bool {
		return tree.Notebooks[i].Name < tree.Notebooks[j].Name
	})
	return normalizeNavTree(tree), nil
}

// normalizeNavTree guarantees no nil slices cross the Wails IPC boundary. A Go
// nil slice serializes to JSON `null`, but the generated TS constructor passes
// `null` through unchanged — the frontend's `.length` reads then crash with
// "Cannot read properties of null", which tears down the reactive update and
// leaves the sidebar blank even though the data is correct (#140). Every
// Sections / Pages / Children slice is normalized to a non-nil empty array.
func normalizeNavTree(tree parser.NavigationTree) parser.NavigationTree {
	if tree.Notebooks == nil {
		tree.Notebooks = []parser.NavigationNotebook{}
	}
	for i := range tree.Notebooks {
		if tree.Notebooks[i].Sections == nil {
			tree.Notebooks[i].Sections = []parser.NavigationSection{}
		}
		for j := range tree.Notebooks[i].Sections {
			tree.Notebooks[i].Sections[j] = normalizeNavSection(tree.Notebooks[i].Sections[j])
		}
	}
	return tree
}

func normalizeNavSection(s parser.NavigationSection) parser.NavigationSection {
	if s.Pages == nil {
		s.Pages = []parser.NavigationPage{}
	}
	if s.Children == nil {
		s.Children = []parser.NavigationSection{}
	}
	for i := range s.Children {
		s.Children[i] = normalizeNavSection(s.Children[i])
	}
	return s
}

// walkSections reads `dirPath` once and returns:
//   - `pages`: the direct .md files in this directory (the "own pages").
//   - `sections`: one NavigationSection per sub-directory, each carrying its
//     own pages and recursively-built children.
//
// `parentSectionID` is the multi-segment section id of `dirPath` itself
// (empty at the notebook root). The caller (ListNavigation) is responsible
// for turning the notebook-root `pages` into the section-less group.
// Sections with no pages and no children are still emitted so freshly-
// created sections appear in the sidebar immediately.
func (a *App) walkSections(
	dirPath, nbName, parentSectionID string,
	counts map[nspKey]int,
) ([]parser.NavigationPage, []parser.NavigationSection) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, nil
	}

	var pages []parser.NavigationPage
	var subDirs []string

	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Skip the attachments/ directory in the sidebar navigator (#101) —
		// it holds binary assets, not pages/sections.
		if e.IsDir() && strings.EqualFold(name, "attachments") {
			continue
		}
		if e.IsDir() {
			subDirs = append(subDirs, name)
			continue
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		pageName := strings.TrimSuffix(name, filepath.Ext(name))
		pages = append(pages, parser.NavigationPage{
			Name:  pageName,
			Count: counts[nspKey{"vault", nbName, parentSectionID, pageName}],
		})
	}
	sortNavPages(pages)
	sortStrings(subDirs)

	sections := []parser.NavigationSection{}

	for _, sd := range subDirs {
		var childID string
		if parentSectionID == "" {
			childID = sd
		} else {
			childID = parentSectionID + "/" + sd
		}
		childPath := filepath.Join(dirPath, sd)
		// Single read: the recursive call returns both the child's own
		// pages and its nested sections, so we never re-read childPath.
		childPages, childSections := a.walkSections(childPath, nbName, childID, counts)
		// Preserve the child even when empty so a freshly-created
		// section shows up in the sidebar.
		sections = append(sections, parser.NavigationSection{
			Name:     sd,
			Path:     childID,
			Pages:    childPages,
			Children: childSections,
		})
	}

	return pages, sections
}

func sortStrings(s []string) {
	sort.Strings(s)
}

func sortNavPages(p []parser.NavigationPage) {
	sort.Slice(p, func(i, j int) bool {
		return p[i].Name < p[j].Name
	})
}

// QueryTagHierarchy returns the hierarchical tag tree for the Tags Explorer.
func (a *App) QueryTagHierarchy() ([]parser.TagNode, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var res []parser.TagNode
	var err error
	a.coordinator.WithDBRead(func() { res, err = a.db.QueryTagHierarchy() })
	return res, err
}

// QueryBlocksByTag returns blocks tagged at or beneath tagPath (prefix match).
func (a *App) QueryBlocksByTag(tagPath string) ([]parser.TaskResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return nil, fmt.Errorf("vault database not loaded")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var res []parser.TaskResult
	var err error
	a.coordinator.WithDBRead(func() { res, err = a.db.QueryBlocksByTag(tagPath) })
	return res, err
}

// SearchBlocks fuzzy searches blocks and headings matching the query. Returns
// the first page (offset 0, limit 50) of FTS5-ranked results for backwards
// compatibility with the original binding; the Svelte search modal that needs
// pagination/snippets calls SearchBlocksPaged instead.
func (a *App) SearchBlocks(query string) ([]parser.TaskResult, error) {
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
		res, err = a.db.SearchBlocks(query)
	})

	return res, err
}

// SearchBlocksPaged runs the FTS5 search and returns a ranked, paginated
// envelope with highlighted snippets, the total match count, and a HasMore
// flag. offset/limit control the page (defaults applied by the caller).
func (a *App) SearchBlocksPaged(query string, offset, limit int) (parser.SearchResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.db == nil {
		return parser.SearchResult{}, fmt.Errorf("vault database not loaded")
	}

	a.wg.Add(1)
	defer a.wg.Done()

	var res parser.SearchResult
	var err error
	a.coordinator.WithDBRead(func() {
		res, err = a.db.SearchBlocksPaged(query, offset, limit)
	})

	return res, err
}

// focusFilePath resolves the on-disk page file for a focus-lease operation,
// routing to the correct root via the notebook's source (#100). Shared by
// Acquire/Release/RefreshFocusLock so the lease key always matches the file
// the watcher sees — including linked notebooks.
func (a *App) focusFilePath(notebook, section, page string) (string, error) {
	safeNotebook := sanitizePathSegment(notebook)
	safeSection := sanitizePathSegment(section)
	safePage := sanitizePathSegment(page)
	if safeNotebook == "" || safePage == "" {
		return "", fmt.Errorf("invalid path metadata")
	}
	notebookDir, err := a.resolveNotebookDir(safeNotebook, a.resolveSourceByName(safeNotebook))
	if err != nil {
		return "", err
	}
	fp := filepath.Join(notebookDir, safeSection, safePage+".md")
	if !isPathWithinRoot(fp, notebookDir) {
		return "", fmt.Errorf("path escapes notebook root")
	}
	return fp, nil
}

// AcquireFocusLock registers a focus lock on a page file to ignore fsnotify updates.
func (a *App) AcquireFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.LockFocus(fp)
	return nil
}

// ReleaseFocusLock removes a focus lock from a page file.
func (a *App) ReleaseFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.UnlockFocus(fp)
	return nil
}

// RefreshFocusLock extends an existing focus lease for a page file. Called by the
// Svelte editor's heartbeat while it stays focused (#38); a no-op if the
// lease already expired (the editor must re-acquire).
func (a *App) RefreshFocusLock(notebook, section, page string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.watcher == nil {
		return fmt.Errorf("watcher not running")
	}
	a.wg.Add(1)
	defer a.wg.Done()
	fp, err := a.focusFilePath(notebook, section, page)
	if err != nil {
		return err
	}
	a.watcher.RefreshFocus(fp)
	return nil
}
