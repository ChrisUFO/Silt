// Smoke test for the NodeView test harness (#127). Proves a live TipTap
// editor constructed with `SiltBlockExtensionsWithNodeViews` boots in jsdom,
// attaches to the DOM, and renders NodeView containers for Smart Graph
// nodes (embedNode, blockReferenceNode). This is the regression gate for
// the harness itself — if a future @tiptap/* or svelte-tiptap bump breaks
// NodeView rendering under vitest, this fails first.
//
// The IPC bindings are mocked (vi.hoisted + vi.mock) because the NodeView
// components (EmbedNodeView → EmbedPortal, BlockReferenceNodeView →
// BlockReferenceChip) call ResolveBlockReference at mount time. The mocks
// return a resolved reference so the happy-path rendering completes.

import { describe, it, expect, vi } from 'vitest'

// --- IPC mocks (must be hoisted before any component import) ---------------
// The NodeView components import from '../../wailsjs/go/main/App.js' (relative
// to the .svelte source). vi.mock resolves by module identity, so mocking the
// same resolved file from this test file (at lib/editor/) applies to every
// component that imports it.
const mocks = vi.hoisted(() => ({
  resolveBlockReference: vi.fn(),
  pluginMutateBlock: vi.fn(),
  fetchPageBlocks: vi.fn(),
  saveFileBlocks: vi.fn(),
  acquireFocusLock: vi.fn(),
  refreshFocusLock: vi.fn(),
  releaseFocusLock: vi.fn()
}))

vi.mock('../../../wailsjs/go/main/App.js', () => ({
  ResolveBlockReference: mocks.resolveBlockReference,
  PluginMutateBlock: mocks.pluginMutateBlock,
  FetchPageBlocks: mocks.fetchPageBlocks,
  SaveFileBlocks: mocks.saveFileBlocks,
  AcquireFocusLock: mocks.acquireFocusLock,
  RefreshFocusLock: mocks.refreshFocusLock,
  ReleaseFocusLock: mocks.releaseFocusLock
}))

vi.mock('../../../wailsjs/runtime/runtime.js', () => ({
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn()
}))

import {
  createNodeViewEditor,
  mountNodeViewEditor,
  mkBlock,
  FIXTURE_UUID_A,
  FIXTURE_UUID_B,
  createContainer
} from './nodeview-test-harness'

describe('NodeView test harness (#127)', () => {
  it('boots an editor with SiltBlockExtensionsWithNodeViews in jsdom', () => {
    const { container, cleanup } = createContainer()
    const blocks = [mkBlock('NOTE', { clean_text: 'hello world' })]
    const editor = createNodeViewEditor(blocks, container)

    expect(editor.isDestroyed).toBe(false)
    // The editor's DOM is attached to the container.
    expect(container.querySelector('.ProseMirror')).toBeTruthy()

    editor.destroy()
    cleanup()
  })

  it('renders a NoteBlock NodeView with a data-node-view-wrapper', async () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello world' })]
    const { editor, container, cleanup } = await mountNodeViewEditor(blocks)

    const wrapper = container.querySelector('[data-node-view-wrapper]')
    expect(wrapper).toBeTruthy()
    // The NodeView target gets a class like `node-noteBlock`.
    expect(container.querySelector('.node-noteBlock')).toBeTruthy()

    cleanup()
  })

  it('renders an embedNode NodeView when clean_text is a sole {{embed:uuid}}', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID_A,
      notebook: 'Work',
      section: 'Projects',
      page: 'Site',
      file_date: '2026-06-15',
      clean_text: 'embedded content'
    })

    const blocks = [
      mkBlock('NOTE', { clean_text: `{{embed:${FIXTURE_UUID_A}}}` })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // A sole-embed NOTE becomes a top-level embedNode (block-level atomic).
    // Its NodeView mounts an EmbedPortal inside a [data-node-view-wrapper].
    const embedWrapper = container.querySelector(
      '.node-embedNode > [data-node-view-wrapper], .node-embedNode'
    )
    expect(embedWrapper).toBeTruthy()

    cleanup()
  })

  it('renders a blockReferenceNode NodeView inside a NOTE block', async () => {
    mocks.resolveBlockReference.mockResolvedValue({
      exists: true,
      id: FIXTURE_UUID_B,
      notebook: 'Work',
      section: '',
      page: 'Top',
      file_date: '2026-06-15',
      clean_text: 'referenced content'
    })

    const blocks = [
      mkBlock('NOTE', { clean_text: `See ((${FIXTURE_UUID_B})) for context.` })
    ]
    const { container, cleanup } = await mountNodeViewEditor(blocks)

    // The block reference is an inline node rendered as a chip. Its NodeView
    // target gets the class `node-blockReferenceNode`.
    const refNode = container.querySelector('.node-blockReferenceNode')
    expect(refNode).toBeTruthy()

    cleanup()
  })

  it('mkBlock produces a ParsedBlock with valid defaults', () => {
    const block = mkBlock('TASK')
    expect(block.id).toBeTruthy()
    expect(block.type).toBe('TASK')
    expect(block.status).toBe('TODO')
    expect(block.clean_text).toBe('sample text')

    const note = mkBlock('NOTE', { clean_text: 'custom' })
    expect(note.type).toBe('NOTE')
    expect(note.status).toBe('')
    expect(note.clean_text).toBe('custom')
  })
})
