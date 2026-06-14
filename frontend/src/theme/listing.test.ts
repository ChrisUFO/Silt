// Vitest coverage for the theme listing store (#74). The store
// fetches the picker listing once on init and re-fetches whenever the
// backend emits "themes:changed" (after a successful import).

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'

const { listThemesMock, eventsOnMock, injectTokensMock } = vi.hoisted(() => ({
  listThemesMock: vi.fn(),
  eventsOnMock: vi.fn(() => () => {}),
  injectTokensMock: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ApplyTheme: vi.fn(),
  GetActiveTheme: vi.fn(),
  ListThemes: listThemesMock,
  ExportActiveTheme: vi.fn(),
  ImportTheme: vi.fn(),
  PickExportPath: vi.fn(),
  PickThemeFile: vi.fn()
}))
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: eventsOnMock,
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))
vi.mock('./inject', () => ({
  injectTokens: injectTokensMock
}))

import { _resetForTests, initThemes, loadThemes, themesState } from './store.svelte'

const sampleThemes = {
  themes: [
    {
      id: 'cyber_forest',
      name: 'Cyber Forest',
      author: 'System',
      description: 'Default',
      swatches: ['#2dd4bf', '#6366f1'],
      source: 'default'
    },
    {
      id: 'terra-test',
      name: 'Terra Test',
      author: 'Tester',
      description: 'A second theme',
      swatches: ['#c2410c', '#4d7c0f'],
      source: 'disk'
    }
  ],
  errors: []
}

describe('theme listing store', () => {
  beforeEach(() => {
    _resetForTests()
    listThemesMock.mockReset()
    eventsOnMock.mockReset()
    injectTokensMock.mockReset()
    listThemesMock.mockResolvedValue(sampleThemes)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('loadThemes populates items + flatTokens from ListThemes', async () => {
    await loadThemes()
    expect(themesState.items).toHaveLength(2)
    expect(themesState.items[0].id).toBe('cyber_forest')
    expect(themesState.items[1].id).toBe('terra-test')
    // flatTokens is optional in the IPC payload; for a fresh
    // payload without the field, the store keeps an empty map.
    expect(themesState.loadError).toBeNull()
    expect(listThemesMock).toHaveBeenCalledTimes(1)
  })

  it('loadThemes surfaces backend errors', async () => {
    listThemesMock.mockRejectedValueOnce(new Error('network down'))
    await loadThemes()
    expect(themesState.loadError).toContain('network down')
    expect(themesState.items).toHaveLength(0)
  })

  it('initThemes is idempotent (called twice, ListThemes called once)', async () => {
    initThemes()
    initThemes()
    // Allow the async loadThemes call inside initThemes to settle.
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(listThemesMock).toHaveBeenCalledTimes(1)
  })

  it('initThemes subscribes to the backend "themes:changed" event', () => {
    initThemes()
    expect(eventsOnMock).toHaveBeenCalledWith('themes:changed', expect.any(Function))
  })

  it('themes:changed event triggers a re-fetch of ListThemes', async () => {
    initThemes()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(listThemesMock).toHaveBeenCalledTimes(1)

    // Extract the handler that initThemes passed to EventsOn and
    // invoke it manually; the handler is debounced (100ms trailing
    // edge) so advance fake timers past the debounce window.
    vi.useFakeTimers()
    const calls = eventsOnMock.mock.calls as unknown as Array<[string, () => void]>
    const handler = calls[0]?.[1]
    expect(typeof handler).toBe('function')
    handler()
    // The debounce delays the actual loadThemes call by 100ms.
    vi.advanceTimersByTime(150)
    vi.useRealTimers()
    // Allow the async loadThemes promise to settle.
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(listThemesMock).toHaveBeenCalledTimes(2)
  })
})
