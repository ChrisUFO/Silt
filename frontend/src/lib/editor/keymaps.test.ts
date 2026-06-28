import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import {
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  SiltColorMarkExtensions,
  UniqueBlockIds,
  SiltBlockKeymaps
} from './index'
import { EmbedNode, BlockReferenceNode } from './schema'
import { setBlockAlign, moveActiveBlock } from './keymaps'
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

// Editor variant that also wires the keyboard shortcut extension so Enter /
// Backspace / Tab outliner semantics are exercised. The base makeEditor()
// omits it to keep the converter/align tests focused on pure state.
function makeEditorWithKeymaps(): Editor {
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
      UniqueBlockIds,
      SiltBlockKeymaps
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

describe('Enter handler — new block bullet after non-note blocks (#258)', () => {
  // Dispatch an Enter keydown through ProseMirror's keydown handler so the
  // SiltBlockKeymaps shortcut runs (jsdom KeyboardEvents don't auto-route
  // through prosemirror-keymap's normalizer reliably).
  function pressEnter(editor: Editor): void {
    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })
    editor.view.someProp('handleKeyDown', (handler) => {
      handler(editor.view, event)
    })
  }

  it('creates a plain (no-bullet) noteBlock after Enter on a taskBlock', () => {
    const editor = makeEditorWithKeymaps()
    editor.commands.setContent(blockDoc('taskBlock', 'task text'))
    editor.commands.focus('end')

    pressEnter(editor)

    expect(editor.state.doc.childCount).toBe(2)
    const newBlock = editor.state.doc.child(1)
    expect(newBlock.type.name).toBe('noteBlock')
    expect(newBlock.attrs.bullet).toBe('')
    editor.destroy()
  })

  it('creates a plain (no-bullet) noteBlock after Enter on a headerBlock', () => {
    const editor = makeEditorWithKeymaps()
    const doc: DocJSON = {
      type: 'doc',
      content: [
        {
          type: 'headerBlock',
          attrs: { id: 'h1', depth: 1 },
          content: [{ type: 'text', text: 'Heading' }]
        }
      ]
    }
    editor.commands.setContent(doc)
    editor.commands.focus('end')

    pressEnter(editor)

    expect(editor.state.doc.childCount).toBe(2)
    const newBlock = editor.state.doc.child(1)
    expect(newBlock.type.name).toBe('noteBlock')
    expect(newBlock.attrs.bullet).toBe('')
    editor.destroy()
  })

  it('continues bullet inheritance after Enter on a bulleted noteBlock', () => {
    const editor = makeEditorWithKeymaps()
    editor.commands.setContent(blockDoc('noteBlock', 'bullet item'))
    editor.commands.focus('end')

    pressEnter(editor)

    expect(editor.state.doc.childCount).toBe(2)
    const newBlock = editor.state.doc.child(1)
    expect(newBlock.type.name).toBe('noteBlock')
    expect(newBlock.attrs.bullet).toBe('- ')
    editor.destroy()
  })

  it('continues plain (no-bullet) after Enter on a plain noteBlock', () => {
    const editor = makeEditorWithKeymaps()
    const doc: DocJSON = {
      type: 'doc',
      content: [
        {
          type: 'noteBlock',
          attrs: { id: 'n1', depth: 0, bullet: '' },
          content: [{ type: 'text', text: 'plain prose' }]
        }
      ]
    }
    editor.commands.setContent(doc)
    editor.commands.focus('end')

    pressEnter(editor)

    expect(editor.state.doc.childCount).toBe(2)
    const newBlock = editor.state.doc.child(1)
    expect(newBlock.type.name).toBe('noteBlock')
    expect(newBlock.attrs.bullet).toBe('')
    editor.destroy()
  })
})

describe('moveActiveBlock — drag-handle keyboard complement (#181)', () => {
  function multiBlockDoc(texts: string[]): DocJSON {
    return {
      type: 'doc',
      content: texts.map((t, i) => ({
        type: 'noteBlock',
        attrs: { id: `b${i}`, depth: 0, bullet: '- ' },
        content: [{ type: 'text', text: t }]
      }))
    }
  }
  const textsOf = (e: Editor) => {
    const kids = (e.getJSON().content ?? []) as Array<{
      content?: Array<{ text?: string }>
    }>
    return kids.map((n) => n.content?.[0]?.text ?? '')
  }

  it('moves the active block down (swaps with the next block)', () => {
    const editor = makeEditor()
    editor.commands.setContent(multiBlockDoc(['a', 'b', 'c']))
    // Block 1 ('b') content sits at pos 4 (block 0 nodeSize 3, content offset 1).
    editor.commands.setTextSelection(4)
    expect(moveActiveBlock(editor, 1)).toBe(true)
    expect(textsOf(editor)).toEqual(['a', 'c', 'b'])
    editor.destroy()
  })

  it('moves the active block up (swaps with the previous block)', () => {
    const editor = makeEditor()
    editor.commands.setContent(multiBlockDoc(['a', 'b', 'c']))
    editor.commands.setTextSelection(4) // 'b'
    expect(moveActiveBlock(editor, -1)).toBe(true)
    expect(textsOf(editor)).toEqual(['b', 'a', 'c'])
    editor.destroy()
  })

  it('no-ops at the top (cannot move the first block up)', () => {
    const editor = makeEditor()
    editor.commands.setContent(multiBlockDoc(['a', 'b']))
    editor.commands.setTextSelection(1) // 'a'
    expect(moveActiveBlock(editor, -1)).toBe(false)
    expect(textsOf(editor)).toEqual(['a', 'b'])
    editor.destroy()
  })

  it('no-ops at the bottom (cannot move the last block down)', () => {
    const editor = makeEditor()
    editor.commands.setContent(multiBlockDoc(['a', 'b']))
    // 'b' content at pos 4.
    editor.commands.setTextSelection(4)
    expect(moveActiveBlock(editor, 1)).toBe(false)
    expect(textsOf(editor)).toEqual(['a', 'b'])
    editor.destroy()
  })
})
