// Regression coverage for the PluginContext pin/progress sentinel translation
// (#123) and the per-active-notebook settings resolver (#133). The Go
// bindings PluginUpdateTaskMeta + GetPluginSettingsForNotebook take raw
// args; the SDK wrapper in context.ts must translate the ergonomic API onto
// them exactly. Never hit real IPC — mock the Wails bindings (AGENTS.md
// canonical pattern).

import { describe, expect, it, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  pluginUpdateTaskMeta: vi.fn(() => Promise.resolve(true)),
  pluginRawQuery: vi.fn(() => Promise.resolve({ rows: [], truncated: false })),
  pluginMutateBlock: vi.fn(() => Promise.resolve(true)),
  pluginUpdateBlockState: vi.fn(() => Promise.resolve(true)),
  getPluginSettingsForNotebook: vi.fn(() => Promise.resolve({})),
  getActiveLocation: vi.fn(() => ({
    notebook: 'Work',
    section: 'Journal',
    page: 'Daily'
  }))
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  PluginRawQuery: mocks.pluginRawQuery,
  PluginMutateBlock: mocks.pluginMutateBlock,
  PluginUpdateBlockState: mocks.pluginUpdateBlockState,
  PluginUpdateTaskMeta: mocks.pluginUpdateTaskMeta,
  GetPluginSettingsForNotebook: mocks.getPluginSettingsForNotebook
}))

vi.mock('./location.svelte', () => ({
  getActiveLocation: mocks.getActiveLocation
}))

import { makePluginContext } from './context'

describe('makePluginContext — updateTaskMeta sentinel translation', () => {
  beforeEach(() => {
    mocks.pluginUpdateTaskMeta.mockClear()
  })

  it('maps pin true → 1', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { pinned: true })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 1, -1)
  })

  it('maps pin false → 0 (explicit [pin:: false], #123)', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { pinned: false })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 0, -1)
  })

  it('maps pin null → -2 (clear the token, #123)', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { pinned: null })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -2, -1)
  })

  it('maps omitted pin → -1 (no change)', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { progress: 50 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -1, 50)
  })

  it('maps progress undefined → -1, number → itself', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { progress: 75 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -1, 75)
  })

  it('maps both fields together', async () => {
    const ctx = makePluginContext('test-plugin')
    await ctx.updateTaskMeta('b1', { pinned: true, progress: 100 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 1, 100)
  })
})

describe('makePluginContext — getPluginSettings (#133)', () => {
  beforeEach(() => {
    mocks.getPluginSettingsForNotebook.mockClear()
    mocks.getActiveLocation.mockReset()
  })

  it('calls GetPluginSettingsForNotebook with the captured pluginID + live notebook', async () => {
    // The real getActiveLocation returns the SAME $state-backed object on
    // every call; its properties mutate in place (#69). Mirror that here so
    // the context's captured `loc` reference sees live navigation changes.
    const loc = { notebook: 'Work', section: 'Journal', page: 'Daily' }
    mocks.getActiveLocation.mockReturnValue(loc)
    mocks.getPluginSettingsForNotebook.mockResolvedValue({ columns: ['TODO'] })
    const ctx = makePluginContext('silt-kanban')
    const got = await ctx.getPluginSettings()
    expect(mocks.getPluginSettingsForNotebook).toHaveBeenCalledWith(
      'silt-kanban',
      'Work'
    )
    expect(got).toEqual({ columns: ['TODO'] })
  })

  it('normalizes a null/undefined response to an empty object', async () => {
    mocks.getActiveLocation.mockReturnValue({
      notebook: 'Work',
      section: '',
      page: ''
    })
    mocks.getPluginSettingsForNotebook.mockResolvedValue(undefined)
    const ctx = makePluginContext('p')
    const got = await ctx.getPluginSettings()
    expect(got).toEqual({})
  })

  it('reads the live active notebook at call time (not capture time)', async () => {
    // Simulate in-app navigation by mutating the SAME $state-backed object
    // the context captured (mirrors how location.svelte.ts works: the
    // object is stable, its properties change).
    const loc = { notebook: 'Work', section: 'Journal', page: 'Daily' }
    mocks.getActiveLocation.mockReturnValue(loc)
    mocks.getPluginSettingsForNotebook.mockResolvedValue({})
    const ctx = makePluginContext('silt-kanban')

    // Navigate to a linked notebook AFTER context construction. The reactive
    // getter must reflect the new value at the next getPluginSettings call.
    loc.notebook = 'Linked'
    await ctx.getPluginSettings()
    expect(mocks.getPluginSettingsForNotebook).toHaveBeenCalledWith(
      'silt-kanban',
      'Linked'
    )
  })
})
