// Shared reactive state for the Kanban board + sidebar (#323).
//
// Kanban.svelte (main area) and KanbanSidebar.svelte (sidebar) both
// read and write the SAME scope + filters, so toggling a filter in the
// sidebar updates the FilterBar and re-queries the main view instantly,
// and vice versa. The Kanban settings schema is unchanged — the
// debounced persistence path still lives in Kanban.svelte (debounce is
// a UI concern, not a state one), but the value it persists now comes
// from this module's setters.
//
// The scope-user-override invariant (#124) is preserved: any setter
// triggered by a USER action sets `scopeUserOverride = true` so the
// navigation-tracking $effect stops auto-narrowing the board on every
// navigation. A navigation-only setter is exposed for the auto-narrow
// path so it can mutate scope without flipping the override.

import type { Scope, KanbanFilters } from './types'

const DEFAULT_FILTERS: KanbanFilters = {
  owners: [],
  priorities: [],
  dueDate: '',
  tags: []
}

export interface KanbanSharedState {
  scope: Scope
  filters: KanbanFilters
  /** True when the user has manually picked a scope (#124). */
  scopeUserOverride: boolean
}

const _state: KanbanSharedState = $state({
  scope: 'vault',
  filters: { ...DEFAULT_FILTERS },
  scopeUserOverride: false
})

/** Read the current shared state (used by Kanban.svelte and the sidebar). */
export function getKanbanState(): KanbanSharedState {
  return _state
}

/**
 * User-initiated scope change (clicked a scope button in either the
 * segmented control OR the sidebar radio). Flips scopeUserOverride so
 * subsequent navigation stops auto-narrowing the board.
 */
export function setScope(s: Scope): void {
  _state.scope = s
  _state.scopeUserOverride = true
}

/**
 * Navigation-driven scope auto-narrow (#124). Mutates scope WITHOUT
 * flipping scopeUserOverride so the user can still click "Follow" to
 * re-engage the auto-narrow, and so the override flag is preserved
 * through the user's prior manual picks.
 */
export function narrowScopeTo(s: Scope): void {
  if (_state.scopeUserOverride) return
  _state.scope = s
}

/** User reset (clicked "Follow" / reset). Re-enables navigation auto-narrow. */
export function clearScopeOverride(): void {
  _state.scopeUserOverride = false
}

/** Full filters replacement (the FilterBar / sidebar quick-toggle writes). */
export function setFilters(f: KanbanFilters): void {
  _state.filters = f
}

/** Clear all active filters. */
export function clearFilters(): void {
  _state.filters = { ...DEFAULT_FILTERS }
}

/** Initialize the shared state from a freshly-loaded config snapshot. */
export function initFromConfig(
  scope: Scope,
  filters: KanbanFilters,
  scopeUserOverride: boolean
): void {
  _state.scope = scope
  _state.filters = { ...filters }
  _state.scopeUserOverride = scopeUserOverride
}

/**
 * Apply a saved board (#323). Sets scope + filters and flips the
 * override flag (clicking a saved board is a user intent).
 */
export function applySavedBoard(b: {
  scope: Scope
  filters: KanbanFilters
}): void {
  _state.scope = b.scope
  _state.filters = { ...b.filters }
  _state.scopeUserOverride = true
}

/**
 * Reset the shared Kanban state to defaults. Called by the loader's
 * vault:closing handler so scope/filters/override from the previous vault
 * don't linger into the next (#326 item 1) — the settings store is reset by
 * loadPlugins, but these module-globals are not. Tests reuse the same entry
 * point (no separate test-only reset — one source of truth).
 */
export function resetKanbanState(): void {
  _state.scope = 'vault'
  _state.filters = { ...DEFAULT_FILTERS }
  _state.scopeUserOverride = false
}
