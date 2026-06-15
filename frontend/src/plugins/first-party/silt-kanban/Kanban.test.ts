import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

// jsdom doesn't implement Element.getAnimations(), which Svelte's
// animate:flip directive calls internally when list items reposition.
// Polyfill with an empty array so the directive no-ops in tests.
if (!Element.prototype.getAnimations) {
  Element.prototype.getAnimations = () => []
}

// Hoisted mutable mock state.
const mocks = vi.hoisted(() => ({
  settings: {
    config: {
      plugins: {
        plugin_settings: {
          'silt-kanban': {
            default_col: 'TODO',
            columns: ['TODO', 'DOING', 'DONE']
          }
        }
      }
    }
  },
  sqliteQuery: vi.fn(),
  updateBlockState: vi.fn()
}))

vi.mock('../../../settings/store.svelte', () => ({
  settings: mocks.settings
}))

import Kanban from './Kanban.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'

function makeCtx(overrides: Partial<PluginContext> = {}): PluginContext {
  return {
    activeNotebook: 'Work',
    activeSection: 'Journal',
    activePage: 'Daily',
    sqliteQuery: mocks.sqliteQuery,
    updateBlockState: mocks.updateBlockState,
    mutateBlock: vi.fn(),
    ...overrides
  }
}

const MANIFEST: PluginManifest = {
  id: 'silt-kanban',
  name: 'Kanban',
  version: '1.0.0'
}

const SAMPLE_ROWS = [
  {
    id: 't1',
    notebook: 'Work',
    section: 'Journal',
    page: 'Daily',
    file_date: '2026-06-14',
    clean_content: 'Write tests',
    status: 'TODO',
    owner: 'Alice',
    start_date: '',
    due_date: '2026-06-20',
    priority: 3
  },
  {
    id: 't2',
    notebook: 'Work',
    section: 'Journal',
    page: 'Daily',
    file_date: '2026-06-14',
    clean_content: 'Implement feature',
    status: 'DOING',
    owner: '',
    start_date: '',
    due_date: '',
    priority: 2
  },
  {
    id: 't3',
    notebook: 'Work',
    section: 'Journal',
    page: 'Daily',
    file_date: '2026-06-14',
    clean_content: 'Ship release',
    status: 'DONE',
    owner: '',
    start_date: '',
    due_date: '',
    priority: 1
  }
]

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('Kanban plugin (#19)', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset()
    mocks.updateBlockState.mockReset()
    mocks.sqliteQuery.mockResolvedValue(SAMPLE_ROWS)
    mocks.updateBlockState.mockResolvedValue(true)
  })

  afterEach(() => {
    cleanup()
  })

  it('renders 3 lanes (To Do / In Progress / Done) from default config', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(screen.getByRole('group', { name: 'To Do' })).toBeInTheDocument()
    expect(
      screen.getByRole('group', { name: 'In Progress' })
    ).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Done' })).toBeInTheDocument()
  })

  it('loads tasks via ctx.sqliteQuery and buckets them by status', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCards = screen
      .getByRole('group', { name: 'To Do' })
      .querySelectorAll('[data-card]')
    const doingCards = screen
      .getByRole('group', { name: 'In Progress' })
      .querySelectorAll('[data-card]')
    const doneCards = screen
      .getByRole('group', { name: 'Done' })
      .querySelectorAll('[data-card]')

    expect(todoCards).toHaveLength(1)
    expect(doingCards).toHaveLength(1)
    expect(doneCards).toHaveLength(1)
  })

  it('defaults to page scope when activePage is set', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    // The first sqliteQuery call should use the page-scope WHERE clause.
    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls[0][0] as string
    expect(sql).toContain('b.page = ?')
    const params = mocks.sqliteQuery.mock.calls[0][1] as unknown[]
    expect(params).toEqual(['Work', 'Journal', 'Daily'])
  })

  it('changing scope to vault re-runs the query without a WHERE filter', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.sqliteQuery.mockClear()

    const vaultRadio = screen.getByRole('radio', { name: 'Vault' })
    await fireEvent.click(vaultRadio)
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalledTimes(1)
    const sql = mocks.sqliteQuery.mock.calls[0][0] as string
    expect(sql).toContain('WHERE 1=1')
    const params = mocks.sqliteQuery.mock.calls[0][1] as unknown[]
    expect(params).toEqual([])
  })

  it('clicking a card dispatches navigate-to-block', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    await fireEvent.click(todoCard!)

    expect(handler).toHaveBeenCalledTimes(1)
    const detail = (handler.mock.calls[0][0] as CustomEvent).detail
    expect(detail.blockId).toBe('t1')
    expect(detail.notebook).toBe('Work')
    window.removeEventListener('navigate-to-block', handler)
  })

  it('keyboard ArrowRight on a TODO card moves it to DOING (calls updateBlockState)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    todoCard!.focus()

    // ArrowRight directly moves to the next lane (DOING) — no pick-up step.
    await fireEvent.keyDown(todoCard!, { key: 'ArrowRight' })

    expect(mocks.updateBlockState).toHaveBeenCalledTimes(1)
    const [id, status] = mocks.updateBlockState.mock.calls[0]
    expect(id).toBe('t1')
    expect(status).toBe('DOING')
  })

  it('updateBlockState rejection reverts the local move and shows the error banner', async () => {
    mocks.updateBlockState.mockRejectedValue(new Error('focus lock held'))
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    todoCard!.focus()

    await fireEvent.keyDown(todoCard!, { key: 'ArrowRight' })
    await flush()

    // The error banner is shown.
    const alert = screen.getByRole('alert')
    expect(alert.textContent).toContain("Couldn't move task")

    // The card is still in To Do (reverted).
    const todoCards = screen
      .getByRole('group', { name: 'To Do' })
      .querySelectorAll('[data-card]')
    expect(todoCards).toHaveLength(1)
  })

  it('shows empty-lane message when no tasks exist for a status', async () => {
    mocks.sqliteQuery.mockResolvedValue([{ ...SAMPLE_ROWS[0] }])
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const doingLane = screen.getByRole('group', { name: 'In Progress' })
    expect(doingLane.textContent).toContain('No in progress tasks')
  })
})
