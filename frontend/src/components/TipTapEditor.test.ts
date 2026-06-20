// Component-level test for TipTapEditor smart-graph content (#127). Renders
// a live TipTap editor with SiltBlockExtensionsWithNodeViews and verifies
// that {{embed:uuid}} and ((uuid)) content renders via the NodeView pipeline
// (SvelteNodeViewRenderer → EmbedNodeView/BlockReferenceNodeView →
// EmbedPortal/BlockReferenceChip).
//
// This is the test #127 explicitly asked for: a component-level NodeView
// integration test that exercises the full Svelte rendering path inside a
// live TipTap editor instance.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { waitFor } from '@testing-library/svelte'
import {
  mountNodeViewEditor,
  mkBlock,
  FIXTURE_UUID_A,
  FIXTURE_UUID_B
} from '../lib/editor/nodeview-test-harness'

const mocks = vi.hoisted(() => ({
  resolveBlockReference: vi.fn(),
  pluginMutateBlock: vi.fn(),
  fetchPageBlocks: vi.fn(),
  saveFileBlocks: vi.fn(),
  acquireFocusLock: vi.fn(),
  refreshFocusLock: vi.fn(),
  releaseFocusLock: vi.fn(),
  eventsOn: vi.fn(() => () => {})
}))

vi.mock('../../wailsjs/go/main/App.js', () => ({
  ResolveBlockReference: mocks.resolveBlockReference,
  PluginMutateBlock: mocks.pluginMutateBlock,
  FetchPageBlocks: mocks.fetchPageBlocks,
  SaveFileBlocks: mocks.saveFileBlocks,
  AcquireFocusLock: mocks.acquireFocusLock,
  RefreshFocusLock: mocks.refreshFocusLock,
  ReleaseFocusLock: mocks.releaseFocusLock
}))

vi.mock('../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: mocks.eventsOn
}))

describe('TipTapEditor smart-graph content (#127)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID_A,
      notebook: 'Work',
      section: 'Projects',
      page: 'Site',
      file_date: '2026-06-15',
      clean_text: 'embedded content'
    })
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders an embedNode NodeView for a sole {{embed:uuid}} block', async () => {
    const blocks = [
      mkBlock('NOTE', { clean_text: `{{embed:${FIXTURE_UUID_A}}}` })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // A sole-embed NOTE becomes a top-level embedNode. The NodeView mounts
    // an EmbedPortal which calls ResolveBlockReference.
    await waitFor(() => {
      expect(mocks.resolveBlockReference).toHaveBeenCalledWith(FIXTURE_UUID_A)
    })

    // The NodeView target gets the class node-embedNode.
    expect(container.querySelector('.node-embedNode')).toBeTruthy()

    cleanup()
  })

  it('renders a blockReferenceNode NodeView for inline ((uuid))', async () => {
    const blocks = [
      mkBlock('NOTE', {
        clean_text: `See ((${FIXTURE_UUID_B})) for details.`
      })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // The inline block reference renders as a NodeView with class
    // node-blockReferenceNode. Its component (BlockReferenceChip) calls
    // ResolveBlockReference.
    await waitFor(() => {
      expect(mocks.resolveBlockReference).toHaveBeenCalledWith(FIXTURE_UUID_B)
    })
    expect(container.querySelector('.node-blockReferenceNode')).toBeTruthy()

    cleanup()
  })

  it('renders both embeds and references in the same block', async () => {
    const blocks = [
      mkBlock('NOTE', {
        clean_text: `Pre {{embed:${FIXTURE_UUID_A}}} mid ((${FIXTURE_UUID_B})) post`
      })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    await waitFor(() => {
      expect(mocks.resolveBlockReference).toHaveBeenCalledWith(FIXTURE_UUID_A)
      expect(mocks.resolveBlockReference).toHaveBeenCalledWith(FIXTURE_UUID_B)
    })

    // Both NodeView types should be rendered.
    expect(container.querySelector('.node-embedNode')).toBeTruthy()
    expect(container.querySelector('.node-blockReferenceNode')).toBeTruthy()

    cleanup()
  })

  it('renders a NodeViewWrapper element for each smart-graph node', async () => {
    const blocks = [
      mkBlock('NOTE', {
        clean_text: `{{embed:${FIXTURE_UUID_A}}} and ((${FIXTURE_UUID_B}))`
      })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // Every Svelte NodeView renders inside a [data-node-view-wrapper] element
    // (the NodeViewWrapper component from svelte-tiptap).
    await waitFor(() => {
      const wrappers = container.querySelectorAll('[data-node-view-wrapper]')
      expect(wrappers.length).toBeGreaterThanOrEqual(2)
    })

    cleanup()
  })

  it('renders multiple NoteBlock NodeViews for multiple blocks', async () => {
    const blocks = [
      mkBlock('NOTE', { clean_text: 'first block' }),
      mkBlock('NOTE', { clean_text: 'second block' })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // Each NOTE block gets its own NodeView.
    const noteNodes = container.querySelectorAll('.node-noteBlock')
    expect(noteNodes.length).toBeGreaterThanOrEqual(2)

    cleanup()
  })
})
