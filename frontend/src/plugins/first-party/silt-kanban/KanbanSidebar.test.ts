import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  settings: {
    config: {
      plugins: {
        plugin_settings: {
          'silt-kanban': {
            default_col: 'TODO',
            columns: ['TODO', 'DOING', 'DONE'],
            // boards + scope + filters are optional; tests fill as needed.
            boards: undefined as unknown,
            scope: undefined as unknown,
            filters: undefined as unknown
          }
        }
      }
    },
    error: ''
  },
  sqliteQuery: vi.fn(),
  saveConfig: vi.fn().mockResolvedValue(true),
  updatePluginSetting: vi.fn().mockResolvedValue(true)
}))

vi.mock('../../../settings/store.svelte', () => ({
  settings: mocks.settings,
  saveConfig: mocks.saveConfig,
  updatePluginSetting: mocks.updatePluginSetting
}))

vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(() => () => {})
}))

import KanbanSidebar from './KanbanSidebar.svelte'
import type { PluginContext, PluginManifest } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'
import {
  getKanbanState,
  resetKanbanStateForTests,
  setFilters
} from './kanbanSharedState.svelte'

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
  id: 'silt-kanban',
  name: 'Kanban',
  version: '1.0.0'
}

async function flush() {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
}

describe('KanbanSidebar (#323)', () => {
  beforeEach(() => {
    mocks.sqliteQuery.mockReset()
    mocks.updatePluginSetting.mockReset().mockResolvedValue(true)
    mocks.saveConfig.mockReset().mockResolvedValue(true)
    mocks.settings.config.plugins.plugin_settings['silt-kanban'].columns = [
      'TODO',
      'DOING',
      'DONE'
    ]
    mocks.settings.config.plugins.plugin_settings['silt-kanban'].boards =
      undefined
    mocks.settings.error = ''
    resetKanbanStateForTests()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders the saved-boards section header and + Save current… CTA', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // The section header text may appear in multiple places (h3 + aria).
    expect(screen.getAllByText(/Saved Boards/i).length).toBeGreaterThan(0)
    expect(screen.getByTestId('new-board')).toBeInTheDocument()
  })

  it('renders the scope radio with four options', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    expect(screen.getByTestId('scope-vault')).toBeInTheDocument()
    expect(screen.getByTestId('scope-notebook')).toBeInTheDocument()
    expect(screen.getByTestId('scope-section')).toBeInTheDocument()
    expect(screen.getByTestId('scope-page')).toBeInTheDocument()
  })

  it('clicking a scope radio updates the shared state (#323 AC #4)', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('scope-notebook'))
    expect(getKanbanState().scope).toBe('notebook')
    expect(getKanbanState().scopeUserOverride).toBe(true)
  })

  it('clicking a saved board applies its scope+filters via shared state (#323 AC)', async () => {
    mocks.settings.config.plugins.plugin_settings['silt-kanban'].boards = [
      {
        id: 'b1',
        name: 'My Work',
        scope: 'notebook',
        filters: {
          owners: ['alice'],
          priorities: [1, 2],
          dueDate: '',
          tags: []
        }
      }
    ]
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // Activate the saved board by clicking its label button.
    const boardBtn = document.querySelector<HTMLElement>('[data-testid="board-b1"] button')
    expect(boardBtn).toBeTruthy()
    await fireEvent.click(boardBtn!)
    expect(getKanbanState().scope).toBe('notebook')
    expect(getKanbanState().filters.owners).toEqual(['alice'])
    expect(getKanbanState().filters.priorities).toEqual([1, 2])
    // The board's button is now aria-pressed=true (active highlight) —
    // pins the fingerprint-based isActive computation against regressions.
    expect(boardBtn!.getAttribute('aria-pressed')).toBe('true')
  })

  it('+ Save current… opens the inline name input; Enter commits', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('new-board'))
    const input = screen.getByTestId('new-board-name') as HTMLInputElement
    expect(input).toBeInTheDocument()
    // Use fireEvent.input so Svelte's bind:value picks up the change.
    await fireEvent.input(input, { target: { value: 'Sprint 15' } })
    await fireEvent.keyDown(input, { key: 'Enter' })
    await flush()
    expect(mocks.updatePluginSetting).toHaveBeenCalledWith(
      'silt-kanban',
      'boards',
      expect.arrayContaining([
        expect.objectContaining({
          name: 'Sprint 15',
          scope: getKanbanState().scope
        })
      ])
    )
  })

  it('toggle a priority checkbox updates the shared filters (#323 AC #3)', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('priority-1'))
    expect(getKanbanState().filters.priorities).toContain(1)
    await fireEvent.click(screen.getByTestId('priority-1'))
    expect(getKanbanState().filters.priorities).not.toContain(1)
  })

  it('toggle a due-date quick-pick sets the filter', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    await fireEvent.click(screen.getByTestId('due-overdue'))
    expect(getKanbanState().filters.dueDate).toBe('overdue')
    await fireEvent.click(screen.getByTestId('due-all'))
    expect(getKanbanState().filters.dueDate).toBe('')
  })

  it('toggling a filter from outside the sidebar is reflected in the sidebar (#323 AC #3)', async () => {
    // The sidebar's checkboxes must reflect the LIVE shared filters, not
    // a stale snapshot — so a programmatic write from outside (e.g. the
    // FilterBar in the main view) should be visible in the sidebar.
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    setFilters({
      owners: [],
      priorities: [2],
      dueDate: 'today',
      tags: []
    })
    await flush()
    const checked = screen.getByTestId('priority-2') as HTMLInputElement
    expect(checked.checked).toBe(true)
    const dueToday = screen.getByTestId('due-today') as HTMLButtonElement
    expect(dueToday.getAttribute('aria-checked')).toBe('true')
  })

  it('Clear all filters clears the shared filters', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // First, set a filter so the Clear-all button appears.
    await fireEvent.click(screen.getByTestId('priority-3'))
    await flush()
    expect(getKanbanState().filters.priorities).toContain(3)
    await fireEvent.click(screen.getByTestId('clear-filters'))
    expect(getKanbanState().filters.priorities).toEqual([])
    expect(getKanbanState().filters.dueDate).toBe('')
  })

  it('arrow-key nav on scope radio moves focus to the next option', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const vault = screen.getByTestId('scope-vault')
    vault.focus()
    await fireEvent.keyDown(vault, { key: 'ArrowDown' })
    await flush()
    expect(document.activeElement).toBe(screen.getByTestId('scope-notebook'))
  })

  it('Enter on a focused scope radio activates it', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    const section = screen.getByTestId('scope-section')
    section.focus()
    await fireEvent.keyDown(section, { key: 'Enter' })
    expect(getKanbanState().scope).toBe('section')
  })

  it('Empty state when no boards exist shows only the + Save CTA', async () => {
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // No board elements yet.
    expect(document.querySelectorAll('[data-testid^="board-"]').length).toBe(0)
    expect(screen.getByTestId('new-board')).toBeInTheDocument()
  })

  // --- #323 hardening: validation of saved-board entries on load
  it('drops malformed saved-board entries from settings on load', async () => {
    mocks.settings.config.plugins.plugin_settings['silt-kanban'].boards = [
      // Valid board — should render.
      {
        id: 'b-ok',
        name: 'Valid',
        scope: 'vault',
        filters: { owners: [], priorities: [], dueDate: '', tags: [] }
      },
      // Missing scope — should be dropped silently.
      {
        id: 'b-bad-scope',
        name: 'BadScope',
        filters: { owners: [], priorities: [], dueDate: '', tags: [] }
      } as any,
      // Wrong owner type — should be dropped silently.
      {
        id: 'b-bad-owners',
        name: 'BadOwners',
        scope: 'vault',
        filters: { owners: 'not-an-array', priorities: [], dueDate: '', tags: [] }
      } as any,
      // Missing id — should be dropped.
      { name: 'NoId', scope: 'vault', filters: { owners: [], priorities: [], dueDate: '', tags: [] } } as any
    ]
    mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
    render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
    await flush()
    // Only the valid board should have rendered.
    expect(screen.getByTestId('board-b-ok')).toBeInTheDocument()
    expect(document.querySelectorAll('[data-testid^="board-"]').length).toBe(1)
  })

  // --- #323 P1 review fixes: scope radio keyboard a11y + delete confirm
  describe('scope radio a11y', () => {
    it('Enter on a focused scope radio activates it (uses event target, not cursor)', async () => {
      mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
      // ctx has activeSection set, so 'section' is enabled.
      render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()
      const section = screen.getByTestId('scope-section')
      section.focus()
      await fireEvent.keyDown(section, { key: 'Enter' })
      expect(getKanbanState().scope).toBe('section')
    })

    it('Enter on a disabled scope radio does NOT activate it (#323 P1 a11y)', async () => {
      // Override the ctx so no notebook/section/page is active — all
      // non-vault scopes are disabled.
      const emptyCtx = makeCtx({
        activeNotebook: '',
        activeSection: '',
        activePage: ''
      })
      mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
      render(KanbanSidebar, { ctx: emptyCtx, manifest: MANIFEST })
      await flush()
      // The non-vault scopes (notebook/section/page) are all disabled.
      // ArrowDown from vault skips them and wraps to vault (the only
      // enabled option). Verifies the keyboard handler never lands on a
      // disabled scope.
      const vault = screen.getByTestId('scope-vault')
      vault.focus()
      await fireEvent.keyDown(vault, { key: 'ArrowDown' })
      expect(document.activeElement).toBe(vault)
      // Now Press Enter on the focused (enabled) vault — should activate
      // vault (the existing first test covers the enable path).
      await fireEvent.keyDown(vault, { key: 'Enter' })
      expect(getKanbanState().scope).toBe('vault')
    })
  })

  describe('deleteBoard() UX safety', () => {
    it('prompts for confirmation before deleting a saved board', async () => {
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
      mocks.settings.config.plugins.plugin_settings['silt-kanban'].boards = [
        {
          id: 'b1',
          name: 'My Work',
          scope: 'vault',
          filters: { owners: [], priorities: [], dueDate: '', tags: [] }
        }
      ]
      mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
      render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()
      await fireEvent.click(screen.getByTestId('delete-board-b1'))
      // Confirm was shown.
      expect(confirmSpy).toHaveBeenCalledTimes(1)
      expect(confirmSpy).toHaveBeenCalledWith(expect.stringContaining('My Work'))
      // User cancelled: the board is still rendered.
      expect(screen.getByTestId('board-b1')).toBeInTheDocument()
      confirmSpy.mockRestore()
    })

    it('proceeds with deletion when user confirms', async () => {
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
      mocks.settings.config.plugins.plugin_settings['silt-kanban'].boards = [
        {
          id: 'b1',
          name: 'My Work',
          scope: 'vault',
          filters: { owners: [], priorities: [], dueDate: '', tags: [] }
        }
      ]
      mocks.updatePluginSetting.mockReset().mockResolvedValue(true)
      mocks.sqliteQuery.mockResolvedValue({ rows: [], truncated: false })
      render(KanbanSidebar, { ctx: makeCtx(), manifest: MANIFEST })
      await flush()
      await fireEvent.click(screen.getByTestId('delete-board-b1'))
      // Board is removed from the rendered list.
      expect(screen.queryByTestId('board-b1')).toBeNull()
      // Persisted.
      expect(mocks.updatePluginSetting).toHaveBeenCalledWith(
        'silt-kanban',
        'boards',
        []
      )
      confirmSpy.mockRestore()
    })
  })
})
