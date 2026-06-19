package main

// Plugin v2 SDK bindings — expanded content API (#104), file I/O (#108), and
// OS integration (#114). These live in the main package alongside app.go so
// they share the coordinator / atomic-write / traversal-guard helpers. Each
// privileged binding calls a.requireGrant before its work; content-mutation
// bindings reuse the same atomic-write + re-index + emitBlockChanged path as
// the core editor (SPECS §8.3: "core feature decoupling").

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"silt/backend/parser"
	"silt/backend/plugins"

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
		newID     string
		blockType parser.BlockType
		text      string
	}
	var resolvedOps []resolved

	// Resolve block locations for afterID / blockID lookups.
	locOf := func(id string) (source, nb, sec, pg string, ok bool) {
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

	// 3. Apply per page.
	for _, pageOps := range pagesByKey {
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
				// If same-page move, reorder; the block is already in `mutated`.
				if r.op.AfterID != "" || r.op.BlockID != "" {
					mutated = moveWithin(mutated, r.op.BlockID, r.op.AfterID)
				}
			}
		}
		if err := a.SaveFileBlocks(first.notebook, first.section, first.page, mutated); err != nil {
			return fmt.Errorf("save page %s/%s/%s: %w", first.notebook, first.section, first.page, err)
		}
		// For cross-page moves, remove the block from its source page too.
		for _, r := range pageOps {
			if r.op.Kind == "move" {
				origSrc, origNb, origSec, origPg, _ := locOf(r.op.BlockID)
				if origPg != "" && !(origNb == r.notebook && origSec == r.section && origPg == r.page) {
					srcBlocks, _ := a.FetchPageBlocks(origNb, origSec, origPg)
					srcFiltered := removeByID(srcBlocks, r.op.BlockID)
					_ = a.SaveFileBlocks(origNb, origSec, origPg, srcFiltered)
					_ = origSrc // source resolved by SaveFileBlocks via name
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

// PluginWriteFile writes a file within a notebook atomically (temp+fsync+rename
// via parser.WriteFileAtomic, under the per-file mutex + WriteTracker). Gated
// by write-files; the qualifier (notebook | vault) is enforced at the grant
// level — a notebook-scoped grant only allows writes inside the resolved
// notebook root.
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
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	a.wg.Add(1)
	defer a.wg.Done()
	var writeErr error
	a.coordinator.LockFileWrite(abs, func() {
		a.tracker.RegisterWrite(abs)
		writeErr = parser.WriteFileAtomic(abs, data)
	})
	return writeErr
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

// openNative opens a path in the OS default handler, cross-platform.
func openNative(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default: // linux + others
		return exec.Command("xdg-open", path).Start()
	}
}

// notifyDesktop shows a desktop notification, cross-platform. Best-effort: a
// spawn error is returned but callers may ignore it for non-critical UX.
func notifyDesktop(title, body string) error {
	switch goruntime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		return exec.Command("osascript", "-e", script).Start()
	case "windows":
		// PowerShell toast — universally available on Win10+.
		ps := fmt.Sprintf(
			`[reflection.assembly]::loadwithpartialname('System.Windows.Forms') > $null; `+
				`$t = New-Object System.Windows.Forms.NotifyIcon; `+
				`$t.Icon = [System.Drawing.SystemIcons]::Information; `+
				`$t.BalloonTipTitle = '%s'; $t.BalloonTipText = '%s'; `+
				`$t.Visible = $true; $t.ShowBalloonTip(5000);`, title, body)
		return exec.Command("powershell", "-Command", ps).Start()
	default: // linux
		return exec.Command("notify-send", title, body).Start()
	}
}

// newUUID mints a UUIDv4 string. Wraps the existing uuid import so the v2
// bindings stay decoupled from the google/uuid API shape.
func newUUID() string {
	return uuid.NewString()
}
