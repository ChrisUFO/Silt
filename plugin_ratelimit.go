package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"silt/backend/plugins"
	"sync"
	"time"
)

const defaultPluginFetchRPS = 1.0

// defaultPluginFetchBurst is the default bucket capacity (10 requests instantly,
// then throttled to rps).
const defaultPluginFetchBurst = 10

// maxPluginFetchRPS is the hard cap on a manifest-declared rps override. A
// plugin cannot declare more than this; the host rejects it at install.
const maxPluginFetchRPS = 10.0

// maxPluginFetchBurst is the hard cap on a manifest-declared burst override.
// Mirrors the install-time validation in plugins.Validate.
const maxPluginFetchBurst = 100

// tokenBucket is a standard token-bucket rate limiter. tokens refill at rps
// up to burst capacity. allow() consumes one token if available.
type tokenBucket struct {
	tokens float64
	last   time.Time
	rps    float64
	burst  int
}

// allow reports whether one token is available, consuming it if so.
func (tb *tokenBucket) allow(now time.Time) bool {
	elapsed := now.Sub(tb.last).Seconds()
	tb.tokens += elapsed * tb.rps
	if tb.tokens > float64(tb.burst) {
		tb.tokens = float64(tb.burst)
	}
	tb.last = now
	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

// pluginRateLimiter is a per-plugin token-bucket map guarded by a mutex.
// A network-granted plugin's fetch calls consult this before hitting the
// network (#153). Buckets are evicted on uninstall so uninstalled plugins
// don't leak entries.
type pluginRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

func newPluginRateLimiter() *pluginRateLimiter {
	return &pluginRateLimiter{buckets: make(map[string]*tokenBucket)}
}

// allow checks (and consumes) one token for pluginID. Returns false if the
// rate limit is exceeded. vaultPath is used only to resolve a manifest-declared
// ratelimit override (#153) the first time a plugin's bucket is created; the
// bucket is then cached, so the disk read happens at most once per plugin per
// session (and is evicted on uninstall).
func (rl *pluginRateLimiter) allow(vaultPath, pluginID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[pluginID]
	if !ok {
		rps, burst := resolvePluginRatelimit(vaultPath, pluginID)
		now := time.Now()
		b = &tokenBucket{
			tokens: float64(burst),
			last:   now,
			rps:    rps,
			burst:  burst,
		}
		rl.buckets[pluginID] = b
		return b.allow(now)
	}
	return b.allow(time.Now())
}

// resolvePluginRatelimit reads the installed plugin's manifest ratelimit
// override (#153) and returns the effective (rps, burst). Returns the host
// defaults when vaultPath is empty, the plugin has no manifest on disk, or the
// declared values are out of range. This is defense in depth — Install already
// validates the override — so a hand-edited or corrupted plugin.json falls back
// to the safe default instead of granting an outsized quota.
func resolvePluginRatelimit(vaultPath, pluginID string) (rps float64, burst int) {
	rps = defaultPluginFetchRPS
	burst = defaultPluginFetchBurst
	if vaultPath == "" || !plugins.IsValidID(pluginID) {
		return
	}
	manifestPath := filepath.Join(vaultPath, ".system", "plugins", pluginID, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return
	}
	var raw struct {
		Ratelimit *struct {
			RPS   float64 `json:"rps"`
			Burst int     `json:"burst"`
		} `json:"ratelimit"`
	}
	if json.Unmarshal(data, &raw) != nil || raw.Ratelimit == nil {
		return
	}
	if raw.Ratelimit.RPS > 0 && raw.Ratelimit.RPS <= maxPluginFetchRPS {
		rps = raw.Ratelimit.RPS
	}
	if raw.Ratelimit.Burst > 0 && raw.Ratelimit.Burst <= maxPluginFetchBurst {
		burst = raw.Ratelimit.Burst
	}
	return
}

// evict removes the bucket for pluginID (called on uninstall/disable).
func (rl *pluginRateLimiter) evict(pluginID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, pluginID)
}
