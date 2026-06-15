// Dev-only UI frame-budget probe (#21). Active only when ?perf=1 is in the
// URL; otherwise measureFrameBudget is a zero-cost pass-through (the `if`
// guard returns before any performance.* call). Designed to catch
// interaction-driven jank (>16ms per frame) during manual testing.
//
// Usage:
//   measureFrameBudget('kanban-drop', () => { /* DOM update */ })
//
// Console output:
//   [perf] ✓ kanban-drop: 3.2ms (budget 16ms)
//   [perf] ⚠️ theme-inject: 24.7ms (budget 16ms)

const PERF_ENABLED =
  typeof window !== 'undefined' &&
  new URLSearchParams(window.location.search).get('perf') === '1'

/**
 * Wraps a synchronous callback in performance.mark/measure and logs the
 * elapsed time once the browser paints (via requestAnimationFrame). The
 * rAF callback reports the total time including layout/paint, which is
 * the true "did we drop a frame?" metric.
 *
 * When ?perf=1 is absent, this is a direct call to `fn` with zero overhead.
 */
export function measureFrameBudget<T>(label: string, fn: () => T): T {
  if (!PERF_ENABLED) return fn()

  const startMark = `perf:${label}:start`
  const endMark = `perf:${label}:end`
  performance.mark(startMark)

  const result = fn()

  performance.mark(endMark)
  performance.measure(`perf:${label}`, startMark, endMark)

  // Report after paint so the timestamp includes the browser's commit cost.
  requestAnimationFrame(() => {
    const entries = performance.getEntriesByName(`perf:${label}`, 'measure')
    const measure = entries[entries.length - 1]
    if (measure) {
      const ms = measure.duration.toFixed(1)
      const flag = measure.duration > 16 ? '⚠️' : '✓'
      console.log(`[perf] ${flag} ${label}: ${ms}ms (budget 16ms)`)
    }
  })

  return result
}
