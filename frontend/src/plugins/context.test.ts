// Regression coverage for the PluginContext pin/progress sentinel translation
// (#123). The Go binding PluginUpdateTaskMeta takes int sentinels; the SDK
// wrapper in context.ts must map the ergonomic tri-state pin (true/false/null/
// undefined) and progress (number/undefined) onto them exactly. Never hit real
// IPC — mock the Wails bindings (AGENTS.md canonical pattern).

import { describe, expect, it, beforeEach, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  pluginUpdateTaskMeta: vi.fn(() => Promise.resolve(true)),
  pluginRawQuery: vi.fn(() => Promise.resolve({ rows: [], truncated: false })),
  pluginMutateBlock: vi.fn(() => Promise.resolve(true)),
  pluginUpdateBlockState: vi.fn(() => Promise.resolve(true)),
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
  PluginUpdateTaskMeta: mocks.pluginUpdateTaskMeta
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
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { pinned: true })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 1, -1)
  })

  it('maps pin false → 0 (explicit [pin:: false], #123)', async () => {
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { pinned: false })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 0, -1)
  })

  it('maps pin null → -2 (clear the token, #123)', async () => {
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { pinned: null })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -2, -1)
  })

  it('maps omitted pin → -1 (no change)', async () => {
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { progress: 50 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -1, 50)
  })

  it('maps progress undefined → -1, number → itself', async () => {
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { progress: 75 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', -1, 75)
  })

  it('maps both fields together', async () => {
    const ctx = makePluginContext()
    await ctx.updateTaskMeta('b1', { pinned: true, progress: 100 })
    expect(mocks.pluginUpdateTaskMeta).toHaveBeenCalledWith('b1', 1, 100)
  })
})
