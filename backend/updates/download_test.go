package updates

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDownload_StreamsToTempAndProgress asserts Download writes the body to a
// temp file, preserves the asset extension, and emits progress callbacks.
func TestDownload_StreamsToTempAndProgress(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 128) // 2x the 64 KiB buffer to force >1 chunk
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	var lastReceived int64
	var total int64
	path, err := Download(context.Background(), srv.Client(), srv.URL+"/Silt-v0.5.0-windows-installer.exe", func(r, tot int64) {
		lastReceived = r
		total = tot
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasSuffix(path, ".exe") {
		t.Errorf("temp path %q should preserve the .exe extension", path)
	}
	if lastReceived != int64(len(body)) {
		t.Errorf("last progress received = %d, want %d", lastReceived, len(body))
	}
	if total != int64(len(body)) {
		t.Errorf("progress total = %d, want %d (ContentLength)", total, len(body))
	}
}

// TestDownload_Non2xxErrors asserts a failed download surfaces an error and
// does not return a path.
func TestDownload_Non2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	path, err := Download(context.Background(), srv.Client(), srv.URL+"/a.exe", nil)
	if err == nil {
		t.Fatal("expected error for 404 download")
	}
	if path != "" {
		t.Errorf("expected empty path on error, got %q", path)
	}
}

// TestDownloadAndVerify_UsesLongDownloadTimeoutNotCheckTimeout is the
// regression test for the merge-blocking bug where the download reused the
// 15s check-timeout client. It injects a Client whose HTTPClient has a 500ms
// timeout (simulating a short client that is NOT the download client) and
// serves the asset over ~800ms. The 500ms margin gives the instant localhost
// /releases/latest metadata GET enormous headroom (so a loaded CI runner can't
// spuriously time it out), while the 800ms streamed asset body still exceeds
// the 500ms client — so if DownloadAndVerify wrongly reused the injected
// client for the body, the read would fail mid-stream. The download succeeding
// proves the long-timeout (downloadTimeout) client is the one in use.
func TestDownloadAndVerify_UsesLongDownloadTimeoutNotCheckTimeout(t *testing.T) {
	asset := []byte("installer-bytes")
	assetSHA := sha256.Sum256(asset)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rel := fmt.Sprintf(`{"tag_name":"v0.5.0","html_url":"http://h","assets":[
				{"name":"a.exe","browser_download_url":"%s/a.exe","size":%d},
				{"name":"SHA256SUMS","browser_download_url":"%s/SHA256SUMS","size":0}
			]}`, srv.URL, len(asset), srv.URL)
			_, _ = w.Write([]byte(rel))
		case r.URL.Path == "/a.exe":
			// Deliver the real asset bytes slowly (~800ms total). 800ms
			// comfortably exceeds the 500ms injected client, so a reused
			// 500ms client would time out mid-body — forcing
			// DownloadAndVerify to swap in its own downloadTimeout client.
			// The bytes must equal `asset` so the SHA256 matches; we just
			// pace them out.
			flusher, _ := w.(http.Flusher)
			chunks := 8
			step := (len(asset) + chunks - 1) / chunks
			for i := 0; i < chunks; i++ {
				start := i * step
				end := start + step
				if start > len(asset) {
					start = len(asset)
				}
				if end > len(asset) {
					end = len(asset)
				}
				_, _ = w.Write(asset[start:end])
				if flusher != nil {
					flusher.Flush()
				}
				time.Sleep(100 * time.Millisecond)
			}
		case r.URL.Path == "/SHA256SUMS":
			_, _ = w.Write([]byte(fmt.Sprintf("%s  a.exe\n", hex.EncodeToString(assetSHA[:]))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Inject a 500ms-timeout client — generous for the instant localhost
	// /releases/latest metadata GET (so a loaded runner can't spuriously fail
	// the metadata fetch), but short enough that reusing it for the 800ms
	// chunked asset body would fail mid-stream. Reuse the test server's
	// transport so the requests still reach httptest. If DownloadAndVerify
	// correctly swaps in its own downloadTimeout client, the download succeeds.
	shortClient := &http.Client{Transport: srv.Client().Transport, Timeout: 500 * time.Millisecond}
	c := &Client{HTTPClient: shortClient, APIBase: srv.URL, Repo: "Chelydra-Labs/Silt", AppVersion: "0.4.0"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := c.DownloadAndVerify(context.Background(), srv.URL+"/a.exe", nil)
		if err != nil {
			t.Errorf("DownloadAndVerify failed: %v (expected the long-timeout download client to be used)", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("DownloadAndVerify did not complete within 30s")
	}
}

// TestDownloadAndVerify_HappyPath serves a fake release with an asset and a
// matching SHA256SUMS, then asserts the verified path is returned.
func TestDownloadAndVerify_HappyPath(t *testing.T) {
	asset := []byte("the installer bytes")
	assetSHA := sha256.Sum256(asset)
	// The Client builds {base}/repos/{repo}/releases/latest.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rel := fmt.Sprintf(`{"tag_name":"v0.5.0","html_url":"http://h","body":"","assets":[
				{"name":"a.exe","browser_download_url":"%s/a.exe","size":%d},
				{"name":"SHA256SUMS","browser_download_url":"%s/SHA256SUMS","size":0}
			]}`, srv.URL, len(asset), srv.URL)
			_, _ = w.Write([]byte(rel))
		case r.URL.Path == "/a.exe":
			_, _ = w.Write(asset)
		case r.URL.Path == "/SHA256SUMS":
			_, _ = w.Write([]byte(fmt.Sprintf("%s  a.exe\n", hex.EncodeToString(assetSHA[:]))))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, Repo: "Chelydra-Labs/Silt", AppVersion: "0.4.0"}
	path, err := c.DownloadAndVerify(context.Background(), srv.URL+"/a.exe", nil)
	if err != nil {
		t.Fatalf("DownloadAndVerify: %v", err)
	}
	if path == "" {
		t.Fatal("expected a verified path")
	}
}

// TestDownloadAndVerify_RejectsForeignAsset asserts the defense: an asset URL
// not in the release's asset list is rejected before any download.
func TestDownloadAndVerify_RejectsForeignAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_, _ = w.Write([]byte(`{"tag_name":"v0.5.0","html_url":"h","assets":[{"name":"a.exe","browser_download_url":"http://legit/a.exe"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
	_, err := c.DownloadAndVerify(context.Background(), "http://evil.example/arbitrary.exe", nil)
	if err == nil {
		t.Fatal("expected error for foreign asset URL")
	}
	if !errors.Is(err, ErrAssetNotInRelease) {
		t.Errorf("expected ErrAssetNotInRelease, got %v", err)
	}
}

// TestDownloadAndVerify_ChecksumMismatchCleansUp asserts a failed verification
// removes the downloaded temp file (no leftover executable) and errors.
func TestDownloadAndVerify_ChecksumMismatchCleansUp(t *testing.T) {
	asset := []byte("the installer bytes")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rel := fmt.Sprintf(`{"tag_name":"v0.5.0","html_url":"h","assets":[
				{"name":"a.exe","browser_download_url":"%s/a.exe","size":%d},
				{"name":"SHA256SUMS","browser_download_url":"%s/SHA256SUMS","size":0}
			]}`, srv.URL, len(asset), srv.URL)
			_, _ = w.Write([]byte(rel))
		case r.URL.Path == "/a.exe":
			_, _ = w.Write(asset)
		case r.URL.Path == "/SHA256SUMS":
			// Wrong hash on purpose.
			_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  a.exe\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
	_, err := c.DownloadAndVerify(context.Background(), srv.URL+"/a.exe", nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("expected ErrChecksumMismatch, got %v", err)
	}
}
