// Package updates implements Silt's in-app update check and self-upgrade
// against the project's GitHub Releases (#312). It owns:
//
//   - CheckForUpdates: one unauthenticated GET to
//     /repos/{owner}/{repo}/releases/latest, parsing the release tag, notes,
//     and assets, and reporting whether the running version is older.
//   - Download: streaming download of a chosen asset to a temp file with
//     progress callbacks.
//   - VerifySHA256Sums: integrity check of a downloaded asset against the
//     published SHA256SUMS file (fail-closed on mismatch).
//   - Install: OS-specific launch-and-replace (Windows NSIS, Linux AppImage).
//
// Design notes (see PLAN.md / ARCHITECTURE §4.3):
//   - No token is embedded: unauthenticated reads of the public repo are
//     sufficient at 24h-throttled desktop-client volumes.
//   - The /releases/latest endpoint already excludes pre-releases and drafts,
//     so no client-side filter is needed (single stable channel).
//   - Version comparison reuses silt/backend/semver; the caller strips the
//     release tag's leading `v` before passing it in.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"silt/backend/semver"
)

// DefaultAPIBase is the public GitHub REST API root.
const DefaultAPIBase = "https://api.github.com"

// DefaultRepo is the Silt repository slug.
const DefaultRepo = "Chelydra-Labs/Silt"

// checkTimeout caps a single update-check request. The release metadata is
// small; 15s is generous and keeps an offline network from hanging the UI.
const checkTimeout = 15 * time.Second

// Sentinel errors. Callers map these to user-facing states (quiet on startup,
// explicit on manual check). Use errors.Is to discriminate.
var (
	// ErrUpdateCheckFailed is returned when the release lookup cannot complete
	// (network error, non-2xx, rate limit, parse failure). The wrapped error
	// carries a user-facing reason.
	ErrUpdateCheckFailed = errors.New("update check failed")
	// ErrPlatformNotSupported is returned when no installable asset exists for
	// the running OS (e.g. macOS, which has no build leg yet).
	ErrPlatformNotSupported = errors.New("no installable update asset for this platform")
)

// UpdateInfo is the result of an update check, surfaced verbatim to the
// frontend. Asset is nil when no platform-matching asset is present (the UI
// then offers the release page link only).
type UpdateInfo struct {
	HasUpdate      bool       `json:"hasUpdate"`
	LatestVersion  string     `json:"latestVersion"`  // numeric, no leading `v`
	ReleaseURL     string     `json:"releaseUrl"`     // html_url of the release
	ReleaseNotes   string     `json:"releaseNotes"`   // markdown body
	Asset          *AssetInfo `json:"asset,omitempty"` // platform-matching asset, if any
	CheckedVersion string     `json:"checkedVersion"` // the version this was checked against
}

// AssetInfo describes a single downloadable release asset for the running
// platform.
type AssetInfo struct {
	Name                string `json:"name"`
	BrowserDownloadURL  string `json:"browserDownloadUrl"`
	Size                int64  `json:"size"`
}

// Client queries GitHub Releases for available updates. Fields are public so
// tests can point HTTPClient at an httptest.Server and override APIBase/Repo.
// Construct via NewClient in production code.
type Client struct {
	HTTPClient *http.Client
	APIBase    string // "" → DefaultAPIBase
	Repo       string // "" → DefaultRepo
	AppVersion string // embedded app version, sent as User-Agent and used as the comparison baseline
}

// NewClient returns a Client with production defaults. appVersion is the
// running Silt version (numeric, no `v`).
func NewClient(appVersion string) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: checkTimeout},
		APIBase:    DefaultAPIBase,
		Repo:       DefaultRepo,
		AppVersion: appVersion,
	}
}

// githubAsset mirrors the relevant fields of a release asset in the
// /releases/latest response.
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// githubRelease mirrors the relevant fields of the /releases/latest response.
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	Body        string        `json:"body"`
	Prerelease  bool          `json:"prerelease"`
	Draft       bool          `json:"draft"`
	Assets      []githubAsset `json:"assets"`
}

// CheckForUpdates fetches the latest release and reports whether appVersion is
// older. It does not filter pre-releases itself — /releases/latest already
// excludes them. A network error, non-2xx response (rate limit, 404), or JSON
// parse failure all surface as ErrUpdateCheckFailed (wrapped).
func (c *Client) CheckForUpdates(ctx context.Context) (UpdateInfo, error) {
	rel, err := c.fetchRelease(ctx)
	if err != nil {
		return UpdateInfo{}, err
	}

	latest := stripLeadingV(rel.TagName)
	info := UpdateInfo{
		LatestVersion:  latest,
		ReleaseURL:     rel.HTMLURL,
		ReleaseNotes:   rel.Body,
		CheckedVersion: c.AppVersion,
	}
	info.HasUpdate = latest != "" && semver.LessThan(c.AppVersion, latest)
	// Selecting a platform asset is best-effort here: a missing asset for the
	// current platform is reported (Asset == nil) but does NOT fail the check
	// — the UI still offers the release-page link.
	if asset, err := SelectPlatformAsset(rel.Assets, currentGOOS); err == nil && asset != nil {
		info.Asset = asset
	}
	return info, nil
}

// fetchRelease issues the /releases/latest GET and decodes the response. Shared
// by CheckForUpdates (compare + asset select) and DownloadAndVerify (asset +
// SHA256SUMS lookup) so the two paths agree on the release state.
func (c *Client) fetchRelease(ctx context.Context) (githubRelease, error) {
	base := c.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	repo := c.Repo
	if repo == "" {
		repo = DefaultRepo
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(base, "/"), repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, fmt.Errorf("%w: build request: %v", ErrUpdateCheckFailed, err)
	}
	// A real User-Agent identifies the client to GitHub; the API version pin
	// keeps the response shape stable. No Authorization header is sent — the
	// public repo is readable unauthenticated and shipping a token would put a
	// secret in the binary (#312 AC7).
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Silt/"+c.AppVersion)

	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: checkTimeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return githubRelease{}, fmt.Errorf("%w: %v", ErrUpdateCheckFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// 403 (rate limited) and 404 (no releases) are expected failure modes;
		// map both to a single quiet error with the status as the reason.
		return githubRelease{}, fmt.Errorf("%w: GitHub API returned %s", ErrUpdateCheckFailed, resp.Status)
	}

	var rel githubRelease
	// Bound the read so a hostile/malformed endpoint cannot stream forever.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap
	if err != nil {
		return githubRelease{}, fmt.Errorf("%w: read response: %v", ErrUpdateCheckFailed, err)
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return githubRelease{}, fmt.Errorf("%w: parse response: %v", ErrUpdateCheckFailed, err)
	}
	return rel, nil
}

// stripLeadingV returns tag with a single leading `v`/`V` removed, so the
// numeric form ("0.2.0") used by semver.LessThan and the embedded VERSION
// matches the GitHub release tag form ("v0.2.0").
func stripLeadingV(tag string) string {
	tag = strings.TrimSpace(tag)
	if len(tag) > 0 && (tag[0] == 'v' || tag[0] == 'V') {
		return tag[1:]
	}
	return tag
}
