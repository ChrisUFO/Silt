package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// networkAuditMu guards the in-memory network audit log. The log is a simple
// append-only slice of {plugin, host, status, time} entries, surfaced in
// Settings → Plugins so a user can see what a networked plugin is doing (#115).
var (
	networkAuditMu sync.Mutex
	networkAudit   []NetworkAuditEntry
)

// NetworkAuditEntry is one row of the plugin network audit log.
type NetworkAuditEntry struct {
	Plugin string `json:"plugin"`
	Host   string `json:"host"`
	Status int    `json:"status"`
	Method string `json:"method"`
	At     string `json:"at"` // RFC3339
}

// truncateNetworkLog reads the log file, keeps the last n lines, and rewrites
// it. Best-effort — errors are silently ignored (the audit log is not a
// security boundary, just a diagnostic aid).
func truncateNetworkLog(path string, keepLines int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= keepLines {
		return
	}
	kept := lines[len(lines)-keepLines:]
	_ = os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}

// GetNetworkAudit returns the in-memory plugin network audit log (#115).
func (a *App) GetNetworkAudit() ([]NetworkAuditEntry, error) {
	networkAuditMu.Lock()
	defer networkAuditMu.Unlock()
	out := make([]NetworkAuditEntry, len(networkAudit))
	copy(out, networkAudit)
	return out, nil
}

// ClearNetworkAudit empties the in-memory audit log AND truncates the on-disk
// per-plugin network.log files so a clear is durable across restarts (#157).
// The on-disk truncation runs under the same lock auditNetwork holds while
// appending, so a concurrent fetch cannot interleave a fresh line into a file
// we just emptied (the I/O is bounded by the small number of plugin dirs).
func (a *App) ClearNetworkAudit() error {
	networkAuditMu.Lock()
	defer networkAuditMu.Unlock()
	networkAudit = nil
	// Best-effort on-disk truncation: walk <vault>/.system/plugins/*/network.log
	// and empty each file. Errors are non-fatal (audit log is diagnostic).
	if a.vaultPath != "" {
		pluginsDir := filepath.Join(a.vaultPath, ".system", "plugins")
		entries, err := os.ReadDir(pluginsDir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				logPath := filepath.Join(pluginsDir, e.Name(), "network.log")
				if _, err := os.Stat(logPath); err == nil {
					_ = os.WriteFile(logPath, []byte{}, 0o600)
				}
			}
		}
	}
	return nil
}

// seedNetworkAuditFromDisk reads every on-disk network.log file under the
// vault's .system/plugins/ tree and seeds the in-memory audit log so entries
// survive a restart (#157). Called once during initializeVaultServices. The
// on-disk format is one line per entry: `<RFC3339> <METHOD> <host> <status>
// <pluginID>`. The in-memory log is capped at 500 entries (most recent).
func seedNetworkAuditFromDisk(vaultPath string) {
	if vaultPath == "" {
		return
	}
	pluginsDir := filepath.Join(vaultPath, ".system", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}
	var seeded []NetworkAuditEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logPath := filepath.Join(pluginsDir, e.Name(), "network.log")
		data, err := os.ReadFile(logPath)
		if err != nil || len(data) == 0 {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		for _, line := range lines {
			entry, ok := parseNetworkLogLine(line)
			if ok {
				seeded = append(seeded, entry)
			}
		}
	}
	// Sort by timestamp (oldest first) so we can trim to the last 500.
	sort.Slice(seeded, func(i, j int) bool {
		return seeded[i].At < seeded[j].At
	})
	if len(seeded) > 500 {
		seeded = seeded[len(seeded)-500:]
	}
	networkAuditMu.Lock()
	// Only seed if the in-memory log is empty (don't overwrite entries that
	// may have been added between vault open and this call).
	if len(networkAudit) == 0 {
		networkAudit = seeded
	}
	networkAuditMu.Unlock()
}

// parseNetworkLogLine parses one line from a network.log file into a
// NetworkAuditEntry. The format is: `<RFC3339> <METHOD> <host> <status>
// <pluginID>`. Returns ok=false on any parse failure (best-effort). Parsing
// from the right (status = second-to-last field, pluginID = last) tolerates
// spaces in the host/path segment, which a left-to-right split would misalign.
func parseNetworkLogLine(line string) (NetworkAuditEntry, bool) {
	parts := strings.Fields(line)
	n := len(parts)
	if n < 5 {
		return NetworkAuditEntry{}, false
	}
	status, err := strconv.Atoi(parts[n-2])
	if err != nil {
		return NetworkAuditEntry{}, false
	}
	return NetworkAuditEntry{
		At:     parts[0],
		Method: parts[1],
		Host:   strings.Join(parts[2:n-2], " "),
		Status: status,
		Plugin: parts[n-1],
	}, true
}

// auditNetwork appends a {plugin, host, status, time} row. The body is NEVER
// logged — only the host + status so a user can see what a plugin is doing
// without leaking sensitive request/response payloads.
func (a *App) auditNetwork(pluginID, method, rawURL string, status int) {
	host := rawURL
	// Best-effort host extraction without a full URL parse (the URL was already
	// validated as http/https above).
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest := rawURL[i+3:]
		// Include the path (up to but not including query string) so the
		// audit log distinguishes GET /health from DELETE /data/all.
		if j := strings.IndexAny(rest, "?#"); j >= 0 {
			rest = rest[:j]
		}
		host = rest
	}
	entry := NetworkAuditEntry{
		Plugin: pluginID,
		Host:   host,
		Status: status,
		Method: method,
		At:     time.Now().Format(time.RFC3339),
	}
	networkAuditMu.Lock()
	networkAudit = append(networkAudit, entry)
	// Bound the in-memory log to the last 500 entries so it does not grow
	// unbounded.
	if len(networkAudit) > 500 {
		networkAudit = networkAudit[len(networkAudit)-500:]
	}
	// Persist to the on-disk log inside the same lock so concurrent
	// PluginFetch calls cannot interleave file writes and corrupt lines.
	// The I/O is a single WriteString — holding the lock briefly is fine.
	const maxPluginNetworkLogBytes = 1 * 1024 * 1024 // 1 MB
	if a.vaultPath != "" {
		logPath := filepath.Join(a.vaultPath, ".system", "plugins", pluginID, "network.log")
		line := fmt.Sprintf("%s %s %s %d %s\n", entry.At, entry.Method, entry.Host, entry.Status, pluginID)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		if info, err := os.Stat(logPath); err == nil && info.Size() > maxPluginNetworkLogBytes {
			truncateNetworkLog(logPath, 200)
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
	}
	networkAuditMu.Unlock()
}
