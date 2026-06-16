//go:build race

package parser

import "time"

// scanBudgetRegressionLimit returns the boot-scanner budget when the race
// detector is enabled. The detector adds ~2x overhead to the file-scan +
// parse workload (non-race baseline ~280ms → ~550ms under race), so the
// gate is scaled to 900ms here to keep the regression test green under the
// normal `go test -race ./...` CI invocation while still catching a
// real regression (a 2x slowdown from the ~280ms baseline lands at ~560ms
// non-race / ~1.1s race — well above this threshold).
func scanBudgetRegressionLimit() time.Duration {
	return 900 * time.Millisecond
}
