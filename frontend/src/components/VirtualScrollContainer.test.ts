// Component coverage for the editor chrome relocated into VirtualScrollContainer
// (the EditorUtilityBar/FormatToolbar conditional + the floating action buttons).
// The heavy editor child + utility bar are stubbed (existing *.stub.svelte
// components) and the IPC/store seams are mocked, so this exercises only VSC's
// own conditional wiring — the contract the deleted EditorUtilityBar tests
// used to cover.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import TipTapEditorStub from './TipTapEditor.stub.svelte'
import EditorUtilityBarStub from './editor/EditorUtilityBar.stub.svelte'
import MarkdownSourceViewerStub from './editor/MarkdownSourceViewer.stub.svelte'

const mocks = vi.hoisted(() => ({
  settings: {
    config: {
      ui: { show_format_toolbar: true },
      editor: { focus_mode: false },
      hotkeys: { toggle_view_mode: 'Ctrl+Shift+V' } as Record<string, string>
    }
  },
  toggleFocusMode: vi.fn(() => Promise.resolve(true)),
  toggleFormatToolbar: vi.fn(() => Promise.resolve(true)),
  onToggleViewMode: vi.fn()
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
vi.mock('./editor/MarkdownSourceViewer.svelte', () => ({
  default: MarkdownSourceViewerStub
}))

vi.mock('../settings/store.svelte.ts', () => ({
  settings: mocks.settings,
  toggleFocusMode: mocks.toggleFocusMode,
  toggleFormatToolbar: mocks.toggleFormatToolbar
}))

import VirtualScrollContainer from './VirtualScrollContainer.svelte'

// Common props: viewMode is now a required prop owned by App.svelte's
// TabEntry (#195). onToggleViewMode is the callback the floating button fires.
const baseProps = () => ({
  notebook: 'NB',
  section: '',
  page: 'PG',
  viewMode: 'edit' as const,
  onToggleViewMode: mocks.onToggleViewMode
})

describe('VirtualScrollContainer editor chrome', () => {
  beforeEach(() => {
    mocks.toggleFocusMode.mockClear()
    mocks.toggleFormatToolbar.mockClear()
    mocks.onToggleViewMode.mockClear()
    mocks.settings.config.ui.show_format_toolbar = true
    // Reset the hotkey (one test remaps it) so test order can't bleed.
    mocks.settings.config.hotkeys = { toggle_view_mode: 'Ctrl+Shift+V' }
  })
  afterEach(() => cleanup())

  it('renders the EditorUtilityBar in edit mode with the toolbar enabled', () => {
    render(VirtualScrollContainer, { props: baseProps() })
    expect(screen.getByTestId('editor-utility-bar-stub')).toBeInTheDocument()
  })

  it('hides the EditorUtilityBar in source view mode', () => {
    render(VirtualScrollContainer, {
      props: { ...baseProps(), viewMode: 'source' }
    })
    expect(screen.queryByTestId('editor-utility-bar-stub')).toBeNull()
    // Source view: the read-only markdown projection renders in place of the
    // editor (#171/#194).
    expect(screen.getByTestId('markdown-source-stub')).toBeInTheDocument()
    // The toggle is a toggle button: a STABLE accessible name + aria-pressed
    // conveys state (no dynamic-label/pressed redundancy). The title carries
    // the contextual action + the live (remappable) hotkey.
    const toggle = screen.getByRole('button', { name: 'Toggle source view' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(toggle).toHaveAttribute('title', 'View Rich Text (Ctrl+Shift+V)')
    expect(toggle).toHaveAttribute('aria-keyshortcuts', 'Ctrl+Shift+V')
  })

  it('mounts TipTapEditor in edit mode but tears it down in source mode (#178)', () => {
    // Edit mode: the full editor (ProseMirror + NodeViews) is mounted.
    const { rerender } = render(VirtualScrollContainer, {
      props: baseProps()
    })
    expect(screen.getByTestId('tiptap-stub')).toBeInTheDocument()
    expect(screen.queryByTestId('markdown-source-stub')).toBeNull()

    // Source mode: TipTapEditor is NOT mounted (Svelte destroyed it), so a tab
    // held in Source view pays no editor memory cost. Only the read-only
    // markdown projection is present.
    rerender({ ...baseProps(), viewMode: 'source' })
    expect(screen.queryByTestId('tiptap-stub')).toBeNull()
    expect(screen.getByTestId('markdown-source-stub')).toBeInTheDocument()
  })

  it('hides the EditorUtilityBar when show_format_toolbar is false', () => {
    mocks.settings.config.ui.show_format_toolbar = false
    render(VirtualScrollContainer, { props: baseProps() })
    expect(screen.queryByTestId('editor-utility-bar-stub')).toBeNull()
  })

  it('renders the floating toggle buttons and dispatches their handlers', async () => {
    render(VirtualScrollContainer, { props: baseProps() })
    await fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Focus Mode' })
    )
    expect(mocks.toggleFocusMode).toHaveBeenCalledTimes(1)
    await fireEvent.click(
      screen.getByRole('button', { name: 'Toggle Formatting Toolbar' })
    )
    expect(mocks.toggleFormatToolbar).toHaveBeenCalledTimes(1)
    // The view-mode button fires the onToggleViewMode callback (#195) — App
    // owns the per-tab state now, not a module store.
    await fireEvent.click(
      screen.getByRole('button', { name: 'Toggle source view' })
    )
    expect(mocks.onToggleViewMode).toHaveBeenCalledTimes(1)
  })

  it('reads the view-mode hotkey live from settings (no stale shortcut text)', () => {
    // Remap the hotkey; the tooltip + aria-keyshortcuts must follow.
    mocks.settings.config.hotkeys = { toggle_view_mode: 'Ctrl+E' }
    render(VirtualScrollContainer, { props: baseProps() })
    const toggle = screen.getByRole('button', { name: 'Toggle source view' })
    expect(toggle).toHaveAttribute('aria-keyshortcuts', 'Ctrl+E')
    expect(toggle.getAttribute('title')).toContain('(Ctrl+E)')
  })

  it('announces the view-mode button state via aria-pressed', () => {
    render(VirtualScrollContainer, {
      props: { ...baseProps(), viewMode: 'source' }
    })
    const btn = screen.getByRole('button', { name: 'Toggle source view' })
    expect(btn).toHaveAttribute('aria-pressed', 'true')
    expect(btn).toHaveAttribute('aria-keyshortcuts', 'Ctrl+Shift+V')
  })
})
