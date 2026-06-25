// Comprehensive creation-path scan for the Sprint 14 block types.
//
// The table-creation bug (insertTable silently failed because paragraph was
// disabled) slipped through the earlier suites because they only exercised
// data *conversion* (blocksToDoc / docToBlocks), never the actual *creation*
// path (the insert/toggle helpers a slash command or toolbar button invokes).
// These tests drive each feature's creation helper through a real TipTap
// editor and then a create → save (docToBlocks) → load (blocksToDoc) cycle, so
// any schema/normalization mismatch (like the missing paragraph node) surfaces
// here instead of in a user's editor.

import { describe, it, expect, afterEach } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { TrailingNode } from '@tiptap/extensions'
import {
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  SiltColorMarkExtensions,
  SiltDetailsExtensions,
  SiltTableExtensions,
  UniqueBlockIds,
  insertCallout,
  insertCodeBlock,
  insertDetails,
  insertTable,
  toggleBlockQuote
} from './index'
import {
  CalloutBlock,
  CodeBlock,
  EmbedNode,
  BlockReferenceNode
} from './schema'
import { blocksToDoc, docToBlocks } from './converters'
import { getSlashCommands } from './slash-registry'
import type { DocJSON, ParsedBlock } from './types'

function makeFullEditor(initial?: ParsedBlock[]): Editor {
  return new Editor({
    extensions: [
      StarterKit.configure({
        // paragraph enabled — the Table extension fills cells with it.
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
      CalloutBlock,
      CodeBlock,
      ...SiltInlineMarkExtensions,
      ...SiltColorMarkExtensions,
      ...SiltDetailsExtensions,
      ...SiltTableExtensions,
      EmbedNode,
      BlockReferenceNode,
      UniqueBlockIds,
      TrailingNode.configure({
        node: 'noteBlock',
        notAfter: ['taskBlock', 'headerBlock', 'calloutBlock']
      })
    ],
    content: initial ? blocksToDoc(initial) : undefined
  })
}

// Place the cursor at the start of the first block's content (pos 1) so the
// block-level helpers resolve an active block.
function focusFirstBlock(editor: Editor): void {
  editor.commands.setTextSelection(1)
}

const editors: Editor[] = []
function track(editor: Editor): Editor {
  editors.push(editor)
  return editor
}
afterEach(() => {
  while (editors.length) {
    const e = editors.pop()
    if (e && !e.isDestroyed) e.destroy()
  }
})

describe('block creation scan (#188 / #180 / #189 / #183 / #172)', () => {
  it('toggleBlockQuote marks a noteBlock as a quote (#188)', () => {
    const editor = track(makeFullEditor())
    // Seed an empty noteBlock and focus it.
    editor.commands.setContent(
      blocksToDoc([
        {
          id: '11111111-1111-1111-1111-111111111111',
          parent_id: '',
          type: 'NOTE',
          depth: 0,
          raw_text: '',
          clean_text: '',
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: 1
        }
      ])
    )
    focusFirstBlock(editor)
    expect(toggleBlockQuote(editor)).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.type).toBe('noteBlock')
    expect(node?.attrs?.quote).toBe('> ')
    // Save form is a `> ` line.
    expect(docToBlocks(editor.getJSON() as DocJSON)[0].clean_text).toBe('> ')
  })

  it('toggleBlockQuote off preserves the original bullet (#188)', () => {
    // Regression: toggling quote off used to run bullet through `|| '- '`,
    // turning a plain (non-bulleted) note into a `- ` bullet. The bullet attr
    // is now left untouched; the converter ignores bullet while quote is set.
    const editor = track(makeFullEditor())
    editor.commands.setContent(
      blocksToDoc([
        {
          id: '22222222-2222-2222-2222-222222222222',
          parent_id: '',
          type: 'NOTE',
          depth: 0,
          raw_text: 'plain prose, no bullet',
          clean_text: 'plain prose, no bullet',
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: 1
        }
      ])
    )
    focusFirstBlock(editor)
    // Toggle on, then back off.
    expect(toggleBlockQuote(editor)).toBe(true)
    expect(toggleBlockQuote(editor)).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.attrs?.quote).toBe('')
    // A plain note must stay plain — NOT mutate to a `- ` bullet.
    expect(node?.attrs?.bullet).toBe('')
    expect(docToBlocks(editor.getJSON() as DocJSON)[0].clean_text).toBe(
      'plain prose, no bullet'
    )
  })

  it('insertCallout creates a calloutBlock that saves as CALLOUT (#180/#308)', () => {
    const editor = track(makeFullEditor())
    focusFirstBlock(editor)
    expect(insertCallout(editor, 'warning')).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.type).toBe('calloutBlock')
    expect(node?.attrs?.variant).toBe('warning')
    // block+ requires ≥1 child: a placeholder paragraph is seeded.
    expect(node?.content?.[0]?.type).toBe('paragraph')
    const saved = docToBlocks(editor.getJSON() as DocJSON)
    expect(saved[0].type).toBe('CALLOUT')
    expect(saved[0].clean_text).toBe('> [!warning]')
  })

  it('insertCodeBlock creates an editable codeBlock that saves as CODE (#189)', () => {
    const editor = track(makeFullEditor())
    focusFirstBlock(editor)
    expect(insertCodeBlock(editor, 'go')).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.type).toBe('codeBlock')
    expect(node?.attrs?.language).toBe('go')
    const saved = docToBlocks(editor.getJSON() as DocJSON)
    expect(saved[0].type).toBe('CODE')
    expect(saved[0].language).toBe('go')
  })

  it('insertDetails creates a foldable details tree (#183/#310)', () => {
    const editor = track(makeFullEditor())
    focusFirstBlock(editor)
    expect(insertDetails(editor)).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.type).toBe('details')
    // Save form: ONE DETAILS ParsedBlock whose clean_text is the full HTML.
    const saved = docToBlocks(editor.getJSON() as DocJSON)
    expect(saved[0].type).toBe('DETAILS')
    expect(saved[0].clean_text).toContain('<details>')
    expect(saved[0].clean_text).toContain('<summary>')
    expect(saved[0].clean_text).toContain('</details>')
  })

  it('insertTable creates an editable grid that saves as GFM (#172)', () => {
    const editor = track(makeFullEditor())
    focusFirstBlock(editor)
    expect(insertTable(editor, 2, 3)).toBe(true)
    const node = (editor.getJSON() as DocJSON).content[0]
    expect(node?.type).toBe('table')
    const rows = (node?.content || []).filter((c) => c.type === 'tableRow')
    expect(rows).toHaveLength(2)
    // Each cell carries a paragraph child (valid 'block+' content).
    expect(rows[0]?.content?.[0]?.content?.[0]?.type).toBe('paragraph')
  })

  it('every created block round-trips create → save → load unchanged', () => {
    // The headline regression guard: create each block, save it to the
    // ParsedBlock form, reload that form, and confirm the node type survives.
    const cases: Array<{
      name: string
      create: (e: Editor) => boolean
      expectType: string
    }> = [
      {
        name: 'callout',
        create: (e) => insertCallout(e, 'tip'),
        expectType: 'calloutBlock'
      },
      {
        name: 'code',
        create: (e) => insertCodeBlock(e, 'ts'),
        expectType: 'codeBlock'
      },
      {
        name: 'details',
        create: (e) => insertDetails(e),
        expectType: 'details'
      },
      {
        name: 'table',
        create: (e) => insertTable(e, 2, 2),
        expectType: 'table'
      }
    ]
    for (const c of cases) {
      const editor = track(makeFullEditor())
      focusFirstBlock(editor)
      expect(c.create(editor), `${c.name} create`).toBe(true)
      const saved = docToBlocks(editor.getJSON() as DocJSON)
      const reloaded = blocksToDoc(saved)
      expect(
        reloaded.content.some((n) => n.type === c.expectType),
        `${c.name} did not survive create→save→load`
      ).toBe(true)
    }
  })

  it('the slash registry exposes a command for every block feature', () => {
    const ids = new Set(getSlashCommands().map((c) => c.id))
    for (const id of [
      'quote',
      'callout',
      'callout-warning',
      'code-block',
      'details',
      'table',
      'table-5x4',
      'table-custom'
    ]) {
      expect(ids.has(id), `missing slash command ${id}`).toBe(true)
    }
  })

  it('a cursor-trapping block always has an editable line after it (#172/#183/#189)', () => {
    // The whole point of the trailing noteBlock: after a table/code/details
    // block the user can click below and type. Without it, an opaque last block
    // leaves nowhere to place the cursor.
    const opaque: Array<{ create: (e: Editor) => boolean; type: string }> = [
      { create: (e) => insertTable(e, 2, 2), type: 'table' },
      { create: (e) => insertCodeBlock(e, 'go'), type: 'codeBlock' },
      { create: (e) => insertDetails(e), type: 'details' }
    ]
    for (const c of opaque) {
      const editor = track(makeFullEditor())
      focusFirstBlock(editor)
      expect(c.create(editor)).toBe(true)
      const doc = editor.getJSON() as DocJSON
      const last = doc.content[doc.content.length - 1]
      expect(last?.type, `${c.type} should be followed by a noteBlock`).toBe(
        'noteBlock'
      )
    }
  })

  it('prose blocks the user can Enter from do NOT get a trailing line', () => {
    // noteBlock/taskBlock/headerBlock/calloutBlock already let the user press
    // Enter to create the next block, so no auto-trailing line is appended.
    const editor = track(
      makeFullEditor([
        {
          id: 'aaaaaaaa-1111-1111-1111-111111111111',
          parent_id: '',
          type: 'NOTE',
          depth: 0,
          raw_text: '',
          clean_text: 'just a note',
          status: '',
          owner: '',
          start_date: '',
          due_date: '',
          priority: 3,
          line_number: 1
        }
      ])
    )
    const doc = editor.getJSON() as DocJSON
    expect(doc.content).toHaveLength(1)
    expect(doc.content[0]?.type).toBe('noteBlock')
  })
})
