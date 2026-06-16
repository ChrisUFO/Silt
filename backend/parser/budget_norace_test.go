//go:build !race

package parser

import "time"

// scanBudgetRegressionLimit returns the boot-scanner budget for the
// <450ms / 1,000-files regression gate. The race detector roughly doubles
// the cost of this I/O+parse workload, so a build-tagged variant scales
// the budget up under -race (see budget_race_test.go). Both paths keep the
// test running in the normal `go test -race ./...` CI gate — only the
// threshold adapts, preserving regression sensitivity in each mode.
func scanBudgetRegressionLimit() time.Duration {
	return 450 * time.Millisecond
}
