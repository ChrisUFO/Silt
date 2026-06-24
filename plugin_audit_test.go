package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appendNetworkAuditLine writes a single-line JSON object per entry (#254).
// The written line must be valid JSON that round-trips through
// parseNetworkLogLine with all fields intact.
func TestAppendNetworkAuditLine_WritesJSON(t *testing.T) {
	vaultPath := t.TempDir()
	entry := &NetworkAuditEntry{
		Plugin: "test-plugin",
		Host:   "example.com/path/with spaces",
		Status: 200,
		Method: "GET",
		At:     time.Now().Format(time.RFC3339),
	}
	appendNetworkAuditLine(vaultPath, entry)

	logPath := filepath.Join(vaultPath, ".system", "plugins", "test-plugin", "network.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read network.log: %v", err)
	}
	line := strings.TrimRight(string(data), "\n")
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("written line is not valid JSON: %v\nline=%q", err, line)
	}
	if got["plugin"] != "test-plugin" {
		t.Errorf("plugin = %v, want test-plugin", got["plugin"])
	}
	if got["host"] != "example.com/path/with spaces" {
		t.Errorf("host = %v, want example.com/path/with spaces", got["host"])
	}
}

// parseNetworkLogLine reads the JSON format and preserves all fields,
// including a host segment that contains embedded spaces (the case that
// motivated the switch away from the space-delimited format).
func TestParseNetworkLogLine_JSON_HostWithSpaces(t *testing.T) {
	line := `{"at":"2026-06-23T10:00:00Z","method":"GET","host":"example.com/path/with spaces","status":200,"plugin":"p"}`
	got, ok := parseNetworkLogLine(line)
	if !ok {
		t.Fatalf("parseNetworkLogLine(JSON) returned ok=false; line=%q", line)
	}
	want := NetworkAuditEntry{
		At:     "2026-06-23T10:00:00Z",
		Method: "GET",
		Host:   "example.com/path/with spaces",
		Status: 200,
		Plugin: "p",
	}
	if got != want {
		t.Errorf("parsed entry = %+v, want %+v", got, want)
	}
}

// parseNetworkLogLine rejects strings that are not valid JSON with a
// non-empty At field.
func TestParseNetworkLogLine_RejectsGarbage(t *testing.T) {
	for _, line := range []string{
		"",
		"not json and too few fields",
		"{}",
		`{"plugin":"x"}`, // missing At — treated as not-a-valid-entry
	} {
		if _, ok := parseNetworkLogLine(line); ok {
			t.Errorf("parseNetworkLogLine(%q) should return ok=false", line)
		}
	}
}

// A full round-trip (write via appendNetworkAuditLine → read via
// parseNetworkLogLine) preserves every field, including a host with embedded
// spaces.
func TestNetworkAuditRoundTrip_JSON(t *testing.T) {
	vaultPath := t.TempDir()
	entry := &NetworkAuditEntry{
		Plugin: "round-trip",
		Host:   "api.example.com/v2/path with spaces/and more",
		Status: 404,
		Method: "DELETE",
		At:     "2026-06-23T12:34:56Z",
	}
	appendNetworkAuditLine(vaultPath, entry)

	logPath := filepath.Join(vaultPath, ".system", "plugins", "round-trip", "network.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read network.log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		got, ok := parseNetworkLogLine(line)
		if !ok {
			t.Fatalf("parseNetworkLogLine returned ok=false; line=%q", line)
		}
		if got != *entry {
			t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, *entry)
		}
	}
}
