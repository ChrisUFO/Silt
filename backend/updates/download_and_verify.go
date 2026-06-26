package updates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
)

// DownloadAndVerify is the install path's download step. Given the asset URL
// the user chose (from a prior CheckForUpdates result), it:
//  1. re-fetches the latest release (so the SHA256SUMS asset URL is fresh and
//     the chosen asset is confirmed to still belong to the current release),
//  2. downloads the asset to a temp file, streaming progress via emitProgress,
//  3. downloads the release's SHA256SUMS and verifies the asset against it,
//  4. returns the verified local path (owned by the caller for cleanup/launch).
//
// The asset MUST be present in the fresh release's asset list; a URL not in the
// list is rejected (ErrAssetNotInRelease) so a stale/coerced frontend cannot
// make the backend download + execute an arbitrary URL. Verification is
// fail-closed: a mismatched or absent hash never returns a path.
//
// emitProgress may be nil. The HTTP client comes from the Client (so tests can
// inject an httptest.Server-backed transport for both the metadata and the
// asset downloads).
func (c *Client) DownloadAndVerify(ctx context.Context, assetURL string, emitProgress func(received, total int64)) (string, error) {
	rel, err := c.fetchRelease(ctx)
	if err != nil {
		return "", err
	}

	// Defense: the asset URL must be one this release actually publishes, and
	// we need its original Name for SHA256SUMS verification (the temp file has
	// a random name). assetNameForURL returns "" when the URL is absent, which
	// the not-found branch below rejects.
	assetName, ok := assetNameForURL(rel.Assets, assetURL)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrAssetNotInRelease, assetURL)
	}

	// Downloads (asset + SHA256SUMS) MUST use the long downloadTimeout, NOT
	// c.HTTPClient directly: NewClient sets c.HTTPClient to the 15s check
	// timeout (fine for the small /releases/latest metadata GET), and Go's
	// http.Client.Timeout covers reading the response body — so reusing it
	// here would cap a 50 MiB installer at 15s and fail on any realistic
	// link. Reuse the test-injected Transport (if any) but apply the long
	// timeout so a slow link can still complete.
	var transport http.RoundTripper
	if c.HTTPClient != nil {
		transport = c.HTTPClient.Transport
	}
	downloadClient := &http.Client{
		Transport: transport,
		Timeout:   downloadTimeout,
	}

	localPath, err := Download(ctx, downloadClient, assetURL, emitProgress)
	if err != nil {
		return "", err
	}

	sums, err := FetchSHA256Sums(ctx, downloadClient, rel.Assets)
	if err != nil {
		// Cleanup the downloaded asset; verification cannot proceed.
		removeQuiet(localPath)
		return "", fmt.Errorf("verify: %w", err)
	}
	if err := VerifySHA256Sums(localPath, assetName, bytes.NewReader(sums)); err != nil {
		removeQuiet(localPath)
		return "", err
	}
	return localPath, nil
}

// ErrAssetNotInRelease is returned by DownloadAndVerify when the requested
// asset URL is not present in the freshly-fetched release asset list.
var ErrAssetNotInRelease = errors.New("requested asset is not in the latest release")

// assetNameForURL returns the Name of the asset whose browser_download_url
// equals url, and whether such an asset exists.
func assetNameForURL(assets []githubAsset, url string) (string, bool) {
	for i := range assets {
		if assets[i].BrowserDownloadURL == url {
			return assets[i].Name, true
		}
	}
	return "", false
}
