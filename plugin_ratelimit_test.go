package main

import (
	"testing"
	"time"
)

// Direct unit tests for the token-bucket refill and burst-cap logic. Existing
// tests (TestTokenBucket_AllowsBurstThenThrottles) cover the happy path; this
// covers the burst-cap and zero-token edge cases.
func TestTokenBucket_BurstCap(t *testing.T) {
	now := time.Now()
	tb := tokenBucket{
		tokens: 0,
		last:   now,
		rps:    10.0,
		burst:  3,
	}

	// After 10 seconds, 100 tokens would refill but burst caps at 3.
	later := now.Add(10 * time.Second)
	for i := 0; i < 3; i++ {
		if !tb.allow(later) {
			t.Errorf("request %d should be allowed (burst cap=3)", i)
		}
	}
	if tb.allow(later) {
		t.Error("4th should be denied (burst cap hit)")
	}
}

func TestTokenBucket_RefillRate(t *testing.T) {
	now := time.Now()
	tb := tokenBucket{
		tokens: 0,
		last:   now,
		rps:    2.0, // 2 tokens per second
		burst:  5,
	}

	// After 1 second, 2 tokens should refill.
	later := now.Add(1 * time.Second)
	if !tb.allow(later) {
		t.Error("should allow after 1s refill (2 tokens)")
	}
	if !tb.allow(later) {
		t.Error("should allow 2nd token after 1s refill")
	}
	if tb.allow(later) {
		t.Error("3rd should be denied (only 2 refilled)")
	}
}

// Tests that two plugins get independent buckets — exhaustion of one does not
// affect the other.
func TestPluginRateLimiter_SeparatePlugins(t *testing.T) {
	rl := newPluginRateLimiter()

	// Exhaust plugin-a's bucket.
	for i := 0; i < defaultPluginFetchBurst; i++ {
		rl.allow("", "plugin-a")
	}
	// plugin-b should still have its own bucket.
	if !rl.allow("", "plugin-b") {
		t.Error("plugin-b should have its own bucket (unaffected by plugin-a)")
	}
}

// Tests that resolvePluginRatelimit returns safe defaults for edge cases.
func TestResolvePluginRatelimit_EdgeCases(t *testing.T) {
	// Empty vaultPath returns defaults.
	rps, burst := resolvePluginRatelimit("", "any-plugin")
	if rps != defaultPluginFetchRPS || burst != defaultPluginFetchBurst {
		t.Errorf("empty vaultPath should return defaults, got rps=%v burst=%v", rps, burst)
	}

	// Invalid plugin ID returns defaults.
	rps, burst = resolvePluginRatelimit("/tmp", "../../../etc/passwd")
	if rps != defaultPluginFetchRPS || burst != defaultPluginFetchBurst {
		t.Errorf("invalid pluginID should return defaults, got rps=%v burst=%v", rps, burst)
	}

	// Non-existent manifest returns defaults.
	dir := t.TempDir()
	rps, burst = resolvePluginRatelimit(dir, "nonexistent-plugin")
	if rps != defaultPluginFetchRPS || burst != defaultPluginFetchBurst {
		t.Errorf("missing manifest should return defaults, got rps=%v burst=%v", rps, burst)
	}
}
