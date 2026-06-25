package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- Core mechanics --------------------------------------------------------

// appendAuditEntry writes a single-line JSON object; GetAuditLog reads it
// back with every field intact.
func TestAuditTrail_RoundTrip(t *testing.T) {
	app := newTestApp(t)

	e := newAuditEntry("install")
	e.PluginID = "test-plugin"
	e.Author = "Tester"
	e.Version = "1.0.0"
	e.SHA256 = "abc123"
	appendAuditEntry(app.vaultPath, e)

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Action != "install" {
		t.Errorf("action = %q, want install", got.Action)
	}
	if got.PluginID != "test-plugin" {
		t.Errorf("plugin_id = %q, want test-plugin", got.PluginID)
	}
	if got.Author != "Tester" {
		t.Errorf("author = %q, want Tester", got.Author)
	}
	if got.SHA256 != "abc123" {
		t.Errorf("sha256 = %q, want abc123", got.SHA256)
	}
	if got.At == "" {
		t.Error("at is empty")
	}
	if got.Actor == "" {
		t.Error("actor is empty")
	}
}

// GetAuditLog returns an empty (non-nil) slice pre-vault and when the file
// does not exist yet.
func TestAuditLog_EmptyPreVault(t *testing.T) {
	app := newTestApp(t)
	app.vaultPath = ""
	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog pre-vault: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("pre-vault got %d entries, want 0", len(entries))
	}
}

// ClearAuditLog empties the file; a subsequent GetAuditLog returns zero entries.
func TestAuditTrail_Clear(t *testing.T) {
	app := newTestApp(t)
	appendAuditEntry(app.vaultPath, newAuditEntry("grant"))
	appendAuditEntry(app.vaultPath, newAuditEntry("revoke"))

	if err := app.ClearAuditLog(); err != nil {
		t.Fatalf("ClearAuditLog: %v", err)
	}
	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("after clear got %d entries, want 0", len(entries))
	}
}

// auditActor returns a non-empty username on the test host.
func TestAuditTrail_ActorNonEmpty(t *testing.T) {
	if a := auditActor(); a == "" {
		t.Error("auditActor() returned empty string")
	}
}

// The on-disk file is written with 0o600 permissions (Unix only — Windows
// has no equivalent of the Unix permission model, so os.OpenFile's mode is
// ignored and the file always reads as 0666).
func TestAuditTrail_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0o600 file permissions are not enforced on Windows")
	}
	app := newTestApp(t)
	appendAuditEntry(app.vaultPath, newAuditEntry("install"))

	info, err := os.Stat(auditLogPath(app.vaultPath))
	if err != nil {
		t.Fatalf("stat audit.log: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("audit.log perm = %o, want 0o600", info.Mode().Perm())
	}
}

// --- Rotation --------------------------------------------------------------

// When the log exceeds maxAuditLogBytes it is rotated to the last
// maxAuditLogLines entries. The cap is on bytes, not lines: after rotation the
// file grows again until the next cap breach, so the line count between
// rotations can exceed maxAuditLogLines. The invariant we assert is that the
// file never grows unbounded (size ≤ cap) and that entries survive rotation.
func TestAuditTrail_Rotation(t *testing.T) {
	app := newTestApp(t)

	// Write enough entries to exceed 1 MB at least once. Each entry is ~120
	// bytes of JSON; ~10000 entries ≈ 1.2 MB, which triggers at least one
	// rotation to 500 lines.
	const totalWrites = 10000
	for i := 0; i < totalWrites; i++ {
		e := newAuditEntry("grant")
		e.PluginID = "rotate-test"
		e.Capability = "network"
		appendAuditEntry(app.vaultPath, e)
	}

	info, err := os.Stat(auditLogPath(app.vaultPath))
	if err != nil {
		t.Fatalf("stat after rotation: %v", err)
	}
	// After rotation the file is well under 1 MB (it was truncated to ~500
	// lines and then grew by the remaining writes, but never enough to breach
	// the cap a second time).
	if info.Size() > maxAuditLogBytes {
		t.Errorf("audit.log size = %d bytes, want ≤ %d (rotation did not trigger)", info.Size(), maxAuditLogBytes)
	}

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after rotation: %v", err)
	}
	// Rotation happened: far fewer entries than total writes.
	if len(entries) >= totalWrites {
		t.Errorf("no rotation occurred: %d entries == %d writes", len(entries), totalWrites)
	}
	if len(entries) == 0 {
		t.Fatal("rotation lost all entries")
	}
	// The surviving entries are valid (all should have action=grant).
	for _, e := range entries {
		if e.Action != "grant" {
			t.Errorf("post-rotation entry action = %q, want grant", e.Action)
		}
	}
}

// --- Hook integration: each action appends exactly one entry ---------------

// RequestCapability appends a grant entry; RevokeCapability appends a revoke
// entry. Each carries the pluginID + capability.
func TestAuditTrail_GrantRevokeHooks(t *testing.T) {
	app := newTestApp(t)

	if err := app.RequestCapability("third-party", "network", ""); err != nil {
		t.Fatalf("RequestCapability: %v", err)
	}
	if err := app.RevokeCapability("third-party", "network"); err != nil {
		t.Fatalf("RevokeCapability: %v", err)
	}

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (grant + revoke)", len(entries))
	}
	if entries[0].Action != "grant" {
		t.Errorf("entry[0] action = %q, want grant", entries[0].Action)
	}
	if entries[0].PluginID != "third-party" {
		t.Errorf("entry[0] plugin_id = %q, want third-party", entries[0].PluginID)
	}
	if entries[0].Capability != "network" {
		t.Errorf("entry[0] capability = %q, want network", entries[0].Capability)
	}
	if entries[1].Action != "revoke" {
		t.Errorf("entry[1] action = %q, want revoke", entries[1].Action)
	}
	if entries[1].Capability != "network" {
		t.Errorf("entry[1] capability = %q, want network", entries[1].Capability)
	}
}

// InstallPlugin appends an install entry (with author/version/sha256);
// UninstallPlugin appends an uninstall entry.
func TestAuditTrail_InstallUninstallHooks(t *testing.T) {
	app := newTestApp(t)

	manifest := `{"id":"audit-test","name":"Audit Test","version":"2.0.0","author":"TestAuthor","main":"index.js"}`
	archive := buildPluginArchive(t, "audit-test", manifest)

	if _, err := app.InstallPlugin(archive); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}

	// Verify the install entry before uninstalling so we can check sha256.
	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after install: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries after install, want 1", len(entries))
	}
	inst := entries[0]
	if inst.Action != "install" {
		t.Errorf("action = %q, want install", inst.Action)
	}
	if inst.PluginID != "audit-test" {
		t.Errorf("plugin_id = %q, want audit-test", inst.PluginID)
	}
	if inst.Author != "TestAuthor" {
		t.Errorf("author = %q, want TestAuthor", inst.Author)
	}
	if inst.Version != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0", inst.Version)
	}
	if inst.SHA256 == "" {
		t.Error("sha256 is empty; installer should have computed ContentSHA256")
	}

	if err := app.UninstallPlugin("audit-test"); err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}
	entries, err = app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after uninstall: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries after uninstall, want 2", len(entries))
	}
	if entries[1].Action != "uninstall" {
		t.Errorf("entry[1] action = %q, want uninstall", entries[1].Action)
	}
	if entries[1].PluginID != "audit-test" {
		t.Errorf("entry[1] plugin_id = %q, want audit-test", entries[1].PluginID)
	}
}

// DisablePlugin appends a disable entry; EnablePlugin appends an enable entry.
func TestAuditTrail_EnableDisableHooks(t *testing.T) {
	app := newTestApp(t)

	manifest := `{"id":"toggle-test","name":"Toggle","version":"1.0.0","main":"index.js"}`
	archive := buildPluginArchive(t, "toggle-test", manifest)
	if _, err := app.InstallPlugin(archive); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}

	if err := app.DisablePlugin("toggle-test"); err != nil {
		t.Fatalf("DisablePlugin: %v", err)
	}
	if err := app.EnablePlugin("toggle-test"); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	// install + disable + enable = 3 entries.
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (install+disable+enable)", len(entries))
	}
	// Find the disable + enable entries.
	var sawDisable, sawEnable bool
	for _, e := range entries {
		if e.Action == "disable" {
			sawDisable = true
			if e.PluginID != "toggle-test" {
				t.Errorf("disable plugin_id = %q, want toggle-test", e.PluginID)
			}
		}
		if e.Action == "enable" {
			sawEnable = true
			if e.PluginID != "toggle-test" {
				t.Errorf("enable plugin_id = %q, want toggle-test", e.PluginID)
			}
		}
	}
	if !sawDisable {
		t.Error("no disable entry in audit log")
	}
	if !sawEnable {
		t.Error("no enable entry in audit log")
	}
}

// LinkNotebook appends a link entry (with id/rootPath/fingerprint);
// UnlinkNotebook appends an unlink entry.
func TestAuditTrail_LinkUnlinkHooks(t *testing.T) {
	app := newTestApp(t)

	linkedRoot := t.TempDir()
	// Create a markdown file so the linked root is non-empty.
	writeFile(t, filepath.Join(linkedRoot, "note.md"), "# Linked\n")

	ln, err := app.LinkNotebook(linkedRoot)
	if err != nil {
		t.Fatalf("LinkNotebook: %v", err)
	}

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after link: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries after link, want 1", len(entries))
	}
	l := entries[0]
	if l.Action != "link" {
		t.Errorf("action = %q, want link", l.Action)
	}
	if l.ID != ln.ID {
		t.Errorf("id = %q, want %q", l.ID, ln.ID)
	}
	if l.RootPath == "" {
		t.Error("root_path is empty")
	}
	if l.Fingerprint == "" {
		t.Error("fingerprint is empty; link entry should carry the F3 root fingerprint")
	}

	if err := app.UnlinkNotebook(ln.ID); err != nil {
		t.Fatalf("UnlinkNotebook: %v", err)
	}
	entries, err = app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog after unlink: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries after unlink, want 2", len(entries))
	}
	if entries[1].Action != "unlink" {
		t.Errorf("entry[1] action = %q, want unlink", entries[1].Action)
	}
	if entries[1].ID != ln.ID {
		t.Errorf("entry[1] id = %q, want %q", entries[1].ID, ln.ID)
	}
}

// Uninstalling a plugin that held capability grants emits per-capability
// "revoke" entries before the "uninstall" entry, so the audit trail captures
// exactly which capabilities were removed at uninstall time (not just that
// the plugin was removed).
func TestAuditTrail_UninstallEmitsPerCapabilityRevokeEntries(t *testing.T) {
	app := newTestApp(t)

	manifest := `{"id":"granted-plugin","name":"Granted","version":"1.0.0","author":"Author","main":"index.js"}`
	archive := buildPluginArchive(t, "granted-plugin", manifest)
	if _, err := app.InstallPlugin(archive); err != nil {
		t.Fatalf("InstallPlugin: %v", err)
	}
	// Grant two distinct capabilities so we can verify both get revoke entries.
	if err := app.RequestCapability("granted-plugin", "network", ""); err != nil {
		t.Fatalf("RequestCapability network: %v", err)
	}
	if err := app.RequestCapability("granted-plugin", "write-files", ""); err != nil {
		t.Fatalf("RequestCapability write-files: %v", err)
	}

	if err := app.UninstallPlugin("granted-plugin"); err != nil {
		t.Fatalf("UninstallPlugin: %v", err)
	}

	entries, err := app.GetAuditLog()
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}

	// Expected sequence: install, grant(network), grant(write-files),
	// revoke(network), revoke(write-files), uninstall.
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action+":"+e.Capability)
	}
	// The install and uninstall entries have empty Capability.
	if len(entries) != 6 {
		t.Fatalf("entry count = %d, want 6\nactions: %v", len(entries), actions)
	}
	if actions[0] != "install:" {
		t.Errorf("actions[0] = %q, want install:", actions[0])
	}
	if actions[5] != "uninstall:" {
		t.Errorf("actions[5] = %q, want uninstall:", actions[5])
	}

	// Grants must contain network and write-files in any order
	grants := map[string]bool{actions[1]: true, actions[2]: true}
	if !grants["grant:network"] || !grants["grant:write-files"] {
		t.Errorf("grants[1:3] did not contain expected actions, got %v", actions[1:3])
	}

	// Revokes must contain network and write-files in any order
	revokes := map[string]bool{actions[3]: true, actions[4]: true}
	if !revokes["revoke:network"] || !revokes["revoke:write-files"] {
		t.Errorf("revokes[3:5] did not contain expected actions, got %v", actions[3:5])
	}
}

// Each audit entry on disk is valid single-line JSON (no embedded newlines
// that would corrupt the line-oriented format).
func TestAuditTrail_JSONSingleLine(t *testing.T) {
	app := newTestApp(t)

	e := newAuditEntry("install")
	e.PluginID = "json-test"
	e.Author = "Author\nWith\nNewlines" // hostile payload
	e.Version = "1.0"
	appendAuditEntry(app.vaultPath, e)

	data, err := os.ReadFile(auditLogPath(app.vaultPath))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d (json.Marshal should escape newlines)", len(lines))
	}
	// Verify the line is valid JSON and the newlines were escaped by Marshal.
	var entry AuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if entry.Author != "Author\nWith\nNewlines" {
		t.Errorf("author round-trip = %q", entry.Author)
	}
}
