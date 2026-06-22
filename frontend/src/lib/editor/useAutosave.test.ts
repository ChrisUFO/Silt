import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AutosaveManager } from './useAutosave'
import type { AutosaveDeps } from './useAutosave'

// Mock the Wails IPC binding.
vi.mock('../../../wailsjs/go/main/App.js', () => ({
  SaveFileBlocks: vi.fn().mockResolvedValue(undefined)
}))

// Mock the perf budget utility.
vi.mock('../perf/frame-budget', () => ({
  measureFrameBudget: vi.fn((_label: string, fn: () => unknown) => fn())
}))

// Mock the converters.
vi.mock('./converters', () => ({
  docToBlocks: vi.fn(() => [{ id: 'block-1', type: 'NOTE', rawText: 'test' }])
}))

// Mock the notification store.
vi.mock('../../notifications/store.svelte', () => ({
  pushNotification: vi.fn()
}))

// Mock the plugin events.
vi.mock('../../plugins/events', () => ({
  dispatch: vi.fn()
}))

function makeDeps(overrides: Partial<AutosaveDeps> = {}): AutosaveDeps {
  return {
    getEditor: () => ({ getJSON: () => ({ type: 'doc', content: [] }) }) as any,
    getNotebook: () => 'Work',
    getSection: () => 'Journal',
    getPage: () => '2026-06-22',
    getDelay: () => 100,
    onUpdate: vi.fn(),
    onStateChange: vi.fn(),
    onSaveStateChange: vi.fn(),
    ...overrides
  }
}

describe('AutosaveManager', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('saves after the configured delay', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    autosave.trigger()

    // Not yet saved.
    expect(SaveFileBlocks).not.toHaveBeenCalled()

    // Advance past the delay.
    await vi.advanceTimersByTimeAsync(150)

    expect(SaveFileBlocks).toHaveBeenCalledWith('Work', 'Journal', '2026-06-22', expect.any(Array))
    expect(deps.onStateChange).toHaveBeenCalledWith(false, null)
    expect(deps.onUpdate).toHaveBeenCalled()
  })

  it('debounces rapid triggers', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    autosave.trigger()
    autosave.trigger()
    autosave.trigger()

    await vi.advanceTimersByTimeAsync(150)

    // Only one save despite three triggers.
    expect(SaveFileBlocks).toHaveBeenCalledTimes(1)
  })

  it('flush() saves immediately', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    autosave.trigger()
    await autosave.flush()

    expect(SaveFileBlocks).toHaveBeenCalledTimes(1)
  })

  it('flush() is a no-op when no save is pending', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    await autosave.flush()

    expect(SaveFileBlocks).not.toHaveBeenCalled()
  })

  it('reports errors via onStateChange', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    vi.mocked(SaveFileBlocks).mockRejectedValueOnce(new Error('disk full'))
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    autosave.trigger()
    await vi.advanceTimersByTimeAsync(150)

    // Wait for the async save to settle.
    await vi.advanceTimersByTimeAsync(0)

    expect(deps.onStateChange).toHaveBeenCalledWith(true, 'disk full')
  })

  it('markClean() resets state', () => {
    const deps = makeDeps()
    const autosave = new AutosaveManager(deps)

    autosave.markClean()

    expect(deps.onStateChange).toHaveBeenCalledWith(false, null)
  })

  it('does not save when editor is null', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    const deps = makeDeps({ getEditor: () => null })
    const autosave = new AutosaveManager(deps)

    autosave.trigger()
    await vi.advanceTimersByTimeAsync(150)

    expect(SaveFileBlocks).not.toHaveBeenCalled()
  })

  it('uses minimum delay of 50ms', async () => {
    const deps = makeDeps({ getDelay: () => 0 })
    const autosave = new AutosaveManager(deps)

    autosave.trigger()

    // At 30ms, should not have saved yet (min delay is 50ms).
    await vi.advanceTimersByTimeAsync(30)
    expect(deps.onUpdate).not.toHaveBeenCalled()

    // At 60ms, should have saved.
    await vi.advanceTimersByTimeAsync(30)
    expect(deps.onUpdate).toHaveBeenCalled()
  })

  it('reads current identity after a page rename (stale-capture regression)', async () => {
    const { SaveFileBlocks } = await import('../../../wailsjs/go/main/App.js')
    let currentPage = 'OldPage'
    const deps = makeDeps({ getPage: () => currentPage })
    const autosave = new AutosaveManager(deps)

    // Save with the original page name.
    autosave.trigger()
    await vi.advanceTimersByTimeAsync(150)
    expect(SaveFileBlocks).toHaveBeenCalledWith('Work', 'Journal', 'OldPage', expect.any(Array))

    // Simulate a rename: the getter now returns the new name.
    currentPage = 'RenamedPage'
    vi.mocked(SaveFileBlocks).mockClear()
    autosave.trigger()
    await vi.advanceTimersByTimeAsync(150)
    expect(SaveFileBlocks).toHaveBeenCalledWith('Work', 'Journal', 'RenamedPage', expect.any(Array))
  })
})
