import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { TextSelection } from '@tiptap/pm/state'
import { freshId } from './uniqueIdPlugin'

// SiltBlockKeymaps — outliner keyboard semantics for the TipTap editor.
//
// Ports the keydown logic from the legacy BlockRenderer.svelte (lines 280-398)
// to TipTap keyboard shortcuts / ProseMirror keymap bindings:
//   - Enter: create a new NoteBlock at the same depth below the cursor.
//   - Backspace at start of empty block: unindent first, then delete+focus-prev.
//   - Tab / Shift+Tab (config-driven): indent / unindent (bounded by previous
//     sibling's depth + 1, matching the legacy outliner constraints).
//   - ArrowUp / ArrowDown at block boundary: move focus to the previous/next block.
//
// The extension reads the indent/unindent hotkeys live from the settings store
// so users can remap or disable them from Settings → General.

/** The Silt block node types, in canonical order. NoteBlock is first (default). */
export const BLOCK_TYPES = [
  'noteBlock',
  'taskBlock',
  'headerBlock',
  'calloutBlock'
] as const

/**
 * Block node types that carry a `depth` attr and participate in the outliner's
 * indent/outdent model. Callout blocks (and other new container/atomic types)
 * are intentionally excluded: they have no depth attr, so indenting them would
 * be silently dropped on save. Tab/Shift+Tab fall through for those so TipTap's
 * default (e.g. table cell navigation) applies.
 */
const DEPTH_BLOCK_TYPES = new Set(['noteBlock', 'taskBlock', 'headerBlock'])

/**
 * Walk up from the editor's current selection to the nearest enclosing block
 * node (noteBlock / taskBlock / headerBlock). Returns the block node and its
 * depth, or `null` if the selection is not inside a block.
 *
 * Shared by every consumer that needs "the block I'm currently in" —
 * previously inlined 4× in TipTapEditor and 2× in HeadingLevelMenu with
 * subtly different return shapes.
 */
export function findActiveBlock(
  editor: Editor
): { node: ProseMirrorNode; depth: number } | null {
  const pos = editor.state.selection.$from
  for (let d = pos.depth; d >= 1; d--) {
    const node = pos.node(d)
    if (BLOCK_TYPES.includes(node.type.name as (typeof BLOCK_TYPES)[number])) {
      return { node, depth: d }
    }
  }
  return null
}

function getNextBullet(currentBullet: string): string {
  if (!currentBullet) return ''
  if (['- ', '* ', '+ '].includes(currentBullet)) {
    return currentBullet
  }
  const match = currentBullet.match(/^(\d+)([.)]\s)$/)
  if (match) {
    const nextNum = parseInt(match[1], 10) + 1
    const punc = match[2]
    return `${nextNum}${punc}`
  }
  return currentBullet
}

// Convert the current block to a new type (#169). Provides the correct attrs
// for each type (discarding type-specific attrs that don't apply). Shared by
// the keymap shortcuts and TipTapEditor's slash command handler.
export function convertToBlock(
  editor: Editor,
  type: 'headerBlock' | 'noteBlock' | 'taskBlock',
  headerDepth?: number
): boolean {
  const active = findActiveBlock(editor)
  if (!active) return false
  const node = active.node
  const baseAttrs = {
    id: node.attrs.id,
    depth:
      type === 'headerBlock' ? (headerDepth ?? 1) : (node.attrs.depth ?? 0),
    file_date: node.attrs.file_date || ''
  }
  if (type === 'noteBlock') {
    editor.commands.setNode(type, { ...baseAttrs, bullet: '- ' })
  } else if (type === 'taskBlock') {
    editor.commands.setNode(type, {
      ...baseAttrs,
      status: 'TODO',
      owner: '',
      start_date: '',
      due_date: '',
      priority: 3
    })
  } else {
    editor.commands.setNode(type, baseAttrs)
  }
  return true
}

function currentBlockInfo(editor: Editor) {
  const active = findActiveBlock(editor)
  if (!active) return null
  const { node, depth } = active
  const pos = editor.state.selection.$from
  return {
    node,
    pos: pos.before(depth),
    depth: node.attrs.depth || 0,
    index: pos.index(depth)
  }
}

function setBlockDepth(
  editor: Editor,
  nodePos: number,
  newDepth: number
): void {
  const tr = editor.state.tr.setNodeAttribute(nodePos, 'depth', newDepth)
  editor.view.dispatch(tr)
}

function focusBlockAt(editor: Editor, blockIndex: number): void {
  const { doc } = editor.state
  if (blockIndex < 0 || blockIndex >= doc.childCount) return
  let pos = 0
  for (let i = 0; i < blockIndex; i++) {
    pos += doc.child(i).nodeSize
  }
  const child = doc.child(blockIndex)
  const endPos = pos + child.nodeSize - 1
  editor.commands.focus()
  const tr = editor.state.tr.setSelection(
    TextSelection.create(editor.state.doc, endPos, endPos)
  )
  editor.view.dispatch(tr)
}

// Set the alignment attr on the current block (#173). No-op for TASK blocks
// (alignment is not supported on tasks — the taskBlock schema has no align attr).
// Shared by the keymap shortcuts and TipTapEditor's slash command handler.
export function setBlockAlign(editor: Editor, align: string): boolean {
  if (!editor || editor.isDestroyed) return false
  const active = findActiveBlock(editor)
  if (!active) return false
  if (active.node.type.name === 'taskBlock') return true // silently skip
  const nodePos = editor.state.selection.$from.before(active.depth)
  const tr = editor.state.tr.setNodeAttribute(nodePos, 'align', align)
  editor.view.dispatch(tr)
  return true
}

// Toggle the blockquote marker on the current noteBlock (#188). Quote and
// bullet are mutually exclusive — the on-disk serializer (docToBlocks) discards
// `bullet` while `quote` is set, so turning quote ON clears the bullet here to
// keep the in-editor state consistent with the save→reload cycle. Toggling
// quote OFF yields a plain note (the bullet was already '' from the quote
// state). No-op on TASK/HEADER blocks (quote is a NOTE marker).
export function toggleBlockQuote(editor: Editor): boolean {
  if (!editor || editor.isDestroyed) return false
  const active = findActiveBlock(editor)
  if (!active) return false
  if (active.node.type.name !== 'noteBlock') return true // silently skip
  const nodePos = editor.state.selection.$from.before(active.depth)
  const isQuote = !!active.node.attrs.quote
  // Quote and bullet are mutually exclusive on disk (docToBlocks discards
  // bullet when quote is set). Clearing bullet on toggle-ON keeps the
  // in-editor state consistent with the save→reload cycle.
  const tr = editor.state.tr.setNodeMarkup(nodePos, undefined, {
    ...active.node.attrs,
    quote: isQuote ? '' : '> ',
    bullet: isQuote ? (active.node.attrs.bullet ?? '') : ''
  })
  editor.view.dispatch(tr)
  return true
}

// Insert a callout block at the current selection (#180/#308). The callout
// replaces the current block when it is an empty note, otherwise inserts a new
// callout below. The variant drives the icon + accent (CALLOUT_VARIANTS in
// schema.ts). Under `content: 'block+'` the callout MUST seed a placeholder
// paragraph (block+ requires ≥1 child).
export function insertCallout(editor: Editor, variant: string): boolean {
  if (!editor || editor.isDestroyed) return false
  const today = new Date().toISOString().slice(0, 10)
  const paragraph = editor.state.schema.nodes.paragraph
  const calloutNode = editor.state.schema.nodes.calloutBlock?.create(
    { id: null, variant, file_date: today },
    paragraph ? [paragraph.create()] : []
  )
  if (!calloutNode) return false
  // If the current block is an empty note/header, replace it in place.
  const active = findActiveBlock(editor)
  const isEmptyNote =
    active &&
    (active.node.type.name === 'noteBlock' ||
      active.node.type.name === 'headerBlock') &&
    (active.node.content.size === 0 || active.node.textContent.trim() === '')
  if (active && isEmptyNote) {
    const pos = editor.state.selection.$from.before(active.depth)
    editor.view.dispatch(
      editor.state.tr.replaceWith(pos, pos + active.node.nodeSize, calloutNode)
    )
    editor.commands.focus()
    return true
  }
  editor.commands.insertContent(calloutNode)
  editor.commands.focus()
  return true
}

// Insert a fenced code block at the current selection (#189). Replaces the
// current block when it is an empty note/header, otherwise inserts below.
export function insertCodeBlock(editor: Editor, language = ''): boolean {
  if (!editor || editor.isDestroyed) return false
  const today = new Date().toISOString().slice(0, 10)
  // An empty code block has NO text children — codeBlock's content is 'text*'
  // (zero or more), which a content-less create satisfies. ProseMirror rejects
  // empty *text nodes* (schema.text('') throws), so we must not synthesize one;
  // the user's typing adds real text nodes as they go.
  const codeNode = editor.state.schema.nodes.codeBlock?.create({
    id: null,
    language,
    file_date: today
  })
  if (!codeNode) return false
  const active = findActiveBlock(editor)
  const isEmptyNote =
    active &&
    (active.node.type.name === 'noteBlock' ||
      active.node.type.name === 'headerBlock') &&
    (active.node.content.size === 0 || active.node.textContent.trim() === '')
  if (active && isEmptyNote) {
    const pos = editor.state.selection.$from.before(active.depth)
    editor.view.dispatch(
      editor.state.tr.replaceWith(pos, pos + active.node.nodeSize, codeNode)
    )
    editor.commands.focus()
    return true
  }
  editor.commands.insertContent(codeNode)
  editor.commands.focus()
  return true
}

// Insert a foldable `<details>` section (#183). Builds the Details >
// DetailsSummary + DetailsContent(placeholder note) tree the TipTap extension
// expects. Replaces an empty note/header in place, otherwise inserts below.
export function insertDetails(editor: Editor): boolean {
  if (!editor || editor.isDestroyed) return false
  const schema = editor.state.schema
  if (!schema.nodes.details) return false
  const today = new Date().toISOString().slice(0, 10)
  // Mint an id for the placeholder up front: it is nested inside
  // detailsContent, so the UniqueBlockIds appendTransaction (which walks only
  // top-level blocks) never reaches it. Without a stable id the inner note
  // would bypass the outliner's identity-keyed ops until the next save.
  const placeholder = schema.nodes.noteBlock?.create(
    { id: freshId(), depth: 0, bullet: '', file_date: today },
    []
  )
  const detailsNode = schema.nodes.details.create(
    { id: null, open: true, file_date: today },
    [
      schema.nodes.detailsSummary.create(
        { id: null },
        schema.text('Section title')
      ),
      schema.nodes.detailsContent.create(
        { id: null },
        placeholder ? [placeholder] : []
      )
    ]
  )
  const active = findActiveBlock(editor)
  const isEmptyNote =
    active &&
    (active.node.type.name === 'noteBlock' ||
      active.node.type.name === 'headerBlock') &&
    (active.node.content.size === 0 || active.node.textContent.trim() === '')
  if (active && isEmptyNote) {
    const pos = editor.state.selection.$from.before(active.depth)
    editor.view.dispatch(
      editor.state.tr.replaceWith(pos, pos + active.node.nodeSize, detailsNode)
    )
    editor.commands.focus()
    return true
  }
  editor.commands.insertContent(detailsNode)
  editor.commands.focus()
  return true
}

// Toggle the `open` attr on the `<details>` enclosing the cursor (#183).
// Walks up from the selection to the nearest details node and flips open.
export function toggleDetails(editor: Editor): boolean {
  if (!editor || editor.isDestroyed) return false
  const $pos = editor.state.selection.$from
  for (let d = $pos.depth; d >= 1; d--) {
    const node = $pos.node(d)
    if (node.type.name === 'details') {
      const pos = $pos.before(d)
      editor.view.dispatch(
        editor.state.tr.setNodeAttribute(pos, 'open', !node.attrs.open)
      )
      return true
    }
  }
  return false
}

// Insert a GFM table (#172). rows/cols include the header row. Builds the
// table > tableRow(tableHeader×cols) + (rows-1)×tableRow(tableCell×cols)
// tree the TipTap Table extension expects, each cell seeded with an empty
// paragraph. Replaces an empty note/header in place, else inserts below.
export function insertTable(editor: Editor, rows = 3, cols = 3): boolean {
  if (!editor || editor.isDestroyed) return false
  const schema = editor.state.schema
  if (!schema.nodes.table) return false
  // TipTap's tableCell has content 'block+' and its row/column commands fill
  // new cells with paragraph nodes. Without a paragraph node in the schema
  // the cells would be empty/invalid and the table would silently fail to
  // insert — fail loudly instead of producing a broken table.
  const paragraph = schema.nodes.paragraph
  if (!paragraph) return false
  const today = new Date().toISOString().slice(0, 10)
  const emptyCell = (type: 'tableHeader' | 'tableCell') =>
    schema.nodes[type].create({}, paragraph.create())
  const headerRow = schema.nodes.tableRow.create(
    {},
    Array.from({ length: cols }, () => emptyCell('tableHeader'))
  )
  const dataRows = Array.from({ length: Math.max(rows - 1, 0) }, () =>
    schema.nodes.tableRow.create(
      {},
      Array.from({ length: cols }, () => emptyCell('tableCell'))
    )
  )
  const table = schema.nodes.table.create({ id: null, file_date: today }, [
    headerRow,
    ...dataRows
  ])
  const active = findActiveBlock(editor)
  const isEmptyNote =
    active &&
    (active.node.type.name === 'noteBlock' ||
      active.node.type.name === 'headerBlock') &&
    (active.node.content.size === 0 || active.node.textContent.trim() === '')
  if (active && isEmptyNote) {
    const pos = editor.state.selection.$from.before(active.depth)
    editor.view.dispatch(
      editor.state.tr.replaceWith(pos, pos + active.node.nodeSize, table)
    )
    editor.commands.focus()
    return true
  }
  editor.commands.insertContent(table)
  editor.commands.focus()
  return true
}

export const SiltBlockKeymaps = Extension.create({
  name: 'siltBlockKeymaps',

  addKeyboardShortcuts() {
    return {
      Enter: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false

        // Default to a plain (no-bullet) line. Only noteBlocks inherit /
        // resequence the bullet marker — pressing Enter after a task or
        // header should start a fresh plain line, not a bulleted one (#258).
        let nextBullet = ''
        if (info.node.type.name === 'noteBlock') {
          nextBullet = getNextBullet(info.node.attrs.bullet || '')
        }

        // Create a new NoteBlock at the same depth right after the current block.
        const newBlock = {
          type: 'noteBlock',
          attrs: {
            id: null,
            depth: info.depth,
            bullet: nextBullet,
            file_date: new Date().toISOString().slice(0, 10)
          }
        }
        const insertPos = info.pos + info.node.nodeSize
        this.editor.view.dispatch(
          this.editor.state.tr.insert(insertPos, [
            this.editor.state.schema.nodeFromJSON(newBlock)
          ])
        )
        // Move cursor into the new block.
        const newPos = insertPos + 1
        this.editor.commands.focus()
        const tr = this.editor.state.tr
        const sel = TextSelection.create(this.editor.state.doc, newPos, newPos)
        this.editor.view.dispatch(tr.setSelection(sel))
        return true
      },

      Backspace: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false

        const { selection } = this.editor.state
        const isAtStart =
          selection.from === selection.to && selection.$from.parentOffset === 0
        if (!isAtStart) return false

        // If the block is a note block and has a bullet, clear the bullet first.
        if (
          info.node.type.name === 'noteBlock' &&
          info.node.attrs.bullet &&
          info.node.attrs.bullet !== ''
        ) {
          const tr = this.editor.state.tr.setNodeAttribute(
            info.pos,
            'bullet',
            ''
          )
          this.editor.view.dispatch(tr)
          return true
        }

        // Only act on truly empty blocks (no text content).
        const isEmpty =
          info.node.content.size === 0 || info.node.textContent.trim() === ''
        if (!isEmpty) return false

        if (info.depth > 0) {
          // Unindent first.
          setBlockDepth(this.editor, info.pos, info.depth - 1)
          return true
        }

        // Delete the block and focus the previous one (if any).
        const { doc } = this.editor.state
        if (doc.childCount <= 1) return false

        // Find the current block's top-level index.
        let blockIndex = -1
        let acc = 0
        for (let i = 0; i < doc.childCount; i++) {
          if (acc === info.pos) {
            blockIndex = i
            break
          }
          acc += doc.child(i).nodeSize
        }
        if (blockIndex <= 0) return false

        // Delete and focus previous.
        const from = info.pos
        const to = info.pos + info.node.nodeSize
        this.editor.view.dispatch(this.editor.state.tr.delete(from, to))
        focusBlockAt(this.editor, blockIndex - 1)
        return true
      },

      Tab: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false
        // Only the depth-bearing prose blocks support indent. Letting Tab fall
        // through for callout/code/table/details keeps TipTap's default (table
        // cell nav, etc.) instead of silently no-op'ing.
        if (!DEPTH_BLOCK_TYPES.has(info.node.type.name)) return false

        // Indent — max is previous sibling's depth + 1.
        const { doc } = this.editor.state
        let blockIndex = -1
        let acc = 0
        for (let i = 0; i < doc.childCount; i++) {
          if (acc === info.pos) {
            blockIndex = i
            break
          }
          acc += doc.child(i).nodeSize
        }
        let maxDepth = 0
        if (blockIndex > 0) {
          maxDepth = (doc.child(blockIndex - 1).attrs.depth || 0) + 1
        }
        if (info.depth < maxDepth) {
          setBlockDepth(this.editor, info.pos, info.depth + 1)
        }
        return true
      },

      'Shift-Tab': () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false
        if (!DEPTH_BLOCK_TYPES.has(info.node.type.name)) return false
        if (info.depth > 0) {
          setBlockDepth(this.editor, info.pos, info.depth - 1)
        }
        return true
      },

      ArrowUp: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false
        const { selection } = this.editor.state
        // Only navigate when at the start of a block.
        if (selection.$from.parentOffset > 0) return false

        const { doc } = this.editor.state
        let blockIndex = -1
        let acc = 0
        for (let i = 0; i < doc.childCount; i++) {
          if (acc === info.pos) {
            blockIndex = i
            break
          }
          acc += doc.child(i).nodeSize
        }
        if (blockIndex > 0) {
          focusBlockAt(this.editor, blockIndex - 1)
          return true
        }
        return false
      },

      ArrowDown: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false
        const { selection } = this.editor.state
        // Only navigate when at the end of a block.
        if (selection.$from.parentOffset < info.node.content.size) return false

        const { doc } = this.editor.state
        let blockIndex = -1
        let acc = 0
        for (let i = 0; i < doc.childCount; i++) {
          if (acc === info.pos) {
            blockIndex = i
            break
          }
          acc += doc.child(i).nodeSize
        }
        if (blockIndex >= 0 && blockIndex < doc.childCount - 1) {
          focusBlockAt(this.editor, blockIndex + 1)
          return true
        }
        return false
      },

      // Strikethrough — the Strike extension uses Mod-Shift-s, but the
      // standard binding is Mod-Shift-x. Register both (#168).
      'Mod-Shift-x': () => {
        this.editor.chain().focus().toggleStrike().run()
        return true
      },

      // Link — no built-in shortcut. Dispatches a custom event so TipTapEditor
      // can show its inline URL input (#168). If already linked, removes.
      'Mod-k': () => {
        const { selection } = this.editor.state
        if (selection.empty) return false
        if (this.editor.isActive('link')) {
          this.editor.chain().focus().unsetLink().run()
        } else {
          window.dispatchEvent(new CustomEvent('silt:open-link-input'))
        }
        return true
      },

      // Heading level shortcuts (#169). Mod-Alt-1/2/3 → H1/H2/H3,
      // Mod-Alt-0 → Note (strip heading/task), Mod-Alt-4 → Task.
      'Mod-Alt-1': () => convertToBlock(this.editor, 'headerBlock', 1),
      'Mod-Alt-2': () => convertToBlock(this.editor, 'headerBlock', 2),
      'Mod-Alt-3': () => convertToBlock(this.editor, 'headerBlock', 3),
      'Mod-Alt-0': () => convertToBlock(this.editor, 'noteBlock'),
      'Mod-Alt-4': () => convertToBlock(this.editor, 'taskBlock'),

      // Text alignment shortcuts (#173). Mod-Shift-L/E/R/J for left/center/
      // right/justify. No-op for TASK blocks (alignment not supported on tasks).
      'Mod-Shift-l': () => setBlockAlign(this.editor, 'left'),
      'Mod-Shift-e': () => setBlockAlign(this.editor, 'center'),
      'Mod-Shift-r': () => setBlockAlign(this.editor, 'right'),
      'Mod-Shift-j': () => setBlockAlign(this.editor, 'justify'),

      // Blockquote toggle (#188). Mod-Shift-9 is the standard blockquote
      // binding. No-op on TASK/HEADER blocks (quote is a NOTE marker).
      'Mod-Shift-9': () => toggleBlockQuote(this.editor),

      // Foldable details toggle (#183). Bound to Mod-Shift-. rather than Mod-.,
      // which is claimed by the Superscript mark extension (SiltInlineMarkExtensions
      // is registered before this keymap, so Superscript would shadow a Mod-.
      // binding). Mod-Shift-. flips the `open` attr on the enclosing <details>.
      'Mod-Shift-.': () => toggleDetails(this.editor),

      // Table row/column insert shortcuts (#172). No-op outside a table.
      'Mod-Shift-Up': () =>
        this.editor.can().addRowBefore?.()
          ? (this.editor.chain().focus().addRowBefore().run(), true)
          : false,
      'Mod-Shift-Down': () =>
        this.editor.can().addRowAfter?.()
          ? (this.editor.chain().focus().addRowAfter().run(), true)
          : false,
      'Mod-Shift-Left': () =>
        this.editor.can().addColumnBefore?.()
          ? (this.editor.chain().focus().addColumnBefore().run(), true)
          : false,
      'Mod-Shift-Right': () =>
        this.editor.can().addColumnAfter?.()
          ? (this.editor.chain().focus().addColumnAfter().run(), true)
          : false
    }
  }
})
