package main

import (
	"fmt"
	"os"
	"path/filepath"
	"silt/backend/parser"
	"silt/backend/plugins"
	"sort"
	"strings"
	"time"
)

// =========================================================================
// Expanded content API (#104)
// =========================================================================

// PluginCreateBlockOp describes a single create/delete/move for the bulk op.
type PluginCreateBlockOp struct {
	Kind     string `json:"kind"` // "create" | "delete" | "move"
	AfterID  string `json:"afterId,omitempty"`
	Type     string `json:"type,omitempty"`     // for create: TASK | NOTE | HEADER
	Text     string `json:"text,omitempty"`     // for create: block body
	BlockID  string `json:"blockId,omitempty"`  // for delete/move
	Notebook string `json:"notebook,omitempty"` // target page for create/move
	Section  string `json:"section,omitempty"`
	Page     string `json:"page,omitempty"`
	NewID    string `json:"newId,omitempty"` // for create: pre-minted UUID (caller captures it)
}

// PluginCreateBlock creates a single block after `afterID` (or at the end of the
// page identified by notebook/section/page when afterID is empty). type must be
// TASK, NOTE, or HEADER. The new block's UUID is the pre-minted NewID carried
// in the op so the caller gets back the exact id that lands on disk.
// Returns the new block's UUID.
// Gated by content-mutate (#156). Session-token verified (#151).
func (a *App) PluginCreateBlock(pluginID, sessionToken, afterID, notebook, section, page, blockType, text string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	if err := a.requireGrant(pluginID, plugins.CapContentMutate); err != nil {
		return "", err
	}
	if a.db == nil {
		return "", fmt.Errorf("vault database not loaded")
	}
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	bt := parser.BlockType(strings.ToUpper(blockType))
	if bt != parser.BlockTask && bt != parser.BlockNote && bt != parser.BlockHeader {
		return "", fmt.Errorf("invalid block type %q (want TASK, NOTE, or HEADER)", blockType)
	}

	newID := newUUID()
	op := PluginCreateBlockOp{
		Kind:     "create",
		AfterID:  afterID,
		Type:     string(bt),
		Text:     text,
		Notebook: notebook,
		Section:  section,
		Page:     page,
		NewID:    newID,
	}
	if err := a.applyBlocksOps([]PluginCreateBlockOp{op}); err != nil {
		return "", err
	}
	return newID, nil
}

// PluginDeleteBlock removes a block by UUID from its source file and re-indexes.
// Gated by content-mutate (#156). Session-token verified (#151).
func (a *App) PluginDeleteBlock(pluginID, sessionToken, blockID string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapContentMutate); err != nil {
		return err
	}
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	if blockID == "" {
		return fmt.Errorf("blockId is required")
	}
	return a.applyBlocksOps([]PluginCreateBlockOp{{Kind: "delete", BlockID: blockID}})
}

// PluginMoveBlock moves a block within its page (after afterID) or to another
// page (notebook/section/page). When afterID is empty the block goes to the end
// of the target page.
// Gated by content-mutate (#156). Session-token verified (#151).
func (a *App) PluginMoveBlock(pluginID, sessionToken, blockID, afterID, notebook, section, page string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapContentMutate); err != nil {
		return err
	}
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	if blockID == "" {
		return fmt.Errorf("blockId is required")
	}
	return a.applyBlocksOps([]PluginCreateBlockOp{{
		Kind:     "move",
		BlockID:  blockID,
		AfterID:  afterID,
		Notebook: notebook,
		Section:  section,
		Page:     page,
	}})
}

// PluginApplyBlocks applies a batch of create/delete/move ops, coalescing per-
// page writes into a single SaveFileBlocks + re-index pass so a bulk op does
// not thrash the WAL with one rewrite per block (#104).
// Gated by content-mutate (#156). Session-token verified (#151).
func (a *App) PluginApplyBlocks(pluginID, sessionToken string, ops []PluginCreateBlockOp) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapContentMutate); err != nil {
		return err
	}
	if a.db == nil {
		return fmt.Errorf("vault database not loaded")
	}
	if len(ops) == 0 {
		return nil
	}
	return a.applyBlocksOps(ops)
}

// applyBlocksOps is the shared engine for create/delete/move (single + bulk).
// It groups ops by target page, fetches each page's blocks once, mutates the
// slice, and writes each affected page exactly once through SaveFileBlocks.
func (a *App) applyBlocksOps(ops []PluginCreateBlockOp) error {
	a.wg.Add(1)
	defer a.wg.Done()

	// 1. Resolve every op to a concrete (source, notebook, section, page).
	type resolved struct {
		op       PluginCreateBlockOp
		source   string
		notebook string
		section  string
		page     string
		// For cross-page moves: the block's original location before the
		// target overwrite. Preserved so the second pass can find/remove it
		// from the source page, and so the first pass can fetch the block's
		// content for insertion into the target.
		origSource   string
		origNotebook string
		origSection  string
		origPage     string
		newID        string
		blockType    parser.BlockType
		text         string
	}
	var resolvedOps []resolved

	// Resolve block locations for afterID / blockID lookups.
	locOf := func(id string) (source, nb, sec, pg string, ok bool) {
		if a.db == nil {
			return "", "", "", "", false
		}
		var loc struct{ s, n, se, p string }
		var e error
		a.coordinator.WithDBReadResult(func() error {
			bl, err := a.db.GetBlockLocation(id)
			if err == nil {
				loc.s, loc.n, loc.se, loc.p = bl.Source, bl.Notebook, bl.Section, bl.Page
			}
			e = err
			return nil
		})
		if e != nil {
			return "", "", "", "", false
		}
		return loc.s, loc.n, loc.se, loc.p, true
	}

	for i, op := range ops {
		r := resolved{op: op}
		switch op.Kind {
		case "create":
			r.blockType = parser.BlockType(strings.ToUpper(op.Type))
			if r.blockType != parser.BlockTask && r.blockType != parser.BlockNote && r.blockType != parser.BlockHeader {
				return fmt.Errorf("op %d: invalid block type %q", i, op.Type)
			}
			r.text = strings.ReplaceAll(op.Text, "\n", " ")
			// Use the caller's pre-minted ID if provided (so PluginCreateBlock
			// returns the exact UUID that lands in the file); mint otherwise.
			r.newID = op.NewID
			if r.newID == "" {
				r.newID = newUUID()
			}
			// Target page: from afterID if given, else explicit notebook/section/page.
			if op.AfterID != "" {
				s, n, se, p, ok := locOf(op.AfterID)
				if !ok {
					return fmt.Errorf("op %d: after block %s not found", i, op.AfterID)
				}
				r.source, r.notebook, r.section, r.page = s, n, se, p
			} else {
				sn := sanitizePathSegment(op.Notebook)
				sp := sanitizePathSegment(op.Page)
				if sn == "" || sp == "" {
					return fmt.Errorf("op %d: create without afterId needs notebook + page", i)
				}
				r.notebook, r.section, r.page = sn, sanitizePathSegment(op.Section), sp
				r.source = a.resolveSourceByName(r.notebook)
			}
		case "delete", "move":
			s, n, se, p, ok := locOf(op.BlockID)
			if !ok {
				return fmt.Errorf("op %d: block %s not found", i, op.BlockID)
			}
			r.source, r.notebook, r.section, r.page = s, n, se, p
			r.origSource, r.origNotebook, r.origSection, r.origPage = s, n, se, p
			if op.Kind == "move" && (op.Notebook != "" || op.Section != "" || op.Page != "") {
				// Cross-page move: target is the explicit page.
				tn := sanitizePathSegment(op.Notebook)
				tp := sanitizePathSegment(op.Page)
				if tn != "" && tp != "" {
					r.notebook, r.section, r.page = tn, sanitizePathSegment(op.Section), tp
					r.source = a.resolveSourceByName(r.notebook)
				}
			}
		default:
			return fmt.Errorf("op %d: unknown kind %q", i, op.Kind)
		}
		resolvedOps = append(resolvedOps, r)
	}

	// 2. Group by target page (same-page ops coalesce into one write).
	pagesByKey := map[string][]resolved{}
	for _, r := range resolvedOps {
		key := fmt.Sprintf("%s|%s/%s/%s", r.source, r.notebook, r.section, r.page)
		pagesByKey[key] = append(pagesByKey[key], r)
	}

	// Sort the page keys for deterministic cross-page ordering (#104 hardening).
	// Without this, Go map iteration randomizes the order, and locOf (which
	// reads a mutating DB) returns inconsistent results across runs.
	pageKeys := make([]string, 0, len(pagesByKey))
	for k := range pagesByKey {
		pageKeys = append(pageKeys, k)
	}
	sort.Strings(pageKeys)

	// 3. Apply per page (deterministic order).
	for _, pk := range pageKeys {
		pageOps := pagesByKey[pk]
		// Sort within-page ops by kind (delete → move → create) so the
		// mutation order is deterministic regardless of input order. This
		// prevents a footgun where [create-after-A, move-A, delete-A] would
		// behave differently under re-ordering (#104 hardening).
		opOrder := map[string]int{"delete": 0, "move": 1, "create": 2}
		sort.SliceStable(pageOps, func(i, j int) bool {
			return opOrder[pageOps[i].op.Kind] < opOrder[pageOps[j].op.Kind]
		})
		first := pageOps[0]
		blocks, err := a.FetchPageBlocks(first.notebook, first.section, first.page)
		if err != nil {
			return fmt.Errorf("fetch page %s/%s/%s: %w", first.notebook, first.section, first.page, err)
		}
		mutated := blocks
		var createdIDs []string
		for _, r := range pageOps {
			switch r.op.Kind {
			case "create":
				nb := parser.ParsedBlock{
					ID:        r.newID,
					Type:      r.blockType,
					CleanText: r.text,
					FileDate:  time.Now().Format("2006-01-02"),
				}
				mutated = insertAfter(mutated, r.op.AfterID, nb)
				createdIDs = append(createdIDs, r.newID)
			case "delete":
				mutated = removeByID(mutated, r.op.BlockID)
			case "move":
				// Same-page move: the block is already in `mutated`, just reorder.
				// Cross-page move: the block lives in the SOURCE page, not in
				// `mutated` (the target page). Fetch it from source and insert.
				isCrossPage := r.origNotebook != "" &&
					(r.origNotebook != r.notebook ||
						r.origSection != r.section ||
						r.origPage != r.page)
				if isCrossPage {
					srcBlocks, srcErr := a.FetchPageBlocks(r.origNotebook, r.origSection, r.origPage)
					if srcErr != nil {
						return fmt.Errorf("cross-page move: fetch source page for block %s: %w", r.op.BlockID, srcErr)
					}
					found := false
					for _, b := range srcBlocks {
						if b.ID == r.op.BlockID {
							mutated = insertAfter(mutated, r.op.AfterID, b)
							found = true
							break
						}
					}
					if !found {
						return fmt.Errorf("cross-page move: block %s not found in source page %s/%s/%s", r.op.BlockID, r.origNotebook, r.origSection, r.origPage)
					}
				} else if r.op.AfterID != "" || r.op.BlockID != "" {
					mutated = moveWithin(mutated, r.op.BlockID, r.op.AfterID)
				}
			}
		}
		if err := a.SaveFileBlocks(first.notebook, first.section, first.page, mutated); err != nil {
			return fmt.Errorf("save page %s/%s/%s: %w", first.notebook, first.section, first.page, err)
		}
		// For cross-page moves, remove the block from its source page.
		//
		// The source-page block list is read from the DB (FetchPageBlocks), NOT
		// from the file. The first-pass SaveFileBlocks already re-indexed the
		// moved block into the TARGET page, so the DB no longer lists it under
		// the source page. Reading the file instead would see stale content
		// (the file hasn't been rewritten yet) and IndexFileBlocks would
		// re-insert the block under the source page, stealing it back from the
		// target — silent data loss under concurrency.
		//
		// LockFileWrite serializes concurrent source-page writes so the file
		// never has a torn write. Errors are NOT swallowed — a failed
		// source-removal would leave the block duplicated.
		for _, r := range pageOps {
			if r.op.Kind == "move" && r.origNotebook != "" &&
				(r.origNotebook != r.notebook ||
					r.origSection != r.section ||
					r.origPage != r.page) {
				sn := sanitizePathSegment(r.origNotebook)
				ss := sanitizePathSegment(r.origSection)
				sp := sanitizePathSegment(r.origPage)
				origDir, dirErr := a.resolveNotebookDir(sn, r.origSource)
				if dirErr != nil {
					return fmt.Errorf("cross-page move: resolve source dir %s: %w", sn, dirErr)
				}
				origPath := filepath.Join(origDir, ss, sp+".md")
				if !isPathWithinRoot(origPath, origDir) {
					return fmt.Errorf("cross-page move: source path escapes notebook root")
				}
				// Remove the moved block from the source page. We do a TARGETED
				// delete (DELETE WHERE id = ?) instead of re-indexing the whole
				// page via IndexFileBlocks. This is critical for concurrency:
				// IndexFileBlocks does "DELETE FROM blocks WHERE id IN (...)"
				// for every block in the filtered list, which would delete blocks
				// that a concurrent goroutine already moved to ANOTHER page.
				// The targeted delete only removes the single block being moved.
				blockID := r.op.BlockID
				var writeErr error
				a.coordinator.LockFileWrite(origPath, func() {
					writeErr = a.removeBlockFromSourcePage(origPath, r.origSource, sn, ss, sp, blockID)
				})
				if writeErr != nil {
					return fmt.Errorf("cross-page move: save source %s/%s/%s: %w", sn, ss, sp, writeErr)
				}
			}
		}
		for _, id := range createdIDs {
			a.emitBlockChanged(id, first.notebook, first.section, first.page, "")
		}
	}

	return nil
}

// removeBlockFromSourcePage removes a single block from the source page FILE
// and does a TARGETED DB delete (DELETE WHERE id = ?). This is used by the
// second pass of a cross-page move. Critically, it does NOT call
// IndexFileBlocks — that function deletes ALL passed-in block IDs from the
// entire table, which would clobber blocks that a concurrent goroutine already
// moved to another page. The targeted delete only removes the single block
// being moved, so concurrent cross-page moves from the same source page are
// safe.
//
// The file is rewritten with the remaining blocks (filtered from the DB
// snapshot). The DB gets a single-row delete for the moved block only.
func (a *App) removeBlockFromSourcePage(filePath, source, notebook, section, page, blockID string) error {
	// NOTE: the file write and DB delete are not atomic together — the file is
	// rewritten first, then the DB row is deleted. If the process crashes
	// between, the file won't have the block but the DB will still list it.
	// This is acceptable: the DB is a re-derivable cache (§0 rule 4), so the
	// next index rebuild (or the next SaveFileBlocks for this page) reconciles
	// the file↔DB state automatically.
	// Read current blocks from the DB (fresh snapshot).
	srcBlocks, err := a.FetchPageBlocks(notebook, section, page)
	if err != nil {
		return fmt.Errorf("fetch source %s/%s/%s: %w", notebook, section, page, err)
	}
	filtered := removeByID(srcBlocks, blockID)

	// Rewrite the file with the remaining blocks (no re-index).
	contentBytes, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read source file: %w", err)
	}
	frontmatter, body := parser.SplitFrontmatter(string(contentBytes))
	if frontmatter == "" {
		frontmatter = fmt.Sprintf("---\nnotebook: %q\nsection: %q\npage: %q\ndate: %q\ntags: []\n---\n",
			notebook, section, page, time.Now().Format("2006-01-02"))
		body = string(contentBytes)
	}
	newContent := parser.RenderFileContent(filtered, body, frontmatter, a.spacesPerTab)
	a.tracker.RegisterWrite(filePath)
	if err := parser.WriteFileAtomic(filePath, []byte(newContent)); err != nil {
		return fmt.Errorf("write source file: %w", err)
	}

	// Targeted DB delete: remove ONLY the moved block from the SOURCE page.
	// Page-scoped so it doesn't delete the block from the TARGET page where
	// the first pass already indexed it.
	a.coordinator.WithDBWrite(func() {
		err = a.db.DeleteBlockFromPage(blockID, source, notebook, section, page)
	})
	if err != nil {
		return fmt.Errorf("delete moved block %s from index: %w", blockID, err)
	}
	return nil
}

func insertAfter(blocks []parser.ParsedBlock, afterID string, nb parser.ParsedBlock) []parser.ParsedBlock {
	if afterID == "" {
		return append(blocks, nb)
	}
	for i, b := range blocks {
		if b.ID == afterID {
			out := make([]parser.ParsedBlock, 0, len(blocks)+1)
			out = append(out, blocks[:i+1]...)
			out = append(out, nb)
			out = append(out, blocks[i+1:]...)
			return out
		}
	}
	return append(blocks, nb)
}

func removeByID(blocks []parser.ParsedBlock, id string) []parser.ParsedBlock {
	out := make([]parser.ParsedBlock, 0, len(blocks))
	for _, b := range blocks {
		if b.ID != id {
			out = append(out, b)
		}
	}
	return out
}

func moveWithin(blocks []parser.ParsedBlock, id, afterID string) []parser.ParsedBlock {
	var moved *parser.ParsedBlock
	filtered := make([]parser.ParsedBlock, 0, len(blocks))
	for i := range blocks {
		if blocks[i].ID == id {
			moved = &blocks[i]
		} else {
			filtered = append(filtered, blocks[i])
		}
	}
	if moved == nil {
		return blocks
	}
	return insertAfter(filtered, afterID, *moved)
}

// PluginCreatePage wraps the core CreatePage for the SDK (sandboxed to the
// declared notebook scope). Returns the resolved date string.
// Session-token verified (#236).
func (a *App) PluginCreatePage(pluginID, sessionToken, notebook, section, page, dateStr string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	return a.CreatePage(notebook, section, page, dateStr)
}

// PluginRegisterSurface is the Go-side capability gate for plugin UI surface
// registration (#154). A plugin without the ui-surface grant gets a
// CapabilityDeniedError here, before the frontend registry adds the surface.
// The frontend registerSurface SDK method calls this first; only on success
// does it add the surface to the frontend surfaces map.
// Session-token verified (#236).
func (a *App) PluginRegisterSurface(pluginID, sessionToken, surfaceID, kind, label string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	return a.requireGrant(pluginID, plugins.CapUISurface)
}

// PluginCreateSection wraps the core CreateSection for the SDK.
// Session-token verified (#236).
func (a *App) PluginCreateSection(pluginID, sessionToken, notebook, section string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	return a.CreateSection(notebook, section)
}

// PluginCreateNotebook wraps the core CreateNotebook for the SDK.
// Session-token verified (#236).
func (a *App) PluginCreateNotebook(pluginID, sessionToken, name string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	return a.CreateNotebook(name)
}

// PluginDeletePage wraps the core DeletePage for the SDK.
// Session-token verified (#236).
func (a *App) PluginDeletePage(pluginID, sessionToken, notebook, section, page string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	return a.DeletePage(notebook, section, page)
}

// PluginRenamePage wraps the core RenamePage for the SDK.
// Session-token verified (#236).
func (a *App) PluginRenamePage(pluginID, sessionToken, notebook, section, oldName, newName string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	return a.RenamePage(notebook, section, oldName, newName)
}
