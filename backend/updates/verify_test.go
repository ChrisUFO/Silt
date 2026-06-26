package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempAsset writes content to a temp file and returns its path + the
// file's SHA256 hex. t.Cleanup removes the file.
func writeTempAsset(t *testing.T, name string, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatalf("write temp asset: %v", err)
	}
	return p
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestVerifySHA256Sums_Match(t *testing.T) {
	content := []byte("hello silt")
	path := writeTempAsset(t, "silt-update-123.exe", content)
	sums := fmt.Sprintf("%s  Silt-v0.5.0-windows-installer.exe\n", sha256Hex(content))
	if err := VerifySHA256Sums(path, "Silt-v0.5.0-windows-installer.exe", strings.NewReader(sums)); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
}

func TestVerifySHA256Sums_Mismatch(t *testing.T) {
	path := writeTempAsset(t, "silt-update-123.exe", []byte("actual"))
	sums := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  asset.exe\n"
	err := VerifySHA256Sums(path, "asset.exe", strings.NewReader(sums))
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "failed SHA256 verification") {
		t.Errorf("expected checksum error, got %v", err)
	}
}

// Fail-closed: an asset not listed in SHA256SUMS must NOT verify.
func TestVerifySHA256Sums_AbsentBasenameFailsClosed(t *testing.T) {
	path := writeTempAsset(t, "silt-update-123.exe", []byte("x"))
	sums := fmt.Sprintf("%s  different-asset.exe\n", sha256Hex([]byte("x")))
	err := VerifySHA256Sums(path, "unlisted.exe", strings.NewReader(sums))
	if err == nil {
		t.Fatal("expected fail-closed error for absent basename, got nil")
	}
}

func TestVerifySHA256Sums_MalformedLine(t *testing.T) {
	path := writeTempAsset(t, "silt-update-123.exe", []byte("x"))
	// No hash, just a filename → malformed.
	sums := "asset.exe\n"
	err := VerifySHA256Sums(path, "asset.exe", strings.NewReader(sums))
	if err == nil {
		t.Fatal("expected error for malformed sums line")
	}
}

// sha256sum emits `<hash> *<file>` for binary mode; the `*` must be tolerated.
func TestVerifySHA256Sums_BinaryModeStar(t *testing.T) {
	content := []byte("binary")
	path := writeTempAsset(t, "silt-update-123.AppImage", content)
	sums := fmt.Sprintf("%s *asset.AppImage\n", sha256Hex(content))
	if err := VerifySHA256Sums(path, "asset.AppImage", strings.NewReader(sums)); err != nil {
		t.Fatalf("expected match with binary-mode star, got %v", err)
	}
}

// A filename containing internal spaces must parse correctly (regression for
// the strings.Fields over-segmentation the two-space/single-space split fixes).
func TestVerifySHA256Sums_FilenameWithSpaces(t *testing.T) {
	content := []byte("spaced name")
	const name = "My Silt Installer.exe"
	path := writeTempAsset(t, "silt-update-123.exe", content)
	// Both text mode (two spaces) and binary mode (space + *) forms.
	for _, line := range []string{
		fmt.Sprintf("%s  %s\n", sha256Hex(content), name),
		fmt.Sprintf("%s *%s\n", sha256Hex(content), name),
	} {
		if err := VerifySHA256Sums(path, name, strings.NewReader(line)); err != nil {
			t.Fatalf("expected match for spaced filename [%s]: %v", line, err)
		}
	}
}

func TestVerifySHA256Sums_UppercaseHashTolerated(t *testing.T) {
	content := []byte("x")
	path := writeTempAsset(t, "silt-update-123.exe", content)
	sums := fmt.Sprintf("%s  a.exe\n", strings.ToUpper(sha256Hex(content)))
	if err := VerifySHA256Sums(path, "a.exe", strings.NewReader(sums)); err != nil {
		t.Fatalf("expected match with uppercase hash, got %v", err)
	}
}

func TestFetchSHA256Sums_FindsAssetAndDownloads(t *testing.T) {
	const want = "deadbeef  a.exe\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/SHA256SUMS" {
			_, _ = w.Write([]byte(want))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	assets := []githubAsset{
		{Name: "a.exe", BrowserDownloadURL: srv.URL + "/a.exe"},
		{Name: "SHA256SUMS", BrowserDownloadURL: srv.URL + "/SHA256SUMS"},
	}
	got, err := FetchSHA256Sums(context.Background(), srv.Client(), assets)
	if err != nil {
		t.Fatalf("FetchSHA256Sums: %v", err)
	}
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFetchSHA256Sums_MissingAssetErrors(t *testing.T) {
	assets := []githubAsset{{Name: "a.exe", BrowserDownloadURL: "http://example/a.exe"}}
	_, err := FetchSHA256Sums(context.Background(), http.DefaultClient, assets)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
