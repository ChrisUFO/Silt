package updates

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ErrChecksumMismatch is returned when a downloaded asset's SHA256 does not
// match the value recorded in the published SHA256SUMS, OR when the asset's
// basename is absent from the sums file. Both are fail-closed: the asset is
// never executed.
var ErrChecksumMismatch = errors.New("downloaded asset failed SHA256 verification")

// sha256sumsAssetName is the release-asset filename the release pipeline
// publishes (see .github/workflows/release.yml, "Generate SHA256SUMS").
const sha256sumsAssetName = "SHA256SUMS"

// VerifySHA256Sums computes the SHA256 of the file at localAssetPath and
// confirms it equals the hash recorded for assetName in the sha256sum-format
// data read from sumsReader. A missing name or a hex-decode/format error is
// treated as a mismatch (fail-closed), never a silent pass.
//
// assetName is the release asset's original filename (e.g. the value the
// release pipeline wrote into SHA256SUMS), NOT the local temp filename — the
// downloaded file is a randomly-named temp, so the basename cannot be derived
// from its path.
//
// The sums format is one `<lowercase-hex-hash>  <filename>` line per asset
// (two spaces, sha256sum-compatible). Lines may use a `*` prefix to denote a
// binary-mode match (e.g. `<hash> *file`); the `*` is tolerated.
func VerifySHA256Sums(localAssetPath, assetName string, sumsReader io.Reader) error {
	expected, ok, err := parseExpectedHash(sumsReader, assetName)
	if err != nil {
		return fmt.Errorf("%w: parse SHA256SUMS: %v", ErrChecksumMismatch, err)
	}
	if !ok {
		// The asset is not listed — refuse to execute an unverified binary.
		return fmt.Errorf("%w: %q not present in SHA256SUMS", ErrChecksumMismatch, assetName)
	}

	f, err := os.Open(localAssetPath)
	if err != nil {
		return fmt.Errorf("open downloaded asset for hashing: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash downloaded asset: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("%w: expected %s, got %s for %s", ErrChecksumMismatch, expected, actual, assetName)
	}
	return nil
}

// parseExpectedHash scans sha256sum-format lines and returns the hash recorded
// for filename. ok is false when the filename is not listed (fail-closed).
func parseExpectedHash(r io.Reader, filename string) (hash string, ok bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// sha256sum emits either `<hash>  <filename>` (text mode, two spaces)
		// or `<hash> *<filename>` (binary mode, space + asterisk). The hash is
		// always 64 hex chars with no spaces, so the FIRST single space is the
		// hash/filename delimiter. Split there (max 2 parts) so a filename
		// containing internal spaces is preserved as the remainder.
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			err = fmt.Errorf("malformed SHA256SUMS line: %q", line)
			return
		}
		h := parts[0]
		name := strings.TrimPrefix(strings.TrimSpace(parts[1]), "*")
		if name == filename {
			return h, true, nil
		}
	}
	if serr := sc.Err(); serr != nil {
		err = serr
	}
	return "", false, nil
}

// FetchSHA256Sums locates the SHA256SUMS asset among a release's assets and
// downloads its contents, returning the raw bytes. It is split from
// VerifySHA256Sums so the App binding can fetch once and the verify step can
// be unit-tested against an in-memory reader.
func FetchSHA256Sums(ctx context.Context, client *http.Client, assets []githubAsset) ([]byte, error) {
	url := ""
	for i := range assets {
		if assets[i].Name == sha256sumsAssetName {
			url = assets[i].BrowserDownloadURL
			break
		}
	}
	if url == "" {
		return nil, fmt.Errorf("SHA256SUMS asset not found in release")
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build SHA256SUMS request: %w", err)
	}
	req.Header.Set("User-Agent", "Silt-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch SHA256SUMS: HTTP %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap for a text file
}
