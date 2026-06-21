import { settings } from '../settings/store.svelte'

export type ViewMode = 'edit' | 'source'

// Soft cap on the session-sticky viewMode cache (#199). Decoupled from
// max_open_tabs because the viewMode cache is a different concern (a separate
// per-page preference memory), and we want it comfortably above the tab
// ceiling so a tab-switch flurry never thrashes it. 50 × ~100 bytes ≈ 5 KB.
const MAX_VIEW_MODES = 50

function pageKey(notebook: string, section: string, page: string): string {
  return `${notebook}/${section}/${page}`
}

// Module-scoped reactive record: page key → view mode. Uses a plain object
// (not Map) so Svelte 5's $state deeply tracks property reads/writes.
const viewModes = $state<Record<string, ViewMode>>({})

// Non-reactive sibling bookkeeping for LRU access-order tracking. Reactivity
// is unnecessary for LRU bookkeeping and would only add re-render overhead;
// mirror the TabEntry.lastActivatedAt pattern from lib/tabs.ts.
const lastUsed: Record<string, number> = {}

// Insertion-order tie-breaker: keys entered earlier sort first. Combined with
// the lastUsed timestamp this gives a fully-deterministic eviction order even
// when many setViewMode calls land in the same millisecond (real wall-clock
// Date.now() collisions are common in a tab-switch flurry).
let insertionCounter = 0
const insertionSeq: Record<string, number> = {}

// Internal: drop the least-recently-used key from both the reactive cache and
// the access-order bookkeeping. Mirrors tabs.ts pickEvictionVictim's reduce,
// with a stable insertion-order tie-breaker so same-millisecond timestamps
// resolve deterministically across JS engines (for...in ordering is not
// specified by ECMA-262).
function evictLRU(): void {
  let victim: string | undefined
  let oldest = Infinity
  let oldestSeq = Infinity
  for (const k in lastUsed) {
    const ts = lastUsed[k]
    const seq = insertionSeq[k] ?? 0
    if (ts < oldest || (ts === oldest && seq < oldestSeq)) {
      oldest = ts
      oldestSeq = seq
      victim = k
    }
  }
  if (victim !== undefined) {
    delete lastUsed[victim]
    delete viewModes[victim]
    delete insertionSeq[victim]
  }
}

export function getViewMode(
  notebook: string,
  section: string,
  page: string
): ViewMode {
  const key = pageKey(notebook, section, page)
  if (viewModes[key]) {
    // Sticky hit — bump access-order so the LRU keeps this page warm.
    lastUsed[key] = Date.now()
    return viewModes[key]
  }
  // Fall back to the per-vault default_view_mode config (#171).
  const configured = settings.config?.editor?.default_view_mode
  return configured === 'source' ? 'source' : 'edit'
}

export function setViewMode(
  notebook: string,
  section: string,
  page: string,
  mode: ViewMode
): void {
  const key = pageKey(notebook, section, page)
  // Capture newness before mutating either record — both writes below would
  // otherwise mask the check. The token lets the LRU tie-breaker resolve
  // same-millisecond timestamps deterministically across JS engines.
  const isNew = !(key in viewModes)
  viewModes[key] = mode
  lastUsed[key] = Date.now()
  if (isNew) insertionSeq[key] = insertionCounter++
  if (Object.keys(viewModes).length > MAX_VIEW_MODES) evictLRU()
}

export function toggleViewMode(
  notebook: string,
  section: string,
  page: string
): void {
  const current = getViewMode(notebook, section, page)
  setViewMode(notebook, section, page, current === 'edit' ? 'source' : 'edit')
}

// Test seam: the LRU bookkeeping is module-scoped state. Tests reset the
// reactive cache, the access-order map, and the insertion-sequence counter.
//
// @internal Not part of the public API — exported only so the viewMode test
// file can reset between cases. Do not import from application code.
export function __resetViewModesForTests(): void {
  for (const k of Object.keys(viewModes)) delete viewModes[k]
  for (const k of Object.keys(lastUsed)) delete lastUsed[k]
  for (const k of Object.keys(insertionSeq)) delete insertionSeq[k]
  insertionCounter = 0
}
