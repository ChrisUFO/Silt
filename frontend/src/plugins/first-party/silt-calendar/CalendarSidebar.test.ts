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
import { resetFocusStateForTests, getFocusState } from './focusState.svelte'

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
    rows: [
      { today, upcoming, overdue, completed, all }
    ],
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
})
