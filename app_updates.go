package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"silt/backend/updates"
	"silt/backend/vault"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// =========================================================================
// In-app update check + self-upgrade (#312)
// =========================================================================
//
// Thin Wails-bound wrappers over backend/updates. The package owns the HTTP,
// semver, download, and SHA256 logic; this file binds it to the App and emits
// progress via the same runtime.EventsEmit channel used by the archive import
// (vault:archive:progress). No token is embedded — unauthenticated reads of
// the public repo are sufficient at 24h-throttled desktop volumes (#312 AC7).

// updateProgressEvent is the Wails event name the frontend subscribes to for
// download progress. Payload: {received:int64, total:int64} (total is -1 when
// ContentLength is unknown).
const updateProgressEvent = "update:download:progress"

// UpdateSettingsResult is the frontend-facing view of the update preferences
// in settings.json. AutoCheck is the resolved default-on value (nil→true).
// ShouldAutoCheck is the backend's throttled startup decision (autoCheck &&
// lastCheck older than 24h), so the frontend does not duplicate the 24h
// constant or the comparison locally.
type UpdateSettingsResult struct {
	AutoCheck        bool   `json:"autoCheck"`
	LastCheckRFC3339 string `json:"lastCheck"`      // empty when never checked
	ShouldAutoCheck  bool   `json:"shouldAutoCheck"` // throttled startup decision
}

// InstallUpdateResult reports the outcome of launching the verified installer.
// WillQuit is true when a self-replacing installer/relaunch was launched
// (Windows NSIS, Linux AppImage in-place): the frontend MUST then call the JS
// runtime Quit() so the app exits via the graceful OnShutdown path (vault +
// WAL flush) and the installer can replace the locked binary. WillQuit is
// false for the Linux xdg-open hand-off, where the app stays running.
type InstallUpdateResult struct {
	WillQuit bool `json:"willQuit"`
}

// CheckForUpdates queries GitHub Releases for the latest non-prerelease and
// reports whether the running version is older. On success it stamps
// LastUpdateCheck in settings.json so the startup auto-check throttle (24h)
// resets. A network/rate-limit/parse failure returns a wrapped error for the
// UI to surface as a quiet "couldn't check" state — it never crashes.
func (a *App) CheckForUpdates() (updates.UpdateInfo, error) {
	client := updates.NewClient(appVersion)
	info, err := client.CheckForUpdates(context.Background())
	if err != nil {
		return updates.UpdateInfo{}, err
	}
	// Stamp the check timestamp regardless of whether an update exists, so the
	// 24h throttle reflects "we just looked." A persist failure is non-fatal:
	// the check still succeeded; the next launch simply re-checks. Logged so a
	// persist failure is diagnosable in support tickets.
	if err := a.stampUpdateCheckTime(); err != nil {
		log.Printf("updates: could not persist LastUpdateCheck: %v", err)
	}
	return info, nil
}

// DownloadUpdate downloads the chosen asset and verifies it against the
// release's SHA256SUMS before returning the local path. Progress is streamed
// to the frontend via the update:download:progress event. It NEVER returns a
// path for an unverified asset — a checksum mismatch or a SHA256SUMS fetch
// failure cleans up the temp file and returns the error.
//
// The asset URL is re-validated against a fresh /releases/latest fetch inside
// the package, so a stale or coerced URL cannot make Silt download + hand to
// InstallUpdate an arbitrary file.
func (a *App) DownloadUpdate(assetURL string) (string, error) {
	client := updates.NewClient(appVersion)
	emitProgress := func(received, total int64) {
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, updateProgressEvent, map[string]any{
				"received": received,
				"total":    total,
			})
		}
	}
	return client.DownloadAndVerify(context.Background(), assetURL, emitProgress)
}

// InstallUpdate launches the verified local asset so it can replace the
// running binary. The result reports whether the app should quit (see
// InstallUpdateResult). The asset at localPath MUST have been verified by
// DownloadUpdate first; this binding intentionally does not re-verify (the
// file is already on disk and trusted from the prior step). The frontend —
// not this binding — performs the actual Quit so the IPC response reaches the
// UI before shutdown begins; quitting here would terminate before the promise
// resolved.
func (a *App) InstallUpdate(localPath string) (InstallUpdateResult, error) {
	willQuit, err := updates.Install(localPath)
	if err != nil {
		log.Printf("updates: install failed for %s: %v", localPath, err)
		return InstallUpdateResult{}, err
	}
	return InstallUpdateResult{WillQuit: willQuit}, nil
}

// GetUpdateSettings returns the user's update preferences from settings.json.
// AutoCheck reflects the default-on resolution (absent → true). ShouldAutoCheck
// is the backend-computed throttled startup decision (single source of truth
// for the 24h rule — the frontend does not duplicate the constant).
func (a *App) GetUpdateSettings() (UpdateSettingsResult, error) {
	settings, err := vault.LoadSettings()
	if err != nil {
		return UpdateSettingsResult{}, fmt.Errorf("load settings: %w", err)
	}
	var last time.Time
	if settings.LastUpdateCheck != "" {
		last, _ = time.Parse(time.RFC3339, settings.LastUpdateCheck)
	}
	return UpdateSettingsResult{
		AutoCheck:        settings.AutoCheckUpdatesEnabled(),
		LastCheckRFC3339: settings.LastUpdateCheck,
		ShouldAutoCheck:  updates.ShouldAutoCheck(last, settings.AutoCheckUpdatesEnabled()),
	}, nil
}

// SetUpdateSettings persists the auto-check toggle. It preserves every other
// settings.json field (vault path, theme, trusted publishers) via the
// transactional vault.UpdateSettings read-modify-write (serialized against all
// other settings writers).
func (a *App) SetUpdateSettings(autoCheck bool) error {
	_, err := vault.UpdateSettings(func(s *vault.AppSettings) {
		s.AutoCheckUpdates = &autoCheck
	})
	return err
}

// stampUpdateCheckTime records now as the last update-check time. Non-fatal on
// error: the check itself already succeeded and the throttle is best-effort.
// Serialized with other settings writers via vault.UpdateSettings.
func (a *App) stampUpdateCheckTime() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := vault.UpdateSettings(func(s *vault.AppSettings) {
		s.LastUpdateCheck = now
	})
	return err
}
