package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// isSafeUrl reports whether url uses an allowed scheme (http/https/mailto).
// Used by PluginOpenUrl (browser-open path).
func isSafeUrl(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return false
	}
	lower := strings.ToLower(u)
	for _, scheme := range []string{"https://", "http://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// isSafeFetchUrl is the stricter check for PluginFetch: only http/https (no
// mailto), and the resolved host must NOT be a loopback, link-local, or
// private IP address (SSRF defense, #115).
func isSafeFetchUrl(rawURL string) bool {
	u := strings.TrimSpace(rawURL)
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return false
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	return blockInternalHost(parsed.Host) == nil
}

// blockInternalHost returns an error if host resolves to (or is literally) a
// loopback, link-local, private, or multicast address — the standard SSRF
// defense so a granted plugin cannot reach internal services or cloud metadata.
func blockInternalHost(host string) error {
	// Strip port.
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return fmt.Errorf("empty host")
	}
	// Resolve and check every returned IP.
	ips, err := net.LookupIP(hostname)
	if err != nil {
		// If we can't resolve, err on the side of caution for literal IPs.
		ips = []net.IP{net.ParseIP(hostname)}
		if ips[0] == nil {
			return fmt.Errorf("cannot resolve host %q", hostname)
		}
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("host %s resolves to a blocked address %s", hostname, ip)
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("host %s resolves to a private address %s", hostname, ip)
		}
	}
	return nil
}

// isPrivateIP reports whether ip is in an RFC-1918 private range (10/8,
// 172.16/12, 192.168/16). net.IP.IsPrivate covers this on Go 1.17+, but
// cloud metadata (169.254.169.254) is link-local and caught by the
// IsLinkLocalUnicast check above.
func isPrivateIP(ip net.IP) bool {
	if ip.IsPrivate() {
		return true
	}
	// Explicitly catch 169.254.x.x (cloud metadata endpoints) even if Go's
	// IsPrivate doesn't (it's link-local, caught above, but belt + suspenders).
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 169 && v4[1] == 254 {
			return true
		}
	}
	return false
}

// safeFetchLookupIP is the resolver used by newSafeFetchClient's DialContext.
// Package-level so tests can swap in a stub that simulates DNS failure and
// pin the fail-closed contract (#234) without exercising the real resolver.
var safeFetchLookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, network, host)
}

// newSafeFetchClient returns an *http.Client with the SSRF-defended transport
// used by every privileged plugin HTTP call (#115 + #101 review). It:
//
//  1. Caps the per-request lifetime with timeout.
//  2. Re-validates every redirect destination against isSafeFetchUrl +
//     blockInternalHost so a 302 to an internal host is rejected even if the
//     initial URL was approved.
//  3. Pins the resolved IP at dial time via a custom DialContext. A name
//     that resolves to 1.2.3.4 at validation and 169.254.169.254 at connect
//     (DNS rebinding) is rejected because the dialer re-runs blockInternalHost
//     against the IPs it actually plans to connect to.
//
// Tests can override the DialContext to swap the resolver (see
// app_plugins_v2_test.go) and exercise the rebinding defense deterministically.
func newSafeFetchClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				return nil, splitErr
			}
			ips, lookupErr := safeFetchLookupIP(ctx, "ip", host)
			if lookupErr != nil || len(ips) == 0 {
				// IP-literal hosts bypass DNS entirely — validate + dial them
				// directly so a lookup failure for "1.2.3.4" doesn't wrongly
				// block a legitimate connect.
				if parsed := net.ParseIP(host); parsed != nil {
					if isInternalIP(parsed) {
						return nil, fmt.Errorf("blocked: dial to %s is a blocked address %s", host, parsed)
					}
					return dialer.DialContext(ctx, network, net.JoinHostPort(parsed.String(), port))
				}
				// Hostname that won't resolve: fail closed. Falling through to
				// the system dialer would let the OS resolver pick an IP that
				// bypasses the dial-time isInternalIP check (#234 DNS-rebinding
				// bypass).
				return nil, fmt.Errorf("blocked: DNS lookup failed for %q: %w", host, lookupErr)
			}
			// Re-validate every resolved IP at dial time so a DNS rebind
			// between isSafeFetchUrl and the actual connect is rejected.
			for _, ip := range ips {
				if isInternalIP(ip) {
					return nil, fmt.Errorf("blocked: dial to %s resolves to a blocked address %s", host, ip)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxPluginFetchRedirects {
				return fmt.Errorf("too many redirects (max %d)", maxPluginFetchRedirects)
			}
			// Re-validate every redirect destination for scheme + SSRF (#115).
			if !isSafeFetchUrl(req.URL.String()) {
				return fmt.Errorf("redirect to blocked URL: %s", req.URL.String())
			}
			if err := blockInternalHost(req.URL.Host); err != nil {
				return fmt.Errorf("redirect to internal host: %w", err)
			}
			// Strict header allowlist (#160): strip everything except safe,
			// non-sensitive headers so custom auth (X-Api-Key, etc.) cannot
			// leak ACROSS hosts. Go's net/http already drops Authorization +
			// Cookie on cross-host redirects, but custom auth headers are not
			// covered. Same-host redirects keep custom headers so legitimate
			// same-origin API redirects (e.g. a version path migration) that
			// depend on X-Api-Key are not broken; cross-host is where the leak
			// risk lives.
			prev := via[len(via)-1]
			if !strings.EqualFold(req.URL.Host, prev.URL.Host) {
				stripHeadersForRedirect(req)
			}
			return nil
		},
	}
}

// isInternalIP is the dial-time analogue of blockInternalHost: it rejects
// loopback, link-local, multicast, unspecified, and private IPs. Sharing
// the predicate with blockInternalHost keeps the dial and the URL check in
// lock-step so the rebinding defense and the URL-level check can never
// drift (#115 hardening).
func isInternalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return isPrivateIP(ip)
}

// isForbiddenPluginHeader reports whether a (lower-cased) header name must
// NOT be settable by a plugin. These headers are controlled by the transport
// layer or carry security-sensitive semantics:
//   - Host / Connection / Content-Length / Transfer-Encoding: request
//     smuggling vectors or would subvert the SSRF dial-time IP check.
//   - Proxy-* / Sec-*: hop-by-hop or browser-fetch metadata that a plugin
//     must not forge.
//   - Cookie / Authorization: would let a plugin exfiltrate or reuse host
//     credentials.
func isForbiddenPluginHeader(lowerKey string) bool {
	switch lowerKey {
	case "host", "connection", "content-length", "transfer-encoding",
		"cookie", "authorization", "proxy-authorization",
		"x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
		"x-real-ip",
		"sec-fetch-mode", "sec-fetch-site", "sec-fetch-user", "sec-fetch-dest",
		"sec-websocket-key", "sec-websocket-version":
		return true
	}
	if strings.HasPrefix(lowerKey, "proxy-") || strings.HasPrefix(lowerKey, "sec-") {
		return true
	}
	return false
}

// redirectSafeHeaders is the strict allowlist of request headers that survive
// a cross-host redirect (#160). Go's net/http strips only Authorization and
// Cookie automatically; custom auth headers (X-Api-Key, etc.) that are not in
// isForbiddenPluginHeader would otherwise leak to the redirect target. This
// allowlist is applied in CheckRedirect so ONLY safe, non-sensitive headers
// are forwarded.
var redirectSafeHeaders = map[string]bool{
	"accept":          true,
	"accept-language": true,
	"content-type":    true,
	"user-agent":      true,
}

// stripHeadersForRedirect removes every header from req that is NOT in the
// redirectSafeHeaders allowlist (#160). Called from CheckRedirect on every
// redirect hop so custom auth headers cannot leak to the redirect target.
func stripHeadersForRedirect(req *http.Request) {
	for k := range req.Header {
		if !redirectSafeHeaders[strings.ToLower(k)] {
			req.Header.Del(k)
		}
	}
}
