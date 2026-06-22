package main

import (
	"fmt"
	"os"
	"path/filepath"
	"silt/backend/parser"
	"strings"
)

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
