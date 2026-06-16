import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { SiltBlockExtensions } from './index'
import {
  TaskMetaSuggest,
  filterMetaKeys,
  getSuggestContext,
  applyMetaSuggestion,
  META_KEYS
} from './taskMetaSuggest'
import type { DocJSON } from './types'

// Editor wired with the Silt schema (no NodeViews — those need a live DOM and
// are not required to exercise the suggest logic). Mirrors the makeEditor()
// pattern in converters.test.ts.
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
      TaskMetaSuggest
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
    content: [
      {
        type,
        attrs,
        content: [{ type: 'text', text }]
      }
    ]
  }
}

describe('TaskMetaSuggest — catalog & filtering', () => {
  it('exposes the six metadata keys', () => {
    expect(META_KEYS.map((m) => m.key)).toEqual([
      'due',
      'start',
      'owner',
      'priority',
      'pin',
      'progress'
    ])
  })

  it('returns the full list for an empty query (typing % alone shows all)', () => {
    expect(filterMetaKeys('').map((m) => m.key)).toEqual([
      'due',
      'start',
      'owner',
      'priority',
      'pin',
      'progress'
    ])
  })

  it('filters by prefix — %d shows only "due"', () => {
    expect(filterMetaKeys('d').map((m) => m.key)).toEqual(['due'])
  })

  it('filters case-insensitively', () => {
    expect(filterMetaKeys('P').map((m) => m.key)).toEqual([
      'priority',
      'pin',
      'progress'
    ])
  })
})

describe('TaskMetaSuggest — context detection', () => {
  it('detects an active context right after % in a task line', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'write %'))
    // "write %" -> block content starts at pos 1, 7 chars, caret at pos 8.
    editor.commands.setTextSelection(8)
    const ctx = getSuggestContext(editor.state)
    expect(ctx).not.toBeNull()
    expect(ctx!.query).toBe('')
    editor.destroy()
  })

  it('reads the query typed after %', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'go %d'))
    // "go %d" -> 5 chars, caret at pos 6.
    editor.commands.setTextSelection(6)
    const ctx = getSuggestContext(editor.state)
    expect(ctx).not.toBeNull()
    expect(ctx!.query).toBe('d')
    editor.destroy()
  })

  it('does NOT trigger on non-task (note) lines', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('noteBlock', 'note %d'))
    editor.commands.setTextSelection(8)
    expect(getSuggestContext(editor.state)).toBeNull()
    editor.destroy()
  })

  it('does NOT trigger when the query contains non-letter characters', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'pct %50'))
    // "pct %50" -> 7 chars, caret at pos 8.
    editor.commands.setTextSelection(8)
    expect(getSuggestContext(editor.state)).toBeNull()
    editor.destroy()
  })

  it('does NOT trigger when there is no % before the caret', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'no trigger here'))
    editor.commands.setTextSelection(5)
    expect(getSuggestContext(editor.state)).toBeNull()
    editor.destroy()
  })
})

describe('TaskMetaSuggest — insertion', () => {
  it('selecting "due" replaces %query with [due:: ] and places the caret before ]', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'ship %d'))
    // "ship %d" -> 7 chars, caret at pos 8.
    editor.commands.setTextSelection(8)

    expect(applyMetaSuggestion(editor, 'due')).toBe(true)

    const text = editor.state.doc.textContent
    expect(text).toBe('ship [due:: ]')

    // Caret should be collapsed and sit just before the closing bracket so
    // typing fills the value slot.
    const sel = editor.state.selection
    expect(sel.from).toBe(sel.to)
    expect(editor.state.doc.textBetween(sel.from, sel.from + 1)).toBe(']')
    editor.destroy()
  })

  it('selecting "pin" auto-fills [pin:: true]', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('taskBlock', 'do %'))
    // "do %" -> 4 chars, caret at pos 5.
    editor.commands.setTextSelection(5)

    expect(applyMetaSuggestion(editor, 'pin')).toBe(true)
    expect(editor.state.doc.textContent).toBe('do [pin:: true]')
    editor.destroy()
  })

  it('returns false and changes nothing when no context is active', () => {
    const editor = makeEditor()
    editor.commands.setContent(blockDoc('noteBlock', 'no suggest %d'))
    editor.commands.setTextSelection(13)
    const before = editor.state.doc.textContent
    expect(applyMetaSuggestion(editor, 'due')).toBe(false)
    expect(editor.state.doc.textContent).toBe(before)
    editor.destroy()
  })
})
