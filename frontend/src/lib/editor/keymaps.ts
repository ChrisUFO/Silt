import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { TextSelection } from '@tiptap/pm/state'
import { freshId } from './uniqueIdPlugin'
import { resolveShortcut } from '../../settings/hotkeys'
import { settings } from '../../settings/store.svelte'

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
  // Guard: converting a calloutBlock to a prose type would silently destroy
  // its content (TipTap's setNode falls through to clearNodes when block+
  // children don't fit inline* content). The user must exit the callout
  // first (Down arrow) then convert the sibling noteBlock below.
  if (node.type.name === 'calloutBlock') return false
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

// Move the active top-level block up (-1) or down (+1), swapping it with its
// neighbor (#181 — keyboard complement to the drag handle). No-ops at the
// document edges or when the active block is not top-level (nested blocks are
// not reorderable this way; Tab/Shift-Tab still indent them).
export function moveActiveBlock(editor: Editor, direction: 1 | -1): boolean {
  if (!editor || editor.isDestroyed) return false
  const active = findActiveBlock(editor)
  if (!active) return false
  // Explicit top-level guard: only ProseMirror tree-depth-1 blocks are
  // reorderable (active.depth is the TREE depth from findActiveBlock — NOT
  // node.attrs.depth, which is the indent level, which would wrongly reject
  // legitimately-indented top-level blocks). Reordering a block nested inside a
  // callout/details would corrupt the doc structure.
  if (active.depth !== 1) return false
  const info = currentBlockInfo(editor)
  if (!info) return false
  const { doc, tr } = editor.state
  let idx = -1
  let posIdx = 0
  let acc = 0
  for (let i = 0; i < doc.childCount; i++) {
    if (acc === info.pos) {
      idx = i
      posIdx = acc
      break
    }
    acc += doc.child(i).nodeSize
  }
  if (idx < 0) return false
  const swap = direction === -1 ? idx - 1 : idx + 1
  if (swap < 0 || swap >= doc.childCount) return false
  const node = doc.child(idx)
  const size = node.nodeSize
  let newTr = tr.delete(posIdx, posIdx + size)
  if (direction === -1) {
    // Up: the previous block's start is unaffected by deleting the block after it.
    let posPrev = 0
    let a = 0
    for (let i = 0; i < swap; i++) a += doc.child(i).nodeSize
    posPrev = a
    newTr = newTr.insert(posPrev, node)
  } else {
    // Down: after the deletion the next block sits at posIdx; insert after it.
    const nextSize = doc.child(swap).nodeSize
    newTr = newTr.insert(posIdx + nextSize, node)
  }
  editor.view.dispatch(newTr)
  focusBlockAt(editor, swap)
  return true
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

// Insert a centered block equation ($$...$$) at the current selection (#191).
// Replaces the current block when it is an empty note/header, otherwise inserts
// below. The latex is set on insert; the NodeView offers click-to-edit.
export function insertBlockMath(editor: Editor, latex = ''): boolean {
  if (!editor || editor.isDestroyed) return false
  const mathNode = editor.state.schema.nodes.blockMathNode?.create({
    id: freshId(),
    latex
  })
  if (!mathNode) return false
  const active = findActiveBlock(editor)
  const isEmptyNote =
    active &&
    (active.node.type.name === 'noteBlock' ||
      active.node.type.name === 'headerBlock') &&
    (active.node.content.size === 0 || active.node.textContent.trim() === '')
  if (active && isEmptyNote) {
    const pos = editor.state.selection.$from.before(active.depth)
    editor.view.dispatch(
      editor.state.tr.replaceWith(pos, pos + active.node.nodeSize, mathNode)
    )
    editor.commands.focus()
    return true
  }
  editor.commands.insertContent(mathNode)
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

// Build the config-driven editor shortcut map (#311). Reads config.hotkeys
// at editor-creation time and converts each binding to the ProseMirror keymap
// format via resolveShortcut. Falls back to hardcoded defaults when the config
// entry is absent or empty. Covers all editor-scoped remappable shortcuts:
// heading levels, alignment, quote/details toggles, table row/col inserts,
// and inline format marks.
function buildConfigDrivenShortcuts(
  editor: Editor
): Record<string, () => boolean> {
  const hk = settings.config?.hotkeys ?? {}
  const pm = (configKey: string, def: string) =>
    resolveShortcut(configKey, def, hk)
  const map: Record<string, () => boolean> = {}

  // Strikethrough — config-driven via format_strike (#311). TipTap's Strike
  // extension registers its own Mod-Shift-s default; this binding overrides
  // it with the user's config choice (or the standard Mod-Shift-x fallback).
  map[pm('format_strike', 'Mod-Shift-x')] = () => {
    editor.chain().focus().toggleStrike().run()
    return true
  }

  // Link — dispatches a custom event so TipTapEditor can show its inline
  // URL input (#168). If already linked, removes.
  map[pm('format_link', 'Mod-k')] = () => {
    const { selection } = editor.state
    if (selection.empty) return false
    if (editor.isActive('link')) {
      editor.chain().focus().unsetLink().run()
    } else {
      window.dispatchEvent(new CustomEvent('silt:open-link-input'))
    }
    return true
  }

  // Heading level shortcuts (#169).
  map[pm('set_h1', 'Mod-Alt-1')] = () =>
    convertToBlock(editor, 'headerBlock', 1)
  map[pm('set_h2', 'Mod-Alt-2')] = () =>
    convertToBlock(editor, 'headerBlock', 2)
  map[pm('set_h3', 'Mod-Alt-3')] = () =>
    convertToBlock(editor, 'headerBlock', 3)
  map[pm('set_note', 'Mod-Alt-0')] = () => convertToBlock(editor, 'noteBlock')
  map[pm('set_task', 'Mod-Alt-4')] = () => convertToBlock(editor, 'taskBlock')

  // Text alignment shortcuts (#173).
  map[pm('align_left', 'Mod-Shift-l')] = () => setBlockAlign(editor, 'left')
  map[pm('align_center', 'Mod-Shift-e')] = () => setBlockAlign(editor, 'center')
  map[pm('align_right', 'Mod-Shift-r')] = () => setBlockAlign(editor, 'right')
  map[pm('align_justify', 'Mod-Shift-j')] = () =>
    setBlockAlign(editor, 'justify')

  // Blockquote toggle (#188).
  map[pm('toggle_quote', 'Mod-Shift-9')] = () => toggleBlockQuote(editor)

  // Foldable details toggle (#183).
  map[pm('toggle_details', 'Mod-Shift-.')] = () => toggleDetails(editor)

  // Table row/column insert shortcuts (#172).
  map[pm('table_insert_row_above', 'Mod-Shift-ArrowUp')] = () =>
    editor.can().addRowBefore?.()
      ? (editor.chain().focus().addRowBefore().run(), true)
      : false
  map[pm('table_insert_row_below', 'Mod-Shift-ArrowDown')] = () =>
    editor.can().addRowAfter?.()
      ? (editor.chain().focus().addRowAfter().run(), true)
      : false
  map[pm('table_insert_col_left', 'Mod-Shift-ArrowLeft')] = () =>
    editor.can().addColumnBefore?.()
      ? (editor.chain().focus().addColumnBefore().run(), true)
      : false
  map[pm('table_insert_col_right', 'Mod-Shift-ArrowRight')] = () =>
    editor.can().addColumnAfter?.()
      ? (editor.chain().focus().addColumnAfter().run(), true)
      : false

  return map
}

// Register config-driven bindings for inline format marks (bold, italic, etc.)
// that are also handled by TipTap StarterKit extensions. These read from config
// at editor-creation time and coexist with the StarterKit's hardcoded defaults.
function buildFormatMarkShortcuts(
  editor: Editor
): Record<string, () => boolean> {
  const hk = settings.config?.hotkeys ?? {}
  const pm = (configKey: string, def: string) =>
    resolveShortcut(configKey, def, hk)
  const map: Record<string, () => boolean> = {}

  map[pm('format_bold', 'Mod-b')] = () => {
    editor.chain().focus().toggleBold().run()
    return true
  }
  map[pm('format_italic', 'Mod-i')] = () => {
    editor.chain().focus().toggleItalic().run()
    return true
  }
  map[pm('format_underline', 'Mod-u')] = () => {
    editor.chain().focus().toggleUnderline().run()
    return true
  }
  map[pm('format_code', 'Mod-e')] = () => {
    editor.chain().focus().toggleCode().run()
    return true
  }
  map[pm('format_highlight', 'Mod-Shift-h')] = () => {
    editor.chain().focus().toggleHighlight().run()
    return true
  }
  map[pm('format_subscript', 'Mod-,')] = () => {
    editor.chain().focus().toggleSubscript().run()
    return true
  }
  map[pm('format_superscript', 'Mod-.')] = () => {
    editor.chain().focus().toggleSuperscript().run()
    return true
  }

  return map
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

      // Alt+ArrowUp/Down reorders the active block (#181) — the keyboard
      // complement to the drag handle. No Mod prefix, to avoid colliding with
      // the Mod-Shift-Arrow table row/column bindings.
      'Alt-ArrowUp': () => moveActiveBlock(this.editor, -1),
      'Alt-ArrowDown': () => moveActiveBlock(this.editor, 1),

      // ---- Config-driven shortcuts (#311) --------------------------------
      // Each editor-scoped shortcut reads its binding from config.hotkeys at
      // editor-creation time (when addKeyboardShortcuts is evaluated). The
      // ProseMirror keymap format uses '-' separators and 'Mod' for Cmd/Ctrl
      // (per prosemirror-keymap source). resolveShortcut converts the config
      // notation and falls back to the hardcoded default if the config entry
      // is absent/empty.
      //
      // LIVE remapping (config change without page navigation) requires
      // re-creating the keymap extension — a documented follow-up. The schema
      // and keymap are immutable at editor-creation time by ProseMirror design.
      ...buildConfigDrivenShortcuts(this.editor),
      ...buildFormatMarkShortcuts(this.editor)
    }
  }
})
