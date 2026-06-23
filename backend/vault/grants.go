package vault

// grants.go implements the F4 host-scoped plugin grants store. Plugin
// capability grants previously lived in the vault-scoped config.yaml
// (`plugins.grants`), which meant a vault synced from another host carried the
// counterpart's grant decisions — including pre-granted capabilities for
// hostile plugins. F4 moves grants to a per-host file alongside settings.json
// so they no longer travel with synced vaults.
//
// The store mirrors the old in-memory shape (pluginID → capability →
// qualifier) so the App-layer capability gate (requireGrant) is unchanged
// beyond swapping the source map. Atomic-write + 0o600 perms match the F7
// protocol used by settings.json and the F20 fingerprint.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"silt/backend/parser"
)

// GrantsStore is the per-host plugin capability grant table: pluginID →
// capability → qualifier ("granted" | "notebook" | "vault"). A missing entry
// means "not granted". First-party plugins are seeded implicitly at vault-open
// time (seedFirstPartyGrants) and never prompt.
type GrantsStore map[string]map[string]string

// GrantsPath returns the absolute path to the host-scoped grants file. It
// lives alongside settings.json in the OS-config dir so the pair travels
// together and a reader never has to look in two places.
func GrantsPath() (string, error) {
	settingsPath, err := GetSettingsPath()
	if err != nil {
		return "", err
	}
	// Replace the .json extension with -grants.json so a glob for settings*
	// picks up both, and the two files sort adjacently.
	dir := filepath.Dir(settingsPath)
	return filepath.Join(dir, "grants.json"), nil
}

// LoadGrants reads the host-scoped grants file. A missing file is NOT an error
// — it returns an empty (non-nil) store, because a fresh host has not granted
// anything yet (first-party plugins are seeded in-memory, not on disk). A file
// that exists but fails to parse returns an error (fail-loud — never silently
// fall through to "no grants", which would silently revoke every capability).
func LoadGrants() (GrantsStore, error) {
	path, err := GrantsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GrantsStore{}, nil
		}
		return nil, fmt.Errorf("read grants file: %w", err)
	}
	var store GrantsStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parse grants file: %w", err)
	}
	if store == nil {
		store = GrantsStore{}
	}
	return store, nil
}

// SaveGrants atomically persists the grants store to the host-scoped file with
// 0o600 perms (WriteFileAtomic uses os.CreateTemp which defaults to 0o600).
// The temp-file + rename protocol guarantees the on-disk file is never a
// half-written intermediate state, matching the durability of settings.json.
func SaveGrants(store GrantsStore) error {
	path, err := GrantsPath()
	if err != nil {
		return err
	}
	if store == nil {
		store = GrantsStore{}
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal grants: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return parser.WriteFileAtomic(path, data)
}

// LoadLegacyVaultGrants reads a vault-scoped config.yaml and extracts any
// legacy `plugins.grants:` block that was written by a pre-F4 Silt version.
// Used during the one-time migration (initializeVaultServices) to detect
// whether a vault needs grant migration. Returns an empty store if the vault
// config has no grants or the file doesn't exist. Tolerant: a parse error in
// the grants sub-tree does NOT fail the vault open — the grants are just
// ignored (the safe default; the user re-grants on first use).
func LoadLegacyVaultGrants(vaultPath string) GrantsStore {
	configPath := filepath.Join(vaultPath, ".system", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return GrantsStore{}
	}
	// Decode only the plugins.grants path so an unrelated parse error in the
	// full config (handled separately by config.Load) doesn't block grant
	// migration. Unknown fields are ignored.
	var partial struct {
		Plugins struct {
			Grants GrantsStore `yaml:"grants"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(data, &partial); err != nil {
		return GrantsStore{}
	}
	if partial.Plugins.Grants == nil {
		return GrantsStore{}
	}
	return partial.Plugins.Grants
}
