// uniqueIdPlugin — a ProseMirror appendTransaction plugin that guarantees
// every Silt block node in the doc has a unique, non-null UUID.
//
// Why this exists: a block's `id` attr is its identity — it becomes the
// `<!-- id: uuid -->` comment on disk, the `blocks` table PRIMARY KEY, and the
// key used by `{{embed:uuid}}` / `((uuid))` references. Without this plugin:
//   - Pasting a block would duplicate its UUID → blocks-table PK collision and
//     embed/reference resolution would target the wrong copy.
//   - A freshly typed block (Enter key) would have id=null → it would reach
//     disk without an identity and the Go parser would assign one on next
//     parse, but the editor's local copy would be stale.
//
// The plugin runs after every doc-changing transaction and:
//   1. Walks the doc's top-level block nodes in order.
//   2. Tracks seen ids in a Set.
//   3. For any block whose id is null/empty OR already seen (paste/duplicate),
//      mints a fresh UUID via crypto.randomUUID() and sets it.
//   4. Returns a follow-up transaction with the fixes (so the change is
//      transactional, undo-able, and fires onUpdate once).
//
// Pattern: discuss.prosemirror.net #2808 (appendTransaction for unique ids).

import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'

const uniqueIdKey = new PluginKey('silt-unique-block-ids')

// The block node names that carry a UUID identity.
const BLOCK_NODE_NAMES = new Set(['taskBlock', 'noteBlock', 'headerBlock'])

function freshId(): string {
  // crypto.randomUUID is available in all modern browsers and the Wails
  // webview. Guard for the test environment (jsdom) which also has it.
  if (
    typeof crypto !== 'undefined' &&
    typeof crypto.randomUUID === 'function'
  ) {
    return crypto.randomUUID()
  }
  // Fallback: RFC4122 v4-ish. Only hit in very old runtimes.
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

export const UniqueBlockIds = Extension.create({
  name: 'uniqueBlockIds',

  addProseMirrorPlugins() {
    return [
      new Plugin({
        key: uniqueIdKey,
        appendTransaction: (transactions, oldState, newState) => {
          // Only react to doc-changing transactions. Pure selection moves
          // (transactions.every(t => !t.docChanged)) need no id fix-up.
          const docChanged = transactions.some((t) => t.docChanged)
          if (!docChanged) return null

          const tr = newState.tr
          const seen = new Set<string>()
          let fixedAny = false

          // Walk top-level block children of the doc in order. The `offset`
          // from forEach is the fragment-relative position of the child —
          // exactly what setNodeAttribute expects (ProseMirror positions are
          // flat: the first child is at position 0, the second at
          // firstChild.nodeSize, etc.).
          newState.doc.forEach((child, offset) => {
            if (!BLOCK_NODE_NAMES.has(child.type.name)) return
            const attrs = child.attrs as Record<string, unknown>
            const currentId = attrs.id as string | null | undefined

            const needsNewId = !currentId || seen.has(currentId)
            if (needsNewId) {
              const newId = freshId()
              tr.setNodeAttribute(offset, 'id', newId)
              seen.add(newId)
              fixedAny = true
            } else {
              seen.add(currentId)
            }
          })

          if (!fixedAny) return null
          // Mark as not user-visible in the history diff so undo collapses the
          // id assignment into the originating step.
          tr.setMeta('addToHistory', false)
          tr.setMeta(uniqueIdKey, true)
          return tr
        }
      })
    ]
  }
})

// Exposed for tests and for the editor surface to seed a fresh id when
// imperatively inserting a new block outside a transaction (rare).
export { freshId }
