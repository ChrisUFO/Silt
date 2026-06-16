import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

// jsdom doesn't implement Element.getAnimations(), which Svelte's
// animate:flip directive calls internally when list items reposition.
// Polyfill with an empty array so the directive no-ops in tests.
if (!Element.prototype.getAnimations) {
  Element.prototype.getAnimations = () => []
}

// jsdom doesn't implement the Web Animations API, which Svelte 5 transitions
// (the CardDetailPanel slide-out uses transition:fly) drive via
// element.animate(). Stub a resolved no-op Animation so the panel mounts in
// jsdom without throwing `element.animate is not a function`.
if (!Element.prototype.animate) {
  Element.prototype.animate = function () {
    return {
      cancel() {},
      finish() {},
      play() {},
      pause() {},
      reverse() {},
      commitStyles() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent() {
        return true
      },
      onfinish: null,
      oncancel: null,
      onremove: null,
      currentTime: 0,
      startTime: null,
      playbackRate: 1,
      playState: 'finished',
      replaceState: 'active',
      pending: false,
      id: '',
      effect: null,
      timeline: null,
      get finished() {
        return Promise.resolve()
      },
      get ready() {
        return Promise.resolve()
      }
    }
  } as unknown as Element['animate']
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
  updateBlockState: vi.fn(),
  updateTaskMeta: vi.fn(),
  saveConfig: vi.fn().mockResolvedValue(true)
}))

vi.mock('../../../settings/store.svelte', () => ({
  settings: mocks.settings,
  saveConfig: mocks.saveConfig
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
    updateTaskMeta: vi.fn(),
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
    priority: 3,
    pinned: 0,
    progress: 0,
    comments_count: 2,
    links_count: 1,
    tags: 'work/milestone-one'
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
    priority: 2,
    pinned: 1,
    progress: 50,
    comments_count: 0,
    links_count: 0,
    tags: ''
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
    priority: 1,
    pinned: 0,
    progress: 0,
    comments_count: 0,
    links_count: 0,
    tags: ''
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
    // PluginContext.sqliteQuery now returns {rows, truncated} (the SDK
    // shape mirroring Go's PluginRawQueryResult). Tests that don't care
    // about truncation can pass the rows straight through.
    mocks.sqliteQuery.mockImplementation(async () => ({
      rows: SAMPLE_ROWS,
      truncated: false
    }))
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

  it('clicking a card opens the detail panel', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    await fireEvent.click(todoCard!)

    // The slide-out detail panel renders as a dialog with the card title.
    const dialog = screen.getByRole('dialog')
    expect(dialog).toBeInTheDocument()
    expect(dialog.textContent).toContain('Write tests')
  })

  it('detail panel "Open in editor" dispatches navigate-to-block', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const handler = vi.fn()
    window.addEventListener('navigate-to-block', handler)

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    await fireEvent.click(todoCard!)

    const openBtn = screen.getByRole('button', { name: /open in editor/i })
    await fireEvent.click(openBtn)

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
    mocks.sqliteQuery.mockResolvedValue({
      rows: [{ ...SAMPLE_ROWS[0] }],
      truncated: false
    })
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const doingLane = screen.getByRole('group', { name: 'In Progress' })
    expect(doingLane.textContent).toContain('No in progress tasks')
  })

  it('keyboard Enter on a focused card opens the detail panel (a11y)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    todoCard!.focus()

    await fireEvent.keyDown(todoCard!, { key: 'Enter' })

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    // Enter must NOT trigger a status change.
    expect(mocks.updateBlockState).not.toHaveBeenCalled()
  })

  it('keyboard Space on a focused card opens the detail panel (a11y)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')
    expect(todoCard).toBeTruthy()
    todoCard!.focus()

    await fireEvent.keyDown(todoCard!, { key: ' ' })

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(mocks.updateBlockState).not.toHaveBeenCalled()
  })

  it('disables Notebook/Section/Page scope buttons when their context is missing', async () => {
    render(Kanban, {
      ctx: makeCtx({ activeNotebook: '', activeSection: '', activePage: '' }),
      manifest: MANIFEST
    })
    await flush()

    const vault = screen.getByRole('radio', { name: 'Vault' })
    const notebook = screen.getByRole('radio', { name: 'Notebook' })
    const section = screen.getByRole('radio', { name: 'Section' })
    const page = screen.getByRole('radio', { name: 'Page' })

    expect(vault).not.toBeDisabled()
    expect(notebook).toBeDisabled()
    expect(section).toBeDisabled()
    expect(page).toBeDisabled()
  })

  it('enables Notebook scope button when an active notebook is set', async () => {
    render(Kanban, {
      ctx: makeCtx({
        activeNotebook: 'Work',
        activeSection: '',
        activePage: ''
      }),
      manifest: MANIFEST
    })
    await flush()

    const notebook = screen.getByRole('radio', { name: 'Notebook' })
    const section = screen.getByRole('radio', { name: 'Section' })
    const page = screen.getByRole('radio', { name: 'Page' })

    expect(notebook).not.toBeDisabled()
    expect(section).toBeDisabled()
    expect(page).toBeDisabled()
  })

  it('surfaces a truncation banner when the Go cap is hit (vault-scope)', async () => {
    // sqliteQuery signals that the Go-side maxPluginQueryRows cap fired.
    mocks.sqliteQuery.mockResolvedValue({
      rows: SAMPLE_ROWS,
      truncated: true
    })
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    // The status region explains the cap + tells the user how to recover.
    const status = screen.getByRole('status')
    expect(status.textContent).toContain('5000')
    expect(status.textContent).toMatch(/narrow.*scope/i)
  })

  it('does not show the truncation banner when the result fits within the cap', async () => {
    mocks.sqliteQuery.mockResolvedValue({
      rows: SAMPLE_ROWS,
      truncated: false
    })
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('rapid scope switches do not clobber the freshest data (race guard)', async () => {
    // Simulate a stale page-scope response landing after a fresh
    // vault-scope response. We resolve calls in reverse-registration
    // order so the first call's promise settles last.
    let resolveFirst!: (v: { rows: unknown[]; truncated: boolean }) => void
    let resolveSecond!: (v: { rows: unknown[]; truncated: boolean }) => void
    mocks.sqliteQuery
      .mockImplementationOnce(
        () => new Promise((r) => (resolveFirst = r)) as any
      )
      .mockImplementationOnce(
        () => new Promise((r) => (resolveSecond = r)) as any
      )

    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // The first call (page-scope) is in flight; flip to vault and let
    // the second call (vault-scope) register.
    const vaultRadio = screen.getByRole('radio', { name: 'Vault' })
    await fireEvent.click(vaultRadio)
    await flush()

    // Now resolve them in stale-then-fresh order. Without the race
    // guard, the late-arriving page-scope data would clobber the
    // vault-scope data, leaving a "b.page = ?" WHERE clause's rows
    // showing for what the user asked to be the vault view.
    const staleRows = [
      {
        ...SAMPLE_ROWS[0],
        id: 'stale',
        clean_content: 'STALE PAGE TASK'
      }
    ]
    const freshRows = [
      { ...SAMPLE_ROWS[0], id: 'fresh', clean_content: 'FRESH VAULT TASK' }
    ]
    resolveSecond({
      rows: freshRows,
      truncated: false
    })
    await flush()
    resolveFirst({
      rows: staleRows,
      truncated: false
    })
    await flush()

    // The board should reflect the most recent call (vault), not the
    // late-arriving page-scope data.
    expect(screen.queryByText('STALE PAGE TASK')).not.toBeInTheDocument()
    expect(screen.getByText('FRESH VAULT TASK')).toBeInTheDocument()
  })

  it('owner filter narrows the result and the SQL includes t.owner IN (?)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.sqliteQuery.mockClear()

    // Open the Owner chip popover and select "Alice". Case-sensitive so
    // it matches the chip ("Owner") and not the card aria-labels that
    // contain lowercase "owner Alice".
    const ownerChip = screen.getByRole('button', { name: /Owner/ })
    await fireEvent.click(ownerChip)
    await flush()

    const aliceCheckbox = screen.getByLabelText('Alice')
    await fireEvent.click(aliceCheckbox)
    await flush()

    // The reload effect should have re-run with an owner IN clause.
    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql).toContain('t.owner IN (?')
    const params = mocks.sqliteQuery.mock.calls.at(-1)![1] as unknown[]
    expect(params).toContain('Alice')
  })

  it('priority filter narrows the result and the SQL includes t.priority IN (?)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.sqliteQuery.mockClear()

    // Open the Priority chip popover and select "Critical".
    const priorityChip = screen.getByRole('button', { name: /Priority/ })
    await fireEvent.click(priorityChip)
    await flush()

    const criticalCheckbox = screen.getByLabelText('Critical')
    await fireEvent.click(criticalCheckbox)
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql).toContain('t.priority IN (?')
    const params = mocks.sqliteQuery.mock.calls.at(-1)![1] as unknown[]
    expect(params).toContain(1)
  })

  it('clicking "+ Add column" prompts for a name and persists via saveConfig', async () => {
    const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('Backlog')
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.saveConfig.mockClear()

    const addBtn = screen.getByRole('button', { name: /add column/i })
    await fireEvent.click(addBtn)
    await flush()

    expect(promptSpy).toHaveBeenCalled()
    // The new column renders as a lane group.
    expect(screen.getByRole('group', { name: 'Backlog' })).toBeInTheDocument()
    // The column list was persisted to config.
    expect(mocks.saveConfig).toHaveBeenCalledTimes(1)
    promptSpy.mockRestore()
  })

  it('column more_horiz → Remove confirms and drops the lane', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.saveConfig.mockClear()

    // Three columns exist initially (TODO / DOING / DONE). Open the
    // actions menu on the first lane (To Do) and remove it.
    expect(screen.getByRole('group', { name: 'To Do' })).toBeInTheDocument()

    const menus = screen.getAllByRole('button', { name: 'Column actions' })
    await fireEvent.click(menus[0]!)
    await flush()

    const removeBtn = screen.getByRole('menuitem', { name: /remove/i })
    await fireEvent.click(removeBtn)
    await flush()

    expect(confirmSpy).toHaveBeenCalled()
    // The To Do lane is gone; the other two remain.
    expect(
      screen.queryByRole('group', { name: 'To Do' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('group', { name: 'In Progress' })
    ).toBeInTheDocument()
    expect(mocks.saveConfig).toHaveBeenCalledTimes(1)
    confirmSpy.mockRestore()
  })
})
