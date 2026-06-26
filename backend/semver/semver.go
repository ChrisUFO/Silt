// Package semver is Silt's single source of truth for dotted, numeric
// version comparison (the `X.Y.Z` shape used by the VERSION file, plugin
// manifests' minSiltVersion, and the app version compared against a GitHub
// release tag). It is deliberately minimal: segment-by-segment integer
// comparison with a shorter version treated as older when all shared segments
// are equal. Inputs MUST be clean numeric strings — callers strip any leading
// non-numeric prefix (e.g. a release tag's `v`) before comparing, since
// strconv.Atoi("v2") silently yields 0.
package semver

import (
	"strconv"
	"strings"
)

// LessThan reports whether version a is strictly older than version b.
// Both inputs are dotted numeric strings ("0.1.0").
func LessThan(a, b string) bool {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) && i < len(bp); i++ {
		ai, _ := strconv.Atoi(ap[i])
		bi, _ := strconv.Atoi(bp[i])
		if ai < bi {
			return true
		}
		if ai > bi {
			return false
		}
	}
	return len(ap) < len(bp) // shorter = older if all segments equal so far
}
