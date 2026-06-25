// Reusable test harness for TipTap + Svelte NodeView integration tests (#127).
//
// The core problem (#127): a full editor-with-NodeViews integration test
// requires a live TipTap Editor constructed with `SiltBlockExtensionsWithNodeViews`
// (the production extension stack that registers Svelte NodeView renderers for
// every block type), attached to a real DOM element so ProseMirror's NodeView
// lifecycle mounts the Svelte components (EmbedNodeView, BlockReferenceNodeView,
// etc.) inside `[data-node-view-wrapper]` containers. This harness centralizes
// that setup so #127's three test files and #142's TabStrip tests share one
// code path.
//
// IMPORTANT: this module does NOT mock the Wails IPC bindings. Each test file
// MUST set up its own `vi.mock('../../wailsjs/go/main/App.js', ...)` (via
// `vi.hoisted`) before importing anything that triggers NodeView component
// mounting. The canonical pattern is in AppearanceTab.test.ts. The relative
// path from a test at `frontend/src/components/X.test.ts` to the bindings is
// `'../../wailsjs/go/main/App.js'`; from `frontend/src/lib/editor/X.test.ts`
// it is `'../../../wailsjs/go/main/App.js'`. Both resolve to the same module,
// so vitest's mock applies to every component that imports the bindings.

import { Editor } from '@tiptap/core'
import type { Editor as EditorType } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import {
  SiltBlockExtensionsWithNodeViews,
  UniqueBlockIds,
  blocksToDoc
} from './index'
import type { ParsedBlock } from './types'

// jsdom omits or stubs layout-dependent DOM APIs that TipTap v3's Placeholder
// viewport tracker touches during editor construction. We omit Placeholder
// from the harness extension stack (matching converters.test.ts), but some
// ProseMirror internals may still touch elementFromPoint during NodeView
// rendering. Patch it defensively on first import.
if (typeof document !== 'undefined' && !document.elementFromPoint) {
  document.elementFromPoint = () => document.body
}

/**
 * Build a live TipTap editor with the SAME extension stack the production
 * TipTapEditor.svelte uses:
 * - StarterKit (with competing block types disabled so Silt's block nodes
 *   are the only top-level blocks)
 * - SiltBlockExtensionsWithNodeViews (Svelte NodeView renderers for
 *   TaskBlock / NoteBlock / HeaderBlock / EmbedNode / BlockReferenceNode /
 *   EmbedBlockNode)
 * - UniqueBlockIds (mints fresh UUIDs for pasted/duplicated blocks)
 *
 * Placeholder is omitted: its viewport tracker calls document.elementFromPoint
 * which jsdom does not implement.
 */
export function createNodeViewEditor(
  blocks: ParsedBlock[],
  container?: HTMLElement
): EditorType {
  const target = container ?? createContainer().container
  return new Editor({
    element: target,
    extensions: [
      StarterKit.configure({
        // paragraph stays enabled: TipTap's Table extension fills cells with
        // paragraph nodes, and calloutBlock uses content:'paragraph+'.
        heading: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        trailingNode: false
      }),
      ...SiltBlockExtensionsWithNodeViews,
      UniqueBlockIds
    ],
    content: blocksToDoc(blocks)
  })
}

/**
 * Create a `<div>` container attached to `document.body`, return it and a
 * cleanup function that removes it. ProseMirror needs the editor element to
 * be in the live DOM for NodeView rendering to work.
 */
export function createContainer(): {
  container: HTMLDivElement
  cleanup: () => void
} {
  const container = document.createElement('div')
  document.body.appendChild(container)
  return {
    container,
    cleanup: () => container.remove()
  }
}

/**
 * Mount a NodeView editor into a fresh container attached to document.body,
 * drain the microtask queue so Svelte NodeView components settle, and return
 * the editor + container + cleanup function.
 *
 * Use this in tests that need to query the rendered NodeView DOM
 * (e.g. `[data-node-view-wrapper]`, `.node-embedNode`,
 * `.node-blockReferenceNode`).
 */
export async function mountNodeViewEditor(blocks: ParsedBlock[]): Promise<{
  editor: EditorType
  container: HTMLDivElement
  cleanup: () => void
}> {
  const { container, cleanup } = createContainer()
  const editor = createNodeViewEditor(blocks, container)
  // Svelte 5's mount() is synchronous, but ProseMirror may defer some DOM
  // writes to a microtask. Drain the queue so NodeView components are
  // fully rendered before the test asserts.
  await new Promise((resolve) => setTimeout(resolve, 0))
  return {
    editor,
    container,
    cleanup: () => {
      editor.destroy()
      cleanup()
    }
  }
}

/**
 * Build a ParsedBlock with sensible defaults for a given type. Mirrors the
 * `mkBlock` helper in converters.test.ts so tests across the editor surface
 * share one block-factory shape.
 */
export function mkBlock(
  type: ParsedBlock['type'],
  overrides: Partial<ParsedBlock> = {}
): ParsedBlock {
  const defaultStatus = type === 'TASK' ? 'TODO' : ''
  return {
    id: crypto.randomUUID(),
    parent_id: '',
    type,
    depth: 0,
    raw_text: '',
    clean_text: 'sample text',
    status: defaultStatus,
    owner: '',
    start_date: '',
    due_date: '',
    priority: 3,
    line_number: 1,
    file_date: '2026-06-15',
    ...overrides
  }
}

/**
 * A stable UUID for tests that need a deterministic embed/reference target.
 */
export const FIXTURE_UUID_A = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
export const FIXTURE_UUID_B = '11111111-2222-4333-8444-555555555555'
