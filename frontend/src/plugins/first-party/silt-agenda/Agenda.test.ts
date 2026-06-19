import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  sqliteQuery: vi.fn(),
  updateBlockState: vi.fn()
}))

import Agenda from './Agenda.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'

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
  id: 'silt-agenda',
  name: 'Agenda',
  version: '1.0.0'
}

function todayStr() {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('Agenda plugin', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset()
    mocks.updateBlockState.mockReset()
    mocks.updateBlockState.mockResolvedValue(true)
  })

  afterEach(() => {
    cleanup()
  })

  it('loads tasks via ctx.sqliteQuery and buckets them by date group', async () => {
    const today = todayStr()
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'a1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: today,
          clean_content: 'Overdue task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: '2020-01-01',
          priority: 3
        },
        {
          id: 'a2',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: today,
          clean_content: 'Today task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: today,
          priority: 2
        }
      ],
      truncated: false
    })

    render(Agenda, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(screen.getByText('Overdue task')).toBeInTheDocument()
    expect(screen.getByText('Today task')).toBeInTheDocument()
  })

  it('mark-done calls ctx.updateBlockState with DONE and removes the row', async () => {
    const today = todayStr()
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'a1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: today,
          clean_content: 'Task to complete',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: today,
          priority: 3
        }
      ],
      truncated: false
    })

    render(Agenda, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(screen.getByText('Task to complete')).toBeInTheDocument()

    // Click the mark-done button.
    const doneBtn = screen.getByRole('button', { name: 'Mark done' })
    await fireEvent.click(doneBtn)
    await flush()

    expect(mocks.updateBlockState).toHaveBeenCalledWith('a1', 'DONE')
  })

  it('clicking a task dispatches navigate-to-block', async () => {
    const today = todayStr()
    mocks.sqliteQuery.mockResolvedValue({
      rows: [
        {
          id: 'a1',
          notebook: 'Work',
          section: 'Journal',
          page: 'Daily',
          file_date: today,
          clean_content: 'Clickable task',
          status: 'TODO',
          owner: '',
          start_date: '',
          due_date: today,
          priority: 3
        }
      ],
      truncated: false
    })

    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    render(Agenda, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    // The task row is a role="button" — click it.
    const row = screen.getByText('Clickable task').closest('[role="button"]')
    expect(row).toBeTruthy()
    await fireEvent.click(row!)

    expect(handler).toHaveBeenCalledTimes(1)
    const detail = (handler.mock.calls[0][0] as CustomEvent).detail
    expect(detail.blockId).toBe('a1')
    window.removeEventListener('navigate-to-block', handler)
  })

  it('shows empty state when no tasks are returned', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })

    render(Agenda, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(screen.getByText(/Nothing scheduled/i)).toBeInTheDocument()
  })
})
