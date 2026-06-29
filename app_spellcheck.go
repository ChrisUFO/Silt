package main

import (
	"fmt"
	"strings"

	"silt/backend/config"
)

// App spellcheck bindings (#196). The per-vault custom dictionary lives in
// editor.custom_dictionary in config.yaml (the per-vault UI tier — ARCHITECTURE
// §0 rule 2; NOT a new file tier and NOT SQLite). These bindings do atomic
// read-modify-write under configMu + RegisterSelfWrite, mirroring
// UpdatePluginSetting. The editor's spellcheck layer (dictionary.ts) reads the
// list reactively from settings.config; Add/Remove return the resolved list so
// the caller can update optimistically.
//
// v1 returns + edits the VAULT list. Linked-notebook co-located overrides
// (Sprint 34) are a follow-up; the management card edits the vault dictionary,
// which is the common case (vault notebooks).

// GetCustomDictionary returns the per-vault custom spellcheck word list. Empty
// (non-nil) when none have been added yet — normalize guarantees non-nil.
func (a *App) GetCustomDictionary() ([]string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	if a.cfg.Editor.CustomDictionary == nil {
		return []string{}, nil
	}
	// Return a copy so callers can't mutate the in-memory config via the slice.
	out := make([]string, len(a.cfg.Editor.CustomDictionary))
	copy(out, a.cfg.Editor.CustomDictionary)
	return out, nil
}

// AddCustomDictionaryWord appends a word to the per-vault custom dictionary.
// The word is trimmed + lowercased; empty/whitespace-only input is rejected.
// config.Save runs normalize (de-dup + sort + lowercase), so the on-disk list
// stays canonical. Returns the resolved list.
func (a *App) AddCustomDictionaryWord(word string) ([]string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return nil, fmt.Errorf("word is required")
	}
	a.configMu.Lock()
	defer a.configMu.Unlock()
	for _, existing := range a.cfg.Editor.CustomDictionary {
		if existing == w {
			// Already present — idempotent. Return the current list.
			out := make([]string, len(a.cfg.Editor.CustomDictionary))
			copy(out, a.cfg.Editor.CustomDictionary)
			return out, nil
		}
	}
	a.cfg.Editor.CustomDictionary = append(a.cfg.Editor.CustomDictionary, w)
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return nil, fmt.Errorf("save custom dictionary: %w", err)
	}
	out := make([]string, len(a.cfg.Editor.CustomDictionary))
	copy(out, a.cfg.Editor.CustomDictionary)
	return out, nil
}

// RemoveCustomDictionaryWord removes a word from the per-vault custom
// dictionary. The word is trimmed + lowercased to match normalize's casing.
// Removing a word that isn't present is a no-op (idempotent). Returns the
// resolved list.
func (a *App) RemoveCustomDictionaryWord(word string) ([]string, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if a.vaultPath == "" {
		return nil, fmt.Errorf("vault not loaded")
	}
	w := strings.ToLower(strings.TrimSpace(word))
	a.configMu.Lock()
	defer a.configMu.Unlock()
	next := make([]string, 0, len(a.cfg.Editor.CustomDictionary))
	for _, existing := range a.cfg.Editor.CustomDictionary {
		if existing != w {
			next = append(next, existing)
		}
	}
	a.cfg.Editor.CustomDictionary = next
	if a.configWatcher != nil {
		a.configWatcher.RegisterSelfWrite()
	}
	if err := config.Save(a.vaultPath, a.cfg); err != nil {
		return nil, fmt.Errorf("save custom dictionary: %w", err)
	}
	out := make([]string, len(a.cfg.Editor.CustomDictionary))
	copy(out, a.cfg.Editor.CustomDictionary)
	return out, nil
}
