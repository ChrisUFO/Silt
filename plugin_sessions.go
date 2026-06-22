package main

import (
	"fmt"
	"silt/backend/plugins"
)

// RegisterPluginSession mints a session token for pluginID and stores it so
// privileged bindings can verify the caller's identity (#151). Called by the
// frontend loader at plugin load time. Returns the token the SDK captures in
// its closures.
func (a *App) RegisterPluginSession(pluginID string) (string, error) {
	if !plugins.IsValidID(pluginID) {
		return "", fmt.Errorf("invalid plugin id %q", pluginID)
	}
	token := newUUID()
	a.pluginSessionsMu.Lock()
	defer a.pluginSessionsMu.Unlock()
	if a.pluginSessions == nil {
		a.pluginSessions = make(map[string]string)
	}
	a.pluginSessions[token] = pluginID
	return token, nil
}

// UnregisterPluginSession invalidates a session token. Called by the loader
// on disable/uninstall/vault-close so a stale token cannot be reused.
func (a *App) UnregisterPluginSession(token string) error {
	a.pluginSessionsMu.Lock()
	defer a.pluginSessionsMu.Unlock()
	if a.pluginSessions != nil {
		delete(a.pluginSessions, token)
	}
	return nil
}

// validatePluginSession verifies that token maps to pluginID. Returns nil if
// valid; an error otherwise. This is the boundary check that prevents a plugin
// from impersonating another by calling a privileged binding with a different
// pluginID (#151). A plugin that bypasses the SDK and calls App.PluginFetch
// directly doesn't have the target plugin's token, so it's rejected here.
//
// First-party plugins (whose SDK closures also capture tokens) are unaffected.
// The residual gap — a main-webview plugin reading another plugin's token from
// closure scope — is documented in docs/PLUGIN_DEVELOPMENT.md §7 (#152).
func (a *App) validatePluginSession(pluginID, token string) error {
	if token == "" {
		return fmt.Errorf("missing session token for plugin %q", pluginID)
	}
	a.pluginSessionsMu.RLock()
	registeredID, ok := a.pluginSessions[token]
	a.pluginSessionsMu.RUnlock()
	if !ok {
		return fmt.Errorf("invalid session token for plugin %q", pluginID)
	}
	if registeredID != pluginID {
		return fmt.Errorf("session token mismatch: token belongs to %q, not %q", registeredID, pluginID)
	}
	return nil
}
