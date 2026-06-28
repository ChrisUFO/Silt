import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  within
} from '@testing-library/svelte'

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
    },
    error: ''
  },
  sqliteQuery: vi.fn(),
  updateBlockState: vi.fn(),
  updateTaskMeta: vi.fn(),
  saveConfig: vi.fn().mockResolvedValue(true),
  updatePluginSetting: vi.fn().mockResolvedValue(true)
}))

vi.mock('../../../settings/store.svelte', () => ({
  settings: mocks.settings,
  saveConfig: mocks.saveConfig,
  updatePluginSetting: mocks.updatePluginSetting
}))

// Mock the Wails runtime so EventsOn (used by the linked-config:changed
// listener, #133) doesn't crash in jsdom (window.runtime is undefined).
vi.mock('../../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(() => () => {})
}))

import Kanban from './Kanban.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'
import { reactiveCtx, setNav, resetNav } from './reactiveCtx.svelte'
import {
  setScope,
  narrowScopeTo,
  resetKanbanStateForTests
} from './kanbanSharedState.svelte'

function makeCtx(overrides: Partial<PluginContext> = {}): PluginContext {
  // The default getPluginSettings returns the mock vault config's
  // silt-kanban entry (columns + filters), mirroring how the real Go binding
  // resolves the vault-scoped config for a vault notebook (#133). Individual
  // tests override this to simulate linked-notebook merge behavior.
  const defaultSettings =
    mocks.settings.config.plugins.plugin_settings['silt-kanban'] ?? {}
  return {
    activeNotebook: 'Work',
    activeSection: 'Journal',
    activePage: 'Daily',
    // Fixed local-day anchor so date-filter assertions are deterministic.
    today: '2026-06-16',
    sqliteQuery: mocks.sqliteQuery,
    updateBlockState: mocks.updateBlockState,
    mutateBlock: vi.fn(),
    updateTaskMeta: vi.fn(),
    getPluginSettings: vi.fn(() => Promise.resolve({ ...defaultSettings })),
    on: () => () => {},
    ...v2CtxStubs,
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
    // Reset the kanban plugin settings to defaults. Column add/remove
    // and filter tests mutate settings.config via persistColumns/
    // persistFilters; without this reset, those mutations leak into the
    // next test's initialColumns()/initialFilters() reads.
    mocks.settings.config.plugins.plugin_settings['silt-kanban'] = {
      default_col: 'TODO',
      columns: ['TODO', 'DOING', 'DONE']
    }
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

    // The status region shows the loaded count (derived from rows.length,
    // not a hard-coded literal) + tells the user how to recover.
    const status = screen.getByRole('status')
    expect(status.textContent).toContain(String(SAMPLE_ROWS.length))
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

  it('due-date "today" filter binds the LOCAL day, not UTC date("now") (#118)', async () => {
    // makeCtx pins today = '2026-06-16'. The fix replaced date('now') with a
    // bound param drawn from ctx.today so comparisons match the local day.
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.sqliteQuery.mockClear()

    const dueChip = screen.getByRole('button', { name: /Due date/ })
    await fireEvent.click(dueChip)
    await flush()

    const todayBtn = screen.getByRole('button', { name: 'Today' })
    await fireEvent.click(todayBtn)
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql).not.toContain("date('now')")
    expect(sql).toContain('t.due_date = ?')
    const params = mocks.sqliteQuery.mock.calls.at(-1)![1] as unknown[]
    expect(params).toContain('2026-06-16')
  })

  it('due-date "this week" filter uses a BETWEEN with local today + 7 (#118)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.sqliteQuery.mockClear()

    const dueChip = screen.getByRole('button', { name: /Due date/ })
    await fireEvent.click(dueChip)
    await flush()

    const weekBtn = screen.getByRole('button', { name: 'This Week' })
    await fireEvent.click(weekBtn)
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql).not.toContain("date('now')")
    expect(sql).toContain('t.due_date BETWEEN ? AND ?')
    const params = mocks.sqliteQuery.mock.calls.at(-1)![1] as unknown[]
    expect(params).toContain('2026-06-16')
    expect(params).toContain('2026-06-23')
  })

  it('clicking "+ Add column" prompts for a name and persists via updatePluginSetting (#120)', async () => {
    const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('Backlog')
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.updatePluginSetting.mockClear()

    const addBtn = screen.getByRole('button', { name: /add column/i })
    await fireEvent.click(addBtn)
    await flush()

    expect(promptSpy).toHaveBeenCalled()
    // The new column renders as a lane group.
    expect(screen.getByRole('group', { name: 'Backlog' })).toBeInTheDocument()
    // #120: the column list is persisted via the atomic per-plugin setter,
    // NOT the read-mutate-saveConfig dance that could clobber an external edit.
    expect(mocks.updatePluginSetting).toHaveBeenCalledTimes(1)
    expect(mocks.updatePluginSetting).toHaveBeenCalledWith(
      'silt-kanban',
      'columns',
      expect.arrayContaining(['Backlog'])
    )
    expect(mocks.saveConfig).not.toHaveBeenCalled()
    promptSpy.mockRestore()
  })

  it('column more_horiz → Remove confirms and drops the lane', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    mocks.updatePluginSetting.mockClear()

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
    expect(mocks.updatePluginSetting).toHaveBeenCalledTimes(1)
    confirmSpy.mockRestore()
  })

  it('pin button disables during the in-flight write to prevent concurrent toggles', async () => {
    let resolvePin!: (v: boolean) => void
    const updateTaskMeta = vi.fn(
      () => new Promise<boolean>((r) => (resolvePin = r))
    )
    render(Kanban, {
      ctx: makeCtx({ updateTaskMeta }),
      manifest: MANIFEST
    })
    await flush()

    // Open the detail panel.
    const card = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')!
    await fireEvent.click(card)
    await flush()

    const dialog = screen.getByRole('dialog')
    const pinBtn = within(dialog).getByRole('button', { name: /pin/i })
    expect(pinBtn).not.toBeDisabled()

    // Click pin — the optimistic state flips + the IPC call is dispatched.
    await fireEvent.click(pinBtn)
    await flush()

    // While the write is in-flight, the button is disabled so a second
    // rapid click can't race the Go-side file write (LockFileWrite
    // serializes per-file but preserves Go IPC arrival order, not JS
    // dispatch order — disabling the control prevents overlap entirely).
    expect(pinBtn).toBeDisabled()
    expect(updateTaskMeta).toHaveBeenCalledTimes(1)

    // A second click while disabled is a no-op.
    await fireEvent.click(pinBtn)
    expect(updateTaskMeta).toHaveBeenCalledTimes(1)

    // Resolve the write — the button re-enables.
    resolvePin(true)
    await flush()

    expect(pinBtn).not.toBeDisabled()
  })

  it('board reloads after a pin toggle in the detail panel (onMetaChanged)', async () => {
    const updateTaskMeta = vi.fn().mockResolvedValue(true)
    render(Kanban, {
      ctx: makeCtx({ updateTaskMeta }),
      manifest: MANIFEST
    })
    await flush()

    // Clear the initial-load query so we can isolate the reload.
    mocks.sqliteQuery.mockClear()

    // Open the detail panel and toggle pin.
    const card = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')!
    await fireEvent.click(card)
    await flush()

    const dialog = screen.getByRole('dialog')
    const pinBtn = within(dialog).getByRole('button', { name: /pin/i })
    await fireEvent.click(pinBtn)
    await flush()

    // updateTaskMeta resolved → onMetaChanged → reload() → sqliteQuery.
    expect(updateTaskMeta).toHaveBeenCalledTimes(1)
    expect(mocks.sqliteQuery).toHaveBeenCalledTimes(1)
  })

  it('dropping a card on a custom (non-status) column is a no-op', async () => {
    // Add a custom column alongside the standard statuses.
    mocks.settings.config.plugins.plugin_settings['silt-kanban'].columns = [
      'TODO',
      'DOING',
      'DONE',
      'Backlog'
    ]
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')!
    const backlogSection = screen.getByRole('group', { name: 'Backlog' })

    // Simulate a drag from To Do + drop on Backlog. The dragStart sets
    // the module-scoped dragCard; the drop fires onLaneDrop with
    // toStatus='Backlog', which the guard rejects.
    await fireEvent.dragStart(todoCard)
    await fireEvent.drop(backlogSection)
    await flush()

    // No status mutation dispatched — the Go handler never sees an
    // invalid status, and no error banner is shown.
    expect(mocks.updateBlockState).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('scope radiogroup supports arrow-key navigation (WAI-ARIA)', async () => {
    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    const radiogroup = screen.getByRole('radiogroup', { name: 'Board scope' })

    // Default scope is 'page' (activePage set). ArrowLeft moves to Section.
    mocks.sqliteQuery.mockClear()
    await fireEvent.keyDown(radiogroup, { key: 'ArrowLeft' })
    await flush()

    // The reload fired with a section-scope WHERE clause (no b.page).
    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql).toContain('b.section = ?')
    expect(sql).not.toContain('b.page = ?')

    // ArrowRight moves back to page scope.
    mocks.sqliteQuery.mockClear()
    await fireEvent.keyDown(radiogroup, { key: 'ArrowRight' })
    await flush()

    expect(mocks.sqliteQuery).toHaveBeenCalled()
    const sql2 = mocks.sqliteQuery.mock.calls.at(-1)![0] as string
    expect(sql2).toContain('b.page = ?')
  })

  it('progress slider disables during in-flight write', async () => {
    let resolveMeta!: (v: boolean) => void
    const updateTaskMeta = vi.fn(
      () => new Promise<boolean>((r) => (resolveMeta = r))
    )
    render(Kanban, {
      ctx: makeCtx({ updateTaskMeta }),
      manifest: MANIFEST
    })
    await flush()

    // Open detail panel.
    const card = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')!
    await fireEvent.click(card)
    await flush()

    const dialog = screen.getByRole('dialog')
    const slider = within(dialog).getByLabelText('Task progress')
    expect(slider).not.toBeDisabled()

    // Change the slider — IPC fires, control disables.
    await fireEvent.change(slider, { target: { value: '75' } })
    await flush()

    expect(slider).toBeDisabled()
    expect(updateTaskMeta).toHaveBeenCalledTimes(1)

    // Resolve — re-enables.
    resolveMeta(true)
    await flush()
    expect(slider).not.toBeDisabled()
  })

  it('moveSeq: earlier move failure does not revert a later move', async () => {
    // Move #1 (t1 TODO→DOING) will fail after a 50ms delay; move #2
    // (t1 DOING→DONE) resolves immediately. Without moveSeq, move #1's
    // revert would wipe move #2's optimistic state.
    mocks.updateBlockState
      .mockImplementationOnce(async () => {
        await new Promise((r) => setTimeout(r, 50))
        throw new Error('lock held')
      })
      .mockResolvedValueOnce(true)

    render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()

    // Move #1: TODO → DOING.
    const todoCard = screen
      .getByRole('group', { name: 'To Do' })
      .querySelector<HTMLElement>('[data-card]')!
    todoCard.focus()
    await fireEvent.keyDown(todoCard, { key: 'ArrowRight' })
    await flush()

    // Card "Write tests" is now in DOING (optimistic). Find it there.
    const doingLane = screen.getByRole('group', { name: 'In Progress' })
    const movedCard = Array.from(
      doingLane.querySelectorAll<HTMLElement>('[data-card]')
    ).find((el) => el.textContent?.includes('Write tests'))!
    movedCard.focus()

    // Move #2: DOING → DONE (resolves immediately).
    await fireEvent.keyDown(movedCard, { key: 'ArrowRight' })

    // Wait for move #1's delayed rejection to settle.
    await new Promise((r) => setTimeout(r, 100))
    await flush()

    // Card should be in DONE (move #2), NOT reverted by move #1's failure.
    const doneLane = screen.getByRole('group', { name: 'Done' })
    const doneHasCard = Array.from(
      doneLane.querySelectorAll('[data-card]')
    ).some((el) => el.textContent?.includes('Write tests'))
    expect(doneHasCard).toBe(true)
    // No error banner — the stale failure was suppressed by moveSeq.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  describe('scope auto-narrow (#124)', () => {
    // These tests drive navigation via a $state-backed ctx (reactiveCtx) so
    // the board's reactive scope effect tracks the change without a remount.
    beforeEach(() => {
      resetNav()
      mocks.sqliteQuery.mockReset()
      mocks.sqliteQuery.mockImplementation(async () => ({
        rows: SAMPLE_ROWS,
        truncated: false
      }))
    })

    const checkedScope = (): string => {
      const group = screen.getByRole('radiogroup', { name: 'Board scope' })
      const checked = within(group)
        .getAllByRole('radio')
        .find((r) => r.getAttribute('aria-checked') === 'true')
      return (checked?.textContent ?? '').trim().toLowerCase()
    }

    it('follows navigation: narrows to page when a page becomes active', async () => {
      setNav({ notebook: 'Work', section: '', page: '' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery),
        manifest: MANIFEST
      })
      await flush()
      // No section/page active -> most-specific scope is notebook.
      expect(checkedScope()).toBe('notebook')

      // Navigate into a page (same as a sidebar click — no remount).
      setNav({ section: 'Journal', page: 'Daily' })
      await flush()
      expect(checkedScope()).toBe('page')
    })

    it('sticks after a manual override; the Follow affordance appears', async () => {
      setNav({ notebook: 'Work', section: 'Journal', page: 'Daily' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery),
        manifest: MANIFEST
      })
      await flush()
      expect(checkedScope()).toBe('page')

      // Manual override to Vault.
      await fireEvent.click(screen.getByRole('radio', { name: /Vault/ }))
      await flush()
      expect(checkedScope()).toBe('vault')
      // The reset affordance appears once the user has overridden.
      expect(
        screen.getByRole('button', {
          name: /Reset board scope to follow navigation/
        })
      ).toBeTruthy()

      // Navigate to a different page — scope must NOT re-narrow.
      setNav({ page: 'OtherPage' })
      await flush()
      expect(checkedScope()).toBe('vault')
    })

    it('reset re-enables auto-follow after an override', async () => {
      setNav({ notebook: 'Work', section: 'Journal', page: 'Daily' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery),
        manifest: MANIFEST
      })
      await flush()

      // Override to Vault, then reset via the Follow affordance.
      await fireEvent.click(screen.getByRole('radio', { name: /Vault/ }))
      await flush()
      await fireEvent.click(
        screen.getByRole('button', {
          name: /Reset board scope to follow navigation/
        })
      )
      await flush()
      // Back to the active page (auto-follow re-enabled).
      expect(checkedScope()).toBe('page')

      // Navigate away to notebook level — follows again.
      setNav({ section: '', page: '' })
      await flush()
      expect(checkedScope()).toBe('notebook')
    })
  })

  describe('per-active-notebook settings (#133)', () => {
    it('re-resolves columns from getPluginSettings on notebook switch', async () => {
      // The vault notebook starts with TODO/DOING/DONE (from the synchronous
      // vault-config init). The async resolution fires on mount but the
      // write-guard skips the assignment (same data = no-op). On the switch
      // to the linked notebook, the resolution returns different data
      // (Backlog/Done) and the guard lets the write through.
      const vaultSettings = { columns: ['TODO', 'DOING', 'DONE'] }
      const linkedSettings = { columns: ['Backlog', 'Done'] }
      const getPluginSettings = vi
        .fn()
        .mockResolvedValueOnce(vaultSettings) // mount on Work
        .mockResolvedValue(linkedSettings) // subsequent calls (switch to Ext)

      setNav({ notebook: 'Work', section: '', page: '' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery, {
          getPluginSettings
        }),
        manifest: MANIFEST
      })
      await flush()

      // Vault notebook: standard columns (the mount resolution returned the
      // same data, so the write-guard skipped the assignment — no flicker).
      expect(screen.getByRole('group', { name: 'To Do' })).toBeInTheDocument()

      // Navigate to the linked notebook — the re-resolution effect fires
      // and overrides columns from getPluginSettings.
      setNav({ notebook: 'Ext', section: '', page: '' })
      await flush()

      // The linked notebook's columns (Backlog/Done) replace the vault's.
      expect(screen.getByRole('group', { name: 'Backlog' })).toBeInTheDocument()
      expect(screen.getByRole('group', { name: 'Done' })).toBeInTheDocument()
      expect(
        screen.queryByRole('group', { name: 'To Do' })
      ).not.toBeInTheDocument()
    })

    it('resolves linked overrides on mount when opened directly on a linked notebook', async () => {
      // Regression: if the app opens directly on a linked notebook (no
      // navigation after mount), the async resolution must still fire and
      // apply the co-located overrides. The old settingsFirstRun skip
      // prevented this — the user saw vault-default columns until they
      // navigated away and back.
      const linkedSettings = { columns: ['Backlog', 'Review', 'Done'] }
      const getPluginSettings = vi.fn().mockResolvedValue(linkedSettings)

      // Mount DIRECTLY on the linked notebook (no vault-first render).
      setNav({ notebook: 'Ext', section: '', page: '' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery, {
          getPluginSettings
        }),
        manifest: MANIFEST
      })
      await flush()

      // The linked overrides (Backlog/Review/Done) appear — not the vault
      // defaults (TODO/DOING/DONE).
      expect(screen.getByRole('group', { name: 'Backlog' })).toBeInTheDocument()
      expect(
        screen.queryByRole('group', { name: 'To Do' })
      ).not.toBeInTheDocument()

      // The resolution fired on mount (not deferred to a navigation event).
      expect(getPluginSettings).toHaveBeenCalled()
    })

    it('surfaces a getPluginSettings rejection as the board error banner', async () => {
      // The Go binding rejects when a co-located linked config is unparseable
      // (GetPluginSettingsForNotebook returns a real error, by design). The
      // re-resolution effect must catch that rejection — otherwise it becomes
      // an unhandled promise rejection and the board sits silently half-loaded
      // with no signal that the user's config file is broken. Route the error
      // into errorMsg so the existing error banner surfaces it.
      const getPluginSettings = vi
        .fn()
        .mockResolvedValueOnce({ columns: ['TODO', 'DOING', 'DONE'] }) // mount on Work (ok)
        .mockRejectedValue(
          // switch to Ext (broken co-located config)
          new Error('linked config for Ext: yaml: line 1: bad')
        )

      setNav({ notebook: 'Work', section: '', page: '' })
      render(Kanban, {
        ctx: reactiveCtx(mocks.sqliteQuery, { getPluginSettings }),
        manifest: MANIFEST
      })
      await flush()

      // Vault notebook loads normally (mount resolution returned same data,
      // write-guard skipped the assignment).
      expect(screen.getByRole('group', { name: 'To Do' })).toBeInTheDocument()

      // Switch to the linked notebook with the broken co-located config.
      setNav({ notebook: 'Ext', section: '', page: '' })
      await flush()

      // The rejection is caught and routed into the error banner (the board
      // view is replaced by the error message, matching the fail-loud contract
      // #133 adopts for unparseable configs).
      const banner = await screen.findByText(
        /linked config for Ext: yaml: line 1: bad/
      )
      expect(banner).toBeInTheDocument()
      // The board lanes are NOT rendered while the error banner is shown.
      expect(
        screen.queryByRole('group', { name: 'To Do' })
      ).not.toBeInTheDocument()
    })

    it('writes still persist to the vault config via updatePluginSetting', async () => {
      // The co-located config is READ-ONLY; user mutations persist to vault.
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
      mocks.updatePluginSetting.mockClear()
      render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()

      // Open the column actions menu on the first lane and remove it.
      const menus = screen.getAllByRole('button', { name: 'Column actions' })
      await fireEvent.click(menus[0]!)
      await flush()

      const removeBtn = screen.getByRole('menuitem', { name: /remove/i })
      await fireEvent.click(removeBtn)
      await flush()

      // The mutation persists via updatePluginSetting (vault-scoped, #120),
      // not via any co-located-config write path.
      expect(confirmSpy).toHaveBeenCalled()
      expect(mocks.updatePluginSetting).toHaveBeenCalledWith(
        'silt-kanban',
        'columns',
        expect.arrayContaining(['DOING', 'DONE'])
      )
      // The removed column (To Do / TODO) is NOT in the persisted set.
      const persistedCall = mocks.updatePluginSetting.mock.calls.find(
        ([key]) => key === 'silt-kanban'
      )
      expect(persistedCall?.[2]).not.toContain('TODO')
      confirmSpy.mockRestore()
    })
  })

  // --- #323 shared state — Kanban.svelte reacts to shared-state writes --
  // Verifies the #323 contract: writes from the sidebar (via
  // kanbanSharedState) propagate to Kanban.svelte's reload effect without
  // needing a direct event. The shared module is the single source of
  // truth; this test pins the bidirectional plumbing without requiring
  // both components to be mounted at once.
  describe('shared state reactivity (#323)', () => {
    it('imports kanbanSharedState and reflects writes from the sidebar', async () => {
      // First mount the Kanban component so it hydrates the shared module.
      mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
      render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()
      // Re-import here so the test's vi.mock for the settings store is
      // already in place.
      const { setFilters: setSharedFilters } =
        await import('./kanbanSharedState.svelte')
      // Write a new filter from the "sidebar side"; Kanban.svelte's
      // existing $effect on `filters` should pick it up and re-query.
      const before = mocks.sqliteQuery.mock.calls.length
      setSharedFilters({
        owners: [],
        priorities: [1],
        dueDate: '',
        tags: []
      })
      await flush()
      // The reload $effect fired — at least one new sqliteQuery.
      expect(mocks.sqliteQuery.mock.calls.length).toBeGreaterThan(before)
    })
  })

  // Pins the #323 persistence contract: only USER-initiated scope picks
  // hit config.yaml. Navigation auto-narrow writes (narrowScopeTo) leave
  // scopeUserOverride false and must not churn the persisted value.
  describe('scope persistence — user picks only (#323)', () => {
    beforeEach(async () => {
      vi.useFakeTimers()
      mocks.sqliteQuery.mockReset()
      mocks.sqliteQuery.mockResolvedValue({
        rows: SAMPLE_ROWS,
        truncated: false
      })
      render(Kanban, { ctx: makeCtx(), manifest: MANIFEST })
      // Let mount effects settle, then reset the shared state so each
      // test starts from a known override=false baseline (narrowScopeTo
      // is a no-op while the override is set, which would mask the bug).
      await tick()
      await vi.advanceTimersByTimeAsync(0)
      resetKanbanStateForTests()
      await tick()
      await vi.advanceTimersByTimeAsync(0)
      mocks.updatePluginSetting.mockClear()
    })

    afterEach(() => {
      cleanup()
      vi.useRealTimers()
    })

    it('persists a user-initiated scope pick after the 500ms debounce', async () => {
      // setScope is the USER path — flips scopeUserOverride so the
      // persistence effect writes through.
      setScope('notebook')
      await tick()

      // Within the debounce window — nothing persisted yet.
      expect(mocks.updatePluginSetting).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(500)

      expect(mocks.updatePluginSetting).toHaveBeenCalledTimes(1)
      expect(mocks.updatePluginSetting).toHaveBeenCalledWith(
        'silt-kanban',
        'scope',
        'notebook'
      )
    })

    it('does not persist navigation-driven auto-narrow writes', async () => {
      // narrowScopeTo is the NAV path — mutates scope WITHOUT flipping
      // scopeUserOverride; the persistence effect must skip it.
      narrowScopeTo('page')
      await tick()

      // Well past the 500ms debounce — no scope write should have fired.
      await vi.advanceTimersByTimeAsync(5_000)

      const scopeCall = mocks.updatePluginSetting.mock.calls.find(
        ([, key]) => key === 'scope'
      )
      expect(scopeCall).toBeUndefined()
    })
  })
})
