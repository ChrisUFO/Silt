package main

import (
	"net"
	"testing"
)

// Direct unit tests for isPrivateIP — existing tests cover it only indirectly
// via integration tests (TestSafeFetchClient_*).
func TestIsPrivateIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true}, // cloud metadata endpoint
		{"169.254.0.1", true},    // link-local (belt + suspenders)
		{"8.8.8.8", false},       // public DNS
		{"1.1.1.1", false},       // public DNS
		{"93.184.216.34", false}, // example.com
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", c.ip)
		}
		if got := isPrivateIP(ip); got != c.want {
			t.Errorf("isPrivateIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// Direct unit tests for isInternalIP — exercises the nil/loopback/private
// branches that existing integration tests don't hit directly.
func TestIsInternalIP(t *testing.T) {
	if !isInternalIP(nil) {
		t.Error("isInternalIP(nil) should be true")
	}
	if !isInternalIP(net.ParseIP("127.0.0.1")) {
		t.Error("isInternalIP(127.0.0.1) should be true")
	}
	if !isInternalIP(net.ParseIP("10.0.0.1")) {
		t.Error("isInternalIP(10.0.0.1) should be true")
	}
	if !isInternalIP(net.ParseIP("169.254.169.254")) {
		t.Error("isInternalIP(169.254.169.254) should be true")
	}
	if !isInternalIP(net.IPv4zero) {
		t.Error("isInternalIP(0.0.0.0) should be true")
	}
	if isInternalIP(net.ParseIP("8.8.8.8")) {
		t.Error("isInternalIP(8.8.8.8) should be false")
	}
}

// Verifies the redirectSafeHeaders allowlist contains exactly the expected
// safe headers and excludes sensitive ones — guards against accidental
// additions to the allowlist.
func TestRedirectSafeHeaders_AllowlistIntegrity(t *testing.T) {
	expected := map[string]bool{
		"accept":          true,
		"accept-language": true,
		"content-type":    true,
		"user-agent":      true,
	}
	for k, v := range expected {
		if redirectSafeHeaders[k] != v {
			t.Errorf("redirectSafeHeaders[%q] = %v, want %v", k, redirectSafeHeaders[k], v)
		}
	}
	if len(redirectSafeHeaders) != len(expected) {
		t.Errorf("redirectSafeHeaders has %d entries, want exactly %d — unexpected key added or removed", len(redirectSafeHeaders), len(expected))
	}
	for _, h := range []string{"authorization", "cookie", "x-api-key", "x-forwarded-for"} {
		if redirectSafeHeaders[h] {
			t.Errorf("redirectSafeHeaders[%q] should be false (sensitive header)", h)
		}
	}
}
