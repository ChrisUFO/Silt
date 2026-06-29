// Inline drag-init for Silt blocks (#339). Replaces the
// `@tiptap/extension-drag-handle` floating grip with a tiny TipTap extension
// that listens for `dragstart` on the existing `<span data-drag-handle>`
// inline markers rendered inside every block-level NodeView
// (`NoteBlockView`, `TaskBlockView`, `HeaderBlockView`, `EmbedBlockNodeView`).
//
// Why this exists — the upstream extension always creates its OWN DOM via
// its `render()` option and positions it via @floating-ui on top of the
// hovered block. It does not reuse `[data-drag-handle]` elements. So for the
// last several sprints every hovered block rendered TWO stacked grip icons —
// the floating one always sat on top of the inline one (see #339 screenshots
// in `wails dev`). This extension retires the floating path entirely and
// turns the inline span into the actual drag trigger by populating
// ProseMirror's standard `view.dragging` slot on `dragstart`, mirroring the
// reference pattern at
// `frontend/node_modules/@tiptap/extension-drag-handle/dist/index.js:464–533`.
//
// Contract with `dragIndentDrop.ts` (BlockIndentOnDrop, #330):
// `view.dragging = { slice, move: true, node: NodeSelection }` is what the
// existing indent-on-drop, depth-guide, and Esc-cancel paths read. We do not
// change the consumer — only the producer. Stage 1 of this branch already
// removed the floating half (#339 commit `fix(editor): drop floating
// DragHandle extension`); this commit finishes the job by re-enabling
// mouse-drag, now from the inline span.
//
// Interactive coverage: HTML5 drag/drop cannot be driven from jsdom
// (per the project AGENTS.md), so the dispatch path is gated on the
// `wails dev` manual matrix in TESTING.md. The pure helpers below
// (`resolveDraggedBlockPosition`, `buildNodeDragSelection`,
// `computeDragImageOffset`) ARE unit-tested in `siltInlineDragHandle.test.ts`.

import { Extension } from '@tiptap/core'
import type { EditorView } from '@tiptap/pm/view'
import { NodeSelection, Plugin, PluginKey } from '@tiptap/pm/state'
import { Slice } from '@tiptap/pm/model'

const siltInlineDragHandleKey = new PluginKey('siltInlineDragHandle')

/**
 * Walk the top-level children of `doc` and return the first whose
 * `attrs.id` matches `blockId`. Returns `null` if no match is found.
 *
 * Pure helper — exported for unit tests. Does not touch the editor view,
 * DOM, or any global state.
 *
 * Why top-level-only: Silt blocks are flat (doc children carry a `depth`
 * ATTR for indent — NOT a ProseMirror tree). Walking beyond top-level would
 * surface nested blocks (e.g. inside a callout or details container) which
 * are not reorder-eligible via the inline drag handle; the consumer in
 * `dragIndentDrop.ts` enforces the same top-level-only invariant.
 */
export function resolveDraggedBlockPosition(
  doc: { forEach: (cb: (child: any, offset: number) => boolean | void) => void },
  blockId: string
): { pos: number; node: any } | null {
  let found: { pos: number; node: any } | null = null
  doc.forEach((child, offset) => {
    if (found) return false
    const attrs = child && (child.attrs as Record<string, unknown> | undefined)
    if (attrs && attrs.id === blockId) {
      found = { pos: offset, node: child }
      return false
    }
    return true
  })
  return found
}

/**
 * Build a `Slice` from a single top-level block node — exactly the slice
 * the upstream `@tiptap/extension-drag-handle` builds via
 * `view.state.doc.slice(from, to)` for a single-block drag (line 492 of
 * `dist/index.js`). The block runs from `from` to `from + node.nodeSize`.
 */
export function buildBlockSlice(doc: any, node: any): Slice {
  const nodeSize = typeof node?.nodeSize === 'number' ? node.nodeSize : 0
  return doc.slice(0, nodeSize)
}

/**
 * Build a `NodeSelection` for the block at `pos` in `doc` — exactly the
 * selection the upstream extension builds on line 493.
 */
export function buildNodeDragSelection(doc: any, pos: number): NodeSelection {
  return NodeSelection.create(doc, pos)
}

/**
 * The horizontal/vertical pixel offsets to pass to
 * `event.dataTransfer.setDragImage(element, x, y)` so the block follows the
 * cursor with the original grab point at the cursor tip.
 *
 * Mirrors `getDragImageOffset` in the reference (extension-drag-handle
 * `dist/index.js:425`). `grabX` is the pixel offset from the drag-image
 * element's left edge to where the user actually grabbed (the inline span's
 * left edge in absolute coords, relative to the block's left edge). `y` is
 * always 0 — the inline span is at the top of the row.
 *
 * Defensive against broken `getBoundingClientRect` (`Number.NaN` left/width)
 * — returns zeros so `setDragImage` still gets a sensible call instead of
 * throwing.
 */
export function computeDragImageOffset(
  blockRectLeft: number,
  blockRectWidth: number,
  handleRectLeft: number,
  handleRectWidth: number
): { x: number; y: number } {
  if (
    !Number.isFinite(blockRectLeft) ||
    !Number.isFinite(blockRectWidth) ||
    !Number.isFinite(handleRectLeft) ||
    !Number.isFinite(handleRectWidth)
  ) {
    return { x: 0, y: 0 }
  }
  const raw = handleRectLeft - blockRectLeft
  const clamped = Math.min(Math.max(raw, 0), Math.max(blockRectWidth - 1, 0))
  return { x: clamped, y: 0 }
}

/**
 * Resolve the block-level DOM element that owns a `dragstart` event. Walks
 * up from `target` looking for the first ancestor carrying a `data-id`
 * attribute — the Silt block wrapper. Returns `null` if the drag originated
 * from a non-Silt element (e.g. the editor's chrome, the format toolbar) so
 * the browser's native drag behaviour runs unaltered.
 *
 * The `data-id` attribute is set by the NodeView templates on
 * `NodeViewWrapper` (see `#181`, `#339`); `closest('[data-id]')` resolves
 * it in O(depth) regardless of how deeply nested the inline span is.
 */
function findBlockEl(target: EventTarget | null): HTMLElement | null {
  if (!(target instanceof Element)) return null
  let el: Element | null = target
  while (el) {
    if (el.hasAttribute('data-id')) return el as HTMLElement
    el = el.parentElement
  }
  return null
}

/**
 * The actual `dragstart` handler — wired on `view.dom` by the plugin's
 * `view` lifecycle. Runs in capture-phase delegation so it fires for any
 * `dragstart` whose target descends from a `[data-id]` ancestor (the inline
 * span itself, or one of its children).
 *
 * Sequence (mirrors the upstream extension's `dragHandler`,
 * `extension-drag-handle/dist/index.js:464–533`):
 *   1. Bail if the event did not originate from a Silt block.
 *   2. Resolve the block position in the doc via `data-id`.
 *   3. Build `{ slice, selection, dragImage }` from the resolved node.
 *   4. Populate `view.dragging = { slice, move: true, node }` BEFORE
 *      dispatching — the upstream sets it before too (line 528); ordering
 *      matters because the indent-on-drop `handleDrop` reads it synchronously.
 *   5. Dispatch a `setSelection(NodeSelection)` so PM's own drop handler
 *      (and anything else that reads `editor.state.selection`) sees the
 *      dragged block.
 *
 * We do NOT `event.preventDefault()` — preventing dragstart cancels the
 * drag entirely. The browser's default behaviour runs after our setup,
 * which is exactly what we want: a transparent drag image that follows
 * the cursor with the block's full DOM.
 */
function handleDragStart(view: EditorView, event: DragEvent): void {
  const blockEl = findBlockEl(event.target)
  if (!blockEl) return

  const blockId = blockEl.getAttribute('data-id')
  if (!blockId) return

  const found = resolveDraggedBlockPosition(view.state.doc, blockId)
  if (!found) return

  const { pos, node } = found

  const slice = buildBlockSlice(view.state.doc, node)
  const selection = buildNodeDragSelection(view.state.doc, pos)

  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.clearData()

    const blockRect = blockEl.getBoundingClientRect()
    const handleEl = event.target instanceof Element ? event.target : blockEl
    const handleRect = handleEl.getBoundingClientRect()
    const { x: grabX, y: grabY } = computeDragImageOffset(
      blockRect.left,
      blockRect.width,
      handleRect.left,
      handleRect.width
    )
    try {
      event.dataTransfer.setDragImage(blockEl, grabX, grabY)
    } catch {
      // setDragImage throws if the element is detached or the document
      // is hidden — degrade to "no custom drag image" (browser default)
      // rather than failing the drag entirely.
    }
  }

  // Populate the consumer contract (see dragIndentDrop.ts:88–92).
  const nodeSel = selection instanceof NodeSelection ? selection : undefined
  // Cast `view.dragging` — the runtime accepts `{slice, move, node}` even
  // though the public `.d.ts` only types `slice` + `move`.
  ;(view as unknown as { dragging: { slice: Slice; move: boolean; node: NodeSelection | undefined } }).dragging = {
    slice,
    move: true,
    node: nodeSel
  }

  // Land the selection on the block BEFORE the browser completes the drag
  // image — PM's drop handler reads `editor.state.selection` synchronously.
  const tr = view.state.tr.setSelection(selection)
  view.dispatch(tr)
}

export const SiltInlineDragHandle = Extension.create({
  name: 'siltInlineDragHandle',

  addProseMirrorPlugins() {
    return [
      new Plugin({
        key: siltInlineDragHandleKey,
        view: (view) => {
          const handler = (event: Event): void => {
            if (!(event instanceof DragEvent)) return
            if (event.type !== 'dragstart') return
            handleDragStart(view, event)
          }
          view.dom.addEventListener('dragstart', handler)
          return {
            destroy() {
              view.dom.removeEventListener('dragstart', handler)
            }
          }
        }
      })
    ]
  }
})
