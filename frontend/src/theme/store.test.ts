// Vitest coverage for the active-theme store (#74). The store
// subscribes to the backend GetActiveTheme / ApplyTheme / ListThemes
// IPC methods, re-resolves 'system' mode via prefers-color-scheme,
// and drives the runtime injector.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'

// Stub the Wails-bound functions the store imports. We avoid pulling
// the real wailsjs/runtime here because the test environment has no
// window.go; vitest's vi.mock swaps the module before the store
// imports it. vi.hoisted keeps these refs available inside the mock
// factory, which is itself hoisted to the top of the file.
const { applyThemeMock, getActiveThemeMock, eventsOnMock, injectTokensMock } =
  vi.hoisted(() => ({
    applyThemeMock: vi.fn(),
    getActiveThemeMock: vi.fn(),
    eventsOnMock: vi.fn(() => () => {}),
    injectTokensMock: vi.fn()
  }))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ApplyTheme: applyThemeMock,
  GetActiveTheme: getActiveThemeMock,
  ListThemes: vi.fn(),
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

import {
  _resetForTests,
  applyTheme,
  initTheme,
  themeState
} from './store.svelte'

const sampleResult = {
  id: 'cyber_forest',
  name: 'Cyber Forest',
  mode: 'dark',
  dark_tokens: {
    '--color-void': '#0c0c0e',
    '--color-accent-primary-start': '#2dd4bf'
  },
  light_tokens: {
    '--color-void': '#f8fafc',
    '--color-accent-primary-start': '#0d9488'
  }
}

describe('theme store', () => {
  beforeEach(async () => {
    _resetForTests()
    applyThemeMock.mockReset()
    getActiveThemeMock.mockReset()
    eventsOnMock.mockReset()
    injectTokensMock.mockReset()
    // Default: GetActiveTheme returns the embedded default snapshot.
    getActiveThemeMock.mockResolvedValue(sampleResult)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('initTheme loads the active theme and injects tokens', async () => {
    await initTheme()
    expect(getActiveThemeMock).toHaveBeenCalled()
    expect(themeState.id).toBe('cyber_forest')
    expect(themeState.darkTokens['--color-void']).toBe('#0c0c0e')
    expect(injectTokensMock).toHaveBeenCalled()
  })

  it('initTheme is idempotent (called twice, GetActiveTheme called once)', async () => {
    await initTheme()
    await initTheme()
    // First call: GetActiveTheme; second call: returns early (the
    // `started` guard).
    expect(getActiveThemeMock).toHaveBeenCalledTimes(1)
  })

  it('applyTheme persists + injects the result returned by ApplyTheme', async () => {
    applyThemeMock.mockResolvedValue({
      ...sampleResult,
      id: 'terra-test',
      name: 'Terra Test',
      mode: 'light'
    })
    const ok = await applyTheme('terra-test', 'light')
    expect(ok).toBe(true)
    expect(themeState.id).toBe('terra-test')
    expect(themeState.mode).toBe('light')
    expect(themeState.lightTokens['--color-void']).toBe('#f8fafc')
    expect(injectTokensMock).toHaveBeenCalled()
  })

  it('applyTheme surfaces backend errors and returns false', async () => {
    applyThemeMock.mockRejectedValue(new Error('theme not available'))
    const ok = await applyTheme('no-such-theme', 'dark')
    expect(ok).toBe(false)
    expect(themeState.error).toContain('theme not available')
  })

  it('subscribes to the theme:changed event on init', async () => {
    await initTheme()
    // The first arg to EventsOn is the event name; the second is the
    // handler. We just check that the event name is "theme:changed"
    // (the one the store listens to) and that a handler was passed.
    expect(eventsOnMock).toHaveBeenCalledWith(
      'theme:changed',
      expect.any(Function)
    )
  })
})
