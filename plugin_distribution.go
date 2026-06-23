package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"silt/backend/plugins"
	"silt/backend/vault"
	"strings"
)

// =========================================================================
// Distribution v2 (#111) — update checks + trusted publishers + downgrade
// =========================================================================

// CheckPluginUpdate fetches a plugin's manifest from its declared updateUrl
// (if present) and returns whether a newer version is available (#111).
//
// The fetch goes through newSafeFetchClient so the update channel enjoys the
// same SSRF + timeout + redirect + DNS-rebinding defenses as PluginFetch
// (#101 review). Without this, a malicious update manifest could 302 to
// 169.254.169.254 or hold the goroutine open with no timeout.
func (a *App) CheckPluginUpdate(pluginID, currentVersion, updateUrl string) (PluginUpdateInfo, error) {
	info := PluginUpdateInfo{PluginID: pluginID, CurrentVersion: currentVersion}
	if updateUrl == "" {
		return info, nil
	}
	if !isSafeFetchUrl(updateUrl) {
		return info, fmt.Errorf("update URL is not a safe http(s) URL")
	}
	client := newSafeFetchClient(defaultPluginFetchTimeout)
	req, err := http.NewRequest("GET", updateUrl, nil)
	if err != nil {
		return info, fmt.Errorf("build update request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return info, fmt.Errorf("update check failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return info, fmt.Errorf("read update manifest: %w", err)
	}
	var manifest struct {
		Version string `json:"version"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return info, fmt.Errorf("parse update manifest: %w", err)
	}
	info.LatestVersion = manifest.Version
	info.DownloadURL = manifest.URL
	info.UpdateAvailable = versionLessThan(currentVersion, manifest.Version)
	return info, nil
}

// PluginUpdateInfo is the result of an update check.
type PluginUpdateInfo struct {
	PluginID        string `json:"pluginId"`
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	DownloadURL     string `json:"downloadUrl"`
}

// GetTrustedPublishers returns the user-global trusted-publishers list (#111).
func (a *App) GetTrustedPublishers() ([]string, error) {
	settings, err := vault.LoadSettings()
	if err != nil {
		return []string{}, nil
	}
	if settings.TrustedPublishers == nil {
		return []string{}, nil
	}
	return settings.TrustedPublishers, nil
}

// AddTrustedPublisher adds a publisher to the trusted list (#111).
func (a *App) AddTrustedPublisher(publisher string) error {
	if publisher == "" {
		return fmt.Errorf("publisher is required")
	}
	settings, err := vault.LoadSettings()
	if err != nil {
		return err
	}
	for _, p := range settings.TrustedPublishers {
		if p == publisher {
			return nil
		}
	}
	settings.TrustedPublishers = append(settings.TrustedPublishers, publisher)
	return vault.SaveSettings(settings)
}

// RemoveTrustedPublisher removes a publisher from the trusted list (#111).
func (a *App) RemoveTrustedPublisher(publisher string) error {
	settings, err := vault.LoadSettings()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(settings.TrustedPublishers))
	for _, p := range settings.TrustedPublishers {
		if p != publisher {
			out = append(out, p)
		}
	}
	settings.TrustedPublishers = out
	return vault.SaveSettings(settings)
}

// PluginReadPluginAsset reads a file from the plugin's OWN install directory
// (`.system/plugins/<pluginID>/<relPath>`), enabling plugin-bundled assets
// like icons, templates, or static HTML for surfaces (#108/#117). The path is
// traversal-guarded (no `..` escapes) and sanitized. NOT capability-gated
// (reading your own bundle is safe). Session-token verified (#236) — a plugin
// cannot read another plugin's bundle by spoofing the pluginID.
func (a *App) PluginReadPluginAsset(pluginID, sessionToken, relPath string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	if a.vaultPath == "" {
		return "", fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return "", fmt.Errorf("invalid plugin id %q", pluginID)
	}
	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("relative path escapes the plugin directory")
	}
	assetPath := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, cleaned)
	if !isPathWithinRoot(assetPath, filepath.Join(a.vaultPath, ".system", "plugins", pluginID)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	data, err := os.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("read plugin asset: %w", err)
	}
	return string(data), nil
}
