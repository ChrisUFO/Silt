import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import {
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  MentionNode,
  CodeBlock
} from './index'
import {
  MentionSuggest,
  filterOwners,
  getMentionContext,
  applyMentionSuggestion,
  planOwnerWriteback
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
      ...SiltInlineMarkExtensions,
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

function taskDoc(text: string): DocJSON {
  return {
    type: 'doc',
    content: [
      {
        type: 'taskBlock',
        attrs: { id: 't1', depth: 0, status: 'TODO' },
        content: [{ type: 'text', text }]
      }
    ]
  }
}

// Flatten a block's inline children into a minimal record so tests can locate
// the mention chip and concatenate plain text without coupling to the serializer.
function inlineKids(block: {
  content?: Array<Record<string, unknown>>
}): Array<{
  type: string
  attrs?: { name?: string }
  text?: string
}> {
  return (block.content ?? []) as Array<{
    type: string
    attrs?: { name?: string }
    text?: string
  }>
}

// Reconstruct how the block would serialize inline: text nodes contribute
// their text, mention chips contribute `@[name]`. Mirrors serialize.ts just
// enough to assert the resulting "task line" shape in one string.
function inlineSerial(block: {
  content?: Array<Record<string, unknown>>
}): string {
  return inlineKids(block)
    .map((k) =>
      k.type === 'mentionNode' ? `@[${k.attrs?.name ?? ''}]` : (k.text ?? '')
    )
    .join('')
}

// Count `[owner:: ...]` tokens anywhere in the doc text (case-insensitive on
// the key, matching OWNER_TOKEN_RE).
function ownerTokenCount(text: string): number {
  return (text.match(/\[owner::[^\]]*\]/gi) ?? []).length
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

  it('does NOT trigger inside an inline code mark', () => {
    const editor = makeEditor()
    editor.commands.setContent({
      type: 'doc',
      content: [
        {
          type: 'noteBlock',
          attrs: { id: 'b1', depth: 0, bullet: '- ' },
          content: [
            { type: 'text', text: 'see ' },
            { type: 'text', marks: [{ type: 'code' }], text: '@al' }
          ]
        }
      ]
    })
    // "see @al" with @al inside a code mark — caret after '@al' (pos 8).
    editor.commands.setTextSelection(8)
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

  it('preserves a separating space after the chip when the query ended with one', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('assign @al '))
    // "assign @al " -> 11 chars, caret at pos 12.
    editor.commands.setTextSelection(12)
    expect(applyMentionSuggestion(editor, 'Alice')).toBe(true)
    const kids = (editor.getJSON().content![0].content ?? []) as Array<{
      type: string
      text?: string
    }>
    const mentionIdx = kids.findIndex((n) => n.type === 'mentionNode')
    expect(mentionIdx).toBeGreaterThanOrEqual(0)
    const after = kids[mentionIdx + 1]
    expect(after?.type).toBe('text')
    expect(after?.text).toBe(' ')
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

describe('planOwnerWriteback — pure planner', () => {
  it('replaces an existing owner token (offsets span the whole match)', () => {
    // "ship it " is 8 chars; "[owner:: bob]" is 13 chars.
    expect(planOwnerWriteback('ship it [owner:: bob] now', 'alice')).toEqual({
      kind: 'replace',
      from: 8,
      to: 21
    })
  })

  it('replaces an empty owner token', () => {
    // "ship " is 5 chars; "[owner:: ]" is 10 chars.
    expect(planOwnerWriteback('ship [owner:: ]', 'alice')).toEqual({
      kind: 'replace',
      from: 5,
      to: 15
    })
  })

  it('inserts at the trimmed end when no token exists', () => {
    // "ship it  " trims to "ship it" (7 chars).
    expect(planOwnerWriteback('ship it  ', 'alice')).toEqual({
      kind: 'insert',
      at: 7
    })
  })

  it('inserts at 0 for an empty block', () => {
    expect(planOwnerWriteback('', 'alice')).toEqual({ kind: 'insert', at: 0 })
  })

  it('same-value replace returns a value-preserving range (no-op on apply)', () => {
    expect(planOwnerWriteback('[owner:: alice]', 'alice')).toEqual({
      kind: 'replace',
      from: 0,
      to: 15
    })
  })

  it('matches the key case-insensitively', () => {
    // "[Owner:: Bob]" is 13 chars.
    expect(planOwnerWriteback('[Owner:: Bob]', 'alice')).toEqual({
      kind: 'replace',
      from: 0,
      to: 13
    })
  })
})

describe('applyMentionSuggestion — owner write-back (#329)', () => {
  it('mention in a taskBlock inserts the chip AND stamps [owner:: name]', () => {
    const editor = makeEditor()
    editor.commands.setContent(taskDoc('@alice'))
    // "@alice" -> 6 chars, caret at pos 7.
    editor.commands.setTextSelection(7)

    expect(applyMentionSuggestion(editor, 'alice')).toBe(true)

    const block = editor.getJSON().content![0]
    const mention = inlineKids(block).find((n) => n.type === 'mentionNode')
    expect(mention).toBeTruthy()
    expect(mention!.attrs!.name).toBe('alice')
    expect(editor.state.doc.textContent).toContain('[owner:: alice]')
    // Serialized inline form: chip followed by the owner token.
    expect(inlineSerial(block)).toBe('@[alice] [owner:: alice]')
    editor.destroy()
  })

  it('mention in a regular paragraph inserts the chip with NO owner token', () => {
    const editor = makeEditor()
    editor.commands.setContent(noteDoc('cc @alice'))
    // "cc @alice" -> 9 chars, caret at pos 10.
    editor.commands.setTextSelection(10)

    expect(applyMentionSuggestion(editor, 'alice')).toBe(true)

    expect(ownerTokenCount(editor.state.doc.textContent)).toBe(0)
    const block = editor.getJSON().content![0]
    expect(inlineKids(block).some((n) => n.type === 'mentionNode')).toBe(true)
    editor.destroy()
  })

  it('mention over an existing [owner:: bob] updates it and leaves one token', () => {
    const editor = makeEditor()
    editor.commands.setContent(taskDoc('@al [owner:: bob]'))
    // "@al [owner:: bob]" -> caret right after "al" (pos 4).
    editor.commands.setTextSelection(4)

    expect(applyMentionSuggestion(editor, 'alice')).toBe(true)

    const text = editor.state.doc.textContent
    expect(ownerTokenCount(text)).toBe(1)
    expect(text).toContain('[owner:: alice]')
    expect(text).not.toContain('[owner:: bob]')
    // Chip still present.
    const block = editor.getJSON().content![0]
    expect(inlineKids(block).some((n) => n.type === 'mentionNode')).toBe(true)
    editor.destroy()
  })

  it('mention fills an empty [owner:: ] token to the confirmed owner', () => {
    const editor = makeEditor()
    editor.commands.setContent(taskDoc('@al [owner:: ]'))
    editor.commands.setTextSelection(4)

    expect(applyMentionSuggestion(editor, 'alice')).toBe(true)

    const text = editor.state.doc.textContent
    expect(ownerTokenCount(text)).toBe(1)
    expect(text).toContain('[owner:: alice]')
    editor.destroy()
  })

  it('caret collapses just after the chip after a task write-back', () => {
    const editor = makeEditor()
    editor.commands.setContent(taskDoc('@alice'))
    editor.commands.setTextSelection(7)

    expect(applyMentionSuggestion(editor, 'alice')).toBe(true)

    const sel = editor.state.selection
    expect(sel.from).toBe(sel.to)
    editor.destroy()
  })
})
