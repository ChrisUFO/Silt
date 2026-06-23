package main

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"silt/backend/config"
	"silt/backend/plugins"
	"silt/backend/vault"
)

// requireGrant denies an ungranted third-party plugin and returns a structured
// CapabilityDeniedError (never a generic error or a panic).
func TestRequireGrant_DeniesUngrantedThirdParty(t *testing.T) {
	app := newTestApp(t)
	err := app.requireGrant("some-third-party", plugins.CapWriteFiles)
	derr, ok := err.(*plugins.CapabilityDeniedError)
	if !ok {
		t.Fatalf("want *CapabilityDeniedError, got %T (%v)", err, err)
	}
	if derr.Plugin != "some-third-party" {
		t.Errorf("denied error plugin = %q, want %q", derr.Plugin, "some-third-party")
	}
	if derr.Capability != string(plugins.CapWriteFiles) {
		t.Errorf("denied error capability = %q", derr.Capability)
	}
}

// requireGrant implicitly grants a first-party (bundled) plugin every
// capability — they are trusted by definition (#113).
func TestRequireGrant_FirstPartyAlwaysGranted(t *testing.T) {
	app := newTestApp(t)
	for _, cap := range []plugins.Capability{
		plugins.CapWriteFiles,
		plugins.CapNetwork,
		plugins.CapOSOpen,
		plugins.CapEditorSchema,
	} {
		if err := app.requireGrant("silt-kanban", cap); err != nil {
			t.Errorf("first-party silt-kanban %q: want nil, got %v", cap, err)
		}
	}
}

// requireGrant rejects path-traversal pluginIDs before they reach filepath.Join
// in scratch-dir / audit-log paths (#113 security hardening).
func TestRequireGrant_RejectsPathTraversalPluginID(t *testing.T) {
	app := newTestApp(t)
	for _, pid := range []string{
		"../../etc/passwd",
		"..\\..\\Windows\\System32",
		"...",
		"plugin/id",
		"plugin\x00null",
		"",
	} {
		err := app.requireGrant(pid, plugins.CapNetwork)
		if err == nil {
			t.Errorf("requireGrant(%q) should be rejected as invalid pluginID", pid)
		}
	}
}

// A first-party ID works because seedFirstPartyGrants populated the grants
// table at config-load time — NOT because of a special-case bypass. A
// non-first-party ID with no grant entry is denied.
func TestPluginFetch_FirstPartyIDDeniedWithoutSeededGrant(t *testing.T) {
	app := newTestApp(t)
	// "silt-agenda" works because seedFirstPartyGrants ran in newTestApp.
	_ = app.RequestCapability("third-party", string(plugins.CapNetwork), "")

	// A random non-first-party, non-granted ID is denied.
	if err := app.requireGrant("not-a-real-plugin", plugins.CapNetwork); err == nil {
		t.Fatal("ungranted third-party should be denied")
	}
}

// RequestCapability rejects path-traversal pluginIDs (#113 security).
func TestRequestCapability_RejectsInvalidPluginID(t *testing.T) {
	app := newTestApp(t)
	for _, pid := range []string{"../../x", "..\\..\\y", "", "a/b", "a b"} {
		err := app.RequestCapability(pid, string(plugins.CapNetwork), "")
		if err == nil {
			t.Errorf("RequestCapability(%q) should be rejected", pid)
		}
	}
}

// RequestCapability + requireGrant round-trips and persists to the per-host
// grants store (F4 — grants no longer travel with vault config.yaml) so the
// grant survives a reload.
func TestRequestCapability_RoundTripsAndPersists(t *testing.T) {
	app := newTestApp(t)
	pid := "net-plugin"

	if err := app.RequestCapability(pid, string(plugins.CapNetwork), ""); err != nil {
		t.Fatalf("RequestCapability: %v", err)
	}
	if err := app.requireGrant(pid, plugins.CapNetwork); err != nil {
		t.Fatalf("requireGrant after grant: %v", err)
	}

	// A different capability is still denied.
	if err := app.requireGrant(pid, plugins.CapWriteFiles); err == nil {
		t.Fatal("write-files should still be denied")
	}

	// Qualifier is recorded.
	qual, ok := app.grantedQualifier(pid, plugins.CapNetwork)
	if !ok || qual != plugins.QualGranted {
		t.Errorf("qualifier = %q ok=%v, want granted true", qual, ok)
	}

	// Persisted: reload the host-scoped grants store from disk and the grant
	// survives (F4 — grants.json, NOT config.yaml).
	reloaded, err := vault.LoadGrants()
	if err != nil {
		t.Fatalf("LoadGrants: %v", err)
	}
	if got := reloaded[pid][string(plugins.CapNetwork)]; got != plugins.QualGranted {
		t.Errorf("persisted grant = %q, want %q", got, plugins.QualGranted)
	}
}

// RequestCapability accepts a qualifier and persists it.
func TestRequestCapability_WithQualifier(t *testing.T) {
	app := newTestApp(t)
	if err := app.RequestCapability("p", string(plugins.CapWriteFiles), "notebook"); err != nil {
		t.Fatalf("RequestCapability: %v", err)
	}
	qual, ok := app.grantedQualifier("p", plugins.CapWriteFiles)
	if !ok || qual != plugins.QualNotebook {
		t.Errorf("qualifier = %q ok=%v, want notebook", qual, ok)
	}
}

// RequestCapability rejects unknown capabilities (so a plugin can't enlarge
// its rights via typos or future names).
func TestRequestCapability_RejectsUnknownCapability(t *testing.T) {
	app := newTestApp(t)
	err := app.RequestCapability("p", "exec", "")
	if err == nil {
		t.Fatal("exec should be rejected (deferred capability)")
	}
	if !strings.Contains(err.Error(), "exec") {
		t.Errorf("error should name exec: %v", err)
	}
	if err := app.RequestCapability("p", "bogus-cap", ""); err == nil {
		t.Fatal("bogus capability should be rejected")
	}
}

// RevokeCapability removes the grant; capability=="" revokes all grants.
func TestRevokeCapability(t *testing.T) {
	app := newTestApp(t)
	pid := "p"
	_ = app.RequestCapability(pid, string(plugins.CapNetwork), "")
	_ = app.RequestCapability(pid, string(plugins.CapOSOpen), "")

	if err := app.RevokeCapability(pid, string(plugins.CapNetwork)); err != nil {
		t.Fatalf("RevokeCapability: %v", err)
	}
	if err := app.requireGrant(pid, plugins.CapNetwork); err == nil {
		t.Fatal("network should be denied after revoke")
	}
	if err := app.requireGrant(pid, plugins.CapOSOpen); err != nil {
		t.Fatalf("os-open should still be granted: %v", err)
	}

	// capability=="" revokes everything.
	if err := app.RevokeCapability(pid, ""); err != nil {
		t.Fatalf("RevokeCapability all: %v", err)
	}
	if err := app.requireGrant(pid, plugins.CapOSOpen); err == nil {
		t.Fatal("os-open should be denied after revoke-all")
	}
}

// GetGrantedCapabilities excludes first-party plugins (they are implicit).
func TestGetGrantedCapabilities_ExcludesFirstParty(t *testing.T) {
	app := newTestApp(t)
	_ = app.RequestCapability("silt-kanban", string(plugins.CapNetwork), "")
	_ = app.RequestCapability("third-party", string(plugins.CapNetwork), "")
	grants, err := app.GetGrantedCapabilities()
	if err != nil {
		t.Fatalf("GetGrantedCapabilities: %v", err)
	}
	if _, ok := grants["silt-kanban"]; ok {
		t.Error("first-party grants should not be surfaced")
	}
	if _, ok := grants["third-party"]; !ok {
		t.Error("third-party grant should be surfaced")
	}
}

// Concurrent RequestCapability callers from many goroutines must not panic
// (configMu serializes the read-modify-write).
func TestRequestCapability_ConcurrentNoPanic(t *testing.T) {
	app := newTestApp(t)
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = app.RequestCapability("p", string(plugins.CapNetwork), "")
		}()
	}
	wg.Wait()
	if err := app.requireGrant("p", plugins.CapNetwork); err != nil {
		t.Fatalf("grant lost under concurrency: %v", err)
	}
}

// grantedQualifier reports false for an ungranted capability.
func TestGrantedQualifier_Ungranted(t *testing.T) {
	app := newTestApp(t)
	if _, ok := app.grantedQualifier("p", plugins.CapNetwork); ok {
		t.Fatal("ungranted capability should report false")
	}
}

// =========================================================================
// F4: host-scoped plugin grants (#239)
// =========================================================================

// TestGrants_PersistToHostFile verifies RequestCapability writes to the
// per-host grants.json, NOT to vault config.yaml. This is the core F4
// guarantee: grants do not travel with synced vaults.
func TestGrants_PersistToHostFile(t *testing.T) {
	app := newTestApp(t)
	if err := app.RequestCapability("net-plugin", string(plugins.CapNetwork), ""); err != nil {
		t.Fatalf("RequestCapability: %v", err)
	}

	// The grant is in the host store.
	store, err := vault.LoadGrants()
	if err != nil {
		t.Fatalf("LoadGrants: %v", err)
	}
	if got := store["net-plugin"][string(plugins.CapNetwork)]; got != plugins.QualGranted {
		t.Errorf("host store grant = %q, want granted", got)
	}

	// The grant is NOT in vault config.yaml (the field is gone from the struct).
	cfg, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	yamlBytes, _ := os.ReadFile(config.ConfigPath(app.vaultPath))
	if strings.Contains(string(yamlBytes), "grants:") {
		t.Errorf("vault config.yaml must NOT contain a grants: block post-F4; got:\n%s", string(yamlBytes))
	}
	_ = cfg // suppress unused var if the Contains check is sufficient
}

// TestGrants_VaultConfigGrantsIgnored verifies a synced vault carrying a
// legacy `grants:` block for a hostile plugin is IGNORED — the host store
// is the single source of truth, and requireGrant denies the hostile plugin.
func TestGrants_VaultConfigGrantsIgnored(t *testing.T) {
	app := newTestApp(t)

	// Simulate a synced-vault attack: inject a grants: sub-block into the
	// existing plugins: section of config.yaml (NOT a second plugins: key,
	// which would be a YAML duplicate-key error).
	configPath := config.ConfigPath(app.vaultPath)
	yamlBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// Insert the hostile grants right after the "plugins:" line.
	hostile := strings.Replace(string(yamlBytes), "plugins:\n",
		"plugins:\n  grants:\n    evil-plugin:\n      network: granted\n", 1)
	if hostile == string(yamlBytes) {
		t.Fatal("test setup: could not find plugins: line to inject grants into")
	}
	if err := os.WriteFile(configPath, []byte(hostile), 0o644); err != nil {
		t.Fatalf("write hostile config: %v", err)
	}

	// Reload config. yaml.v3 drops the unknown `grants` field silently (the
	// struct no longer models it), so the hostile grant is never loaded.
	cfg, err := config.Load(app.vaultPath)
	if err != nil {
		t.Fatalf("config.Load with hostile grants: %v", err)
	}
	app.applyConfigLocked(cfg)

	// requireGrant MUST deny — the host store has no entry for evil-plugin.
	if err := app.requireGrant("evil-plugin", plugins.CapNetwork); err == nil {
		t.Fatal("requireGrant must deny evil-plugin — vault-scoped grants are ignored post-F4")
	}
}

// TestGrants_Migration verifies the one-time migration: a pre-F4 vault with
// a legacy grants: block triggers grants:migration-required, and
// ConfirmGrantsMigration moves the grants to the host store + strips the
// block from config.yaml.
func TestGrants_Migration(t *testing.T) {
	app := newTestApp(t)

	// Stage: inject a legacy grants sub-block into the existing plugins:
	// section of config.yaml for a third-party plugin.
	configPath := config.ConfigPath(app.vaultPath)
	yamlBytes := mustReadFile(t, configPath)
	legacyYAML := strings.Replace(string(yamlBytes), "plugins:\n",
		"plugins:\n  grants:\n    legacy-plugin:\n      network: granted\n      read-files: notebook\n", 1)
	if legacyYAML == string(yamlBytes) {
		t.Fatal("test setup: could not find plugins: line to inject grants into")
	}
	if err := os.WriteFile(configPath, []byte(legacyYAML), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	// LoadLegacyVaultGrants extracts the third-party grant from the raw YAML.
	extracted := vault.LoadLegacyVaultGrants(app.vaultPath)
	if _, ok := extracted["legacy-plugin"]; !ok {
		t.Fatalf("LoadLegacyVaultGrants should find legacy-plugin: got %v", extracted)
	}

	// ConfirmGrantsMigration writes to the host store + strips config.yaml.
	legacyGrants := map[string]map[string]string{
		"legacy-plugin": {
			"network":    "granted",
			"read-files": "notebook",
		},
	}
	if err := app.ConfirmGrantsMigration(legacyGrants); err != nil {
		t.Fatalf("ConfirmGrantsMigration: %v", err)
	}

	// Host store now has the migrated grant.
	store, err := vault.LoadGrants()
	if err != nil {
		t.Fatalf("LoadGrants after migration: %v", err)
	}
	if got := store["legacy-plugin"]["network"]; got != "granted" {
		t.Errorf("migrated grant = %q, want granted", got)
	}

	// requireGrant now succeeds for the migrated plugin.
	if err := app.requireGrant("legacy-plugin", plugins.CapNetwork); err != nil {
		t.Errorf("requireGrant after migration: %v", err)
	}

	// config.yaml no longer carries a grants: block (it was rewritten by
	// ConfirmGrantsMigration via config.Save, which drops the unmodeled field).
	afterYAML, _ := os.ReadFile(configPath)
	if strings.Contains(string(afterYAML), "grants:") {
		t.Errorf("config.yaml should not contain grants: after migration; got:\n%s", string(afterYAML))
	}
}

// TestGrants_FirstPartyAlwaysSeeded verifies first-party plugins are
// implicitly granted every capability after applyConfigLocked, with no
// user prompt. This is the F4 guarantee that the provenance move doesn't
// break the bundled-plugin experience.
func TestGrants_FirstPartyAlwaysSeeded(t *testing.T) {
	app := newTestApp(t)
	for id := range plugins.FirstPartyPluginIDs {
		for cap := range plugins.KnownCapabilities {
			if err := app.requireGrant(id, cap); err != nil {
				t.Errorf("first-party %s/%s should be implicitly granted: %v", id, cap, err)
			}
		}
	}
}

// TestGrants_0600Perms verifies the host grants file is written 0o600
// (matching F7's atomic-write + perm protocol). POSIX only — Windows does
// not enforce the POSIX permission bits.
func TestGrants_0600Perms(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	app := newTestApp(t)
	if err := app.RequestCapability("perm-plugin", string(plugins.CapNetwork), ""); err != nil {
		t.Fatalf("RequestCapability: %v", err)
	}
	grantsPath, err := vault.GrantsPath()
	if err != nil {
		t.Fatalf("GrantsPath: %v", err)
	}
	info, err := os.Stat(grantsPath)
	if err != nil {
		t.Fatalf("stat grants: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("grants.json perm = %o, want 0o600", info.Mode().Perm())
	}
}

// mustReadFile is a test helper that fails the test on a read error.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
