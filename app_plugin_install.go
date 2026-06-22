package main

import (
	"fmt"
	"silt/backend/config"
	"silt/backend/parser"
	"silt/backend/plugins"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// --- Plugin install / uninstall (.silt-plugin) ---------------------------

// ValidatePluginArchive validates a .silt-plugin file without installing it,
// returning its manifest and any non-fatal warnings bundled in a single struct
// (so both cross the Wails IPC boundary together).
func (a *App) ValidatePluginArchive(archivePath string) (parser.PluginValidationResult, error) {
	manifest, warnings, err := plugins.Validate(archivePath)
	if err != nil {
		return parser.PluginValidationResult{Warnings: warnings}, err
	}
	if verr := enforceMinVersion(manifest.MinSiltVersion); verr != nil {
		return parser.PluginValidationResult{Warnings: warnings}, verr
	}
	return parser.PluginValidationResult{
		Manifest: manifestToParser(manifest),
		Warnings: warnings,
	}, nil
}

// PickPluginArchive opens the native file picker (filtered to .silt-plugin)
// and returns the chosen path, or empty string if cancelled.
func (a *App) PickPluginArchive() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	selected, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select a .silt-plugin package",
		Filters: []runtime.FileFilter{
			{DisplayName: "Silt Plugin (*.silt-plugin)", Pattern: "*.silt-plugin"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to open file picker: %w", err)
	}
	return selected, nil
}

// InstallPlugin installs a .silt-plugin archive into .system/plugins/<id>/,
// emits plugins:changed so the loader re-runs, and returns the manifest.
func (a *App) InstallPlugin(archivePath string) (parser.PluginManifest, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return parser.PluginManifest{}, fmt.Errorf("vault not loaded")
	}
	manifest, err := plugins.Install(a.vaultPath, archivePath)
	if err != nil {
		return parser.PluginManifest{}, err
	}
	if verr := enforceMinVersion(manifest.MinSiltVersion); verr != nil {
		// Roll back the install since the version requirement isn't met.
		_ = plugins.Uninstall(a.vaultPath, manifest.ID)
		return parser.PluginManifest{}, verr
	}
	// Publisher-trust gate (#111 distribution v2, #150 follow-up): when the
	// user has populated TrustedPublishers, the plugin's Author must be on
	// the list. An empty TrustedPublishers preserves the current "everyone
	// is welcome" posture — populating the list is an explicit opt-in to
	// a stricter stance. A plugin with an empty Author cannot be matched
	// against a non-empty trust list, which is the correct (defense-
	// in-depth) default: anonymous plugins require no trust decision.
	if verr := enforcePublisherTrust(manifest.Author); verr != nil {
		_ = plugins.Uninstall(a.vaultPath, manifest.ID)
		return parser.PluginManifest{}, verr
	}
	a.emitPluginsChanged()
	return manifestToParser(manifest), nil
}

// UninstallPlugin removes a plugin folder and emits plugins:changed. It also
// revokes every capability grant for the plugin so a later reinstall re-prompts
// rather than inheriting the prior trust decision (#113).
func (a *App) UninstallPlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if err := plugins.Uninstall(a.vaultPath, pluginID); err != nil {
		return err
	}
	// Evict the rate-limiter bucket so uninstalled plugins don't leak entries (#153).
	if a.rateLimiter != nil {
		a.rateLimiter.evict(pluginID)
	}
	// Best-effort grant cleanup; a failure here must not mask the successful
	// uninstall (the folder is already gone). The grants block is harmless if
	// it lingers, but cleaning it keeps the manager UI honest.
	_ = a.revokeAllGrants(pluginID)
	a.emitPluginsChanged()
	return nil
}

// revokeAllGrants removes every capability grant for pluginID without
// emitting plugins:changed (the caller decides whether to emit). Used by
// UninstallPlugin and the vault teardown path. Acquires configMu internally.
func (a *App) revokeAllGrants(pluginID string) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Plugins.Grants == nil {
		return nil
	}
	if _, ok := a.cfg.Plugins.Grants[pluginID]; !ok {
		return nil
	}
	delete(a.cfg.Plugins.Grants, pluginID)
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	return config.Save(a.vaultPath, a.cfg)
}

// EnablePlugin / DisablePlugin toggle a per-plugin ".disabled" sentinel
// (the loader skips disabled plugins), then emit plugins:changed.
func (a *App) EnablePlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if err := plugins.SetDisabled(a.vaultPath, pluginID, false); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

func (a *App) DisablePlugin(pluginID string) error {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if err := plugins.SetDisabled(a.vaultPath, pluginID, true); err != nil {
		return err
	}
	a.emitPluginsChanged()
	return nil
}

func (a *App) emitPluginsChanged() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, "plugins:changed", struct{}{})
}
