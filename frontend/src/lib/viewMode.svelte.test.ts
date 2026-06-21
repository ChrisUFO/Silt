import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// Hoist the settings-store mock so the vi.mock factory can reference it
// (vi.mock factories are hoisted above imports).
const mocks = vi.hoisted(() => ({
  // Mutable snapshot of the minimal slice viewMode reads (editor.default_view_mode).
  // Tests mutate in place — never reassign — so the mock factory's reference
  // remains valid across tests.
  config: { editor: { default_view_mode: 'edit' as 'edit' | 'source' } }
}))

vi.mock('../settings/store.svelte', () => ({
  // viewMode reads `settings.config?.editor?.default_view_mode`.
  settings: mocks
}))

import {
  getViewMode,
  setViewMode,
  toggleViewMode,
  __resetViewModesForTests
} from './viewMode.svelte'

const N = 'Notebook'
const S = 'Section'
const P = 'Page'

// LRU bookkeeping keys off Date.now(). Real wall-clock granularity is too
// coarse for the in-process test burst (all 60+ setViewMode calls land in
// the same millisecond), so we stub Date.now to a monotonically-advancing
// counter. Tests that need to inspect the access-order relative to other
// calls advance `now` by 1 between operations.
let now = 0
beforeEach(() => {
  now = 0
  vi.spyOn(Date, 'now').mockImplementation(() => now)
  __resetViewModesForTests()
  mocks.config.editor.default_view_mode = 'edit'
})
afterEach(() => {
  vi.restoreAllMocks()
})

function tick(): void {
  now += 1
}

describe('viewMode (#199 — LRU eviction)', () => {
  it('round-trips setViewMode → getViewMode', () => {
    setViewMode(N, S, P, 'source')
    expect(getViewMode(N, S, P)).toBe('source')
  })

  it('toggleViewMode flips between edit and source', () => {
    setViewMode(N, S, P, 'edit')
    toggleViewMode(N, S, P)
    expect(getViewMode(N, S, P)).toBe('source')
    toggleViewMode(N, S, P)
    expect(getViewMode(N, S, P)).toBe('edit')
  })

  it('falls back to default_view_mode when the page was never set', () => {
    mocks.config.editor.default_view_mode = 'source'
    expect(getViewMode(N, S, 'fresh')).toBe('source')
  })

  it('falls back to "edit" when default_view_mode is unset', () => {
    // No explicit default → "edit" sentinel.
    expect(getViewMode(N, S, 'fresh')).toBe('edit')
  })

  it('getViewMode on an evicted key falls back to default_view_mode', () => {
    // Fill the cache past the cap (50) so the first key is evicted.
    for (let i = 0; i < 51; i++) {
      setViewMode(N, S, `p${i}`, 'source')
      tick()
    }
    // Page p0 was the first inserted and has the oldest lastUsed → evicted.
    // Subsequent pages occupy the 50 slots.
    mocks.config.editor.default_view_mode = 'source'
    expect(getViewMode(N, S, 'p0')).toBe('source')
  })

  it('toggleViewMode on an evicted key toggles relative to default_view_mode', () => {
    for (let i = 0; i < 51; i++) {
      setViewMode(N, S, `p${i}`, 'source')
      tick()
    }
    mocks.config.editor.default_view_mode = 'edit'
    // p0 evicted → current is the default ('edit'), so toggle → 'source'.
    toggleViewMode(N, S, 'p0')
    expect(getViewMode(N, S, 'p0')).toBe('source')
  })

  it('does not grow the cache beyond MAX_VIEW_MODES (50) entries', () => {
    // The MAX cap is intentionally not exported, but we can probe its
    // existence by inserting 51 distinct keys — the 51st setViewMode
    // must have evicted the oldest to stay at the cap. Subsequent sets
    // continue to evict and never grow.
    for (let i = 0; i < 100; i++) {
      setViewMode(N, S, `p${i}`, 'source')
      tick()
    }
    // Every setViewMode bumped its lastUsed, so the eviction order cycles
    // deterministically. After 100 inserts the most-recent set must be
    // present (p99 just landed) and the 50th-from-last set must survive.
    expect(getViewMode(N, S, 'p99')).toBe('source')
    expect(getViewMode(N, S, 'p50')).toBe('source')
    // p0 was evicted long ago.
    mocks.config.editor.default_view_mode = 'edit'
    expect(getViewMode(N, S, 'p0')).toBe('edit')
  })

  it('evicts the least-recently-used key, not necessarily the oldest-inserted', () => {
    // Insert 50 keys (fill to cap).
    for (let i = 0; i < 50; i++) {
      setViewMode(N, S, `p${i}`, 'source')
      tick()
    }
    // Re-access p0 via getViewMode → bumps its lastUsed, making it the
    // MRU among the 50. The next setViewMode should evict the next-oldest
    // (p1), not p0.
    getViewMode(N, S, 'p0')
    tick()
    setViewMode(N, S, 'newpage', 'source')
    mocks.config.editor.default_view_mode = 'edit'
    expect(getViewMode(N, S, 'p0')).toBe('source') // promoted, still present
    expect(getViewMode(N, S, 'p1')).toBe('edit') // the actual victim
  })

  it('setting the same key twice does not grow the record', () => {
    for (let i = 0; i < 50; i++) {
      setViewMode(N, S, `p${i}`, 'source')
      tick()
    }
    // Re-set an existing key 10 more times — no growth past 50.
    for (let i = 0; i < 10; i++) {
      setViewMode(N, S, 'p0', 'source')
      tick()
    }
    // Insert one new key — should evict the LRU (p1, untouched since first
    // insert and not promoted), not p0.
    setViewMode(N, S, 'pNew', 'edit')
    mocks.config.editor.default_view_mode = 'edit'
    expect(getViewMode(N, S, 'p0')).toBe('source') // still present
    expect(getViewMode(N, S, 'p1')).toBe('edit') // evicted
  })

  it('insertion-sequence tie-breaker resolves same-millisecond eviction', () => {
    // Without tick() between inserts, all 51 keys share Date.now()=0. The
    // insertion-sequence tie-breaker must evict the oldest-inserted key
    // (p0) rather than relying on for...in order, which ECMA-262 leaves
    // unspecified.
    for (let i = 0; i < 51; i++) {
      setViewMode(N, S, `p${i}`, 'source')
    }
    mocks.config.editor.default_view_mode = 'edit'
    expect(getViewMode(N, S, 'p0')).toBe('edit') // evicted: oldest-inserted
    expect(getViewMode(N, S, 'p1')).toBe('source') // would be wrong victim without tie-breaker
    expect(getViewMode(N, S, 'p50')).toBe('source') // most-recent insert survives
  })
})
