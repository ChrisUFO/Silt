/**
 * Roving-tabindex traversal: find the next enabled index from `from`,
 * stepping by `delta` (+1 forward / −1 backward) and wrapping. Returns
 * `from` unchanged when every entry is disabled — callers must treat that
 * as "no enabled target" so the toolbar stays put instead of looping.
 */
export function nearestEnabledIndex(
  disabled: boolean[],
  from: number,
  delta: number
): number {
  const count = disabled.length
  if (count === 0) return from
  let idx = from
  for (let k = 0; k < count; k++) {
    idx = (idx + delta + count) % count
    if (!disabled[idx]) return idx
  }
  return from
}
