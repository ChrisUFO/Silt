package main

import (
	"fmt"
	"os"
	"path/filepath"
	"silt/backend/parser"
	"silt/backend/plugins"
	"sort"
	"strings"
)

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
func (a *App) PluginWriteFile(pluginID, sessionToken, notebook, relPath string, data []byte) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
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
// directory the plugin owns (per-notebook + vault + linked notebooks). Walks
// the relevant trees directly so a stale cumulative counter cannot drift out
// of sync with disk. Linked notebook roots are enumerated via cfg.LinkedNotebooks
// (#159) so a plugin cannot bypass the cap by writing into a linked notebook's
// scratch dir.
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
	// Linked notebook scratch dirs: each linked root is a notebook whose
	// scratch dir lives at <linkedRoot>/.system/plugins/<pluginID>/data/.
	// Without this, a plugin can bypass the cap by writing into a linked
	// notebook's scratch dir (#159). Snapshot only the root paths under the
	// config lock, then run the (slow) recursive dirSizeUnder walks without
	// it so a plugin size check can't stall config readers/writers.
	a.configMu.RLock()
	linkedRoots := make([]string, 0, len(a.cfg.LinkedNotebooks))
	for _, ln := range a.cfg.LinkedNotebooks {
		linkedRoots = append(linkedRoots, ln.RootPath)
	}
	a.configMu.RUnlock()
	for _, root := range linkedRoots {
		linkedDir := filepath.Join(root, ".system", "plugins", pluginID, "data")
		n, err := dirSizeUnder(linkedDir)
		if err != nil {
			return 0, err
		}
		total += n
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
func (a *App) PluginDeleteFile(pluginID, sessionToken, notebook, relPath string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
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
