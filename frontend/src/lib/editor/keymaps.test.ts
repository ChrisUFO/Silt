import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import './test-setup'
import {
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  SiltColorMarkExtensions,
  UniqueBlockIds
} from './index'
import { EmbedNode, BlockReferenceNode } from './schema'
import { setBlockAlign } from './keymaps'
import type { DocJSON } from './types'

// Mirror the makeEditor() pattern from converters.test.ts — a real TipTap
// editor wired with the Silt schema. No Placeholder (avoids the jsdom
// elementFromPoint gap that other tests sidestep).
function makeEditor(): Editor {
  return new Editor({
    extensions: [
      StarterKit.configure({
        paragraph: false,
        heading: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        trailingNode: false
      }),
      ...SiltBlockExtensions,
      ...SiltInlineMarkExtensions,
      ...SiltColorMarkExtensions,
      EmbedNode,
      BlockReferenceNode,
      UniqueBlockIds
    ]
  })
}

function blockDoc(type: 'taskBlock' | 'noteBlock', text: string): DocJSON {
  const attrs =
    type === 'taskBlock'
      ? { id: 'b1', depth: 0, status: 'TODO' }
      : { id: 'b1', depth: 0, bullet: '- ' }
  return {
    type: 'doc',
    content: [{ type, attrs, content: [{ type: 'text', text }] }]
  }
}

function currentBlockAlign(editor: Editor): string | undefined {
  const { selection } = editor.state
  const pos = selection.$from
  for (let d = pos.depth; d >= 1; d--) {
    const node = pos.node(d)
    if (['noteBlock', 'headerBlock', 'taskBlock'].includes(node.type.name)) {
      return node.attrs.align
    }
  }
  return undefined
}

describe('setBlockAlign (#200 — shared helper)', () => {
  it('sets align on a noteBlock and returns true', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('noteBlock', 'hello'))
    const result = setBlockAlign(editor, 'center')
    expect(result).toBe(true)
    expect(currentBlockAlign(editor)).toBe('center')
    editor.destroy()
  })

  it('is a no-op (returns true) on a taskBlock', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'task me'))
    const result = setBlockAlign(editor, 'right')
    expect(result).toBe(true)
    expect(currentBlockAlign(editor)).toBeUndefined()
    editor.destroy()
  })

  it('overwrites a prior align value', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('noteBlock', 'first'))
    setBlockAlign(editor, 'left')
    expect(currentBlockAlign(editor)).toBe('left')
    setBlockAlign(editor, 'justify')
    expect(currentBlockAlign(editor)).toBe('justify')
    editor.destroy()
  })

  it('returns false on a destroyed editor', () => {
    const editor = makeEditor()
    editor.destroy()
    expect(setBlockAlign(editor, 'left')).toBe(false)
  })
})
