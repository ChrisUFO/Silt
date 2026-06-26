package main

import (
	"fmt"
	"log"
	"silt/backend/parser"
	"silt/backend/plugins"
	"silt/backend/semver"
	"silt/backend/vault"
	"strings"
)

// --- v2 SDK capability & permission model (#113) -------------------------

// isFirstPartyPlugin reports whether pluginID is a bundled (trusted) plugin.
// Delegates to plugins.IsFirstPartyID so the reserved-id set has a single
// source of truth in package plugins (where Validate can also reach it to
// reject impostor archives at install time — #240, audit F5). Kept as a
// package-main shim so existing call sites keep their readable name.
func isFirstPartyPlugin(pluginID string) bool {
	return plugins.IsFirstPartyID(pluginID)
}

// requireGrant is the single server-side enforcement point for every privileged
// v2 SDK binding (#113). It returns nil if the plugin may use the capability,
// or a structured *plugins.CapabilityDeniedError (never a panic) the frontend
// SDK surfaces as an actionable message + re-prompt.
//
// pluginID is validated against IsValidID to reject path-traversal payloads
// before they reach filepath.Join in scratch-dir / audit-log paths. First-party
// plugins receive their grants via seedFirstPartyGrants at config-load time,
// so there is NO special-case bypass here — a third-party plugin cannot
// spoof a first-party ID to bypass capability checks.
//
// Callers that need the qualifier (e.g. to enforce notebook vs vault scope on
// file writes) read it via grantedQualifier after a successful requireGrant.
func (a *App) requireGrant(pluginID string, cap plugins.Capability) error {
	if !plugins.IsValidID(pluginID) {
		return &plugins.CapabilityDeniedError{
			Plugin:     "<invalid>",
			Capability: string(cap),
			Requested:  plugins.QualGranted,
		}
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	// F4: grants now live in the per-host store (a.grants), not vault-scoped
	// config.yaml. A synced vault's legacy grants block is ignored on load.
	if a.grants != nil {
		if _, ok := a.grants[pluginID]; ok {
			if qual, ok := a.grants[pluginID][string(cap)]; ok && qual != "" {
				return nil
			}
		}
	}
	return &plugins.CapabilityDeniedError{
		Plugin:     pluginID,
		Capability: string(cap),
		Requested:  plugins.QualGranted,
	}
}

// grantedQualifier returns the scope qualifier for a granted capability, or
// ("", false) if not granted. Used by bindings that narrow scope (file-write
// notebook vs vault).
func (a *App) grantedQualifier(pluginID string, cap plugins.Capability) (string, bool) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	if a.grants != nil {
		if caps, ok := a.grants[pluginID]; ok {
			if qual, ok := caps[string(cap)]; ok && qual != "" {
				return qual, true
			}
		}
	}
	return "", false
}

// RequestCapability grants a capability to a plugin and persists it atomically
// to the per-host grants file (F4 — grants no longer travel with config.yaml).
// qualifier is normalized to a known value ("" or "true" → "granted"). The
// capability must be a recognized one (unknown caps are rejected). Emits
// plugins:changed so the manager UI refreshes.
func (a *App) RequestCapability(pluginID, capability, qualifier string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return fmt.Errorf("invalid plugin id %q (must match ^[a-z0-9-]+$)", pluginID)
	}
	if !plugins.KnownCapabilities[plugins.Capability(capability)] {
		return fmt.Errorf("unknown capability %q (recognized: %s)", capability, plugins.ListCapabilities())
	}
	qual := plugins.QualGranted
	if qualifier != "" && qualifier != "true" {
		if !pluginsValidQualifier(qualifier) {
			return fmt.Errorf("invalid qualifier %q", qualifier)
		}
		qual = qualifier
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.grants == nil {
		a.grants = vault.GrantsStore{}
	}
	caps, ok := a.grants[pluginID]
	if !ok || caps == nil {
		caps = map[string]string{}
	}
	caps[capability] = qual
	a.grants[pluginID] = caps
	// F4: persist to the host-scoped grants file, NOT vault config.yaml.
	if err := vault.SaveGrants(a.grants); err != nil {
		return err
	}
	e := newAuditEntry("grant")
	e.PluginID = pluginID
	e.Capability = capability
	e.Qualifier = qual
	appendAuditEntry(a.vaultPath, e)
	a.emitPluginsChanged()
	return nil
}

// RevokeCapability revokes a capability grant. capability == "" revokes every
// grant for the plugin (used on uninstall). Emits plugins:changed.
func (a *App) RevokeCapability(pluginID, capability string) error {
	if a.vaultPath == "" {
		return fmt.Errorf("vault not loaded")
	}
	if !plugins.IsValidID(pluginID) {
		return fmt.Errorf("invalid plugin id %q (must match ^[a-z0-9-]+$)", pluginID)
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.grants == nil {
		return nil // nothing to revoke
	}
	caps, ok := a.grants[pluginID]
	if !ok {
		return nil
	}
	if capability == "" {
		delete(a.grants, pluginID)
	} else {
		delete(caps, capability)
		if len(caps) == 0 {
			delete(a.grants, pluginID)
		}
	}
	// F4: persist to the host-scoped grants file, NOT vault config.yaml.
	if err := vault.SaveGrants(a.grants); err != nil {
		return err
	}
	e := newAuditEntry("revoke")
	e.PluginID = pluginID
	e.Capability = capability
	appendAuditEntry(a.vaultPath, e)
	a.emitPluginsChanged()
	return nil
}

// GetGrantedCapabilities returns the full per-plugin capability grant table
// (pluginID → capability → qualifier) so the manager UI can show
// requested-vs-granted. First-party plugins are NOT included (they are
// implicitly granted). Returns an empty (non-nil) map pre-vault.
func (a *App) GetGrantedCapabilities() (map[string]map[string]string, error) {
	if a.vaultPath == "" {
		return map[string]map[string]string{}, nil
	}
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	out := make(map[string]map[string]string, len(a.grants))
	for pid, caps := range a.grants {
		if isFirstPartyPlugin(pid) {
			continue
		}
		clone := make(map[string]string, len(caps))
		for k, v := range caps {
			clone[k] = v
		}
		out[pid] = clone
	}
	return out, nil
}

// pluginsValidQualifier is a tiny adapter so app.go does not need to reach into
// the plugins package's unexported validQualifiers map.
func pluginsValidQualifier(q string) bool {
	switch q {
	case plugins.QualGranted, plugins.QualNotebook, plugins.QualVault:
		return true
	}
	return false
}

// enforceMinVersion rejects a plugin whose minSiltVersion exceeds the current
// app version (semver-style segment-by-segment comparison).
func enforceMinVersion(minSiltVersion string) error {
	if minSiltVersion == "" {
		return nil
	}
	if semver.LessThan(appVersion, minSiltVersion) {
		return fmt.Errorf("plugin requires Silt %s or later (current: %s)", minSiltVersion, appVersion)
	}
	return nil
}

// enforcePublisherTrust gates a plugin install on its Author matching a name
// in settings.TrustedPublishers (#111 distribution v2, #150 follow-up).
//
// Policy:
//   - Empty/nil TrustedPublishers → allow (preserves the current
//     "everyone-is-welcome" behavior so populating the list is an explicit
//     opt-in to a stricter stance).
//   - Non-empty TrustedPublishers AND author in the list → allow.
//   - Non-empty TrustedPublishers AND author NOT in the list → reject.
//   - Non-empty TrustedPublishers AND empty author → reject (anonymous
//     plugins cannot match a trust list, which is the correct
//     defense-in-depth default).
//
// The function distinguishes "settings file does not exist" (fail-open: no
// trust list configured) from "settings file exists but unreadable/corrupt"
// (fail-closed: a hostile plugin that can interfere with settings reads must
// not disable the trust gate). The error is logged at warn level.
func enforcePublisherTrust(author string) error {
	settings, err := vault.LoadSettings()
	if err != nil {
		log.Printf("enforcePublisherTrust: settings file exists but is unreadable — failing closed to protect the trust gate: %v", err)
		return fmt.Errorf("trusted-publishers list is configured but settings could not be read (corrupt settings.json?): %w", err)
	}
	trusted := settings.TrustedPublishers
	if len(trusted) == 0 {
		return nil
	}
	author = strings.TrimSpace(author)
	if author == "" {
		return fmt.Errorf("plugin author is empty; cannot be matched against the non-empty trusted-publishers list")
	}
	for _, p := range trusted {
		if strings.EqualFold(strings.TrimSpace(p), author) {
			return nil
		}
	}
	return fmt.Errorf("plugin author %q is not in the trusted-publishers list (add it via AddTrustedPublisher to install)", author)
}

func manifestToParser(m plugins.Manifest) parser.PluginManifest {
	return parser.PluginManifest{
		ID:             m.ID,
		Name:           m.Name,
		Version:        m.Version,
		Author:         m.Author,
		Description:    m.Description,
		Icon:           m.Icon,
		Main:           m.Main,
		MinSiltVersion: m.MinSiltVersion,
		Capabilities:   m.Capabilities,
		Settings:       m.Settings,
		Homepage:       m.Homepage,
		UpdateURL:      m.UpdateURL,
	}
}
