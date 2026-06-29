/**
 * The view cycle driven by the `cycle_view_layout` hotkey (default Ctrl+Alt+V).
 *
 * Pure function + const — pulled out of App.svelte so the cycle order is
 * testable in isolation. If `current` is not in the cycle (e.g. a plugin
 * view), `nextView` jumps to `'notes'` as the anchor.
 *
 * Note: `'agenda'` is intentionally NOT in the cycle after #322 merged the
 * Agenda view into Calendar as a third mode. The activity bar no longer
 * exposes Agenda as an entry, and routing `activeView === 'agenda'` would
 * send the user to the (now-defunct) silt-agenda plugin's standalone view
 * rather than the unified Calendar with its Agenda mode. Pressing
 * Ctrl+Alt+V from Tags jumps directly to Calendar.
 */
export const VIEW_CYCLE = ['notes', 'tags', 'calendar', 'kanban'] as const
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
