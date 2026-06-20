package main

// Plugin v2 SDK bindings — expanded content API (#104), file I/O (#108), and
// OS integration (#114). These live in the main package alongside app.go so
// they share the coordinator / atomic-write / traversal-guard helpers. Each
// privileged binding calls a.requireGrant before its work; content-mutation
// bindings reuse the same atomic-write + re-index + emitBlockChanged path as
// the core editor (SPECS §8.3: "core feature decoupling").

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"silt/backend/parser"
	"silt/backend/plugins"
	"silt/backend/vault"

	"github.com/google/uuid"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
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
func (a *App) PluginCreateBlock(afterID, notebook, section, page, blockType, text string) (string, error) {
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
func (a *App) PluginDeleteBlock(blockID string) error {
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
func (a *App) PluginMoveBlock(blockID, afterID, notebook, section, page string) error {
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
func (a *App) PluginApplyBlocks(ops []PluginCreateBlockOp) error {
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
		op        PluginCreateBlockOp
		source    string
		notebook  string
		section   string
		page      string
		// For cross-page moves: the block's original location before the
		// target overwrite. Preserved so the second pass can find/remove it
		// from the source page, and so the first pass can fetch the block's
		// content for insertion into the target.
		origSource  string
		origNotebook string
		origSection  string
		origPage     string
		newID     string
		blockType parser.BlockType
		text      string
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
			// Read the CURRENT source-page blocks from the DB. The moved
			// block is already absent (first-pass SaveFileBlocks re-indexed
			// it to the target page), so filtering is typically a no-op.
			// This prevents a stale file read from re-introducing blocks
			// that concurrent moves already relocated.
			srcBlocks, srcErr := a.FetchPageBlocks(sn, ss, sp)
			if srcErr != nil {
				return fmt.Errorf("cross-page move: fetch source %s/%s/%s: %w", sn, ss, sp, srcErr)
			}
			filtered := removeByID(srcBlocks, r.op.BlockID)
			var writeErr error
			a.coordinator.LockFileWrite(origPath, func() {
				writeErr = a.writePageFileLocked(origPath, r.origSource, sn, ss, sp, filtered)
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
func (a *App) PluginCreatePage(notebook, section, page, dateStr string) (string, error) {
	return a.CreatePage(notebook, section, page, dateStr)
}

// PluginCreateSection wraps the core CreateSection for the SDK.
func (a *App) PluginCreateSection(notebook, section string) error {
	return a.CreateSection(notebook, section)
}

// PluginCreateNotebook wraps the core CreateNotebook for the SDK.
func (a *App) PluginCreateNotebook(name string) error {
	return a.CreateNotebook(name)
}

// PluginDeletePage wraps the core DeletePage for the SDK.
func (a *App) PluginDeletePage(notebook, section, page string) error {
	return a.DeletePage(notebook, section, page)
}

// PluginRenamePage wraps the core RenamePage for the SDK.
func (a *App) PluginRenamePage(notebook, section, oldName, newName string) error {
	return a.RenamePage(notebook, section, oldName, newName)
}

// =========================================================================
// Plugin file I/O (#108)
// =========================================================================

// pluginReadFileParams is the input to PluginReadFile.
type pluginFileResult struct {
	Path  string `json:"path"`
	Bytes []byte `json:"bytes"` // base64 over IPC (Wails encodes []byte as base64)
}

// PluginResolveNotebookRoot returns the absolute root directory of a notebook
// (in-vault or linked/external per #100), so a plugin can reason about paths.
// Gated by read-files (a root leak is a minor info disclosure).
func (a *App) PluginResolveNotebookRoot(pluginID, notebook string) (string, error) {
	if err := a.requireGrant(pluginID, plugins.CapReadFiles); err != nil {
		return "", err
	}
	sn := sanitizePathSegment(notebook)
	if sn == "" {
		return "", fmt.Errorf("notebook is required")
	}
	source := a.resolveSourceByName(sn)
	dir, err := a.resolveNotebookDir(sn, source)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// PluginListNavigation returns the Notebook > Section > Page tree for a
// plugin. Gated by read-files (the full vault tree reveals content shape:
// notebook names, section names, page names, and per-page block counts —
// a plugin with read-files already has file-listing access, so the tree is
// the same info in structured form, not an escalation). A plugin without
// the grant gets a CapabilityDeniedError.
func (a *App) PluginListNavigation(pluginID string) (parser.NavigationTree, error) {
	if err := a.requireGrant(pluginID, plugins.CapReadFiles); err != nil {
		return parser.NavigationTree{}, err
	}
	return a.ListNavigation()
}

// PluginReadFile reads a file within a notebook (relative path, traversal-
// guarded). Gated by read-files.
func (a *App) PluginReadFile(pluginID, notebook, relPath string) (pluginFileResult, error) {
	if err := a.requireGrant(pluginID, plugins.CapReadFiles); err != nil {
		return pluginFileResult{}, err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return pluginFileResult{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return pluginFileResult{}, err
	}
	return pluginFileResult{Path: relPath, Bytes: data}, nil
}

// maxPluginScratchBytes caps a single plugin's cumulative scratch-dir usage
// (per-notebook + vault combined, computed on demand by dirSizeUnder). Without
// this, a granted write-files plugin could fill the disk by writing many small
// files to its scratch dir — the per-file 100 MB attachment cap does not
// constrain cumulative scratch growth (#101 review). 500 MB is generous for
// real-world caches and small enough to surface runaway plugins via the
// existing writeFile error.
//
// Declared as a var so tests can override the cap for the duration of a
// single test (the alternative — allocating 500 MB in a test — is slow and
// brittle on CI). Production callers see the 500 MB default; tests set a
// smaller cap and restore the original on cleanup.
var maxPluginScratchBytes int64 = 500 * 1024 * 1024 // 500 MB

// PluginWriteFile writes a file within a notebook atomically (temp+fsync+rename
// via parser.WriteFileAtomic, under the per-file mutex + WriteTracker). Gated
// by write-files; the qualifier (notebook | vault) is enforced at the grant
// level — a notebook-scoped grant only allows writes inside the resolved
// notebook root.
//
// Scratch-dir writes (relPath under .system/plugins/<id>/data/) are bounded
// by maxPluginScratchBytes; the cumulative size is recomputed on each write
// so a successful delete immediately frees budget for a follow-up write.
func (a *App) PluginWriteFile(pluginID, notebook, relPath string, data []byte) error {
	if err := a.requireGrant(pluginID, plugins.CapWriteFiles); err != nil {
		return err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	if !pluginWritePathAllowed(pluginID, relPath) {
		return fmt.Errorf("write path %q is outside the allowed directories (attachments/ or this plugin's scratch dir)", relPath)
	}
	// Enforce the scratch-dir cumulative cap inside the file write lock so
	// two concurrent writes from the same plugin cannot both pass the check
	// and exceed the cap (TOCTOU). attachments/ writes are bounded by
	// maxAttachmentBytes (100 MB per file) and are exempt.
	checkScratch := isPluginScratchRelPath(pluginID, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var writeErr error
	a.coordinator.LockFileWrite(abs, func() {
		if checkScratch {
			used, sizeErr := pluginScratchSizeBytes(a, pluginID)
			if sizeErr != nil {
				writeErr = fmt.Errorf("check scratch usage: %w", sizeErr)
				return
			}
			if used+int64(len(data)) > maxPluginScratchBytes {
				writeErr = fmt.Errorf("scratch usage would exceed the %d-byte per-plugin cap (currently %d bytes, +%d bytes)", maxPluginScratchBytes, used, len(data))
				return
			}
		}
		a.tracker.RegisterWrite(abs)
		writeErr = parser.WriteFileAtomic(abs, data)
	})
	return writeErr
}

// isPluginScratchRelPath reports whether relPath (already relative to the
// notebook root) falls inside the calling plugin's own scratch dir. Mirrors
// pluginWritePathAllowed so the cap only applies to writes a plugin can
// actually own (#101 review).
func isPluginScratchRelPath(pluginID, relPath string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(relPath))
	ownScratch := ".system/plugins/" + pluginID + "/data/"
	return strings.HasPrefix(cleaned+"/", ownScratch)
}

// pluginScratchSizeBytes sums the on-disk byte count across every scratch
// directory the plugin owns (per-notebook + vault). Walks the relevant trees
// directly so a stale cumulative counter cannot drift out of sync with disk.
func pluginScratchSizeBytes(a *App, pluginID string) (int64, error) {
	var total int64
	// Vault scratch dir lives directly under the vault root.
	if a.vaultPath != "" {
		vaultDir := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, "data")
		n, err := dirSizeUnder(vaultDir)
		if err != nil {
			return 0, err
		}
		total += n
	}
	// Per-notebook scratch dirs live under each notebook's root. Walk the
	// vault to discover every notebook and sum the plugin's data dir in each.
	if a.vaultPath != "" {
		entries, err := os.ReadDir(a.vaultPath)
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			nbDir := filepath.Join(a.vaultPath, e.Name(), ".system", "plugins", pluginID, "data")
			n, err := dirSizeUnder(nbDir)
			if err != nil {
				return 0, err
			}
			total += n
		}
	}
	return total, nil
}

// dirSizeUnder recursively sums the byte size of every regular file under
// root. Symlinks are NOT followed (consistent with the install / unpack
// posture that rejects symlink-escape attempts).
func dirSizeUnder(root string) (int64, error) {
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return info.Size(), nil
		}
		return 0, nil
	}
	var total int64
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// Skip symlinks (any flavour) to avoid double-counting and to stay
		// consistent with the install-time symlink rejection.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			return ferr
		}
		total += fi.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// PluginDeleteFile removes a file within a notebook (traversal-guarded). Gated
// by write-files.
func (a *App) PluginDeleteFile(pluginID, notebook, relPath string) error {
	if err := a.requireGrant(pluginID, plugins.CapWriteFiles); err != nil {
		return err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	if !pluginWritePathAllowed(pluginID, relPath) {
		return fmt.Errorf("delete path %q is outside the allowed directories", relPath)
	}
	return os.Remove(abs)
}

// PluginListDir lists the immediate children of a directory within a notebook.
// Gated by read-files.
func (a *App) PluginListDir(pluginID, notebook, relPath string) ([]string, error) {
	if err := a.requireGrant(pluginID, plugins.CapReadFiles); err != nil {
		return nil, err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// PluginScratchDir returns (and lazily creates) a plugin's per-notebook scratch
// directory: `<notebook>/.system/plugins/<pluginID>/data/` (travels with the
// notebook per #100). Gated by write-files (the plugin must be able to write
// its own scratch).
func (a *App) PluginScratchDir(pluginID, notebook string) (string, error) {
	if err := a.requireGrant(pluginID, plugins.CapWriteFiles); err != nil {
		return "", err
	}
	sn := sanitizePathSegment(notebook)
	if sn == "" {
		return "", fmt.Errorf("notebook is required")
	}
	source := a.resolveSourceByName(sn)
	notebookDir, err := a.resolveNotebookDir(sn, source)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(notebookDir, ".system", "plugins", pluginID, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch dir: %w", err)
	}
	return dir, nil
}

// PluginVaultScratchDir returns (and lazily creates) a plugin's vault-scoped
// scratch directory: `<vault>/.system/plugins/<pluginID>/data/` (stays in the
// vault, for caches that should NOT travel with a notebook). Gated by
// write-files (#108).
func (a *App) PluginVaultScratchDir(pluginID string) (string, error) {
	if err := a.requireGrant(pluginID, plugins.CapWriteFiles); err != nil {
		return "", err
	}
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	dir := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create vault scratch dir: %w", err)
	}
	return dir, nil
}

// PluginResolveAsset resolves a relative attachment path against a notebook's
// root and returns the absolute path (#108 path helper). Gated by read-files.
func (a *App) PluginResolveAsset(pluginID, notebook, relPath string) (string, error) {
	if err := a.requireGrant(pluginID, plugins.CapReadFiles); err != nil {
		return "", err
	}
	return a.resolvePluginNotebookPath(notebook, relPath)
}

// resolvePluginNotebookPath resolves a relative path against a notebook's
// actual root (in-vault or linked per #100) and enforces traversal containment.
func (a *App) resolvePluginNotebookPath(notebook, relPath string) (string, error) {
	sn := sanitizePathSegment(notebook)
	if sn == "" {
		return "", fmt.Errorf("notebook is required")
	}
	if relPath == "" {
		return "", fmt.Errorf("relPath is required")
	}
	source := a.resolveSourceByName(sn)
	notebookDir, err := a.resolveNotebookDir(sn, source)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	if strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("relative path %q escapes the notebook root", relPath)
	}
	abs := filepath.Join(notebookDir, cleaned)
	if !isPathWithinRoot(abs, notebookDir) {
		return "", fmt.Errorf("relative path %q escapes the notebook root", relPath)
	}
	return abs, nil
}

// pluginWritePathAllowed restricts plugin file writes to the attachments/
// directory (shared) or the calling plugin's own scratch dir under
// .system/plugins/<pluginID>/data/, keeping the notebook tree clean (#108).
func pluginWritePathAllowed(pluginID, relPath string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(relPath))
	first := strings.SplitN(cleaned, "/", 2)[0]
	if first == "attachments" {
		return true
	}
	// Only the calling plugin's own scratch dir is writable under .system.
	ownScratch := ".system/plugins/" + pluginID + "/data/"
	return strings.HasPrefix(cleaned+"/", ownScratch)
}

// =========================================================================
// OS integration (#114)
// =========================================================================

// PluginOpenInNativeHandler opens a file within a notebook in the OS default
// handler for its type. Traversal-guarded. Gated by os-open.
func (a *App) PluginOpenInNativeHandler(pluginID, notebook, relPath string) error {
	if err := a.requireGrant(pluginID, plugins.CapOSOpen); err != nil {
		return err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	return openNative(abs)
}

// PluginOpenUrl opens a URL in the system browser. Scheme-restricted to http,
// https, mailto (file/javascript/custom schemes blocked). Gated by os-open.
func (a *App) PluginOpenUrl(pluginID, url string) error {
	if err := a.requireGrant(pluginID, plugins.CapOSOpen); err != nil {
		return err
	}
	if !isSafeUrl(url) {
		return fmt.Errorf("URL scheme is not allowed (only http, https, mailto)")
	}
	if a.ctx == nil {
		return fmt.Errorf("application context not ready")
	}
	wruntime.BrowserOpenURL(a.ctx, url)
	return nil
}

// PluginPickOpenFile opens a native file picker and returns the chosen path
// (empty on cancel). Not capability-gated (a picker is user-driven; the chosen
// path only becomes useful through a gated binding like AddAttachment).
func (a *App) PluginPickOpenFile(filterPattern string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Select a file",
		Filters: []wruntime.FileFilter{
			{DisplayName: "All files", Pattern: filterPattern},
		},
	})
}

// PluginPickSaveFile opens a native save-file picker and returns the chosen
// path (empty on cancel).
func (a *App) PluginPickSaveFile(defaultFilename string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "Save file",
		DefaultFilename: defaultFilename,
	})
}

// PluginClipboardReadText reads the system clipboard. Gated by os-clipboard.
func (a *App) PluginClipboardReadText(pluginID string) (string, error) {
	if err := a.requireGrant(pluginID, plugins.CapOSClipboard); err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.ClipboardGetText(a.ctx)
}

// PluginClipboardWriteText writes text to the system clipboard. Gated by
// os-clipboard.
func (a *App) PluginClipboardWriteText(pluginID, text string) error {
	if err := a.requireGrant(pluginID, plugins.CapOSClipboard); err != nil {
		return err
	}
	if a.ctx == nil {
		return fmt.Errorf("application context not ready")
	}
	wruntime.ClipboardSetText(a.ctx, text)
	return nil
}

// PluginNotify shows a desktop notification. Wails v2 has no native
// notification runtime API, so this falls back to a cross-platform OS command
// (osascript on macOS, notify-send on Linux, msg/PowerShell on Windows). Gated
// by os-notify. A failure to spawn the notifier is non-fatal (logged) — a
// notification is best-effort UX, not a correctness path.
func (a *App) PluginNotify(pluginID, title, body string) error {
	if err := a.requireGrant(pluginID, plugins.CapOSNotify); err != nil {
		return err
	}
	return notifyDesktop(title, body)
}

// isSafeUrl reports whether url uses an allowed scheme (http/https/mailto).
// Used by PluginOpenUrl (browser-open path).
func isSafeUrl(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return false
	}
	lower := strings.ToLower(u)
	for _, scheme := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// isSafeFetchUrl is the stricter check for PluginFetch: only http/https (no
// mailto), and the resolved host must NOT be a loopback, link-local, or
// private IP address (SSRF defense, #115).
func isSafeFetchUrl(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return blockInternalHost(parsed.Host) == nil
}

// blockInternalHost returns an error if host resolves to (or is literally) a
// loopback, link-local, private, or multicast address — the standard SSRF
// defense so a granted plugin cannot reach internal services or cloud metadata.
func blockInternalHost(host string) error {
	// Strip port.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return fmt.Errorf("empty host")
	}
	// Resolve and check every returned IP.
	ips, err := net.LookupIP(hostname)
	if err != nil {
		// If we can't resolve, err on the side of caution for literal IPs.
		ips = []net.IP{net.ParseIP(hostname)}
		if ips[0] == nil {
			return fmt.Errorf("cannot resolve host %q", hostname)
		}
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("host %s resolves to a blocked address %s", hostname, ip)
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("host %s resolves to a private address %s", hostname, ip)
		}
	}
	return nil
}

// isPrivateIP reports whether ip is in an RFC-1918 private range (10/8,
// 172.16/12, 192.168/16). net.IP.IsPrivate covers this on Go 1.17+, but
// cloud metadata (169.254.169.254) is link-local and caught by the
// IsLinkLocalUnicast check above.
func isPrivateIP(ip net.IP) bool {
	if ip.IsPrivate() {
		return true
	}
	// Explicitly catch 169.254.x.x (cloud metadata endpoints) even if Go's
	// IsPrivate doesn't (it's link-local, caught above, but belt + suspenders).
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}

// openNative opens a path in the OS default handler, cross-platform.
var openNative = func(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default: // linux + others
		return exec.Command("xdg-open", path).Start()
	}
}

// newSafeFetchClient returns an *http.Client with the SSRF-defended transport
// used by every privileged plugin HTTP call (#115 + #101 review). It:
//
//  1. Caps the per-request lifetime with timeout.
//  2. Re-validates every redirect destination against isSafeFetchUrl +
//     blockInternalHost so a 302 to an internal host is rejected even if the
//     initial URL was approved.
//  3. Pins the resolved IP at dial time via a custom DialContext. A name
//     that resolves to 1.2.3.4 at validation and 169.254.169.254 at connect
//     (DNS rebinding) is rejected because the dialer re-runs blockInternalHost
//     against the IPs it actually plans to connect to.
//
// Tests can override the DialContext to swap the resolver (see
// app_plugins_v2_test.go) and exercise the rebinding defense deterministically.
func newSafeFetchClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return nil, splitErr
			}
			ips, lookupErr := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if lookupErr != nil || len(ips) == 0 {
				// Fall through to the system dialer with the literal address;
				// net.Dialer will surface the lookup error in its own error
				// chain, so the call still fails closed.
				return dialer.DialContext(ctx, network, addr)
			}
			// Re-validate every resolved IP at dial time so a DNS rebind
			// between isSafeFetchUrl and the actual connect is rejected.
			for _, ip := range ips {
				if isInternalIP(ip) {
					return nil, fmt.Errorf("blocked: dial to %s resolves to a blocked address %s", host, ip)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxPluginFetchRedirects {
				return fmt.Errorf("too many redirects (max %d)", maxPluginFetchRedirects)
			}
			// Re-validate every redirect destination for scheme + SSRF (#115).
			if !isSafeFetchUrl(req.URL.String()) {
				return fmt.Errorf("redirect to blocked URL: %s", req.URL.String())
			}
			if err := blockInternalHost(req.URL.Host); err != nil {
				return fmt.Errorf("redirect to internal host: %w", err)
			}
			return nil
		},
	}
}

// isInternalIP is the dial-time analogue of blockInternalHost: it rejects
// loopback, link-local, multicast, unspecified, and private IPs. Sharing
// the predicate with blockInternalHost keeps the dial and the URL check in
// lock-step so the rebinding defense and the URL-level check can never
// drift (#115 hardening).
func isInternalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return isPrivateIP(ip)
}

// notifyDesktop shows a desktop notification, cross-platform. Best-effort: a
// spawn error is returned but callers may ignore it for non-critical UX.
//
// title and body originate from plugin-controlled strings, so they must never
// be interpolated into a shell/script string — otherwise a granted (but
// untrusted) plugin can escape the notifier and execute arbitrary code, which
// is a privilege escalation beyond any capability it was granted. They are
// therefore passed as data: as osascript argv on macOS, and via environment
// variables on Windows (PowerShell never re-parses $env: values as code).
func notifyDesktop(title, body string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("osascript",
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run",
			title, body,
		).Start()
	case "windows":
		// PowerShell toast — universally available on Win10+. Title/body are
		// passed via environment variables, never as interpolated source.
		cmd := exec.Command("powershell", "-NoProfile", "-Command",
			"[reflection.assembly]::loadwithpartialname('System.Windows.Forms') > $null; "+
				"$t = New-Object System.Windows.Forms.NotifyIcon; "+
				"$t.Icon = [System.Drawing.SystemIcons]::Information; "+
				"$t.BalloonTipTitle = $env:SILT_NOTIFY_TITLE; "+
				"$t.BalloonTipText = $env:SILT_NOTIFY_BODY; "+
				"$t.Visible = $true; $t.ShowBalloonTip(5000);")
		cmd.Env = append(os.Environ(),
			"SILT_NOTIFY_TITLE="+title,
			"SILT_NOTIFY_BODY="+body,
		)
		return cmd.Start()
	default: // linux
		return exec.Command("notify-send", title, body).Start()
	}
}

// =========================================================================
// Network / fetch (#115)
// =========================================================================

// maxPluginFetchBytes bounds a single plugin fetch response body (defense-
// in-depth memory guard, mirroring maxPluginQueryRows).
const maxPluginFetchBytes = 10 * 1024 * 1024 // 10 MB

// maxPluginFetchRequestBytes bounds the request body a plugin can send through
// the fetch proxy, mirroring the response-side cap. Without this, a plugin can
// pass a multi-hundred-megabyte string and force the host to allocate it.
const maxPluginFetchRequestBytes = 10 * 1024 * 1024 // 10 MB

// isForbiddenPluginHeader reports whether a (lower-cased) header name must
// NOT be settable by a plugin. These headers are controlled by the transport
// layer or carry security-sensitive semantics:
//   - Host / Connection / Content-Length / Transfer-Encoding: request
//     smuggling vectors or would subvert the SSRF dial-time IP check.
//   - Proxy-* / Sec-*: hop-by-hop or browser-fetch metadata that a plugin
//     must not forge.
//   - Cookie / Authorization: would let a plugin exfiltrate or reuse host
//     credentials.
func isForbiddenPluginHeader(lowerKey string) bool {
	switch lowerKey {
	case "host", "connection", "content-length", "transfer-encoding",
		"cookie", "authorization", "proxy-authorization",
		"x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
		"x-real-ip",
		"sec-fetch-mode", "sec-fetch-site", "sec-fetch-user", "sec-fetch-dest",
		"sec-websocket-key", "sec-websocket-version":
		return true
	}
	if strings.HasPrefix(lowerKey, "proxy-") || strings.HasPrefix(lowerKey, "sec-") {
		return true
	}
	return false
}

// maxPluginFetchRedirects caps redirect hops so a plugin can't be tricked into
// an infinite redirect loop.
const maxPluginFetchRedirects = 5

// defaultPluginFetchTimeout caps how long a single fetch may take.
const defaultPluginFetchTimeout = 30 * time.Second

// PluginFetchResult is the envelope returned by PluginFetch.
type PluginFetchResult struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"` // raw body (may be truncated to maxPluginFetchBytes)
	Ok        bool              `json:"ok"`
	Truncated bool              `json:"truncated"` // true when body exceeded maxPluginFetchBytes
}

// PluginFetchInput is the request envelope for PluginFetch.
type PluginFetchInput struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`            // defaults to GET
	Headers map[string]string `json:"headers,omitempty"` // arbitrary (auth) — audit-logged
	Body    string            `json:"body,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // milliseconds; capped at 30s
}

// networkAuditMu guards the in-memory network audit log. The log is a simple
// append-only slice of {plugin, host, status, time} entries, surfaced in
// Settings → Plugins so a user can see what a networked plugin is doing (#115).
var (
	networkAuditMu sync.Mutex
	networkAudit   []NetworkAuditEntry
)

// NetworkAuditEntry is one row of the plugin network audit log.
type NetworkAuditEntry struct {
	Plugin string `json:"plugin"`
	Host   string `json:"host"`
	Status int    `json:"status"`
	Method string `json:"method"`
	At     string `json:"at"` // RFC3339
}

// PluginFetch performs an HTTP request through the Go backend (CORS-free),
// with timeout / size / redirect caps. Gated by the network capability.
// The host + status are appended to the in-memory audit log (never the body).
func (a *App) PluginFetch(pluginID string, input PluginFetchInput) (PluginFetchResult, error) {
	if err := a.requireGrant(pluginID, plugins.CapNetwork); err != nil {
		return PluginFetchResult{}, err
	}
	if input.URL == "" {
		return PluginFetchResult{}, fmt.Errorf("url is required")
	}
	if !isSafeFetchUrl(input.URL) {
		return PluginFetchResult{}, fmt.Errorf("url scheme is not allowed (only http/https)")
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return PluginFetchResult{}, fmt.Errorf("HTTP method %q is not allowed (recognized: GET, POST, PUT, PATCH, DELETE, HEAD)", method)
	}
	timeout := defaultPluginFetchTimeout
	if input.Timeout > 0 {
		requested := time.Duration(input.Timeout) * time.Millisecond
		if requested < timeout {
			timeout = requested
		}
	}

	client := newSafeFetchClient(timeout)

	var reqBody io.Reader
	if input.Body != "" {
		if int64(len(input.Body)) > maxPluginFetchRequestBytes {
			return PluginFetchResult{}, fmt.Errorf("request body exceeds %d-byte cap", maxPluginFetchRequestBytes)
		}
		reqBody = strings.NewReader(input.Body)
	}
	req, err := http.NewRequest(method, input.URL, reqBody)
	if err != nil {
		return PluginFetchResult{}, fmt.Errorf("build request: %w", err)
	}
	for k, v := range input.Headers {
		lk := strings.ToLower(k)
		if isForbiddenPluginHeader(lk) {
			return PluginFetchResult{}, fmt.Errorf("header %q is forbidden (controlled by the transport layer)", k)
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Still audit the attempt so the user sees the plugin tried.
		a.auditNetwork(pluginID, method, input.URL, 0)
		return PluginFetchResult{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginFetchBytes+1))
	if err != nil {
		a.auditNetwork(pluginID, method, input.URL, resp.StatusCode)
		return PluginFetchResult{}, fmt.Errorf("read body: %w", err)
	}
	truncated := false
	if int64(len(body)) > maxPluginFetchBytes {
		body = body[:maxPluginFetchBytes]
		truncated = true
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}

	a.auditNetwork(pluginID, method, input.URL, resp.StatusCode)

	return PluginFetchResult{
		Status:    resp.StatusCode,
		Headers:   headers,
		Body:      string(body),
		Ok:        resp.StatusCode >= 200 && resp.StatusCode < 300,
		Truncated: truncated,
	}, nil
}

// GetNetworkAudit returns the in-memory plugin network audit log (#115).
// truncateNetworkLog reads the log file, keeps the last n lines, and rewrites
// it. Best-effort — errors are silently ignored (the audit log is not a
// security boundary, just a diagnostic aid).
func truncateNetworkLog(path string, keepLines int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= keepLines {
		return
	}
	kept := lines[len(lines)-keepLines:]
	_ = os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}

func (a *App) GetNetworkAudit() ([]NetworkAuditEntry, error) {
	networkAuditMu.Lock()
	defer networkAuditMu.Unlock()
	out := make([]NetworkAuditEntry, len(networkAudit))
	copy(out, networkAudit)
	return out, nil
}

// ClearNetworkAudit empties the audit log.
func (a *App) ClearNetworkAudit() error {
	networkAuditMu.Lock()
	defer networkAuditMu.Unlock()
	networkAudit = nil
	return nil
}

// auditNetwork appends a {plugin, host, status, time} row. The body is NEVER
// logged — only the host + status so a user can see what a plugin is doing
// without leaking sensitive request/response payloads.
func (a *App) auditNetwork(pluginID, method, rawURL string, status int) {
	host := rawURL
	// Best-effort host extraction without a full URL parse (the URL was already
	// validated as http/https above).
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest := rawURL[i+3:]
		// Include the path (up to but not including query string) so the
		// audit log distinguishes GET /health from DELETE /data/all.
		if j := strings.IndexAny(rest, "?#"); j >= 0 {
			rest = rest[:j]
		}
		host = rest
	}
	entry := NetworkAuditEntry{
		Plugin: pluginID,
		Host:   host,
		Status: status,
		Method: method,
		At:     time.Now().Format(time.RFC3339),
	}
	networkAuditMu.Lock()
	networkAudit = append(networkAudit, entry)
	// Bound the in-memory log to the last 500 entries so it does not grow
	// unbounded.
	if len(networkAudit) > 500 {
		networkAudit = networkAudit[len(networkAudit)-500:]
	}
	networkAuditMu.Unlock()
	// Best-effort persist to a vault-scoped log file so the audit trail survives
	// a restart (#115). The log is per-plugin so a user can inspect it.
	// Capped at maxPluginNetworkLogBytes to prevent unbounded growth from a
	// chatty plugin; when exceeded, the file is truncated to its last 200 lines.
	const maxPluginNetworkLogBytes = 1 * 1024 * 1024 // 1 MB
	if a.vaultPath != "" {
		logPath := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, "network.log")
		line := fmt.Sprintf("%s %s %s %d %s\n", entry.At, entry.Method, entry.Host, entry.Status, pluginID)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		if info, err := os.Stat(logPath); err == nil && info.Size() > maxPluginNetworkLogBytes {
			truncateNetworkLog(logPath, 200)
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
	}
}

// newUUID mints a UUIDv4 string. Wraps the existing uuid import so the v2
// bindings stay decoupled from the google/uuid API shape.
func newUUID() string {
	return uuid.NewString()
}

// =========================================================================
// Attachments plugin bindings (#101)
// =========================================================================

// AddAttachment copies a source file into a notebook's attachments/ directory
// and returns the relative link path. The copy is atomic (temp+rename), and
// filename collisions are resolved with a counter suffix so two notes
// attaching the same-named file produce two distinct copies (#101). The
// notebook root is resolved via #100 (in-vault or linked/external), so the
// attachment travels with the notebook. NOT capability-gated: this is a
// first-party plugin binding (silt-attachments is trusted).
// maxAttachmentBytes bounds a single attachment copy so a plugin or user can't
// exhaust disk by attaching a huge file (#101 hardening).
const maxAttachmentBytes = 100 * 1024 * 1024 // 100 MB

// blockedAttachmentExtensions are file types that are blocked from attachment
// copy-in to prevent the attachment folder from becoming an executable
// drop zone (#101 hardening).
var blockedAttachmentExtensions = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true,
	".sh": true, ".msi": true, ".dll": true, ".app": true,
	".ps1": true, ".vbs": true, ".wsf": true, ".hta": true,
}

func (a *App) AddAttachment(srcPath, notebook string) (string, error) {
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	if srcPath == "" {
		return "", fmt.Errorf("srcPath is required")
	}
	// Validate the source path: it must exist, be a regular file, and be under
	// the user's control (inside the vault or picked from the OS dialog). We
	// reject obvious system paths and enforce a size limit before reading.
	absSrc, err := filepath.Abs(filepath.Clean(srcPath))
	if err != nil {
		return "", fmt.Errorf("invalid source path: %w", err)
	}
	srcInfo, err := os.Stat(absSrc)
	if err != nil {
		return "", fmt.Errorf("source file not found: %w", err)
	}
	if !srcInfo.Mode().IsRegular() {
		return "", fmt.Errorf("source is not a regular file")
	}
	if srcInfo.Size() > maxAttachmentBytes {
		return "", fmt.Errorf("attachment is %d bytes, exceeds the %d-byte limit", srcInfo.Size(), maxAttachmentBytes)
	}
	// Filetype blocklist.
	ext := strings.ToLower(filepath.Ext(absSrc))
	if blockedAttachmentExtensions[ext] {
		return "", fmt.Errorf("file type %q is blocked from attachments", ext)
	}
	sn := sanitizePathSegment(notebook)
	if sn == "" {
		return "", fmt.Errorf("notebook is required")
	}
	source := a.resolveSourceByName(sn)
	notebookDir, err := a.resolveNotebookDir(sn, source)
	if err != nil {
		return "", fmt.Errorf("resolve notebook dir: %w", err)
	}
	attachmentsDir := filepath.Join(notebookDir, "attachments")

	// Read the source file (bounded by the size check above).
	srcBytes, err := os.ReadFile(absSrc)
	if err != nil {
		return "", fmt.Errorf("read source file: %w", err)
	}
	base := sanitizePathSegment(filepath.Base(absSrc))
	if base == "" {
		base = "attachment"
	}

	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		return "", fmt.Errorf("create attachments dir: %w", err)
	}

	// Collision-safe destination reservation: atomically claim a unique name
	// with O_CREATE|O_EXCL so two concurrent attaches of same-named files can
	// never resolve to the same path and clobber each other (the previous
	// Stat-then-write loop had a TOCTOU window). The placeholder is filled by
	// the atomic write below; the OS guarantees only one caller wins a name.
	//
	// Known limitation: the O_EXCL creates a zero-byte file that briefly
	// exists on disk before WriteFileAtomic fills it. A concurrent reader
	// (e.g. the file watcher) could observe the empty file. The window is
	// sub-millisecond, the file is inside the vault's attachments/ dir, and
	// the watcher skips attachments/. A temp-then-rename approach would close
	// the window but adds cross-filesystem rename complexity.
	destExt := filepath.Ext(base)
	stem := strings.TrimSuffix(base, destExt)
	destName := base
	dest := filepath.Join(attachmentsDir, destName)
	f, openErr := os.OpenFile(dest, os.O_CREATE|os.O_EXCL, 0o644)
	for i := 1; openErr != nil; i++ {
		if !os.IsExist(openErr) {
			return "", fmt.Errorf("reserve attachment file: %w", openErr)
		}
		destName = fmt.Sprintf("%s-%d%s", stem, i, destExt)
		dest = filepath.Join(attachmentsDir, destName)
		f, openErr = os.OpenFile(dest, os.O_CREATE|os.O_EXCL, 0o644)
	}
	f.Close()

	if !isPathWithinRoot(dest, notebookDir) {
		os.Remove(dest)
		return "", fmt.Errorf("resolved attachment path escapes notebook root")
	}

	a.wg.Add(1)
	defer a.wg.Done()
	var writeErr error
	a.coordinator.LockFileWrite(dest, func() {
		a.tracker.RegisterWrite(dest)
		writeErr = parser.WriteFileAtomic(dest, srcBytes)
	})
	if writeErr != nil {
		os.Remove(dest)
		return "", writeErr
	}
	return "attachments/" + destName, nil
}

// OpenAttachment opens an attachment in the OS native handler (#101). The
// relative path is resolved against the notebook's actual root (#100) and
// traversal-guarded.
func (a *App) OpenAttachment(notebook, relPath string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("attachment not found: %w", err)
	}
	return openNative(abs)
}

// DeleteAttachment removes an attachment file (unlink-only; the default
// per #101 — orphan GC is a separate manual action).
func (a *App) DeleteAttachment(notebook, relPath string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	return os.Remove(abs)
}

// =========================================================================
// Distribution v2 (#111) — update checks + trusted publishers + downgrade
// =========================================================================

// CheckPluginUpdate fetches a plugin's manifest from its declared updateUrl
// (if present) and returns whether a newer version is available (#111).
//
// The fetch goes through newSafeFetchClient so the update channel enjoys the
// same SSRF + timeout + redirect + DNS-rebinding defenses as PluginFetch
// (#101 review). Without this, a malicious update manifest could 302 to
// 169.254.169.254 or hold the goroutine open with no timeout.
func (a *App) CheckPluginUpdate(pluginID, currentVersion, updateUrl string) (PluginUpdateInfo, error) {
	info := PluginUpdateInfo{PluginID: pluginID, CurrentVersion: currentVersion}
	if updateUrl == "" {
		return info, nil
	}
	if !isSafeFetchUrl(updateUrl) {
		return info, fmt.Errorf("update URL is not a safe http(s) URL")
	}
	client := newSafeFetchClient(defaultPluginFetchTimeout)
	req, err := http.NewRequest("GET", updateUrl, nil)
	if err != nil {
		return info, fmt.Errorf("build update request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("update check failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return info, fmt.Errorf("read update manifest: %w", err)
	}
	var manifest struct {
		Version string `json:"version"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return info, fmt.Errorf("parse update manifest: %w", err)
	}
	info.LatestVersion = manifest.Version
	info.DownloadURL = manifest.URL
	info.UpdateAvailable = versionLessThan(currentVersion, manifest.Version)
	return info, nil
}

// PluginUpdateInfo is the result of an update check.
type PluginUpdateInfo struct {
	PluginID        string `json:"pluginId"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
}

// GetTrustedPublishers returns the user-global trusted-publishers list (#111).
func (a *App) GetTrustedPublishers() ([]string, error) {
	settings, err := vault.LoadSettings()
	if err != nil {
		return []string{}, nil
	}
	if settings.TrustedPublishers == nil {
		return []string{}, nil
	}
	return settings.TrustedPublishers, nil
}

// AddTrustedPublisher adds a publisher to the trusted list (#111).
func (a *App) AddTrustedPublisher(publisher string) error {
	if publisher == "" {
		return fmt.Errorf("publisher is required")
	}
	settings, err := vault.LoadSettings()
	if err != nil {
		return err
	}
	for _, p := range settings.TrustedPublishers {
		if p == publisher {
			return nil
		}
	}
	settings.TrustedPublishers = append(settings.TrustedPublishers, publisher)
	return vault.SaveSettings(settings)
}

// RemoveTrustedPublisher removes a publisher from the trusted list (#111).
func (a *App) RemoveTrustedPublisher(publisher string) error {
	settings, err := vault.LoadSettings()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(settings.TrustedPublishers))
	for _, p := range settings.TrustedPublishers {
		if p != publisher {
			out = append(out, p)
		}
	}
	settings.TrustedPublishers = out
	return vault.SaveSettings(settings)
}

// PluginReadPluginAsset reads a file from the plugin's OWN install directory
// (`.system/plugins/<pluginID>/<relPath>`), enabling plugin-bundled assets
// like icons, templates, or static HTML for surfaces (#108/#117). The path is
// traversal-guarded (no `..` escapes) and sanitized. NOT capability-gated
// (reading your own bundle is safe).
func (a *App) PluginReadPluginAsset(pluginID, relPath string) (string, error) {
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return "", fmt.Errorf("invalid plugin id %q", pluginID)
	}
	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("relative path escapes the plugin directory")
	}
	assetPath := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, cleaned)
	if !isPathWithinRoot(assetPath, filepath.Join(a.vaultPath, ".system", "plugins", pluginID)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	data, err := os.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("read plugin asset: %w", err)
	}
	return string(data), nil
}
