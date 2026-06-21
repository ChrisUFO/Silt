// Pure state machine for the preview/pin tab model (#142). Mirrors the
// common single-click-preview / double-click-pin editor convention.
//
// Every function here is PURE: it takes the current tab list + active id and
// returns the next state without mutation. The Svelte layer (App.svelte)
// wraps these in an imperative `openPage()` that applies the result to the
// `$state` runes. Keeping the logic pure makes it exhaustively unit-testable
// (tabs.test.ts) and decouples the tab model from Svelte's reactivity.
//
// Contract:
// - Single-click in the Explorer opens a page in a transient PREVIEW tab
//   (rendered italic). The same preview slot is reused: clicking another
//   page replaces its content.
// - Double-click (or middle-click, or first edit) PROMOTES a preview to a
//   PINNED tab (dedicated, persistent).
// - If a PINNED tab for (notebook, section, page) already exists, opening
//   that page just activates it, regardless of mode.
// - Ctrl+Tab / Ctrl+Shift+Tab cycle tabs in MRU order.
// - Closing the active tab activates the MRU neighbor.
// - enable_preview_tabs: false makes every open a pin (browser-tabs mode).

let tabIdCounter = 0

/**
 * Generate a stable, unique slot id for a new tab. NOT the block UUID — this
 * identifies the tab slot, so two tabs pointing at the same page (rare, but
 * possible during a preview→pin transition) have distinct ids.
 */
export function generateTabId(): string {
  tabIdCounter += 1
  return `tab-${Date.now().toString(36)}-${tabIdCounter}`
}

/**
 * A locator for a page. Section is "" for section-less pages.
 */
export interface PageRef {
  notebook: string
  section: string
  page: string
}

/**
 * A single open tab. `id` is the stable slot id; `preview` distinguishes the
 * transient preview tab from dedicated pinned tabs.
 */
export interface TabEntry extends PageRef {
  id: string
  preview: boolean
  /** Scroll-to-block target for navigate-to-block. */
  blockTarget?: { fileDate?: string; blockId?: string }
  /** Monotonic timestamp for MRU ordering (higher = more recent). */
  lastActivatedAt: number
}

/**
 * How a caller wants a page opened.
 * - `'preview'` — open in the reusable preview tab (single-click).
 * - `'pin'` — open or promote to a dedicated pinned tab (double-click /
 *   middle-click / first-edit).
 * - `'activate-only'` — no tab change; just scroll-to-block on the current
 *   page (used by navigate-to-block when the target is already active).
 */
export type OpenPageMode = 'preview' | 'pin' | 'activate-only'

export interface OpenPageOptions {
  /** When false, every open is a pin (enable_preview_tabs: false). */
  enablePreviewTabs?: boolean
  /** Max simultaneously-open tabs; LRU-evict on overflow. Default 8. */
  maxOpenTabs?: number
  /** Optional scroll-to-block target for the opened tab. */
  blockTarget?: { fileDate?: string; blockId?: string }
}

export interface TabsState {
  tabs: TabEntry[]
  activeId: string
}

/** True if a tab points at the given page locator. */
export function tabMatches(tab: TabEntry, ref: PageRef): boolean {
  return (
    tab.notebook === ref.notebook &&
    tab.section === ref.section &&
    tab.page === ref.page
  )
}

/** Find a tab by its page locator. Returns the first match. */
export function findTab(tabs: TabEntry[], ref: PageRef): TabEntry | undefined {
  return tabs.find((t) => tabMatches(t, ref))
}

/** Return tabs sorted by lastActivatedAt descending (most-recent-first). */
export function mruOrder(tabs: TabEntry[]): TabEntry[] {
  return [...tabs].sort((a, b) => b.lastActivatedAt - a.lastActivatedAt)
}

/**
 * The core open-page state machine. Returns the next { tabs, activeId }.
 *
 * Rules (see module docstring):
 * 1. If a PINNED tab for (notebook, section, page) exists → activate it,
 *    ignore `mode`.
 * 2. If a PREVIEW tab for the same page exists → activate it.
 * 3. `mode: 'activate-only'` → no tab change (just update blockTarget on the
 *    active tab if it matches).
 * 4. `mode: 'preview'` + preview tab showing a DIFFERENT page → replace its
 *    content (reuse the slot).
 * 5. `mode: 'pin'` → open a new pinned tab, or promote the existing preview
 *    for that page in place.
 * 6. `opts.enablePreviewTabs === false` → coerce `mode: 'preview'` to
 *    `mode: 'pin'`.
 * 7. Enforce `opts.maxOpenTabs` via LRU eviction: least-recently-active
 *    PREVIEW first, then oldest PINNED.
 */
export function openPage(
  state: TabsState,
  ref: PageRef,
  mode: OpenPageMode,
  opts: OpenPageOptions = {}
): TabsState {
  const { enablePreviewTabs = true, maxOpenTabs = 8, blockTarget } = opts

  // activate-only: no tab change at all (navigate-to-block on current page).
  if (mode === 'activate-only') {
    const active = state.tabs.find((t) => t.id === state.activeId)
    if (active && tabMatches(active, ref)) {
      // Same page — just update the scroll target.
      const tabs = state.tabs.map((t) =>
        t.id === active.id
          ? { ...t, blockTarget: blockTarget ?? t.blockTarget }
          : t
      )
      return { tabs, activeId: state.activeId }
    }
    // Not the current page — fall through to a normal preview open so the
    // block navigation still works.
    mode = 'preview'
  }

  // Rule 6: enable_preview_tabs: false coerces preview → pin.
  if (mode === 'preview' && !enablePreviewTabs) {
    mode = 'pin'
  }

  // Rule 1: a PINNED tab for this page already exists → activate it.
  const pinned = state.tabs.find((t) => !t.preview && tabMatches(t, ref))
  if (pinned) {
    return activateTab(state, pinned.id, blockTarget)
  }

  // Rule 2: a PREVIEW tab for this same page exists → activate it, OR
  // promote it if the caller asked for a pin. This handles the
  // double-click-in-sidebar flow: the first click opens a preview (Rule 4),
  // the dblclick fires openPage('pin'), and this rule promotes the just-
  // opened preview to pinned.
  const previewSamePage = state.tabs.find(
    (t) => t.preview && tabMatches(t, ref)
  )
  if (previewSamePage) {
    if (mode === 'pin') {
      // Promote the preview in place: flip preview:false, then activate.
      const promoted = promotePreview(
        { tabs: state.tabs, activeId: state.activeId },
        previewSamePage.id
      )
      return activateTab(promoted, previewSamePage.id, blockTarget)
    }
    return activateTab(state, previewSamePage.id, blockTarget)
  }

  const now = Date.now()

  if (mode === 'preview') {
    // Rule 4: reuse the existing preview slot if one exists (showing a
    // different page). Replace its content.
    const existingPreview = state.tabs.find((t) => t.preview)
    if (existingPreview) {
      const tabs = state.tabs.map((t) =>
        t.id === existingPreview.id
          ? {
              ...t,
              notebook: ref.notebook,
              section: ref.section,
              page: ref.page,
              blockTarget,
              lastActivatedAt: now
            }
          : t
      )
      return { tabs, activeId: existingPreview.id }
    }
    // No preview slot yet — create one (after eviction if needed).
    return createTab(state, ref, true, now, blockTarget, maxOpenTabs)
  }

  // mode === 'pin'
  // Rule 5: if a preview tab for this page exists, promote it in place.
  // (We already checked for same-page preview above and would have returned,
  // so if we reach here with a preview match it was skipped. But since we
  // checked `previewSamePage` above and didn't return, there's no same-page
  // preview. So we create a new pinned tab.)
  return createTab(state, ref, false, now, blockTarget, maxOpenTabs)
}

/**
 * Internal: activate an existing tab (bump its lastActivatedAt, set scroll
 * target). Returns a new state with the tab activated.
 */
function activateTab(
  state: TabsState,
  id: string,
  blockTarget?: { fileDate?: string; blockId?: string }
): TabsState {
  const now = Date.now()
  const tabs = state.tabs.map((t) =>
    t.id === id
      ? {
          ...t,
          lastActivatedAt: now,
          blockTarget: blockTarget ?? t.blockTarget
        }
      : t
  )
  return { tabs, activeId: id }
}

/**
 * Internal: create a new tab (preview or pinned), evicting via LRU if the tab
 * count would exceed the cap.
 */
function createTab(
  state: TabsState,
  ref: PageRef,
  preview: boolean,
  now: number,
  blockTarget: OpenPageOptions['blockTarget'],
  maxOpenTabs: number
): TabsState {
  let tabs = [...state.tabs]

  // Evict if at capacity. LRU: least-recently-active PREVIEW first, then
  // oldest PINNED. Never evict the tab we're about to activate (it doesn't
  // exist yet, so this is always safe).
  while (tabs.length >= maxOpenTabs && tabs.length > 0) {
    const victim = pickEvictionVictim(tabs)
    if (!victim) break
    tabs = tabs.filter((t) => t.id !== victim.id)
  }

  const newTab: TabEntry = {
    id: generateTabId(),
    notebook: ref.notebook,
    section: ref.section,
    page: ref.page,
    preview,
    blockTarget,
    lastActivatedAt: now
  }
  tabs.push(newTab)
  return { tabs, activeId: newTab.id }
}

/**
 * Pick the tab to evict under the LRU policy: the least-recently-active
 * PREVIEW tab. If no preview tabs exist, the oldest (lowest lastActivatedAt)
 * PINNED tab. Returns undefined only if the list is empty.
 */
export function pickEvictionVictim(tabs: TabEntry[]): TabEntry | undefined {
  if (tabs.length === 0) return undefined
  const previews = tabs.filter((t) => t.preview)
  const pool = previews.length > 0 ? previews : tabs
  // Lowest lastActivatedAt = least recently used.
  return pool.reduce((min, t) =>
    t.lastActivatedAt < min.lastActivatedAt ? t : min
  )
}

/**
 * Close a tab by id. If the closed tab was active, activate the MRU neighbor
 * (the most-recently-active of the remaining tabs). Returns the new state.
 * If closing the last tab, activeId becomes '' (the blank notes view).
 */
export function closeTab(state: TabsState, id: string): TabsState {
  const tabs = state.tabs.filter((t) => t.id !== id)
  let activeId = state.activeId

  if (activeId === id) {
    // The active tab was closed — activate the MRU neighbor.
    if (tabs.length === 0) {
      activeId = ''
    } else {
      const mru = mruOrder(tabs)
      activeId = mru[0].id
    }
  }

  return { tabs, activeId }
}

/**
 * Promote a preview tab to pinned (flip `preview: false`). Used by the
 * edit-to-pin trigger (first keystroke in a preview tab) and by double-click
 * on a preview tab's header. No-op if the tab doesn't exist or is already
 * pinned. Returns a new state.
 */
export function promotePreview(state: TabsState, id: string): TabsState {
  const tab = state.tabs.find((t) => t.id === id)
  if (!tab || !tab.preview) return state
  const tabs = state.tabs.map((t) =>
    t.id === id ? { ...t, preview: false } : t
  )
  return { tabs, activeId: state.activeId }
}

/**
 * Cycle the active tab in MRU order. `dir: 1` = next (Ctrl+Tab),
 * `dir: -1` = previous (Ctrl+Shift+Tab). No-op if fewer than 2 tabs.
 * Returns a new state.
 */
export function cycleTab(state: TabsState, dir: 1 | -1): TabsState {
  if (state.tabs.length < 2) return state
  const ordered = mruOrder(state.tabs)
  const currentIdx = ordered.findIndex((t) => t.id === state.activeId)
  if (currentIdx === -1) return state
  const nextIdx = (currentIdx + dir + ordered.length) % ordered.length
  return activateTab(state, ordered[nextIdx].id)
}
