import { Extension } from '@tiptap/core'
import type { EditorView } from '@tiptap/pm/view'
import { NodeSelection, Plugin, PluginKey } from '@tiptap/pm/state'
import { Fragment, Slice } from '@tiptap/pm/model'
import { dropPoint } from '@tiptap/pm/transform'

// Notion-style indent-on-drop for the @tiptap/extension-drag-handle (#330,
// #181 follow-up). Native ProseMirror drop already reorders whole top-level
// blocks; this extension adds (a) a horizontal-position-driven depth change
// on drop and (b) a drop-zone indicator showing the target depth.
//
// The depth math is extracted as a pure helper (`resolveDropDepth`) so it
// can be unit-tested in jsdom — the HTML5 drag/drop pipeline itself cannot
// be driven from jsdom (no real DataTransfer / layout-driven posAtCoords),
// so the interactive path is covered by the TESTING.md manual matrix.
//
// RISK MODEL (this is document-mutating logic): every branch that cannot
// prove it can build a clean transaction returns `false`, handing control
// back to ProseMirror's native drop (which reorders without changing
// depth). We never partially dispatch — `tr` is a local object until
// `view.dispatch(tr)`, so bailing mid-build is always safe.

// INDENT_STEP_PX matches `--indent-unit` (frontend/src/index.css:103), the
// per-depth left-padding the block container CSS applies via
// `[data-depth='N'] { padding-left: calc(var(--indent-unit) * N) }` (index.css:444).
// Keeping this in sync is load-bearing: if the CSS unit changes, the snap
// grid here must change with it or the indicator and the applied depth
// diverge from the rendered indent.
export const INDENT_STEP_PX = 24

// MAX_DEPTH matches the deepest `[data-depth='N']` rule in the editor CSS
// (frontend/src/index.css:459 — `[data-depth='6']`). The Tab-indent keymap
// bounds indent by previous-sibling-depth + 1 (a relative cap), not an
// absolute constant, so this is the only absolute cap in the outliner —
// and it is driven by where the renderer stops drawing indent padding.
// Beyond depth 6 the block would render at depth-0 width (no matching CSS
// rule), so deeper indents would be invisible to the user.
export const MAX_DEPTH = 6

/**
 * Pure depth resolver: given the drop's clientX, the depth-0 content left
 * edge, the pixels per indent level, and the max allowed depth, return the
 * snapped indent level in `[0, maxDepth]`.
 *
 * Snaps to the nearest indent step (Notion-style grid): drop further right
 * → deeper indent. `clientX <= contentLeft` → 0. `clientX` beyond
 * `maxDepth * step` → `maxDepth`.
 *
 * Defensive: clamps `indentStepPx` to a positive value and `maxDepth` to a
 * non-negative value so a caller passing garbage never gets a divide-by-zero
 * or negative result. This is the ONLY logic the jsdom suite tests — it
 * must never throw.
 */
export function resolveDropDepth(
  clientX: number,
  contentLeft: number,
  indentStepPx: number,
  maxDepth: number
): number {
  // Guard against non-finite inputs (NaN/Infinity from a broken DOM rect).
  // Treat them as "left of content" → depth 0.
  if (!Number.isFinite(clientX) || !Number.isFinite(contentLeft)) return 0
  // Clamp the cap first so a negative maxDepth doesn't survive.
  const cap =
    Number.isFinite(maxDepth) && maxDepth > 0 ? Math.floor(maxDepth) : 0
  if (clientX <= contentLeft) return 0
  // step must be strictly positive; fall back to 1 to avoid div-by-zero /
  // inverted slope. (Pure-helper contract: never throw.)
  const step =
    Number.isFinite(indentStepPx) && indentStepPx > 0 ? indentStepPx : 1
  // Math.round uses round-half-up (0.5 → 1, 1.5 → 2). Documented so the test
  // suite can assert the exact midpoint direction.
  const raw = Math.round((clientX - contentLeft) / step)
  if (raw <= 0) return 0
  if (raw > cap) return cap
  return raw
}

// ---- internal helpers (unexported; only the pure fn above is unit-tested) --

// Shape of `view.dragging` at runtime. ProseMirror's public .d.ts types this
// as `{slice, move}` only, but the runtime Dragging class also carries
// `node: NodeSelection | undefined` for node drags (prosemirror-view's
// `class Dragging`). The DragHandle extension populates this whenever it
// initiates a block drag, so `node.from` gives us the dragged block's
// current source position — kept fresh across doc changes by
// `EditorView.updateDraggedNode`. No id-scan, no guessing.
interface DraggingLike {
  slice: Slice
  move: boolean
  node?: NodeSelection | null
}

/**
 * The depth-0 content left edge: the x-coordinate where a depth-0 block's
 * container starts. Computed from the editor DOM's bounding rect plus its
 * computed left padding (the block divs are direct children of
 * `.ProseMirror`, and `[data-depth='0']` adds no `padding-left`).
 *
 * Returns null if the rect or computed style can't be parsed (jsdom edge
 * cases, detached DOM) — callers must treat null as "bail to native".
 */
function resolveContentLeft(view: EditorView): number | null {
  const dom = view.dom as HTMLElement
  const rect = dom.getBoundingClientRect()
  if (!rect || !Number.isFinite(rect.left)) return null
  let padding = 0
  try {
    const computed = getComputedStyle(dom).paddingLeft
    const parsed = computed ? parseFloat(computed) : 0
    if (Number.isFinite(parsed)) padding = parsed
  } catch {
    // getComputedStyle can throw if the document is detached; treat as 0.
  }
  return rect.left + padding
}

/** Safe posAtCoords: returns the position or null on any failure. */
function safePosAtCoords(
  view: EditorView,
  clientX: number,
  clientY: number
): number | null {
  if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) return null
  try {
    const r = view.posAtCoords({ left: clientX, top: clientY })
    return r && typeof r.pos === 'number' ? r.pos : null
  } catch {
    return null
  }
}

// ---- drop-zone indicator (DOM overlay) -------------------------------------
// A single fixed-position element appended to the editor's ownerDocument
// body, scoped per-editor-view via a WeakMap so multiple editors never share
// indicators. Styled by `[data-silt-drop-indicator]` in index.css. The
// element renders a horizontal line at the drop boundary; a CSS ::before
// pseudo-element draws the vertical depth guide at `--silt-drop-depth-left`.
// The whole path is wrapped in try/catch — if DOM measurement is broken
// (jsdom), it silently hides and never blocks the drop.

const indicators = new WeakMap<EditorView, HTMLElement>()

function ensureIndicator(view: EditorView): HTMLElement | null {
  try {
    const cached = indicators.get(view)
    if (cached && cached.isConnected) return cached
    const owner = view.dom.ownerDocument
    const body = owner?.body
    if (!owner || !body) return null
    const el = owner.createElement('div')
    el.setAttribute('data-silt-drop-indicator', '')
    el.setAttribute('aria-hidden', 'true')
    el.style.position = 'fixed'
    el.style.pointerEvents = 'none'
    el.style.display = 'none'
    body.appendChild(el)
    indicators.set(view, el)
    return el
  } catch {
    return null
  }
}

function positionIndicator(
  view: EditorView,
  clientX: number,
  clientY: number
): void {
  const el = ensureIndicator(view)
  if (!el) return
  const contentLeft = resolveContentLeft(view)
  if (contentLeft == null) {
    hideIndicator(view)
    return
  }
  const pos = safePosAtCoords(view, clientX, clientY)
  if (pos == null) {
    hideIndicator(view)
    return
  }
  let coords: { top: number; bottom: number; left: number; right: number }
  try {
    coords = view.coordsAtPos(pos)
  } catch {
    hideIndicator(view)
    return
  }
  if (
    !Number.isFinite(coords.top) ||
    !Number.isFinite(coords.bottom) ||
    !Number.isFinite(coords.left)
  ) {
    hideIndicator(view)
    return
  }
  const newDepth = resolveDropDepth(
    clientX,
    contentLeft,
    INDENT_STEP_PX,
    MAX_DEPTH
  )
  const editorRect = view.dom.getBoundingClientRect()
  // Horizontal line spans the editor's content width (rect-based, not the
  // narrow cursor-coords, so the affordance reads as a row boundary).
  const width = Math.max(editorRect.right - coords.left, 0)
  el.style.top = `${coords.top}px`
  el.style.left = `${coords.left}px`
  el.style.width = `${width}px`
  el.style.height = `${Math.max(coords.bottom - coords.top, 0)}px`
  // The vertical depth-guide pseudo-element reads this to position itself
  // at the resolved indent column.
  el.style.setProperty(
    '--silt-drop-depth-left',
    `${contentLeft + newDepth * INDENT_STEP_PX}px`
  )
  el.style.setProperty('--silt-drop-depth-top', `${coords.top}px`)
  el.style.setProperty(
    '--silt-drop-depth-height',
    `${Math.max(coords.bottom - coords.top, 0)}px`
  )
  el.style.display = 'block'
}

function hideIndicator(view: EditorView): void {
  try {
    const el = indicators.get(view)
    if (el) el.style.display = 'none'
  } catch {
    // ignore — indicator cleanup is best-effort
  }
}

// ---- the extension ---------------------------------------------------------

const pluginKey = new PluginKey('siltBlockIndentOnDrop')

export const BlockIndentOnDrop = Extension.create({
  name: 'siltBlockIndentOnDrop',

  addProseMirrorPlugins() {
    return [
      new Plugin({
        key: pluginKey,
        props: {
          // handleDrop returns true ONLY when we fully handled the drop
          // (deleted the source, inserted at a valid top-level pos, and set
          // the new depth). Any uncertainty → return false → PM's native
          // drop handler runs (reorder-only, no depth change). This is the
          // conservative fallback: native reorder still works, we just
          // don't touch depth.
          handleDrop(view, event) {
            const dragEvent = event as DragEvent
            const clientX = dragEvent.clientX
            const clientY = dragEvent.clientY
            if (!Number.isFinite(clientX) || !Number.isFinite(clientY)) {
              return false
            }

            // 1. Only handle node drags. Inline-content drags have no
            //    `dragging.node`; bail so PM's native inline-paste-on-drop
            //    runs.
            const dragging = view.dragging as DraggingLike | null
            const nodeSel = dragging?.node
            if (!nodeSel) return false

            const draggedNode = nodeSel.node
            const oldPos = nodeSel.from
            if (!draggedNode || !Number.isFinite(oldPos)) return false

            // 2. The dragged node must carry a `depth` attr. Prose blocks
            //    (noteBlock/taskBlock/headerBlock) do; calloutBlock, tables,
            //    codeBlock, embeds, details do NOT — indenting those would
            //    silently no-op on save (docToBlocks only reads depth for
            //    the prose types). Bail so native reorders without a bogus
            //    attr write.
            if (draggedNode.attrs.depth === undefined) return false

            // 3. Top-level-only guard. Silt blocks are flat (doc children
            //    carry a `depth` ATTR for indent — NOT a ProseMirror tree).
            //    Allowing a block nested inside a callout/details to be
            //    re-indented-and-moved this way would corrupt structure.
            //    `resolve(oldPos).depth === 0` ⟺ the node is a direct
            //    child of the doc (mirror of moveActiveBlock's
            //    `active.depth !== 1` guard in keymaps.ts:172).
            const { state } = view
            const { doc } = state
            // Bounds + identity guard. `nodeSel.from` is NOT remapped across
            // transactions applied mid-drag (IPC sync update, plugin side-
            // effect), so a stale `from` can point at a DIFFERENT block.
            // Bounds-check before resolve (avoids RangeError), confirm the
            // node is a direct doc child ($pos.depth is ProseMirror tree
            // depth — 0 == doc child — unrelated to the node's attrs.depth
            // indent attr), then confirm the doc child at oldPos IS the
            // dragged node. Else bail to native: never delete the wrong block.
            if (oldPos < 0 || oldPos > doc.content.size) return false
            const $old = doc.resolve(oldPos)
            if ($old.depth !== 0) return false
            if (!$old.nodeAfter || !$old.nodeAfter.eq(draggedNode)) return false

            // 4. Resolve drop position. Bail if the cursor is outside the
            //    editor (posAtCoords returns null) — native handles edge
            //    drops.
            const dropPos = safePosAtCoords(view, clientX, clientY)
            if (dropPos == null) {
              hideIndicator(view)
              return false
            }

            // 5. Compute contentLeft + new depth via the PURE helper.
            const contentLeft = resolveContentLeft(view)
            if (contentLeft == null) return false
            const newDepth = resolveDropDepth(
              clientX,
              contentLeft,
              INDENT_STEP_PX,
              MAX_DEPTH
            )

            // 6. Find a valid insert position in the ORIGINAL doc via
            //    dropPoint. dropPoint returns null when no valid drop
            //    exists (e.g. the slice can't land at the resolved pos);
            //    bail in that case so native gets a chance.
            const slice = new Slice(Fragment.from(draggedNode), 0, 0)
            const insertAt = dropPoint(doc, dropPos, slice)
            if (insertAt == null) return false

            // 7. Build ONE transaction: delete the source, map the insert
            //    position through the delete, re-validate top-level-ness
            //    on the POST-delete doc, then insert + set depth.
            const tr = state.tr
            tr.delete(oldPos, oldPos + draggedNode.nodeSize)
            const mappedInsert = tr.mapping.map(insertAt)
            // The mapped insert position must still resolve to a top-level
            // child slot. A drop that resolves to a position inside a
            // container node (callout/details/table cell) after the delete
            // shift would nest the dragged block — corruption. Bail.
            try {
              if (tr.doc.resolve(mappedInsert).depth !== 0) return false
            } catch {
              return false
            }
            tr.insert(mappedInsert, draggedNode)
            // setNodeAttribute targets the node starting AT mappedInsert,
            // which post-insert is the just-inserted dragged node (insert
            // does not shift positions ≤ mappedInsert). Verified depth attr
            // exists on this node type at step 2.
            tr.setNodeAttribute(mappedInsert, 'depth', newDepth)

            // 8. Land a NodeSelection on the moved block so the caret/focus
            //    land on it in its new home (mirrors PM's native drop
            //    selection), then dispatch. Dispatch is wrapped: a step that
            //    fails to apply (should be unreachable given the guards above)
            //    bails to native rather than throwing into PM's dispatcher.
            tr.setSelection(NodeSelection.create(tr.doc, mappedInsert))
            hideIndicator(view)
            try {
              view.dispatch(tr)
            } catch {
              return false
            }
            view.focus()
            return true
          },

          // Drop-zone indicator. `handleDOMEvents` is ProseMirror's prop
          // name (TipTap's `addDomEventHandler` wraps it). Returning false
          // lets PM's built-in dragover preventDefault still run (it must,
          // or the browser refuses the drop). The handlers only position/
          // hide the overlay; they never preventDefault themselves.
          handleDOMEvents: {
            dragover(view: EditorView, event: Event) {
              const e = event as DragEvent
              if (Number.isFinite(e.clientX) && Number.isFinite(e.clientY)) {
                positionIndicator(view, e.clientX, e.clientY)
              }
              return false
            },
            dragleave(view: EditorView, event: Event) {
              // Only hide when the cursor actually leaves the editor (not
              // when moving between child elements, which also fires
              // dragleave on the parent).
              const e = event as DragEvent
              const related = e.relatedTarget as Node | null
              if (related && view.dom.contains(related)) return false
              hideIndicator(view)
              return false
            },
            dragend(view: EditorView) {
              hideIndicator(view)
              return false
            }
          }
        },
        // Clean up the overlay element when the editor (plugin) is torn down.
        view(view: EditorView) {
          return {
            destroy() {
              try {
                const el = indicators.get(view)
                if (el && el.isConnected) el.remove()
                indicators.delete(view)
              } catch {
                // ignore
              }
            }
          }
        }
      })
    ]
  }
})
