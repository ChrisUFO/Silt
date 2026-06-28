// Shared reactive state for the Calendar/Agenda unified view (#322).
//
// Calendar.svelte (main area) and CalendarSidebar.svelte (sidebar) both
// need to coordinate on:
//   - `focusDate`: the date the user picked from the mini calendar; the
//     main view scrolls / jumps its cursor to this date when it changes.
//   - `activeFilter`: the smart-list selection driving which tasks are
//     dimmed in the month/week grids or which group the agenda scrolls to.
//
// Both sides import this module and read/write its `$state` fields, so
// the propagation is instantaneous via Svelte 5 runes — no IPC, no
// window events for the read paths (write paths that the rest of the
// app needs to react to — e.g. the main view jumping on a mini-calendar
// click — also dispatch `calendar:focus-date` so non-Svelte consumers
// can listen if they ever need to).
//
// This module is the single source of truth for these two values; any
// caller that wants to mutate them MUST go through the exported setters
// so the side-effects (event dispatch, persistence) stay in one place.

/** Smart-list filter the user picked from the Calendar sidebar. */
export type CalendarFilter = 'all' | 'today' | 'upcoming' | 'overdue' | 'completed'

export interface CalendarFocusState {
  /** Picked from the mini calendar in the sidebar; YYYY-MM-DD. */
  focusDate: string
  /** Active smart-list filter; 'all' means no filter. */
  activeFilter: CalendarFilter
}

const _state: CalendarFocusState = $state({
  focusDate: '',
  activeFilter: 'all'
})

/** Read the current focus state. */
export function getFocusState(): CalendarFocusState {
  return _state
}

/** Set the focus date (from a mini-calendar click). */
export function setFocusDate(iso: string): void {
  _state.focusDate = iso
  // Non-Svelte consumers (e.g. tests, future integrations) listen on
  // `window`; Svelte 5 reactive getters already see the change.
  window.dispatchEvent(
    new CustomEvent('calendar:focus-date', { detail: { date: iso } })
  )
}

/** Clear the focus date (called when the user dismisses a pick). */
export function clearFocusDate(): void {
  _state.focusDate = ''
  window.dispatchEvent(
    new CustomEvent('calendar:focus-date', { detail: { date: '' } })
  )
}

/** Set the active smart-list filter. */
export function setActiveFilter(f: CalendarFilter): void {
  _state.activeFilter = f
}

/** Reset the filter to 'all' (the X / "All Tasks" affordance). */
export function clearActiveFilter(): void {
  _state.activeFilter = 'all'
}

/**
 * Reset to defaults. Production path used on vault switch so a stale
 * focusDate or activeFilter from the previous vault does not dim or
 * jump the cursor in the newly-opened vault. Mirrors the
 * KanbanSharedState.resetKanbanStateForTests contract but is exported
 * under a production name so callers can wire it to lifecycle events.
 */
export function resetFocusState(): void {
  _state.focusDate = ''
  _state.activeFilter = 'all'
}

/**
 * Test-only: reset all state to defaults. Not exported in the public
 * SDK surface; only consumed by Vitest specs.
 */
export function resetFocusStateForTests(): void {
  _state.focusDate = ''
  _state.activeFilter = 'all'
}
