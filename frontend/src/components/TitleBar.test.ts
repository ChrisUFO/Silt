// Regression coverage for the TitleBar wordmark token (#138).
//
// The "Silt" wordmark used the accent token, which masked theme switches on
// the three cool-accent themes (Cyber Forest teal / Graphite blue / Linen
// slate-blue all read similarly). It now follows --text-primary so each
// theme's body-text hue is visible in the titlebar chrome. This test pins
// the class so the accent-token relapse is caught.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup } from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  WindowMinimise: vi.fn(),
  WindowToggleMaximise: vi.fn(),
  WindowIsMaximised: vi.fn().mockResolvedValue(false),
  Quit: vi.fn()
}))

// Mirror the exact specifier TitleBar.svelte imports (same directory, so the
// relative path resolves to the same absolute module the component sees).
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  WindowMinimise: mocks.WindowMinimise,
  WindowToggleMaximise: mocks.WindowToggleMaximise,
  WindowIsMaximised: mocks.WindowIsMaximised,
  Quit: mocks.Quit
}))

import TitleBar from './TitleBar.svelte'

describe('TitleBar', () => {
  beforeEach(() => {
    mocks.WindowMinimise.mockReset()
    mocks.WindowToggleMaximise.mockReset()
    mocks.WindowIsMaximised.mockReset()
    mocks.Quit.mockReset()
    mocks.WindowIsMaximised.mockResolvedValue(false)
  })

  afterEach(() => {
    cleanup()
  })

  it('renders the "Silt" wordmark in text-primary (not accent) per #138', async () => {
    render(TitleBar, {
      props: {
        activeView: 'notes',
        sidebarCollapsed: false,
        onSearchClick: () => {},
        onOpenSettings: () => {}
      }
    })
    await tick()

    // getByText('Silt') matches the wordmark <span> only — the adjacent
    // <img alt="Silt"> exposes its name via the alt attribute, not text
    // content, so it is not matched.
    const wordmark = screen.getByText('Silt')
    expect(wordmark).toHaveClass('text-text-primary')
    expect(wordmark).not.toHaveClass('text-accent-primary-start')
  })
})
