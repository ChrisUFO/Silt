/**
 * The view cycle driven by the `cycle_view_layout` hotkey (default Alt+Tab).
 *
 * Pure function + const — pulled out of App.svelte so the cycle order is
 * testable in isolation. If `current` is not in the cycle (e.g. a plugin
 * view), `nextView` jumps to `'notes'` as the anchor.
 */
export const VIEW_CYCLE = [
  'notes',
  'tags',
  'agenda',
  'calendar',
  'kanban'
] as const
export type CycleView = (typeof VIEW_CYCLE)[number]

/**
 * Return the next view in the cycle, or `'notes'` if `current` is not in the
 * cycle. Wraps modulo `VIEW_CYCLE.length`.
 */
export function nextView(current: string): CycleView {
  const idx = VIEW_CYCLE.indexOf(current as CycleView)
  if (idx === -1) return 'notes'
  return VIEW_CYCLE[(idx + 1) % VIEW_CYCLE.length]
}
