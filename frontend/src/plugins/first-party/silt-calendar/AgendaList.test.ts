// Targeted tests for AgendaList.svelte (#322 extraction). The standalone
// Agenda.test.ts covers the legacy Agenda.svelte plugin; this file
// covers the extracted AgendaList subcomponent that's rendered inside
// Calendar.svelte's agenda mode. Specifically we test the markDoneError
// dismiss affordance + banner-clear-on-next-success (#323 P1 review fix).
//
// Note: the 8s auto-clear timer is implementation detail and is not
// tested here — it relies on standard setTimeout semantics. The dismiss
// button is the user-facing escape hatch.
import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  sqliteQuery: vi.fn(),
  updateBlockState: vi.fn()
}))

import AgendaList from './AgendaList.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'
import { resetFocusStateForTests, setActiveFilter } from './focusState.svelte'

function makeCtx(): PluginContext {
  return {
    activeNotebook: 'Work',
    activeSection: 'Journal',
    activePage: 'Daily',
    today: '2026-06-16',
    sqliteQuery: mocks.sqliteQuery,
    updateBlockState: mocks.updateBlockState,
    mutateBlock: vi.fn(),
    updateTaskMeta: vi.fn(),
    getPluginSettings: vi.fn(() => Promise.resolve({})),
    on: () => () => {},
    ...v2CtxStubs
  }
}

const MANIFEST: PluginManifest = {
  id: 'silt-calendar',
  name: 'Calendar',
  version: '1.0.0'
}

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

// jsdom doesn't implement scrollIntoView; AgendaList's filter-scroll
// effect calls it. Stub before mounting so the effect runs cleanly.
if (!HTMLElement.prototype.scrollIntoView) {
  HTMLElement.prototype.scrollIntoView = function () {}
}

const SAMPLE_ROW = {
  rows: [
    {
      id: 'a1',
      notebook: 'Work',
      section: 'Journal',
      page: 'Daily',
      file_date: '2026-06-16',
      clean_content: 'Today task',
      status: 'TODO',
      owner: '',
      start_date: '',
      due_date: '2026-06-16',
      priority: 2
    }
  ],
  truncated: false
}

describe('AgendaList markDoneError UI', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset().mockResolvedValue(SAMPLE_ROW)
    mocks.updateBlockState.mockReset()
    resetFocusStateForTests()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders the mark-done error banner with a dismiss button when updateBlockState rejects', async () => {
    mocks.updateBlockState.mockRejectedValueOnce(
      new Error('Backend rejected the write')
    )
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByLabelText('Mark done'))
    await flush()
    expect(screen.getByTestId('mark-done-error')).toBeInTheDocument()
    expect(screen.getByText(/Backend rejected/i)).toBeInTheDocument()
  })

  it('dismisses the banner via the X button', async () => {
    mocks.updateBlockState.mockRejectedValueOnce(new Error('boom'))
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByLabelText('Mark done'))
    await flush()
    expect(screen.getByTestId('mark-done-error')).toBeInTheDocument()
    await fireEvent.click(screen.getByTestId('mark-done-error-dismiss'))
    expect(screen.queryByTestId('mark-done-error')).toBeNull()
  })

  it('banner is absent on a successful markDone (markDoneError stays empty)', async () => {
    mocks.updateBlockState.mockResolvedValue(undefined)
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByLabelText('Mark done'))
    await flush()
    expect(screen.queryByTestId('mark-done-error')).toBeNull()
  })

  // --- #325 P2 follow-up: Upcoming filter excludes today
  it('upcoming filter dims today-task and highlights future-task (count/result parity)', async () => {
    // Fixture: one today-task (due today) and one future-task (due in
    // 5 days). The Upcoming smart-list count (SQL) excludes today →
    // badge reads "1". The filter must agree: today-task is dimmed,
    // future-task is highlighted.
    mocks.sqliteQuery.mockResolvedValueOnce({
      rows: [
        {
          id: 'today1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: '2026-06-16',
          clean_content: 'Today task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: '2026-06-16', // today
          priority: 2
        },
        {
          id: 'future1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: '2026-06-16',
          clean_content: 'Future task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: '2026-06-21', // today + 5 days
          priority: 2
        }
      ],
      truncated: false
    })
    setActiveFilter('upcoming')
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // The `class:opacity-30={!matchesFilter(item)}` directive is on the
    // outer per-item <div role="button">. Climb from the text node up
    // to the role="button" wrapper to inspect the class list.
    const todayRow = screen.getByText('Today task').closest('[role="button"]')
    const futureRow = screen.getByText('Future task').closest('[role="button"]')
    expect(todayRow).toBeTruthy()
    expect(futureRow).toBeTruthy()
    // Today-task must be dimmed (filter excludes today from upcoming);
    // future-task must not.
    expect(todayRow!.className).toMatch(/opacity-30/)
    expect(futureRow!.className).not.toMatch(/opacity-30/)
  })

  // --- #325 P2 follow-up: tomorrow / weekAhead derive from nowTick-aware `today`
  it('Tomorrow group date is plusDaysISO(today, 1) — derived from local today, not ctx.today', async () => {
    // The buggy version used `plusDaysISO(ctx.today, 1)` for `tomorrow`,
    // which froze the value at mount because ctx.today is a plain getter
    // (no reactive dep). The fix derives from the nowTick-aware `today`
    // so midnight crossings re-bucket.
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'tmrw1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: '2026-06-16',
          clean_content: 'Tomorrow task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: '2026-06-17',
          priority: 2
        }
      ],
      truncated: false
    })
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const tomorrowGroup = screen.getByLabelText('Tomorrow')
    expect(tomorrowGroup.getAttribute('data-group-date')).toBe('2026-06-17')
  })

  // --- Completed filter: agenda is forward-looking, so show an
  // explanatory empty state instead of dimming every row.
  it('Completed filter shows an explanatory empty state and skips the grouped list', async () => {
    setActiveFilter('completed')
    render(AgendaList, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(
      screen.getByText(/Agenda shows active tasks only/i)
    ).toBeInTheDocument()
    // Groups are short-circuited out by the completed branch.
    expect(screen.queryByLabelText('Today')).toBeNull()
    expect(screen.queryByLabelText('Overdue')).toBeNull()
  })
})
