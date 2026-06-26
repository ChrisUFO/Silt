package updates

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// currentGOOS is the OS the selector matches against. It is a package var
// (seeded from runtime.GOOS) so tests can override it without faking the
// runtime. Restore it to runtime.GOOS after each test.
var currentGOOS = runtime.GOOS

// SelectPlatformAsset picks the single downloadable asset appropriate for the
// target OS from a release's asset list:
//   - windows → the NSIS installer (.exe); the portable .zip is intentionally
//     not chosen because it cannot self-install.
//   - linux   → the AppImage (.AppImage), which is self-contained and can be
//     replaced in place via the AppImage runtime's $APPIMAGE env var.
//
// An unsupported OS (e.g. darwin, which has no build leg) or no matching asset
// returns ErrPlatformNotSupported. Matching is by extension so the selector is
// robust to version/naming drift in the build scripts.
func SelectPlatformAsset(assets []githubAsset, goos string) (*AssetInfo, error) {
	for i := range assets {
		ext := strings.ToLower(filepath.Ext(assets[i].Name))
		if !platformAssetMatch(goos, ext) {
			continue
		}
		return &AssetInfo{
			Name:               assets[i].Name,
			BrowserDownloadURL: assets[i].BrowserDownloadURL,
			Size:               assets[i].Size,
		}, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrPlatformNotSupported, goos)
}

// platformAssetMatch reports whether a file extension is the installable
// asset for the target OS. Non-listed OSes never match, surfacing
// ErrPlatformNotSupported.
func platformAssetMatch(goos, ext string) bool {
	switch goos {
	case "windows":
		return ext == ".exe"
	case "linux":
		return ext == ".appimage"
	}
	return false
}
