package updates

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"
)

// downloadTimeout caps a full asset download. Assets are tens of MiB; 10
// minutes is generous while still bounding a stalled connection.
const downloadTimeout = 10 * time.Minute

// downloadBufferSize is the chunk size for streamed copies; each completed
// buffer triggers a progress callback so the UI bar stays live without
// per-byte overhead.
const downloadBufferSize = 64 * 1024

// Download streams assetURL into a temp file and returns the file's path.
// emitProgress (if non-nil) is called with (bytesReceived, totalBytes) as the
// download proceeds; totalBytes is ContentLength (-1 when unknown). On any
// error — including context cancellation — the temp file is removed and a
// non-empty path is never returned.
//
// The returned path is owned by the caller; it must remove the file once the
// installer has launched (or on failure of a later step).
func Download(ctx context.Context, client *http.Client, assetURL string, emitProgress func(received, total int64)) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "Silt-updater")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %s", resp.Status)
	}

	// Temp file in the OS temp dir, preserving the asset's extension so the
	// Windows launcher can re-derive the installer type if needed. path.Ext
	// (not filepath.Ext) is correct because the download URL is a URL path,
	// not an OS-native path.
	tmp, err := os.CreateTemp("", "silt-update-*"+path.Ext(assetURL))
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpPath) }

	// Stream with progress. On error mid-copy the temp file is removed so a
	// partial download never survives.
	buf := make([]byte, downloadBufferSize)
	var received int64
	total := resp.ContentLength
	for {
		select {
		case <-ctx.Done():
			cleanup()
			return "", ctx.Err()
		default:
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				cleanup()
				return "", fmt.Errorf("write temp file: %w", werr)
			}
			received += int64(n)
			if emitProgress != nil {
				emitProgress(received, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			cleanup()
			return "", fmt.Errorf("read response body: %w", readErr)
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp file: %w", err)
	}
	return tmpPath, nil
}

// removeQuiet removes a path, ignoring errors. Used to clean up a downloaded
// asset after a failed verification so a partial/bad file is never left behind
// for a later run to accidentally execute.
func removeQuiet(path string) { _ = os.Remove(path) }
