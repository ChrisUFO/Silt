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

func TestIsSafeUrl(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"mailto:user@example.com", true},
		{"javascript:alert(1)", false},
		{"file:///etc/passwd", false},
		{"ftp://example.com", false},
		{"", false},
		{"  https://example.com  ", true},
		{"HTTPS://EXAMPLE.COM", true},
	}
	for _, c := range cases {
		if got := isSafeUrl(c.url); got != c.want {
			t.Errorf("isSafeUrl(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestIsSafeFetchUrl(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://example.com/path", true},
		{"http://8.8.8.8/api", true},
		{"http://localhost:3000/api", false},
		{"http://127.0.0.1:8080", false},
		{"http://169.254.169.254/latest/meta-data", false},
		{"file:///etc/passwd", false},
		{"ftp://example.com", false},
		{"javascript:alert(1)", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isSafeFetchUrl(c.url); got != c.want {
			t.Errorf("isSafeFetchUrl(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestIsForbiddenPluginHeader(t *testing.T) {
	forbidden := []string{
		"host", "connection", "content-length", "transfer-encoding",
		"cookie", "authorization", "proxy-authorization",
		"x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
		"x-real-ip",
		"sec-fetch-mode", "sec-fetch-site", "sec-fetch-user", "sec-fetch-dest",
		"sec-websocket-key", "sec-websocket-version",
		"proxy-connection",
	}
	for _, h := range forbidden {
		if !isForbiddenPluginHeader(h) {
			t.Errorf("isForbiddenPluginHeader(%q) should be true", h)
		}
	}
	allowed := []string{
		"accept", "content-type", "user-agent", "accept-language",
		"x-custom-header", "authorization-bearer",
	}
	for _, h := range allowed {
		if isForbiddenPluginHeader(h) {
			t.Errorf("isForbiddenPluginHeader(%q) should be false", h)
		}
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
