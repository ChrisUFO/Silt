package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxPluginNetworkLogBytes bounds a single per-plugin on-disk network.log so
// it cannot grow unbounded across a long session. When exceeded, the log is
// truncated to the most recent maxPluginNetworkLogLines.
const (
	maxPluginNetworkLogBytes = 1 * 1024 * 1024 // 1 MB
	maxPluginNetworkLogLines = 200             // keep-lines on truncation
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

// appendNetworkAuditLine writes one entry to the per-plugin on-disk log file.
// Extracted from auditNetwork's pre-#235 inline path; the I/O is identical.
// Best-effort — errors are logged, never surfaced (the audit log is
// diagnostic).
//
// Concurrency: NOT goroutine-safe — callers must serialize. In production the
// background writer goroutine (networkAuditWriterState.process) is the sole
// caller; in the inline fallback path (tests, pre-init) auditNetwork holds
// networkAuditMu. Mirrors the pre-#235 contract.
//
// #254: the on-disk format is a single-line JSON object per entry (one
// json.Marshal + trailing newline). JSON is self-describing, survives column
// re-ordering, and is parseable by standard tooling (jq, SIEM ingest). The
// read path (parseNetworkLogLine) still accepts the legacy space-delimited
// format for one release so existing logs survive an upgrade.
func appendNetworkAuditLine(vaultPath string, entry *NetworkAuditEntry) {
	if vaultPath == "" {
		return
	}
	logPath := filepath.Join(vaultPath, ".system", "plugins", entry.Plugin, "network.log")
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("appendNetworkAuditLine: json.Marshal failed: %v", err)
		return
	}
	_ = os.MkdirAll(filepath.Dir(logPath), 0o700)
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxPluginNetworkLogBytes {
		truncateNetworkLog(logPath, maxPluginNetworkLogLines)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}

// clearNetworkAuditFiles empties every per-plugin on-disk network.log under
// the vault's .system/plugins/ tree. Extracted from ClearNetworkAudit's
// pre-#235 inline path; best-effort (errors silently ignored).
func clearNetworkAuditFiles(vaultPath string) {
	if vaultPath == "" {
		return
	}
	pluginsDir := filepath.Join(vaultPath, ".system", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return
	}
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
// When the background writer is running, the on-disk truncation is enqueued
// and processed in FIFO order with concurrent auditNetwork appends so a
// fetch that fires after the clear click cannot interleave a line into a file
// we just emptied. When the writer is not running (tests, pre-initialize),
// the truncation runs inline — the pre-#235 behavior.
func (a *App) ClearNetworkAudit() error {
	networkAuditMu.Lock()
	networkAudit = nil
	w := currentNetworkAuditWriter()
	if w == nil {
		// Writer not running: truncate inline (pre-#235 path).
		clearNetworkAuditFiles(a.vaultPath)
		networkAuditMu.Unlock()
		return nil
	}
	// The clear op MUST be enqueued while holding networkAuditMu so it is
	// ordered relative to concurrent auditNetwork appends. Both producers
	// send on w.ch under this lock; without it, a concurrent auditNetwork
	// could enqueue its entry AFTER our networkAudit = nil but BEFORE our
	// clear op, causing the writer to append-then-truncate (deleting a
	// post-clear entry from disk — a #157 restart-persistence regression).
	// The blocking send is safe: the 256-slot buffer makes it non-blocking
	// in practice (would need >5000 RPS to fill), and in the degraded case
	// (writer stuck on slow I/O), blocking is correct backpressure. The
	// wait on op.done happens OUTSIDE the lock so concurrent fetches can
	// keep appending to the in-memory slice while the writer truncates.
	op := &networkAuditOp{clear: true, done: make(chan struct{})}
	w.ch <- op
	networkAuditMu.Unlock()
	<-op.done
	return nil
}

// seedNetworkAuditFromDisk reads every on-disk network.log file under the
// vault's .system/plugins/ tree and seeds the in-memory audit log so entries
// survive a restart (#157). Called once during initializeVaultServices. The
// on-disk format is one line per entry (#254: a single-line JSON object; the
// reader also accepts the legacy space-delimited format for one release). The
// in-memory log is capped at 500 entries (most recent).
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
// NetworkAuditEntry. The current format (#254) is a single-line JSON object.
// Falls back to the legacy space-delimited format
// (`<RFC3339> <METHOD> <host> <status> <pluginID>`) for logs written by the
// previous release, so an upgrade does not drop existing entries. The legacy
// parser will be removed after one release (follow-up issue). Returns ok=false
// on any parse failure (best-effort). The legacy parse is right-to-left
// (status = second-to-last field, pluginID = last) to tolerate spaces in the
// host/path segment, which a left-to-right split would misalign.
func parseNetworkLogLine(line string) (NetworkAuditEntry, bool) {
	// JSON format (current release): one json object per line.
	var entry NetworkAuditEntry
	if err := json.Unmarshal([]byte(line), &entry); err == nil && entry.At != "" {
		return entry, true
	}
	// Legacy format (pre-#254): space-delimited, parsed from the right.
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
	// Decouple the on-disk write from the lock: enqueue onto the background
	// writer's channel (non-blocking — the 256-slot buffer handles burst rates
	// far beyond any plugin's allotment). If the writer is not running, fall
	// back to inline I/O so behavior is identical for tests that don't start
	// the writer (#235).
	w := currentNetworkAuditWriter()
	if w != nil {
		select {
		case w.ch <- &networkAuditOp{entry: &entry}:
		default:
			log.Printf("auditNetwork: writer queue full; dropping on-disk write for plugin %q", pluginID)
		}
	} else {
		appendNetworkAuditLine(a.vaultPath, &entry)
	}
	networkAuditMu.Unlock()
}

// --- Background audit-log writer (#235) ----------------------------------
//
// The writer drains on-disk audit writes off the networkAuditMu lock so
// concurrent PluginFetch calls don't serialize on per-plugin file I/O. A
// single goroutine processes the channel in FIFO order, preserving the
// "no interleaved line in a file we just emptied" invariant ClearNetworkAudit
// relies on.

// networkAuditOp is one operation queued to the background writer.
type networkAuditOp struct {
	entry *NetworkAuditEntry // non-nil = append to on-disk log
	clear bool               // true = truncate on-disk logs
	done  chan struct{}      // optional: closed when this op is fully processed
}

// networkAuditWriterState is the background writer's mutable state. It is
// non-nil while the writer goroutine is running.
type networkAuditWriterState struct {
	ch        chan *networkAuditOp
	stop      chan struct{}
	done      chan struct{} // closed when the goroutine has exited
	vaultPath string
}

var (
	networkAuditWriterMu sync.Mutex // guards networkAuditWriter
	networkAuditWriter   *networkAuditWriterState
)

// currentNetworkAuditWriter returns the active writer state, or nil if the
// writer is not running. Thread-safe; callers may use the returned pointer
// without holding networkAuditWriterMu (the state struct is never mutated
// after creation; only the package-level pointer is swapped).
func currentNetworkAuditWriter() *networkAuditWriterState {
	networkAuditWriterMu.Lock()
	defer networkAuditWriterMu.Unlock()
	return networkAuditWriter
}

// startNetworkAuditWriter launches the background audit-log writer goroutine
// for the given vault. Idempotent — a second call while the writer is running
// is a no-op. The writer drains on-disk audit writes off the networkAuditMu
// lock so concurrent PluginFetch calls don't serialize on file I/O (#235).
func startNetworkAuditWriter(vaultPath string) {
	networkAuditWriterMu.Lock()
	defer networkAuditWriterMu.Unlock()
	if networkAuditWriter != nil {
		return
	}
	w := &networkAuditWriterState{
		ch:        make(chan *networkAuditOp, 256),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		vaultPath: vaultPath,
	}
	networkAuditWriter = w
	go w.run()
}

// stopNetworkAuditWriter signals the writer to drain remaining ops and exit,
// then blocks until the goroutine is done. Idempotent. Guarantees no queued
// entry is lost on vault close (#157 persistent-audit contract).
func stopNetworkAuditWriter() {
	networkAuditWriterMu.Lock()
	w := networkAuditWriter
	if w == nil {
		networkAuditWriterMu.Unlock()
		return
	}
	networkAuditWriter = nil
	networkAuditWriterMu.Unlock()
	close(w.stop)
	<-w.done
}

// run is the writer goroutine body. It processes ops in FIFO order until
// stop is closed, then drains every remaining queued op before exiting so
// no entry is lost on shutdown.
func (w *networkAuditWriterState) run() {
	defer close(w.done)
	for {
		select {
		case op := <-w.ch:
			w.process(op)
		case <-w.stop:
			// Drain every queued op before exiting so no entry is lost.
			for {
				select {
				case op := <-w.ch:
					w.process(op)
				default:
					return
				}
			}
		}
	}
}

// process handles one op. Entry appends to the per-plugin on-disk log; clear
// empties every on-disk log. If done is non-nil it is closed after the op
// completes so the caller (ClearNetworkAudit) can synchronize.
func (w *networkAuditWriterState) process(op *networkAuditOp) {
	if op.entry != nil {
		appendNetworkAuditLine(w.vaultPath, op.entry)
	}
	if op.clear {
		clearNetworkAuditFiles(w.vaultPath)
	}
	if op.done != nil {
		close(op.done)
	}
}
