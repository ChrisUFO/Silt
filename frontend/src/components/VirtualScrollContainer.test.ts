// Component coverage for the editor chrome relocated into VirtualScrollContainer
// (the EditorUtilityBar/FormatToolbar conditional + the floating action buttons).
// The heavy editor child + utility bar are stubbed (existing *.stub.svelte
// components) and the IPC/store seams are mocked, so this exercises only VSC's
// own conditional wiring — the contract the deleted EditorUtilityBar tests
// used to cover.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'
import { waitFor } from '@testing-library/dom'
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

describe('Edit↔Source scroll preservation (#319)', () => {
  // jsdom has no layout, so back scrollTop/scrollHeight with a controlled mock
  // scoped to this describe block (restored in afterEach). All elements share
  // one value, which is fine here — only the scroll container reads/writes it.
  let scrollTopVal = 0
  let scrollHeightVal = 1000
  const origScrollTop = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'scrollTop'
  )
  const origScrollHeight = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'scrollHeight'
  )

  beforeEach(() => {
    mocks.onToggleViewMode.mockClear()
    scrollTopVal = 0
    scrollHeightVal = 1000
    Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
      configurable: true,
      get() {
        return scrollTopVal
      },
      set(v: number) {
        scrollTopVal = v
      }
    })
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return scrollHeightVal
      }
    })
  })
  afterEach(() => {
    if (origScrollTop)
      Object.defineProperty(HTMLElement.prototype, 'scrollTop', origScrollTop)
    if (origScrollHeight)
      Object.defineProperty(
        HTMLElement.prototype,
        'scrollHeight',
        origScrollHeight
      )
    cleanup()
  })

  it('restores the Edit scroll offset after an Edit→Source→Edit round-trip', async () => {
    const { rerender } = render(VirtualScrollContainer, {
      props: baseProps()
    })
    // User scrolled down in Edit mode.
    scrollTopVal = 480
    // Leave Edit: $effect.pre captures 480 before the editor unmounts.
    rerender({ ...baseProps(), viewMode: 'source' })
    expect(screen.getByTestId('markdown-source-stub')).toBeInTheDocument()
    // Simulate the fresh editor remount starting back at the top.
    scrollTopVal = 0
    // Return to Edit: the remounted editor signals readiness → restore.
    rerender(baseProps())
    expect(screen.getByTestId('tiptap-stub')).toBeInTheDocument()
    await waitFor(() => {
      expect(scrollTopVal).toBe(480)
    })
  })

  it('clamps a stale offset that exceeds the current scroll height', async () => {
    const { rerender } = render(VirtualScrollContainer, {
      props: baseProps()
    })
    scrollTopVal = 900
    rerender({ ...baseProps(), viewMode: 'source' })
    // Doc shortened while in Source view (autosave/fsnotify external edit).
    scrollHeightVal = 300
    scrollTopVal = 0
    rerender(baseProps())
    await waitFor(() => {
      // Clamped to the shorter height — no overscroll, no crash.
      expect(scrollTopVal).toBe(300)
    })
  })

  it('does not force-scroll on a cold Edit open (no prior Source detour)', async () => {
    scrollTopVal = 0
    render(VirtualScrollContainer, { props: baseProps() })
    // No edit→source transition happened, so pendingRestore stays false and
    // the readiness handler is a no-op (target-block nav owns cold opens).
    await new Promise((r) => setTimeout(r, 0))
    expect(scrollTopVal).toBe(0)
  })
})
