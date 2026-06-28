import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  sqliteQuery: vi.fn()
}))

vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(() => () => {})
}))

import CalendarSidebar from './CalendarSidebar.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'
import {
  resetFocusStateForTests,
  getFocusState,
  setFocusDate,
  setActiveFilter
} from './focusState.svelte'

function makeCtx(overrides: Partial<PluginContext> = {}): PluginContext {
  return {
    activeNotebook: 'Work',
    activeSection: 'Journal',
    activePage: 'Daily',
    today: '2026-06-16',
    sqliteQuery: mocks.sqliteQuery,
    updateBlockState: vi.fn(),
    mutateBlock: vi.fn(),
    updateTaskMeta: vi.fn(),
    getPluginSettings: vi.fn(() => Promise.resolve({})),
    on: () => () => {},
    ...v2CtxStubs,
    ...overrides
  }
}

const MANIFEST: PluginManifest = {
  id: 'silt-calendar',
  name: 'Calendar',
  version: '1.0.0'
}

/**
 * Build a row for the smart-list counts query. The sidebar's first query
 * is a conditional aggregate; the second (per-day dots) is a
 * GROUP BY t.due_date. Each test injects both via the same mock.
 */
function mockCounts(
  today: number,
  upcoming: number,
  overdue: number,
  completed: number,
  all: number
) {
  return {
    rows: [{ today, upcoming, overdue, completed, all }],
    truncated: false
  }
}

function mockDayCounts(entries: Array<{ d: string; c: number }>) {
  return {
    rows: entries.map((e) => ({ d: e.d, c: e.c })),
    truncated: false
  }
}

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('CalendarSidebar (#322)', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset()
    resetFocusStateForTests()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders all five smart-list rows', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(3, 12, 1, 0, 49)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(screen.getByTestId('today')).toBeInTheDocument()
    expect(screen.getByTestId('upcoming')).toBeInTheDocument()
    expect(screen.getByTestId('overdue')).toBeInTheDocument()
    expect(screen.getByTestId('completed')).toBeInTheDocument()
    expect(screen.getByTestId('all')).toBeInTheDocument()
  })

  it('renders the count badges from the SQLite aggregate query', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(3, 12, 1, 0, 49)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(screen.getByTestId('count-today').textContent?.trim()).toBe('3')
    expect(screen.getByTestId('count-upcoming').textContent?.trim()).toBe('12')
    expect(screen.getByTestId('count-overdue').textContent?.trim()).toBe('1')
    expect(screen.getByTestId('count-all').textContent?.trim()).toBe('49')
  })

  it('quotes the `all` aggregate alias — ALL is a SQLite keyword', async () => {
    // Regression guard: `AS all` (bare) is a syntax error because ALL is a
    // reserved word (UNION ALL / SELECT ALL). The query mocks sqliteQuery per
    // the IPC-boundary convention, so the SQL string itself is never executed
    // in jsdom — assert the quoted alias here so it can't silently regress.
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const countSql = mocks.sqliteQuery.mock.calls
      .map((c) => String(c[0]))
      .find((s) => s.includes('SUM(CASE'))
    expect(countSql).toBeDefined()
    expect(countSql).toContain('AS "all"')
    expect(countSql).not.toMatch(/AS all\b/)
  })

  it('clicking the Today smart list sets activeFilter to "today"', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(3, 12, 1, 0, 49)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('today'))
    expect(getFocusState().activeFilter).toBe('today')
  })

  it('clicking the All Tasks smart list clears the active filter', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(3, 12, 1, 0, 49)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('today'))
    expect(getFocusState().activeFilter).toBe('today')
    await fireEvent.click(screen.getByTestId('all'))
    expect(getFocusState().activeFilter).toBe('all')
  })

  it('mini calendar shows dot indicators on days with tasks', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([
        { d: '2026-06-16', c: 2 },
        { d: '2026-06-20', c: 1 }
      ])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const dayWithDots = document.querySelector(
      '[data-testid="mini-day-2026-06-16"] [aria-hidden="true"]'
    )
    expect(dayWithDots).toBeTruthy()
    const dayWithoutDots = document.querySelector(
      '[data-testid="mini-day-2026-06-17"] [aria-hidden="true"]'
    )
    expect(dayWithoutDots).toBeNull()
  })

  it('clicking a mini-calendar day sets focusDate and dispatches the focus event (#322 AC #4)', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    const handler = vi.fn()
    window.addEventListener('calendar:focus-date', handler)
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const cell = document.querySelector<HTMLElement>(
      '[data-testid="mini-day-2026-06-16"]'
    )
    expect(cell).toBeTruthy()
    await fireEvent.click(cell!)
    expect(getFocusState().focusDate).toBe('2026-06-16')
    expect(handler).toHaveBeenCalled()
    const detail = (handler.mock.calls[0][0] as CustomEvent).detail
    expect(detail.date).toBe('2026-06-16')
    window.removeEventListener('calendar:focus-date', handler)
  })

  it('arrow-key keyboard nav on smart lists moves the focus index', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const first = screen.getByTestId('today')
    first.focus()
    await fireEvent.keyDown(first, { key: 'ArrowDown' })
    await flush()
    const second = screen.getByTestId('upcoming')
    expect(document.activeElement).toBe(second)
  })

  it('Enter on a focused smart list activates it', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const upcoming = screen.getByTestId('upcoming')
    upcoming.focus()
    await fireEvent.keyDown(upcoming, { key: 'Enter' })
    expect(getFocusState().activeFilter).toBe('upcoming')
  })

  it('Clear-filter button appears when a filter is active', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(screen.queryByTestId('clear-filter')).toBeNull()
    await fireEvent.click(screen.getByTestId('overdue'))
    await flush()
    expect(screen.queryByTestId('clear-filter')).toBeTruthy()
    await fireEvent.click(screen.getByTestId('clear-filter'))
    expect(getFocusState().activeFilter).toBe('all')
    expect(screen.queryByTestId('clear-filter')).toBeNull()
  })

  // --- #322 polish: "Today" chip on mini-calendar header (#322 polish)
  it('mini-cal "Today" button clears the focus date', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // Simulate a sidebar click that set the focusDate to a future day
    // (we don't navigate the mini-cal month here — we exercise the
    // Today chip's clear-focus behaviour directly via the public state).
    setFocusDate('2026-08-15')
    expect(getFocusState().focusDate).toBe('2026-08-15')
    const todayBtn = screen.getByTestId('mini-today')
    expect(todayBtn).toBeInTheDocument()
    await fireEvent.click(todayBtn)
    expect(getFocusState().focusDate).toBe('')
  })

  // --- #323 P1 review fixes
  it('refresh-navigation clears focusDate + activeFilter (vault switch reset)', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // Set non-default focusDate + activeFilter (simulating the user
    // having interacted with the sidebar in vault A).
    setFocusDate('2026-08-15')
    setActiveFilter('overdue')
    expect(getFocusState().focusDate).toBe('2026-08-15')
    expect(getFocusState().activeFilter).toBe('overdue')
    // Vault switch fires refresh-navigation; the sidebar drops the
    // stale state so the new vault starts clean.
    window.dispatchEvent(new CustomEvent('refresh-navigation'))
    await flush()
    expect(getFocusState().focusDate).toBe('')
    expect(getFocusState().activeFilter).toBe('all')
  })

  it('Today smart-list count does NOT include overdue tasks', async () => {
    // The badge reads "Today" — overdue items have their own separate
    // count. Conflating them makes the badge misleading when there are
    // N overdue tasks and 0 due-today.
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 12, 5, 0, 17)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(screen.getByTestId('count-today').textContent?.trim()).toBe('0')
    expect(screen.getByTestId('count-overdue').textContent?.trim()).toBe('5')
    expect(screen.getByTestId('count-upcoming').textContent?.trim()).toBe('12')
  })

  // --- #325 merge-blocking regression: cleanup on unmount
  it('cleans up refresh-navigation listener, calendar:clear-filter listener, nowInterval, and block:changed subscription on unmount', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    // Track every unsubscribe fn returned by ctx.on — the component
    // stores it in offBlock and calls it on cleanup.
    const offBlockFns: Array<() => void> = []
    const ctx = makeCtx({
      on: vi.fn(() => {
        const fn = vi.fn()
        offBlockFns.push(fn)
        return fn
      })
    })
    // Spy AFTER makeCtx so the spy is in place before the component
    // mounts and registers its listeners.
    const removeEventListenerSpy = vi.spyOn(window, 'removeEventListener')
    const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval')
    render(CalendarSidebar, { ctx, manifest: MANIFEST })
    await flush()
    // Sanity: the component registered at least the block:changed
    // subscription that the cleanup will tear down.
    expect(offBlockFns.length).toBeGreaterThan(0)
    // Trigger unmount.
    cleanup()
    // 1. refresh-navigation listener removed.
    expect(removeEventListenerSpy).toHaveBeenCalledWith(
      'refresh-navigation',
      expect.any(Function)
    )
    // 2. calendar:clear-filter listener removed (second onMount in the
    //    same component file).
    expect(removeEventListenerSpy).toHaveBeenCalledWith(
      'calendar:clear-filter',
      expect.any(Function)
    )
    // 3. nowInterval cleared.
    expect(clearIntervalSpy).toHaveBeenCalled()
    // 4. block:changed unsubscribe invoked — every offBlock fn that
    //    was returned by ctx.on('block:changed', …) must have been
    //    called exactly once on cleanup.
    for (const fn of offBlockFns) {
      expect(fn).toHaveBeenCalledTimes(1)
    }
  })

  // --- #325 P3 follow-up: mount-time reload fires exactly once
  it('fires reload() exactly once on cold mount (one $effect for both nowTick and miniCursor)', async () => {
    mocks.sqliteQuery.mockReset()
    let queryCount = 0
    mocks.sqliteQuery.mockImplementation(async () => {
      queryCount++
      // The conditional-aggregate query returns the count row.
      if (queryCount === 1) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    // Flush both microtasks (mount + first effect run) and any
    // post-microtask scheduling.
    await flush()
    await new Promise((r) => setTimeout(r, 0))
    // The component fires 2 SQLite queries per reload() call (count +
    // day-dots). With a single $effect firing once on mount, exactly
    // 2 queries are issued. Two $effects would issue 4.
    expect(queryCount).toBe(2)
  })

  // --- #323 perf: nowTick must not re-fire reload() when the local day is unchanged
  it('does NOT reload on nowTick ticks when ctx.today is unchanged (no-query same-day ticks)', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    // Capture the nowTick interval callback so we can drive it
    // deterministically without waiting 60s of real time and without
    // the microtask-tangling that vi.useFakeTimers introduces across
    // reload()'s await chain.
    let tickCb: (() => void) | undefined
    const setIntervalSpy = vi
      .spyOn(globalThis, 'setInterval')
      .mockImplementation(((fn: () => void) => {
        tickCb = fn
        return 0 as any
      }) as any)
    try {
      render(CalendarSidebar, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()
      const afterMount = mocks.sqliteQuery.mock.calls.length
      // Cold mount fires reload() exactly once → 2 SQLite queries
      // (conditional-aggregate counts + per-day dots).
      expect(afterMount).toBe(2)
      expect(tickCb).toBeTruthy()
      // Five minute-ticks with the local-day anchor unchanged. The
      // gating effect must short-circuit each one instead of re-running
      // reload() — the previous unguarded effect wasted ~960 redundant
      // queries per workday.
      for (let i = 0; i < 5; i++) {
        tickCb!()
        await flush()
      }
      expect(mocks.sqliteQuery.mock.calls.length).toBe(afterMount)
    } finally {
      setIntervalSpy.mockRestore()
    }
  })

  it('still reloads when ctx.today rolls to a new day (midnight re-bucket still fires)', async () => {
    mocks.sqliteQuery.mockImplementation(async (sql: string) => {
      if (sql.includes('SUM(CASE')) return mockCounts(0, 0, 0, 0, 0)
      return mockDayCounts([])
    })
    const ctx = makeCtx({ today: '2026-06-16' })
    let tickCb: (() => void) | undefined
    const setIntervalSpy = vi
      .spyOn(globalThis, 'setInterval')
      .mockImplementation(((fn: () => void) => {
        tickCb = fn
        return 0 as any
      }) as any)
    try {
      render(CalendarSidebar, { ctx, manifest: MANIFEST })
      await flush()
      const afterMount = mocks.sqliteQuery.mock.calls.length
      expect(afterMount).toBe(2)
      // Simulate midnight: mutate the same ctx object the effect
      // re-reads on each nowTick tick (ctx.today is a plain getter per
      // sdk.ts:82). The next tick must see the day string differ from
      // lastSeenToday and fire reload() again so the smart-list counts
      // re-bucket under the new local day.
      ctx.today = '2026-06-17'
      expect(tickCb).toBeTruthy()
      tickCb!()
      await flush()
      // reload() fires once more → 2 additional SQLite queries.
      expect(mocks.sqliteQuery.mock.calls.length).toBe(afterMount + 2)
    } finally {
      setIntervalSpy.mockRestore()
    }
  })
})
