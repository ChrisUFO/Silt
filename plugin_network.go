package main

import (
	"fmt"
	"io"
	"net/http"
	"silt/backend/plugins"
	"strings"
	"time"

	"github.com/google/uuid"
)

// =========================================================================
// Network / fetch (#115)
// =========================================================================

// maxPluginFetchBytes bounds a single plugin fetch response body (defense-
// in-depth memory guard, mirroring maxPluginQueryRows). Reduced from 10 MB to
// 2 MB in #153: the per-plugin rate limiter is now the primary throttle, and
// 2 MB is generous for real plugin API responses.
const maxPluginFetchBytes = 2 * 1024 * 1024 // 2 MB

// maxPluginFetchRequestBytes bounds the request body a plugin can send through
// the fetch proxy, mirroring the response-side cap. Without this, a plugin can
// pass a multi-hundred-megabyte string and force the host to allocate it.
const maxPluginFetchRequestBytes = 10 * 1024 * 1024 // 10 MB

// maxPluginFetchRedirects caps redirect hops to prevent an infinite redirect loop.
const maxPluginFetchRedirects = 5

// defaultPluginFetchTimeout caps how long a single fetch may take.
const defaultPluginFetchTimeout = 30 * time.Second

// PluginFetchResult is the envelope returned by PluginFetch.
type PluginFetchResult struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"` // raw body (may be truncated to maxPluginFetchBytes)
	Ok        bool              `json:"ok"`
	Truncated bool              `json:"truncated"` // true when body exceeded maxPluginFetchBytes
}

// PluginFetchInput is the request envelope for PluginFetch.
type PluginFetchInput struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`            // defaults to GET
	Headers map[string]string `json:"headers,omitempty"` // arbitrary (auth) — audit-logged
	Body    string            `json:"body,omitempty"`
	Timeout int               `json:"timeout,omitempty"` // milliseconds; capped at 30s
}

// PluginFetch performs an HTTP request through the Go backend (CORS-free),
// with timeout / size / redirect caps. Gated by the network capability.
// The host + status are appended to the in-memory audit log (never the body).
// Per-plugin rate-limited (#153): a network-granted plugin's RPS is capped.
func (a *App) PluginFetch(pluginID, sessionToken string, input PluginFetchInput) (PluginFetchResult, error) {
	a.vaultMu.RLock()
	defer a.vaultMu.RUnlock()
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return PluginFetchResult{}, err
	}
	if err := a.requireGrant(pluginID, plugins.CapNetwork); err != nil {
		return PluginFetchResult{}, err
	}
	if a.rateLimiter != nil && !a.rateLimiter.allow(a.vaultPath, pluginID) {
		rps, burst := resolvePluginRatelimit(a.vaultPath, pluginID)
		return PluginFetchResult{}, fmt.Errorf("plugin %q fetch rate limit exceeded (max %.1f rps, burst %d); retry after a short delay", pluginID, rps, burst)
	}
	if input.URL == "" {
		return PluginFetchResult{}, fmt.Errorf("url is required")
	}
	if !isSafeFetchUrl(input.URL) {
		return PluginFetchResult{}, fmt.Errorf("url scheme is not allowed (only http/https)")
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
	default:
		return PluginFetchResult{}, fmt.Errorf("HTTP method %q is not allowed (recognized: GET, POST, PUT, PATCH, DELETE, HEAD)", method)
	}
	timeout := defaultPluginFetchTimeout
	if input.Timeout > 0 {
		requested := time.Duration(input.Timeout) * time.Millisecond
		if requested < timeout {
			timeout = requested
		}
	}

	client := newSafeFetchClient(timeout)

	var reqBody io.Reader
	if input.Body != "" {
		if int64(len(input.Body)) > maxPluginFetchRequestBytes {
			return PluginFetchResult{}, fmt.Errorf("request body exceeds %d-byte cap", maxPluginFetchRequestBytes)
		}
		reqBody = strings.NewReader(input.Body)
	}
	req, err := http.NewRequest(method, input.URL, reqBody)
	if err != nil {
		return PluginFetchResult{}, fmt.Errorf("build request: %w", err)
	}
	for k, v := range input.Headers {
		lk := strings.ToLower(k)
		if isForbiddenPluginHeader(lk) {
			return PluginFetchResult{}, fmt.Errorf("header %q is forbidden (controlled by the transport layer)", k)
		}
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Still audit the attempt so the user sees the plugin tried.
		a.auditNetwork(pluginID, method, input.URL, 0)
		return PluginFetchResult{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPluginFetchBytes+1))
	if err != nil {
		a.auditNetwork(pluginID, method, input.URL, resp.StatusCode)
		return PluginFetchResult{}, fmt.Errorf("read body: %w", err)
	}
	truncated := false
	if int64(len(body)) > maxPluginFetchBytes {
		body = body[:maxPluginFetchBytes]
		truncated = true
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}

	a.auditNetwork(pluginID, method, input.URL, resp.StatusCode)

	return PluginFetchResult{
		Status:    resp.StatusCode,
		Headers:   headers,
		Body:      string(body),
		Ok:        resp.StatusCode >= 200 && resp.StatusCode < 300,
		Truncated: truncated,
	}, nil
}

// newUUID mints a UUIDv4 string. Wraps the existing uuid import so the v2
// bindings stay decoupled from the google/uuid API shape.
func newUUID() string {
	return uuid.NewString()
}
