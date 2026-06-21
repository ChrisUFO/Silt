// Context menu tests for TipTapEditor.svelte — verifies rendering, item
// visibility (including block-scoped items), Escape dismissal, role=menuitem
// coverage, and keyboard navigation (ArrowUp/ArrowDown cycling).

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, fireEvent, waitFor } from '@testing-library/svelte'
import TipTapEditor from './TipTapEditor.svelte'
import { mkBlock } from '../lib/editor/nodeview-test-harness'

// jsdom stub: Patch elementFromPoint which TipTap's Placeholder viewport
// tracker touches during editor construction (same approach as
// nodeview-test-harness.ts).
if (typeof document !== 'undefined' && !document.elementFromPoint) {
  document.elementFromPoint = () => document.body
}

const mocks = vi.hoisted(() => ({
  saveFileBlocks: vi.fn().mockResolvedValue(undefined),
  acquireFocusLock: vi.fn().mockResolvedValue(undefined),
  refreshFocusLock: vi.fn().mockResolvedValue(undefined),
  releaseFocusLock: vi.fn().mockResolvedValue(undefined),
  eventsOn: vi.fn(() => () => {})
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  SaveFileBlocks: mocks.saveFileBlocks,
  AcquireFocusLock: mocks.acquireFocusLock,
  RefreshFocusLock: mocks.refreshFocusLock,
  ReleaseFocusLock: mocks.releaseFocusLock
}))

vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mocks.eventsOn
}))

vi.mock('../settings/store.svelte', () => ({
  settings: { config: null },
  saveConfig: vi.fn()
}))

vi.mock('../theme/store.svelte', () => ({
  themeState: { mode: 'dark' }
}))

vi.mock('../notifications/store.svelte', () => ({
  pushNotification: vi.fn()
}))

vi.mock('../plugins/events', () => ({
  dispatch: vi.fn()
}))

vi.mock('../lib/perf/frame-budget', () => ({
  measureFrameBudget: vi.fn((_label: string, fn: () => unknown) => fn())
}))

async function openContextMenu(container: HTMLElement): Promise<void> {
  const host = container.querySelector('.tiptap-editor-host') as HTMLElement
  await fireEvent.contextMenu(host)
  await waitFor(() => {
    expect(container.querySelector('[role="menu"]')).toBeTruthy()
  })
}

describe('TipTapEditor context menu', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('opens on right-click with standard items visible', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)

    const text = container.textContent!
    for (const label of [
      'Cut',
      'Copy',
      'Paste',
      'Copy as Markdown',
      'Copy as Plain Text',
      'Clear Formatting'
    ]) {
      expect(text).toContain(label)
    }

    unmount()
  })

  it('shows block-scoped items when a block is resolved', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)

    const text = container.textContent!
    expect(text).toContain('Duplicate Block')
    expect(text).toContain('Delete Block')
    expect(text).toContain('Copy Block Reference')
    expect(text).toContain('Copy Block Embed')

    unmount()
  })

  it('closes on Escape key', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)
    expect(container.querySelector('[role="menu"]')).toBeTruthy()

    await fireEvent.keyDown(window, { key: 'Escape' })

    await waitFor(() => {
      expect(container.querySelector('[role="menu"]')).toBeNull()
    })

    unmount()
  })

  it('has role=menuitem on every menu button', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)

    const menuItems = container.querySelectorAll('[role="menuitem"]')
    // 6 standard + 4 block-scoped = 10
    expect(menuItems.length).toBe(10)

    unmount()
  })

  it('navigates items with ArrowDown/ArrowUp', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)

    // After open, the first enabled item should be focused via the
    // requestAnimationFrame effect. Wait for it.
    const menu = container.querySelector('[role="menu"]') as HTMLElement
    await waitFor(() => {
      expect(document.activeElement).toBe(
        menu.querySelector('button:not([disabled])')
      )
    })

    // ArrowDown should move focus to the second item
    await fireEvent.keyDown(menu, { key: 'ArrowDown' })
    const items = Array.from(
      menu.querySelectorAll<HTMLButtonElement>('button:not([disabled])')
    )
    expect(document.activeElement).toBe(items[1])

    // ArrowUp should wrap to the last item
    await fireEvent.keyDown(menu, { key: 'ArrowUp' })
    // currentIndex is 1, ArrowUp goes to 0... wait, we're at index 1 now.
    // ArrowUp: (1 - 1 + N) % N = 0. So back to first item.
    // Let me press ArrowUp again to test wrap:
    await fireEvent.keyDown(menu, { key: 'ArrowUp' })
    // currentIndex is 0, ArrowUp: (0 - 1 + N) % N = N-1 = last item
    expect(document.activeElement).toBe(items[items.length - 1])

    unmount()
  })

  it('disables Delete Block when only one block remains', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'only block' })]
    const { container, unmount } = render(TipTapEditor, {
      props: {
        notebook: 'NB',
        section: 'S',
        page: 'P',
        blocks,
        onUpdate: () => {}
      }
    })

    await waitFor(() => {
      expect(container.querySelector('.ProseMirror')).toBeTruthy()
    })

    await openContextMenu(container)

    const deleteBtn = Array.from(
      container.querySelectorAll('[role="menuitem"]')
    ).find((btn) =>
      btn.textContent?.includes('Delete Block')
    ) as HTMLButtonElement
    expect(deleteBtn).toBeTruthy()
    expect(deleteBtn.disabled).toBe(true)

    unmount()
  })
})
