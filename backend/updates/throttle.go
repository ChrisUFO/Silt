package updates

import "time"

// AutoCheckInterval is the minimum elapsed time between automatic update
// checks. A manual "Check for updates" is never throttled; only the startup
// auto-check consults this. 24h matches the issue's recommendation and keeps
// desktop-client volumes well under GitHub's unauthenticated rate limit.
const AutoCheckInterval = 24 * time.Hour

// ShouldAutoCheck reports whether the startup auto-check should fire, given the
// last check timestamp and the user's auto-check preference. A zero last
// (never checked) always passes when auto-check is enabled. This is pure so the
// startup decision (frontend) and the backend share one rule.
func ShouldAutoCheck(last time.Time, autoEnabled bool) bool {
	if !autoEnabled {
		return false
	}
	if last.IsZero() {
		return true
	}
	return time.Since(last) >= AutoCheckInterval
}
