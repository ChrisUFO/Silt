package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
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
//
// `user-agent` is intentionally NOT allowlisted (#247, F13): a plugin that
// embeds credentials in the UA would leak them across a cross-host redirect.
// stripHeadersForRedirect resets the UA explicitly (see
// TestStripHeadersForRedirect_ResetsUserAgent).
func TestRedirectSafeHeaders_AllowlistIntegrity(t *testing.T) {
	expected := map[string]bool{
		"accept":          true,
		"accept-language": true,
		"content-type":    true,
	}
	for k, v := range expected {
		if redirectSafeHeaders[k] != v {
			t.Errorf("redirectSafeHeaders[%q] = %v, want %v", k, redirectSafeHeaders[k], v)
		}
	}
	if len(redirectSafeHeaders) != len(expected) {
		t.Errorf("redirectSafeHeaders has %d entries, want exactly %d — unexpected key added or removed", len(redirectSafeHeaders), len(expected))
	}
	for _, h := range []string{"authorization", "cookie", "x-api-key", "x-forwarded-for", "user-agent"} {
		if redirectSafeHeaders[h] {
			t.Errorf("redirectSafeHeaders[%q] should be false (sensitive header)", h)
		}
	}
}

// stripHeadersForRedirect must RESET User-Agent (not just delete it) so a
// plugin that embedded credentials in the UA cannot leak them across a
// cross-host redirect (#247, F13).
func TestStripHeadersForRedirect_ResetsUserAgent(t *testing.T) {
	req := &http.Request{
		Header: http.Header{},
	}
	// Simulate a plugin that embeds a credential in the UA — the exact
	// anti-pattern F13 protects against.
	req.Header.Set("User-Agent", "my-plugin/1.0 token=abc123")
	req.Header.Set("X-Api-Key", "leak-me-please")
	req.Header.Set("Accept", "application/json")

	stripHeadersForRedirect(req)

	// UA must be reset to Go's default, NOT preserved.
	if got := req.Header.Get("User-Agent"); got != "Go-http-client/1.1" {
		t.Errorf("User-Agent = %q, want %q (reset, not preserved)", got, "Go-http-client/1.1")
	}
	// Custom auth header must be stripped.
	if got := req.Header.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key = %q, want empty (stripped)", got)
	}
	// Safe header must be preserved.
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want %q (preserved)", got, "application/json")
	}
}

// stripHeadersForRedirect must reset UA even when the request did not set
// one explicitly (the transport would inject one anyway, but setting it
// explicitly makes the intent visible and survives transport changes).
func TestStripHeadersForRedirect_SetsDefaultUserAgentWhenAbsent(t *testing.T) {
	req := &http.Request{
		Header: http.Header{},
	}
	// No User-Agent set on the incoming request.
	if _, hadUA := req.Header["User-Agent"]; hadUA {
		t.Fatal("precondition: request must not have a User-Agent")
	}

	stripHeadersForRedirect(req)

	if got := req.Header.Get("User-Agent"); got != "Go-http-client/1.1" {
		t.Errorf("User-Agent = %q, want %q (explicitly set even when absent)", got, "Go-http-client/1.1")
	}
}

// withSafeFetchLookupIP swaps the package-level resolver used by
// newSafeFetchClient's DialContext and restores it at test cleanup. Tests
// inject a stub to deterministically simulate DNS failure / empty lookups
// (#234 fail-closed contract).
func withSafeFetchLookupIP(t *testing.T, fn func(ctx context.Context, network, host string) ([]net.IP, error)) {
	t.Helper()
	safeFetchLookupMu.Lock()
	orig := safeFetchLookupIP
	safeFetchLookupIP = fn
	safeFetchLookupMu.Unlock()
	t.Cleanup(func() {
		safeFetchLookupMu.Lock()
		safeFetchLookupIP = orig
		safeFetchLookupMu.Unlock()
	})
}

// newSafeFetchClient's DialContext MUST fail-closed when LookupIP returns an
// error for a hostname — the previous code fell through to dialer.DialContext
// with the literal address, letting the OS resolver pick an IP that bypasses
// the dial-time isInternalIP check (#234 DNS-rebinding bypass).
func TestSafeFetchClient_DialerFailsClosedOnLookupError(t *testing.T) {
	withSafeFetchLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return nil, errors.New("simulated DNS outage")
	})
	client := newSafeFetchClient(1_000_000_000)
	// Issue a request whose host is NOT an IP literal. The production
	// DialContext must call the stubbed resolver, see the error, find that
	// net.ParseIP returns nil for a hostname, and return fail-closed. It must
	// NOT fall through to dialer.DialContext (the #234 bypass).
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected fail-closed error on DNS lookup failure, got nil")
	}
	if !strings.Contains(err.Error(), "DNS lookup failed") {
		t.Errorf("error = %v, want to mention 'DNS lookup failed'", err)
	}
}

// The fail-closed branch also fires when LookupIP succeeds with zero IPs
// (the other half of the `lookupErr != nil || len(ips) == 0` condition).
// Today's code falls through to the system dialer; the fix must reject.
func TestSafeFetchClient_DialerFailsClosedOnLookupEmpty(t *testing.T) {
	withSafeFetchLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return nil, nil // zero IPs, no error
	})
	client := newSafeFetchClient(1_000_000_000)
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected fail-closed error on empty DNS lookup, got nil")
	}
	if !strings.Contains(err.Error(), "DNS lookup failed") {
		t.Errorf("error = %v, want to mention 'DNS lookup failed'", err)
	}
}

// A literal IP-literal host MUST still dial successfully after isInternalIP
// re-validation, even when LookupIP itself fails. This is the legitimate
// case the #234 fix must not break: http://8.8.8.8/api should connect when
// DNS is down, but http://10.0.0.1/api must still be rejected as private.
func TestSafeFetchClient_DialerHandlesIPLiteral(t *testing.T) {
	withSafeFetchLookupIP(t, func(_ context.Context, _, _ string) ([]net.IP, error) {
		return nil, errors.New("simulated DNS outage — IP-literal path must NOT depend on this")
	})
	client := newSafeFetchClient(1_000_000_000)

	// Public IP literal: must bypass the resolver and reach the dialer. There
	// is no server on 8.8.8.8:80 in CI, so the connect fails — but the error
	// must NOT be "DNS lookup failed" (that would mean the IP-literal branch
	// was missed and the fail-closed path ran instead).
	req, _ := http.NewRequest("GET", "http://8.8.8.8/", nil)
	_, err := client.Do(req)
	if err == nil {
		// A successful connect would mean 8.8.8.8:80 is reachable; still
		// acceptable (we reached the dialer), but unusual in CI.
	} else if strings.Contains(err.Error(), "DNS lookup failed") {
		t.Errorf("public IP-literal must not hit the fail-closed path; got %v", err)
	}

	// Private IP literal: must be rejected by isInternalIP at the IP-literal
	// branch, BEFORE the dialer is invoked.
	req, _ = http.NewRequest("GET", "http://10.0.0.1/", nil)
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected isInternalIP rejection for private IP-literal")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want to mention 'blocked'", err)
	}
}
