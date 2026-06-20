// Decoration provider registry tests (#110, #158).
import { describe, expect, it, beforeEach, vi } from 'vitest'

// Mock the grants module so the registry-internal gate can be controlled
// per-test without hitting wailsjs IPC (#158).
vi.mock('../../plugins/grants.svelte', () => ({
  isGranted: vi.fn(() => true),
  initGrants: vi.fn(),
  refreshGrants: vi.fn(),
  resetGrantsForTests: vi.fn(),
  setGrantsForTests: vi.fn()
}))

import {
  registerDecorationProvider,
  unregisterPluginDecorations,
  computeDecorations,
  resetDecorationsForTests
} from './decorations'
import { isGranted } from '../../plugins/grants.svelte'

describe('decoration provider registry (#110, #158)', () => {
  beforeEach(() => {
    resetDecorationsForTests()
    vi.mocked(isGranted).mockReturnValue(true)
  })

  it('registers a provider and computes decorations', () => {
    const off = registerDecorationProvider('highlights', 'my-plugin', () => [
      { from: 0, to: 5, class: 'hl' }
    ])
    const decos = computeDecorations({ content: [] })
    expect(decos).toHaveLength(1)
    expect(decos[0].class).toBe('hl')
    off()
  })

  it('unregister removes the provider', () => {
    const off = registerDecorationProvider('x', 'p', () => [{ from: 0, to: 1 }])
    off()
    expect(computeDecorations({ content: [] })).toHaveLength(0)
  })

  it('unregisterPluginDecorations removes all providers for a plugin', () => {
    registerDecorationProvider('a', 'p', () => [{ from: 0, to: 1 }])
    registerDecorationProvider('b', 'p', () => [{ from: 0, to: 1 }])
    registerDecorationProvider('c', 'q', () => [{ from: 0, to: 1 }])
    unregisterPluginDecorations('p')
    expect(computeDecorations({ content: [] })).toHaveLength(1)
  })

  it('a throwing provider does not break the editor', () => {
    registerDecorationProvider('bad', 'p', () => {
      throw new Error('boom')
    })
    registerDecorationProvider('good', 'p', () => [
      { from: 0, to: 1, class: 'ok' }
    ])
    const decos = computeDecorations({ content: [] })
    expect(decos).toHaveLength(1)
    expect(decos[0].class).toBe('ok')
  })

  // --- #158: registry-internal capability gate -------------------------------

  it('refuses providers without editor-schema grant', () => {
    vi.mocked(isGranted).mockReturnValue(false)
    const off = registerDecorationProvider('blocked', 'ungranted', () => [
      { from: 0, to: 1 }
    ])
    off()
    expect(computeDecorations({ content: [] })).toHaveLength(0)
  })
})
