import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  sqliteQuery: vi.fn()
}))

import Calendar from './Calendar.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'

function makeCtx(): PluginContext {
  return {
    activeNotebook: 'Work',
    activeSection: 'Journal',
    activePage: 'Daily',
    today: '2026-06-16',
    sqliteQuery: mocks.sqliteQuery,
    updateBlockState: vi.fn(),
    mutateBlock: vi.fn(),
    updateTaskMeta: vi.fn(),
    getPluginSettings: vi.fn(() => Promise.resolve({}))
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

describe('Calendar plugin', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders a month grid with tasks from ctx.sqliteQuery', async () => {
    const now = new Date()
    const ymd = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'c1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: ymd,
          clean_content: 'Meeting today',
          status: 'TODO',
          due_date: ymd
        }
      ],
      truncated: false
    })

    render(Calendar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalled()
    // The task should appear in the calendar grid.
    expect(screen.getByText('Meeting today')).toBeInTheDocument()
  })

  it('Today button resets the cursor to the current date', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })

    render(Calendar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todayBtn = screen.getByRole('button', { name: 'Today' })
    expect(todayBtn).toBeInTheDocument()
    await fireEvent.click(todayBtn)
    // After clicking Today the heading should contain the current month/year.
    const now = new Date()
    const monthYear = `${now.toLocaleString('en', { month: 'long' })} ${now.getFullYear()}`
    expect(screen.getByText(monthYear)).toBeInTheDocument()
  })

  it('clicking a task dispatches navigate-to-block', async () => {
    const now = new Date()
    const ymd = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'c1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: ymd,
          clean_content: 'Standup meeting',
          status: 'TODO',
          due_date: ymd
        }
      ],
      truncated: false
    })

    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    render(Calendar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    // Click the task button in the grid.
    const taskBtn = screen.getByText('Standup meeting')
    await fireEvent.click(taskBtn)

    expect(handler).toHaveBeenCalledTimes(1)
    const detail = (handler.mock.calls[0][0] as CustomEvent).detail
    expect(detail.blockId).toBe('c1')
    window.removeEventListener('navigate-to-block', handler)
  })
})
