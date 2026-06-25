// Component coverage for the editor chrome relocated into VirtualScrollContainer
// (the EditorUtilityBar/FormatToolbar conditional + the floating action buttons).
// The heavy editor child + utility bar are stubbed (existing *.stub.svelte
// components) and the IPC/store/viewMode seams are mocked, so this exercises
// only VSC's own conditional wiring — the contract the deleted EditorUtilityBar
// tests used to cover.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import TipTapEditorStub from './TipTapEditor.stub.svelte'
import EditorUtilityBarStub from './editor/EditorUtilityBar.stub.svelte'

const mocks = vi.hoisted(() => ({
  settings: {
    config: {
      ui: { show_format_toolbar: true },
      editor: { focus_mode: false }
    }
  },
  viewMode: 'edit' as string,
  toggleFocusMode: vi.fn(() => Promise.resolve(true)),
  toggleFormatToolbar: vi.fn(() => Promise.resolve(true)),
  toggleViewMode: vi.fn()
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  FetchPageBlocks: vi.fn(() => Promise.resolve([])),
  RenamePage: vi.fn(() => Promise.resolve(undefined))
}))
vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  // VSC stores the returned unsubscribe and calls it on destroy; return a noop.
  EventsOn: vi.fn(() => () => {})
}))

vi.mock('./TipTapEditor.svelte', () => ({ default: TipTapEditorStub }))
vi.mock('./editor/EditorUtilityBar.svelte', () => ({
  default: EditorUtilityBarStub
}))

vi.mock('../settings/store.svelte.ts', () => ({
  settings: mocks.settings,
  toggleFocusMode: mocks.toggleFocusMode,
  toggleFormatToolbar: mocks.toggleFormatToolbar
}))
vi.mock('../lib/viewMode.svelte', () => ({
  getViewMode: () => mocks.viewMode,
  toggleViewMode: mocks.toggleViewMode
}))

import VirtualScrollContainer from './VirtualScrollContainer.svelte'

describe('VirtualScrollContainer editor chrome', () => {
  beforeEach(() => {
    mocks.toggleFocusMode.mockClear()
    mocks.toggleFormatToolbar.mockClear()
    mocks.toggleViewMode.mockClear()
    mocks.viewMode = 'edit'
    mocks.settings.config.ui.show_format_toolbar = true
  })
  afterEach(() => cleanup())

  it('renders the EditorUtilityBar in edit mode with the toolbar enabled', () => {
    render(VirtualScrollContainer, {
      props: { notebook: 'NB', section: '', page: 'PG' }
    })
    expect(screen.getByTestId('editor-utility-bar-stub')).toBeInTheDocument()
  })

  it('hides the EditorUtilityBar in source view mode', () => {
    mocks.viewMode = 'source'
    render(VirtualScrollContainer, {
      props: { notebook: 'NB', section: '', page: 'PG' }
    })
    expect(screen.queryByTestId('editor-utility-bar-stub')).toBeNull()
    // The view-mode toggle reflects the action available (switch back to edit).
    expect(
      screen.getByRole('button', { name: 'Toggle View Mode' })
    ).toHaveAttribute('title', 'View Rich Text (Ctrl+Shift+V)')
  })

  it('hides the EditorUtilityBar when show_format_toolbar is false', () => {
    mocks.settings.config.ui.show_format_toolbar = false
    render(VirtualScrollContainer, {
      props: { notebook: 'NB', section: '', page: 'PG' }
    })
    expect(screen.queryByTestId('editor-utility-bar-stub')).toBeNull()
  })

  it('renders the three floating toggle buttons and dispatches their handlers', async () => {
    render(VirtualScrollContainer, {
      props: { notebook: 'NB', section: '', page: 'PG' }
    })
    await fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Focus Mode' })
    )
    expect(mocks.toggleFocusMode).toHaveBeenCalledTimes(1)
    await fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Formatting Toolbar' })
    )
    expect(mocks.toggleFormatToolbar).toHaveBeenCalledTimes(1)
    // The view-mode button is present and announces the current mode.
    expect(
      screen.getByRole('button', { name: 'Toggle View Mode' })
    ).toBeInTheDocument()
  })
})
