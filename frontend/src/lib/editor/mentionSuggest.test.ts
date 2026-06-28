import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { SiltBlockExtensions, MentionNode, CodeBlock } from './index'
import {
  MentionSuggest,
  filterOwners,
  getMentionContext,
  applyMentionSuggestion
} from './mentionSuggest'
import type { DocJSON } from './types'

// Editor wired with the Silt schema (no NodeViews — those need a live DOM and
// are not required to exercise the suggest logic). MentionNode is added so
// applyMentionSuggestion can resolve `editor.schema.nodes.mentionNode`.
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
      CodeBlock,
      MentionNode,
      MentionSuggest
    ]
  })
}

function noteDoc(text: string): DocJSON {
  return {
    type: 'doc',
    content: [
      {
        type: 'noteBlock',
        attrs: { id: 'b1', depth: 0, bullet: '- ' },
        content: [{ type: 'text', text }]
      }
    ]
  }
}

function codeDoc(text: string): DocJSON {
  return {
    type: 'doc',
    content: [
      {
        type: 'codeBlock',
        attrs: { id: 'c1', depth: 0, language: '' },
        content: [{ type: 'text', text }]
      }
    ]
  }
}

describe('MentionSuggest — owner filtering', () => {
  const owners = ['Alice', 'Bob', 'Ada Lovelace']

  it('returns the full list for an empty query', () => {
    expect(filterOwners(owners, '')).toEqual(owners)
  })

  it('filters by case-insensitive substring (partial mid-name)', () => {
    expect(filterOwners(owners, 'lic')).toEqual(['Alice'])
    expect(filterOwners(owners, 'a')).toEqual(['Alice', 'Ada Lovelace'])
  })

  it('returns nothing for a query matching no owner', () => {
    expect(filterOwners(owners, 'zoe')).toEqual([])
  })
})

describe('MentionSuggest — context detection', () => {
  it('detects an active context right after @', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('ping @'))
    // "ping @" -> 6 chars, caret at pos 7.
    editor.commands.setTextSelection(7)
    const ctx = getMentionContext(editor.state)
    expect(ctx).not.toBeNull()
    expect(ctx!.query).toBe('')
    editor.destroy()
  })

  it('reads the query typed after @', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('assign @al'))
    // "assign @al" -> 10 chars, caret at pos 11.
    editor.commands.setTextSelection(11)
    const ctx = getMentionContext(editor.state)
    expect(ctx).not.toBeNull()
    expect(ctx!.query).toBe('al')
    editor.destroy()
  })

  it('allows spaces in the query (multi-word owner names)', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('cc @ada lov'))
    // "cc @ada lov" -> 11 chars, caret at pos 12.
    editor.commands.setTextSelection(12)
    const ctx = getMentionContext(editor.state)
    expect(ctx).not.toBeNull()
    expect(ctx!.query).toBe('ada lov')
    editor.destroy()
  })

  it('does NOT trigger on an email-style foo@bar', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('mail foo@bar'))
    // "mail foo@bar" -> 12 chars, caret at pos 13.
    editor.commands.setTextSelection(13)
    expect(getMentionContext(editor.state)).toBeNull()
    editor.destroy()
  })

  it('does NOT trigger inside a fenced code block', () => {
    const editor = makeEditor()
    editor.commands.setContent(codeDoc('@owner'))
    editor.commands.setTextSelection(1 + '@owner'.length)
    expect(getMentionContext(editor.state)).toBeNull()
    editor.destroy()
  })

  it('does NOT trigger when there is no @ before the caret', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('no trigger here'))
    editor.commands.setTextSelection(5)
    expect(getMentionContext(editor.state)).toBeNull()
    editor.destroy()
  })
})

describe('MentionSuggest — insertion', () => {
  it('selecting an owner replaces @query with an atomic MentionNode and moves the caret after it', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('assign @al'))
    editor.commands.setTextSelection(11)

    expect(applyMentionSuggestion(editor, 'Alice')).toBe(true)

    const block = editor.getJSON().content![0]
    const kids = (block.content ?? []) as Array<{
      type: string
      attrs?: { name?: string }
      text?: string
    }>
    const mention = kids.find((n) => n.type === 'mentionNode')
    expect(mention).toBeTruthy()
    expect(mention!.attrs!.name).toBe('Alice')
    // The '@al' query text is gone; only 'assign ' precedes the chip.
    const text = kids
      .filter((n) => n.type === 'text')
      .map((n) => n.text)
      .join('')
    expect(text).toBe('assign ')
    // Caret collapsed, sitting just after the chip.
    const sel = editor.state.selection
    expect(sel.from).toBe(sel.to)
    editor.destroy()
  })

  it('returns false and changes nothing when no context is active', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('no at sign here'))
    editor.commands.setTextSelection(5)
    const before = editor.state.doc.textContent
    expect(applyMentionSuggestion(editor, 'Alice')).toBe(false)
    expect(editor.state.doc.textContent).toBe(before)
    editor.destroy()
  })
})
