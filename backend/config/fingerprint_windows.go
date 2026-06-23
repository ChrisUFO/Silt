//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// computeFingerprint returns a "volume:fileindex" fingerprint for Windows.
// GetFileInformationByHandle returns the NTFS stable file identity (volume
// serial + file index), which changes when a directory is moved to a
// different volume (the re-link prompt handles this) but is stable for
// renames within the same volume.
func computeFingerprint(absPath string, info os.FileInfo) (string, error) {
	// Open the directory to get a handle, then query its file information.
	// The path must be absolute and cleaned (LinkNotebook ensures this).
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("fingerprint: abs %q: %w", absPath, err)
	}
	// windows.CreateFile uses UTF16; FILE_FLAG_BACKUP_SEMANTICS is required
	// to open a directory handle on Windows.
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(abs),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", fmt.Errorf("fingerprint: CreateFile %q: %w", abs, err)
	}
	defer windows.CloseHandle(h)
	var fd windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fd); err != nil {
		return "", fmt.Errorf("fingerprint: GetFileInformationByHandle %q: %w", abs, err)
	}
	return fmt.Sprintf("%x:%x%x", fd.VolumeSerialNumber, fd.FileIndexHigh, fd.FileIndexLow), nil
}
