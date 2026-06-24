import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { TextSelection } from '@tiptap/pm/state'

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

/** The three Silt block node types, in canonical order. */
export const BLOCK_TYPES = ['taskBlock', 'noteBlock', 'headerBlock'] as const

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

      // Code block insert (#189). Mod-Alt-C creates a new fenced code block.
      'Mod-Alt-c': () => {
        this.editor.commands.insertContent({
          type: 'codeBlock',
          attrs: { lang: '' }
        })
        return true
      },

      // Blockquote toggle (#188). Mod-Shift-9 mirrors Word/Google Docs binding.
      'Mod-Shift-9': () => {
        const active = findActiveBlock(this.editor)
        if (!active || active.node.type.name !== 'noteBlock') return false
        const nodePos = this.editor.state.selection.$from.before(active.depth)
        const currentQuote = active.node.attrs.quote || false
        const tr = this.editor.state.tr.setNodeAttribute(
          nodePos,
          'quote',
          !currentQuote
        )
        this.editor.view.dispatch(tr)
        return true
      },

      // Quote depth increment (#188). Mod-Shift-8 bumps quoteDepth up.
      'Mod-Shift-8': () => {
        const active = findActiveBlock(this.editor)
        if (
          !active ||
          active.node.type.name !== 'noteBlock' ||
          !active.node.attrs.quote
        )
          return false
        const nodePos = this.editor.state.selection.$from.before(active.depth)
        const depth = (active.node.attrs.quoteDepth || 1) + 1
        const tr = this.editor.state.tr.setNodeAttribute(
          nodePos,
          'quoteDepth',
          depth
        )
        this.editor.view.dispatch(tr)
        return true
      },

      // Table row/column hotkeys (#172). Look up live bindings from config so
      // user remapping in settings.json is reflected at runtime.
      'Mod-Shift-ArrowUp': () => {
        if ((this.editor as any).can().addRowBefore()) {
          ;(this.editor as any).chain().focus().addRowBefore().run()
          return true
        }
        return false
      },
      'Mod-Shift-ArrowDown': () => {
        if ((this.editor as any).can().addRowAfter()) {
          ;(this.editor as any).chain().focus().addRowAfter().run()
          return true
        }
        return false
      },
      'Mod-Shift-ArrowLeft': () => {
        if ((this.editor as any).can().addColBefore()) {
          ;(this.editor as any).chain().focus().addColBefore().run()
          return true
        }
        return false
      },
      'Mod-Shift-ArrowRight': () => {
        if ((this.editor as any).can().addColAfter()) {
          ;(this.editor as any).chain().focus().addColAfter().run()
          return true
        }
        return false
      },

      // Text alignment shortcuts (#173). Mod-Shift-L/E/R/J for left/center/
      // right/justify. No-op for TASK blocks (alignment not supported on tasks).
      'Mod-Shift-l': () => setBlockAlign(this.editor, 'left'),
      'Mod-Shift-e': () => setBlockAlign(this.editor, 'center'),
      'Mod-Shift-r': () => setBlockAlign(this.editor, 'right'),
      'Mod-Shift-j': () => setBlockAlign(this.editor, 'justify')
    }
  }
})
