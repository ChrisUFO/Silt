// Component-level a11y/keyboard coverage for the theme picker (#50).
// The #50 acceptance criterion is "the picker is keyboard-navigable
// with correct ARIA"; the injector/store unit tests cover the data
// pipeline, but only a rendered-component test can assert the listbox/
// radiogroup contract and the roving-tabindex keyboard map that
// AppearanceTab.svelte implements.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
// jest-dom matchers are registered via vitest.setup.ts (the /vitest
// entry); no inline import needed here.

// Hoisted, mutable state objects + function mocks. vi.hoisted keeps
// these refs available inside the vi.mock factories (which are
// themselves hoisted above the imports). The objects are PLAIN (not
// $state) — sufficient for assertions against the initial render and
// the component's own $state-driven interactions (focusIndex, rowRefs).
const mocks = vi.hoisted(() => ({
  themeState: {
    id: 'cyber_forest',
    name: 'Cyber Forest',
    mode: 'dark' as 'dark' | 'light' | 'system',
    darkTokens: { '--bg-void': '#0c0c0e' },
    lightTokens: { '--bg-void': '#f8fafc' },
    error: null as string | null
  },
  themesState: {
    items: [
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
    flatTokens: {} as Record<string, { dark: Record<string, string>; light: Record<string, string> }>,
    loadError: null as string | null,
    loading: false
  },
  themeStatus: { kind: 'info' as const, message: '', fields: [] as { field: string; message: string }[] },
  applyTheme: vi.fn(),
  restoreActiveTheme: vi.fn(),
  injectTokens: vi.fn(),
  loadThemes: vi.fn(),
  clearStatus: vi.fn(),
  exportActiveTheme: vi.fn(),
  importThemeFromPath: vi.fn(),
  pickAndImportTheme: vi.fn()
}))

vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))
vi.mock('../../theme/inject', () => ({ injectTokens: mocks.injectTokens }))
vi.mock('../../theme/store.svelte', () => ({
  themeState: mocks.themeState,
  themesState: mocks.themesState,
  themeStatus: mocks.themeStatus,
  applyTheme: mocks.applyTheme,
  restoreActiveTheme: mocks.restoreActiveTheme,
  loadThemes: mocks.loadThemes,
  clearStatus: mocks.clearStatus,
  exportActiveTheme: mocks.exportActiveTheme,
  importThemeFromPath: mocks.importThemeFromPath,
  pickAndImportTheme: mocks.pickAndImportTheme
}))

import AppearanceTab from './AppearanceTab.svelte'

describe('AppearanceTab picker a11y (#50)', () => {
  beforeEach(() => {
    mocks.applyTheme.mockReset()
    mocks.restoreActiveTheme.mockReset()
    mocks.injectTokens.mockReset()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders a listbox of option rows with aria-selected on the active theme', () => {
    render(AppearanceTab)

    const listbox = screen.getByRole('listbox', { name: 'Available themes' })
    expect(listbox).toBeInTheDocument()

    const options = screen.getAllByRole('option')
    expect(options).toHaveLength(2)

    // The active theme (themeState.id === 'cyber_forest') is selected.
    const active = screen.getByRole('option', { name: /Cyber Forest/i })
    expect(active).toHaveAttribute('aria-selected', 'true')
    const inactive = screen.getByRole('option', { name: /Terra Test/i })
    expect(inactive).toHaveAttribute('aria-selected', 'false')
  })

  it('renders a radiogroup for Dark/Light/System with aria-checked', () => {
    render(AppearanceTab)

    const group = screen.getByRole('radiogroup', { name: 'Color mode' })
    expect(group).toBeInTheDocument()

    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(3)

    // themeState.mode === 'dark' → the Dark radio is checked.
    const dark = screen.getByRole('radio', { name: /Dark/i })
    expect(dark).toHaveAttribute('aria-checked', 'true')
    const light = screen.getByRole('radio', { name: /Light/i })
    expect(light).toHaveAttribute('aria-checked', 'false')
  })

  it('ArrowDown moves focus to the next theme row (roving tabindex)', async () => {
    render(AppearanceTab)

    const options = screen.getAllByRole('option')
    // Initially the first row is the roving-tabindex entry point (0).
    options[0].focus()
    expect(document.activeElement).toBe(options[0])

    // ArrowDown moves focus to the second row.
    await fireEvent.keyDown(options[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(options[1])

    // The roving tabindex updated: row 1 is now tabbable (0), row 0 is
    // removed from the tab order (-1).
    expect(options[1]).toHaveAttribute('tabindex', '0')
    expect(options[0]).toHaveAttribute('tabindex', '-1')
  })

  it('Home jumps focus to the first row; End to the last', async () => {
    render(AppearanceTab)

    const options = screen.getAllByRole('option')
    options[1].focus()
    expect(document.activeElement).toBe(options[1])

    await fireEvent.keyDown(options[1], { key: 'Home' })
    expect(document.activeElement).toBe(options[0])

    await fireEvent.keyDown(options[0], { key: 'End' })
    expect(document.activeElement).toBe(options[1])
  })

  it('Enter on a non-active row selects it (calls applyTheme)', async () => {
    render(AppearanceTab)

    const terra = screen.getByRole('option', { name: /Terra Test/i })
    terra.focus()
    await fireEvent.keyDown(terra, { key: 'Enter' })

    expect(mocks.applyTheme).toHaveBeenCalledTimes(1)
    // applyTheme(id, mode) — id of the focused row, current mode.
    const [id, mode] = mocks.applyTheme.mock.calls[0]
    expect(id).toBe('terra-test')
    expect(mode).toBe('dark')
  })

  it('Space also commits the focused row', async () => {
    render(AppearanceTab)

    const terra = screen.getByRole('option', { name: /Terra Test/i })
    terra.focus()
    await fireEvent.keyDown(terra, { key: ' ' })

    expect(mocks.applyTheme).toHaveBeenCalledWith('terra-test', 'dark')
  })

  it('clicking a mode radio calls applyTheme with the new mode', async () => {
    render(AppearanceTab)

    const light = screen.getByRole('radio', { name: /Light/i })
    await fireEvent.click(light)

    expect(mocks.applyTheme).toHaveBeenCalledTimes(1)
    const [id, mode] = mocks.applyTheme.mock.calls[0]
    expect(id).toBe('cyber_forest') // mode change never changes the theme
    expect(mode).toBe('light')
  })
})
