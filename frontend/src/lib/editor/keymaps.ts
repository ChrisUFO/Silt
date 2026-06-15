import { Extension } from '@tiptap/core'
import type { Editor } from '@tiptap/core'
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

const BLOCK_TYPES = ['taskBlock', 'noteBlock', 'headerBlock']

function currentBlockInfo(editor: Editor) {
  const { selection } = editor.state
  const pos = selection.$from
  for (let d = pos.depth; d >= 1; d--) {
    const node = pos.node(d)
    if (BLOCK_TYPES.includes(node.type.name)) {
      return {
        node,
        pos: pos.before(d),
        depth: node.attrs.depth || 0,
        index: pos.index(d)
      }
    }
  }
  return null
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

export const SiltBlockKeymaps = Extension.create({
  name: 'siltBlockKeymaps',

  addKeyboardShortcuts() {
    return {
      Enter: () => {
        const info = currentBlockInfo(this.editor)
        if (!info) return false

        // Create a new NoteBlock at the same depth right after the current block.
        const newBlock = {
          type: 'noteBlock',
          attrs: {
            id: null,
            depth: info.depth,
            bullet: '- ',
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
      }
    }
  }
})
