import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  openPage,
  closeTab,
  promotePreview,
  cycleTab,
  mruOrder,
  pickEvictionVictim,
  findTab,
  tabMatches,
  generateTabId,
  type TabEntry,
  type PageRef,
  type TabsState
} from './tabs'

// Helpers -------------------------------------------------------------

function mkTab(
  ref: PageRef,
  opts: { preview?: boolean; lastActivatedAt?: number; id?: string } = {}
): TabEntry {
  return {
    id: opts.id ?? generateTabId(),
    notebook: ref.notebook,
    section: ref.section,
    page: ref.page,
    preview: opts.preview ?? false,
    lastActivatedAt: opts.lastActivatedAt ?? Date.now()
  }
}

const PAGE_A: PageRef = { notebook: 'Work', section: 'Projects', page: 'Site' }
const PAGE_B: PageRef = { notebook: 'Work', section: 'Journal', page: 'Daily' }
const PAGE_C: PageRef = { notebook: 'Personal', section: '', page: 'Top' }

function state(tabs: TabEntry[], activeId?: string): TabsState {
  return { tabs, activeId: activeId ?? tabs[0]?.id ?? '' }
}

// Reset the tab id counter between tests so ids are deterministic-ish.
beforeEach(() => {
  vi.clearAllMocks()
})

// --- openPage rules ---------------------------------------------------

describe('openPage — VS Code preview/pin state machine', () => {
  describe('Rule 1: pinned tab exists → activate, ignore mode', () => {
    it('activates the existing pinned tab when mode=preview', () => {
      const tab = mkTab(PAGE_A, { preview: false, lastActivatedAt: 100 })
      const tabB = mkTab(PAGE_B, { preview: false, lastActivatedAt: 200 })
      const st = state([tab, tabB], tabB.id)
      const result = openPage(st, PAGE_A, 'preview')
      expect(result.activeId).toBe(tab.id)
      expect(result.tabs).toHaveLength(2) // no new tab created
      // lastActivatedAt bumped
      const activated = result.tabs.find((t) => t.id === tab.id)!
      expect(activated.lastActivatedAt).toBeGreaterThan(100)
    })

    it('activates the existing pinned tab when mode=pin', () => {
      const tab = mkTab(PAGE_A, { preview: false, lastActivatedAt: 100 })
      const st = state([tab], tab.id)
      const result = openPage(st, PAGE_A, 'pin')
      expect(result.activeId).toBe(tab.id)
      expect(result.tabs).toHaveLength(1)
    })
  })

  describe('Rule 2: preview tab for same page', () => {
    it('activates the existing preview tab for the same page (mode=preview)', () => {
      const preview = mkTab(PAGE_A, { preview: true, lastActivatedAt: 100 })
      const st = state([preview], preview.id)
      const result = openPage(st, PAGE_A, 'preview')
      expect(result.activeId).toBe(preview.id)
      expect(result.tabs).toHaveLength(1)
      // Page content unchanged (it's the same page)
      expect(result.tabs[0].page).toBe('Site')
      // Still a preview (not promoted)
      expect(result.tabs[0].preview).toBe(true)
    })

    it('PROMOTES the preview to pinned when mode=pin (dblclick flow)', () => {
      // Simulates: single-click opens preview, dblclick fires openPage('pin').
      const preview = mkTab(PAGE_A, { preview: true, lastActivatedAt: 100 })
      const st = state([preview], preview.id)
      const result = openPage(st, PAGE_A, 'pin')
      expect(result.activeId).toBe(preview.id)
      expect(result.tabs).toHaveLength(1) // no new tab — promoted in place
      expect(result.tabs[0].preview).toBe(false) // promoted to pinned
    })
  })

  describe('Rule 3: activate-only', () => {
    it('does nothing if the active tab IS the target page', () => {
      const tab = mkTab(PAGE_A, { lastActivatedAt: 500 })
      const st = state([tab], tab.id)
      const result = openPage(st, PAGE_A, 'activate-only')
      expect(result.activeId).toBe(tab.id)
      expect(result.tabs).toHaveLength(1)
      // lastActivatedAt NOT bumped (it's not a real activation)
      expect(result.tabs[0].lastActivatedAt).toBe(500)
    })

    it('updates blockTarget on the active tab when provided', () => {
      const tab = mkTab(PAGE_A)
      const st = state([tab], tab.id)
      const result = openPage(st, PAGE_A, 'activate-only', {
        blockTarget: { blockId: 'abc-123' }
      })
      expect(result.tabs[0].blockTarget).toEqual({ blockId: 'abc-123' })
    })

    it('falls through to preview open if target is NOT the active page', () => {
      const tab = mkTab(PAGE_A)
      const st = state([tab], tab.id)
      const result = openPage(st, PAGE_B, 'activate-only')
      expect(result.tabs).toHaveLength(2)
      expect(result.activeId).not.toBe(tab.id)
    })
  })

  describe('Rule 4: preview slot reuse on different page', () => {
    it('replaces the preview tab content when opening a different page', () => {
      const preview = mkTab(PAGE_A, { preview: true, id: 'preview-1' })
      const st = state([preview], preview.id)
      const result = openPage(st, PAGE_B, 'preview')
      expect(result.tabs).toHaveLength(1) // still one tab (reused)
      expect(result.tabs[0].id).toBe('preview-1') // same slot id
      expect(result.tabs[0].page).toBe('Daily') // content replaced
      expect(result.tabs[0].preview).toBe(true) // still preview
      expect(result.activeId).toBe('preview-1')
    })
  })

  describe('Rule 5: pin mode', () => {
    it('creates a new pinned tab when no tab for the page exists', () => {
      const tabA = mkTab(PAGE_A, { preview: false })
      const st = state([tabA], tabA.id)
      const result = openPage(st, PAGE_B, 'pin')
      expect(result.tabs).toHaveLength(2)
      const newTab = result.tabs.find((t) => t.page === 'Daily')!
      expect(newTab.preview).toBe(false)
      expect(result.activeId).toBe(newTab.id)
    })
  })

  describe('Rule 6: enable_preview_tabs=false coerces to pin', () => {
    it('creates a pinned tab even with mode=preview', () => {
      const st = state([], '')
      const result = openPage(st, PAGE_A, 'preview', {
        enablePreviewTabs: false
      })
      expect(result.tabs).toHaveLength(1)
      expect(result.tabs[0].preview).toBe(false)
    })

    it('does NOT reuse a preview slot (creates a new pinned tab)', () => {
      const preview = mkTab(PAGE_A, { preview: true })
      const st = state([preview], preview.id)
      const result = openPage(st, PAGE_B, 'preview', {
        enablePreviewTabs: false
      })
      expect(result.tabs).toHaveLength(2) // new tab, not reuse
      const newTab = result.tabs.find((t) => t.page === 'Daily')!
      expect(newTab.preview).toBe(false)
    })
  })

  describe('Rule 7: LRU eviction at maxOpenTabs', () => {
    it('evicts the oldest preview tab when at capacity', () => {
      // 3 pinned tabs + 1 preview, maxOpenTabs=4. Opening a new preview
      // should evict the existing preview (it's the only preview).
      const p1 = mkTab(PAGE_A, { preview: false, lastActivatedAt: 100 })
      const p2 = mkTab(PAGE_B, { preview: false, lastActivatedAt: 200 })
      const p3 = mkTab(PAGE_C, { preview: false, lastActivatedAt: 300 })
      const oldPreview = mkTab(
        { notebook: 'X', section: '', page: 'Old' },
        { preview: true, lastActivatedAt: 50 }
      )
      const st = state([p1, p2, p3, oldPreview], p3.id)
      const result = openPage(
        st,
        { notebook: 'Y', section: '', page: 'New' },
        'preview',
        { maxOpenTabs: 4 }
      )
      expect(result.tabs).toHaveLength(4) // evicted 1, added 1
      expect(
        findTab(result.tabs, { notebook: 'X', section: '', page: 'Old' })
      ).toBeUndefined()
      const newTab = result.tabs.find((t) => t.page === 'New')!
      expect(newTab.preview).toBe(true)
    })

    it('evicts oldest pinned tab when no previews exist', () => {
      // 3 pinned tabs at maxOpenTabs=3. Opening a new pin evicts the oldest.
      const p1 = mkTab(PAGE_A, { preview: false, lastActivatedAt: 100 })
      const p2 = mkTab(PAGE_B, { preview: false, lastActivatedAt: 200 })
      const p3 = mkTab(PAGE_C, { preview: false, lastActivatedAt: 300 })
      const st = state([p1, p2, p3], p3.id)
      const result = openPage(
        st,
        { notebook: 'Y', section: '', page: 'New' },
        'pin',
        { maxOpenTabs: 3 }
      )
      expect(result.tabs).toHaveLength(3) // evicted 1, added 1
      expect(findTab(result.tabs, PAGE_A)).toBeUndefined() // p1 was oldest
      expect(result.tabs.find((t) => t.page === 'New')).toBeTruthy()
    })

    it('prefers evicting preview over pinned even if pinned is older', () => {
      const oldPinned = mkTab(PAGE_A, { preview: false, lastActivatedAt: 10 })
      const newerPreview = mkTab(PAGE_B, {
        preview: true,
        lastActivatedAt: 999
      })
      const st = state([oldPinned, newerPreview], newerPreview.id)
      const result = openPage(
        st,
        { notebook: 'Y', section: '', page: 'New' },
        'pin',
        { maxOpenTabs: 2 }
      )
      // Preview should be evicted (preferred), old pinned stays.
      expect(findTab(result.tabs, PAGE_B)).toBeUndefined()
      expect(findTab(result.tabs, PAGE_A)).toBeTruthy()
    })
  })

  it('opening into an empty state creates the first tab and activates it', () => {
    const st = state([], '')
    const result = openPage(st, PAGE_A, 'preview')
    expect(result.tabs).toHaveLength(1)
    expect(result.activeId).toBe(result.tabs[0].id)
  })

  it('blockTarget is attached to the opened tab', () => {
    const st = state([], '')
    const bt = { blockId: 'uuid-123', fileDate: '2026-06-15' }
    const result = openPage(st, PAGE_A, 'pin', { blockTarget: bt })
    expect(result.tabs[0].blockTarget).toEqual(bt)
  })
})

// --- closeTab --------------------------------------------------------

describe('closeTab', () => {
  it('removes the tab by id', () => {
    const t1 = mkTab(PAGE_A)
    const t2 = mkTab(PAGE_B)
    const st = state([t1, t2], t1.id)
    const result = closeTab(st, t1.id)
    expect(result.tabs).toHaveLength(1)
    expect(result.tabs[0].id).toBe(t2.id)
  })

  it('activates the MRU neighbor when closing the active tab', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 100 })
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 200 })
    const t3 = mkTab(PAGE_C, { lastActivatedAt: 300 })
    // Active is t3 (most recent). Close t3 → MRU neighbor is t2.
    const st = state([t1, t2, t3], t3.id)
    const result = closeTab(st, t3.id)
    expect(result.activeId).toBe(t2.id)
  })

  it('sets activeId="" when closing the last tab', () => {
    const t1 = mkTab(PAGE_A)
    const st = state([t1], t1.id)
    const result = closeTab(st, t1.id)
    expect(result.tabs).toHaveLength(0)
    expect(result.activeId).toBe('')
  })

  it('does not change activeId when closing a non-active tab', () => {
    const t1 = mkTab(PAGE_A)
    const t2 = mkTab(PAGE_B)
    const st = state([t1, t2], t2.id)
    const result = closeTab(st, t1.id)
    expect(result.activeId).toBe(t2.id)
  })
})

// --- promotePreview --------------------------------------------------

describe('promotePreview', () => {
  it('flips preview:false on the target tab', () => {
    const preview = mkTab(PAGE_A, { preview: true })
    const st = state([preview], preview.id)
    const result = promotePreview(st, preview.id)
    expect(result.tabs[0].preview).toBe(false)
    expect(result.activeId).toBe(preview.id)
  })

  it('is a no-op on an already-pinned tab', () => {
    const pinned = mkTab(PAGE_A, { preview: false })
    const st = state([pinned], pinned.id)
    const result = promotePreview(st, pinned.id)
    expect(result).toBe(st) // same reference (no change)
  })

  it('is a no-op on a non-existent tab id', () => {
    const tab = mkTab(PAGE_A)
    const st = state([tab], tab.id)
    const result = promotePreview(st, 'nonexistent')
    expect(result).toBe(st)
  })
})

// --- cycleTab --------------------------------------------------------

describe('cycleTab', () => {
  it('cycles to the next MRU tab (dir=1)', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 300 }) // MRU: 1st
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 200 }) // 2nd
    const t3 = mkTab(PAGE_C, { lastActivatedAt: 100 }) // 3rd
    const st = state([t1, t2, t3], t1.id)
    const result = cycleTab(st, 1)
    expect(result.activeId).toBe(t2.id)
  })

  it('cycles to the previous MRU tab (dir=-1)', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 300 })
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 200 })
    const t3 = mkTab(PAGE_C, { lastActivatedAt: 100 })
    const st = state([t1, t2, t3], t1.id)
    const result = cycleTab(st, -1)
    expect(result.activeId).toBe(t3.id) // wraps to last
  })

  it('wraps around at the end of the MRU list', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 300 })
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 200 })
    const t3 = mkTab(PAGE_C, { lastActivatedAt: 100 })
    // Active is t3 (last in MRU). cycleTab(1) wraps to t1.
    const st = state([t1, t2, t3], t3.id)
    const result = cycleTab(st, 1)
    expect(result.activeId).toBe(t1.id)
  })

  it('is a no-op with fewer than 2 tabs', () => {
    const t1 = mkTab(PAGE_A)
    const st = state([t1], t1.id)
    expect(cycleTab(st, 1)).toBe(st)
    expect(cycleTab(st, -1)).toBe(st)
  })
})

// --- pure helpers ----------------------------------------------------

describe('mruOrder', () => {
  it('returns tabs sorted by lastActivatedAt descending', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 100 })
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 300 })
    const t3 = mkTab(PAGE_C, { lastActivatedAt: 200 })
    const ordered = mruOrder([t1, t2, t3])
    expect(ordered.map((t) => t.id)).toEqual([t2.id, t3.id, t1.id])
  })

  it('does not mutate the input array', () => {
    const t1 = mkTab(PAGE_A, { lastActivatedAt: 100 })
    const t2 = mkTab(PAGE_B, { lastActivatedAt: 200 })
    const input = [t1, t2]
    mruOrder(input)
    expect(input[0].id).toBe(t1.id) // unchanged
  })
})

describe('pickEvictionVictim', () => {
  it('returns the oldest preview tab when previews exist', () => {
    const pinned = mkTab(PAGE_A, { preview: false, lastActivatedAt: 10 })
    const oldPreview = mkTab(PAGE_B, { preview: true, lastActivatedAt: 50 })
    const newPreview = mkTab(PAGE_C, { preview: true, lastActivatedAt: 200 })
    const victim = pickEvictionVictim([pinned, oldPreview, newPreview])
    expect(victim?.id).toBe(oldPreview.id)
  })

  it('returns the oldest pinned tab when no previews exist', () => {
    const t1 = mkTab(PAGE_A, { preview: false, lastActivatedAt: 10 })
    const t2 = mkTab(PAGE_B, { preview: false, lastActivatedAt: 200 })
    const victim = pickEvictionVictim([t1, t2])
    expect(victim?.id).toBe(t1.id)
  })

  it('returns undefined for an empty list', () => {
    expect(pickEvictionVictim([])).toBeUndefined()
  })
})

describe('tabMatches / findTab', () => {
  it('matches by notebook/section/page triple', () => {
    const tab = mkTab(PAGE_A)
    expect(tabMatches(tab, PAGE_A)).toBe(true)
    expect(tabMatches(tab, PAGE_B)).toBe(false)
  })

  it('findTab returns the first matching tab', () => {
    const t1 = mkTab(PAGE_A)
    const t2 = mkTab(PAGE_A) // same page, different id
    const found = findTab([t1, t2], PAGE_A)
    expect(found?.id).toBe(t1.id)
  })
})

describe('generateTabId', () => {
  it('produces unique ids', () => {
    const ids = new Set<string>()
    for (let i = 0; i < 100; i++) {
      ids.add(generateTabId())
    }
    expect(ids.size).toBe(100)
  })
})
