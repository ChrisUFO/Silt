// Component coverage for GlobalReplaceModal's apply/undo/stale-guard paths.
// IPC is mocked via vi.hoisted + vi.mock over the wailsjs binding module
// (canonical pattern — see AppearanceTab.test.ts). The matcher module is
// exercised for real so these tests assert actual find/replace behavior.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor
} from '@testing-library/svelte'
import {
  registerEditor,
  _resetEditorRegistryForTests
} from '../../lib/editor/editorRegistry.svelte'

const mocks = vi.hoisted(() => ({
  SearchBlocksPaged: vi.fn(),
  FetchPageBlocks: vi.fn(),
  SaveFileBlocks: vi.fn()
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  SearchBlocksPaged: mocks.SearchBlocksPaged,
  FetchPageBlocks: mocks.FetchPageBlocks,
  SaveFileBlocks: mocks.SaveFileBlocks
}))
vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  OnFileDrop: vi.fn(),
  OnFileDropOff: vi.fn(),
  EventsOn: vi.fn(),
  EventsOff: vi.fn(),
  EventsEmit: vi.fn()
}))

import GlobalReplaceModal from './GlobalReplaceModal.svelte'

// Shared block factory: keep raw_text equal to clean_text so the snapshot
// shape matches what the component actually persists.
function block(id: string, clean_text: string) {
  return { id, clean_text, raw_text: clean_text }
}

// Render, fill Find/Replace, click Preview, and wait until the Replace
// button shows a numeric match count — the signal that preview populated
// accepted matches and the stale-guard is settled (previewStale === false).
async function renderAndPreview(find: string, replace: string) {
  render(GlobalReplaceModal, { onClose: () => {} })
  await tick()
  await fireEvent.input(screen.getByLabelText('Find'), {
    target: { value: find }
  })
  await fireEvent.input(screen.getByLabelText('Replace with'), {
    target: { value: replace }
  })
  await fireEvent.click(screen.getByRole('button', { name: 'Preview' }))
  await waitFor(
    () => {
      expect(
        screen.getByRole('button', { name: /^Replace \d/ })
      ).toBeInTheDocument()
    },
    { timeout: 2000 }
  )
}

describe('GlobalReplaceModal apply/undo/stale-guard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    _resetEditorRegistryForTests()
  })

  afterEach(() => {
    cleanup()
  })

  it('Preview → Apply writes through SaveFileBlocks once per changed page', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        },
        {
          id: 'b2',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo baz'
        }
      ],
      total: 2,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([
      block('b1', 'foo bar'),
      block('b2', 'foo baz')
    ])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    await renderAndPreview('foo', 'qux')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))

    await waitFor(
      () => {
        expect(mocks.SaveFileBlocks).toHaveBeenCalledTimes(1)
      },
      { timeout: 2000 }
    )

    const saved = mocks.SaveFileBlocks.mock.calls[0][3] as Array<{
      clean_text: string
    }>
    expect(saved).toHaveLength(2)
    expect(saved[0].clean_text).toBe('qux bar')
    expect(saved[1].clean_text).toBe('qux baz')
  })

  it('Undo restores original blocks for the last applied page', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        },
        {
          id: 'b2',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo baz'
        }
      ],
      total: 2,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([
      block('b1', 'foo bar'),
      block('b2', 'foo baz')
    ])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    await renderAndPreview('foo', 'qux')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))
    await waitFor(
      () => {
        expect(mocks.SaveFileBlocks).toHaveBeenCalledTimes(1)
      },
      { timeout: 2000 }
    )

    // The revert log now holds one batch (page1); Undo must surface.
    const restoreBtn = await screen.findByRole('button', {
      name: /Restore last apply/
    })
    await fireEvent.click(restoreBtn)

    // Second SaveFileBlocks call restores the pre-replace originals.
    await waitFor(
      () => {
        expect(mocks.SaveFileBlocks).toHaveBeenCalledTimes(2)
      },
      { timeout: 2000 }
    )
    const restored = mocks.SaveFileBlocks.mock.calls[1][3] as Array<{
      clean_text: string
    }>
    expect(restored[0].clean_text).toBe('foo bar')
    expect(restored[1].clean_text).toBe('foo baz')
  })

  it('editing findText after Preview marks the preview stale and disables Apply', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        },
        {
          id: 'b2',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo baz'
        }
      ],
      total: 2,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([
      block('b1', 'foo bar'),
      block('b2', 'foo baz')
    ])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    await renderAndPreview('foo', 'qux')

    // Retyping Find without re-previewing would otherwise apply a matcher
    // the user never saw — the stale-guard must catch this.
    await fireEvent.input(screen.getByLabelText('Find'), {
      target: { value: 'bar' }
    })
    await tick()

    await waitFor(
      () => {
        expect(screen.getByRole('button', { name: /^Replace/ })).toBeDisabled()
      },
      { timeout: 2000 }
    )
    expect(screen.getByText(/Preview is stale/)).toBeInTheDocument()
    expect(mocks.SaveFileBlocks).not.toHaveBeenCalled()
  })

  it('a mid-loop Apply failure leaves Undo available for already-persisted pages', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        },
        {
          id: 'b2',
          notebook: 'vault',
          section: 'notes',
          page: 'page2',
          snippet: 'foo qux'
        }
      ],
      total: 2,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockImplementation(
      (_nb: string, _sec: string, page: string) => {
        if (page === 'page1') return Promise.resolve([block('b1', 'foo bar')])
        if (page === 'page2') return Promise.resolve([block('b2', 'foo qux')])
        return Promise.resolve([])
      }
    )
    // page1 persists; page2 fails mid-batch. The finally-block must still
    // commit newLog (page1 only) so Undo is available for the written page.
    mocks.SaveFileBlocks.mockImplementation(
      (_nb: string, _sec: string, page: string) => {
        if (page === 'page1') return Promise.resolve()
        return Promise.reject(new Error('disk full'))
      }
    )

    await renderAndPreview('foo', 'ZZZ')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))

    await waitFor(
      () => {
        expect(screen.getByText(/Apply failed/)).toBeInTheDocument()
      },
      { timeout: 2000 }
    )

    // Batch holds exactly the one page that persisted.
    const restoreBtn = await screen.findByRole('button', {
      name: /Restore last apply \(1 page\)/
    })
    expect(restoreBtn).toBeInTheDocument()
    await fireEvent.click(restoreBtn)

    // Undo restores page1's pre-replace originals.
    await waitFor(
      () => {
        const undoCalls = mocks.SaveFileBlocks.mock.calls.filter(
          (c) => c[2] === 'page1'
        )
        expect(undoCalls.length).toBeGreaterThanOrEqual(2)
      },
      { timeout: 2000 }
    )
    const undoCall = [...mocks.SaveFileBlocks.mock.calls]
      .reverse()
      .find((c) => c[2] === 'page1')!
    const restored = undoCall[3] as Array<{ clean_text: string }>
    expect(restored[0].clean_text).toBe('foo bar')
  })

  it('refuses a linked-notebook page even if it slips into the preview (#343)', async () => {
    // Defense in depth: the server VaultOnly filter excludes linked hits,
    // but if one reached the preview it must NOT be written.
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          source: 'linked:abc',
          notebook: 'External',
          section: 'notes',
          page: 'linked-page',
          snippet: 'foo bar'
        }
      ],
      total: 1,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([block('b1', 'foo bar')])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    await renderAndPreview('foo', 'qux')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))

    // SaveFileBlocks must never be called for the linked page.
    await waitFor(
      () => {
        expect(mocks.SaveFileBlocks).not.toHaveBeenCalled()
      },
      { timeout: 2000 }
    )
  })

  it('flushes a dirty open editor before applying, then force-reloads it (#345)', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          source: 'vault',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        }
      ],
      total: 1,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([block('b1', 'foo bar')])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    // Register a fake dirty editor for the target page. Record the call order
    // across flush / forceExternalReload / SaveFileBlocks to assert the
    // lifecycle fix (#345): the reload flag must arm BEFORE the write so the
    // editor's sync $effect consumes it on the block:changed reload rather than
    // the flag leaking and later clobbering an unrelated edit.
    const order: string[] = []
    let dirty = true
    registerEditor({
      key: 'vault\x00notes\x00page1',
      isDirty: () => dirty,
      flush: async () => {
        order.push('flush')
        dirty = false
        return true
      },
      forceExternalReload: () => order.push('forceExternalReload')
    })
    mocks.SaveFileBlocks.mockImplementation(async () => {
      order.push('SaveFileBlocks')
    })

    await renderAndPreview('foo', 'qux')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))

    await waitFor(
      () => {
        expect(order).toContain('SaveFileBlocks')
      },
      { timeout: 2000 }
    )
    // flush runs first; forceExternalReload must precede SaveFileBlocks.
    const flushAt = order.indexOf('flush')
    const reloadAt = order.indexOf('forceExternalReload')
    const saveAt = order.indexOf('SaveFileBlocks')
    expect(flushAt).toBeLessThan(reloadAt)
    expect(reloadAt).toBeLessThan(saveAt)
  })

  it('skips a page whose dirty editor cannot flush (save error) (#345)', async () => {
    mocks.SearchBlocksPaged.mockResolvedValue({
      results: [
        {
          id: 'b1',
          source: 'vault',
          notebook: 'vault',
          section: 'notes',
          page: 'page1',
          snippet: 'foo bar'
        }
      ],
      total: 1,
      offset: 0,
      limit: 200,
      has_more: false
    })
    mocks.FetchPageBlocks.mockResolvedValue([block('b1', 'foo bar')])
    mocks.SaveFileBlocks.mockResolvedValue(undefined)

    // Register a dirty editor whose flush fails (stays dirty).
    registerEditor({
      key: 'vault\x00notes\x00page1',
      isDirty: () => true,
      flush: async () => false,
      forceExternalReload: vi.fn()
    })

    await renderAndPreview('foo', 'qux')
    await fireEvent.click(screen.getByRole('button', { name: /^Replace/ }))

    // The page must NOT be written: writing from stale disk content would
    // silently discard the user's unsaved edits.
    await waitFor(
      () => {
        expect(mocks.SaveFileBlocks).not.toHaveBeenCalled()
      },
      { timeout: 2000 }
    )
  })
})
