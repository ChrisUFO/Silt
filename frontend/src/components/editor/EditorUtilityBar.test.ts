// Component tests for EditorUtilityBar (#202) — extracted from
// VirtualScrollContainer. The extracted component gets dedicated coverage
// for the conditional render logic and prop pass-through since VSC has zero
// existing tests and the utility-bar contract is now its own concern.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup } from '@testing-library/svelte'
import type { Editor } from 'svelte-tiptap'

// Hoisted mock state — vi.mock factories are hoisted above imports, so any
// mutable refs they capture must live inside vi.hoisted.
const mocks = vi.hoisted(() => ({
  // Settings store — EditorUtilityBar reads ui.show_format_toolbar and
  // ui.formatting.color_enabled. Mutated per test.
  config: {
    ui: {
      show_format_toolbar: true as boolean,
      formatting: { color_enabled: true as boolean }
    }
  }
}))

vi.mock('../../settings/store.svelte', () => ({
  settings: mocks
}))
vi.mock('../../lib/systemTheme.svelte', () => ({
  isSystemDark: vi.fn(() => false)
}))

// Use the .stub.svelte companions (mirrors the EmbedPortal/RichText stub
// pattern from #127) so the real toolbar/toggle components don't pull in
// their full svelte-tiptap / TipTap dependency tree.
vi.mock('./FormatToolbar.svelte', async () => {
  const mod = await import('./FormatToolbar.stub.svelte')
  return { default: mod.default }
})
vi.mock('./ViewModeToggle.svelte', async () => {
  const mod = await import('./ViewModeToggle.stub.svelte')
  return { default: mod.default }
})

import EditorUtilityBar from './EditorUtilityBar.svelte'

beforeEach(() => {
  cleanup()
  mocks.config = {
    ui: { show_format_toolbar: true, formatting: { color_enabled: true } }
  }
})

describe('EditorUtilityBar (#202 — extracted from VSC)', () => {
  it('renders FormatToolbar + ViewModeToggle in edit mode with toolbar enabled', () => {
    const marks = new Set<string>(['bold', 'italic'])
    render(EditorUtilityBar, {
      props: {
        editor: { isDestroyed: false } as unknown as Editor,
        activeMarks: marks,
        viewMode: 'edit',
        onToggleViewMode: vi.fn()
      }
    })
    const ft = document.querySelector('[data-testid="format-toolbar-stub"]')
    expect(ft).toBeTruthy()
    expect(ft?.getAttribute('data-editor')).toBe('present')
    expect(ft?.getAttribute('data-active-marks')).toBe('bold,italic')
    expect(ft?.getAttribute('data-is-dark')).toBe('false')
    expect(ft?.getAttribute('data-color-enabled')).toBe('true')
    const vt = document.querySelector('[data-testid="view-mode-toggle-stub"]')
    expect(vt).toBeTruthy()
    expect(vt?.getAttribute('data-mode')).toBe('edit')
  })

  it('renders only ViewModeToggle in source mode', () => {
    render(EditorUtilityBar, {
      props: {
        editor: null,
        activeMarks: new Set<string>(),
        viewMode: 'source',
        onToggleViewMode: vi.fn()
      }
    })
    expect(
      document.querySelector('[data-testid="format-toolbar-stub"]')
    ).toBeNull()
    const vt = document.querySelector('[data-testid="view-mode-toggle-stub"]')
    expect(vt).toBeTruthy()
    expect(vt?.getAttribute('data-mode')).toBe('source')
  })

  it('renders only ViewModeToggle when show_format_toolbar is false', () => {
    mocks.config.ui.show_format_toolbar = false
    render(EditorUtilityBar, {
      props: {
        editor: null,
        activeMarks: new Set<string>(),
        viewMode: 'edit',
        onToggleViewMode: vi.fn()
      }
    })
    expect(
      document.querySelector('[data-testid="format-toolbar-stub"]')
    ).toBeNull()
    expect(
      document.querySelector('[data-testid="view-mode-toggle-stub"]')
    ).toBeTruthy()
  })

  it('passes color_enabled: false through to FormatToolbar', () => {
    mocks.config.ui.formatting.color_enabled = false
    render(EditorUtilityBar, {
      props: {
        editor: null,
        activeMarks: new Set<string>(),
        viewMode: 'edit',
        onToggleViewMode: vi.fn()
      }
    })
    const ft = document.querySelector('[data-testid="format-toolbar-stub"]')
    expect(ft?.getAttribute('data-color-enabled')).toBe('false')
  })

  it('calls onToggleViewMode when the ViewModeToggle is interacted with', () => {
    const onToggle = vi.fn()
    render(EditorUtilityBar, {
      props: {
        editor: null,
        activeMarks: new Set<string>(),
        viewMode: 'edit',
        onToggleViewMode: onToggle
      }
    })
    const btn = document.querySelector(
      '[data-testid="toggle-btn"]'
    ) as HTMLButtonElement
    expect(btn).toBeTruthy()
    btn.click()
    expect(onToggle).toHaveBeenCalledOnce()
  })
})
