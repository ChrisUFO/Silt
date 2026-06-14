import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { SiltBlockExtensions, UniqueBlockIds } from './index'
import { blocksToDoc, docToBlocks } from './converters'
import type { ParsedBlock, DocJSON } from './types'

// Helper: build a ParsedBlock with sensible defaults for a given type.
function mkBlock(
  type: ParsedBlock['type'],
  overrides: Partial<ParsedBlock> = {}
): ParsedBlock {
  // A TASK always has a status (TODO/DOING/DONE) — the Go parser assigns it
  // from the checkbox marker on every parse. Default to TODO here so the
  // round-trip data is semantically valid.
  const defaultStatus = type === 'TASK' ? 'TODO' : ''
  return {
    id: crypto.randomUUID(),
    parent_id: '',
    type,
    depth: 0,
    raw_text: '',
    clean_text: 'sample text',
    status: defaultStatus,
    owner: '',
    start_date: '',
    due_date: '',
    priority: 3,
    line_number: 1,
    ...overrides
  }
}

// A real TipTap editor wired with the Silt schema + uniqueIdPlugin. Used to
// prove the doc JSON we produce is accepted by setContent and round-trips
// through getJSON. The Placeholder extension is omitted to avoid the jsdom
// elementFromPoint gap in the core converter tests (covered separately).
function makeEditor() {
  return new Editor({
    extensions: [
      StarterKit.configure({
        // Disable StarterKit's paragraph/heading/list/blockquote so they do
        // not compete with the Silt block nodes as top-level block groups.
        paragraph: false,
        heading: false,
        bulletList: false,
        orderedList: false,
        listItem: false,
        blockquote: false,
        codeBlock: false,
        horizontalRule: false,
        // TrailingNode auto-appends an empty paragraph after setContent; our
        // block model manages its own trailing blocks.
        trailingNode: false
      }),
      ...SiltBlockExtensions,
      UniqueBlockIds
    ]
  })
}

// Semantic fields that MUST survive the blocksToDoc -> docToBlocks round-trip.
// raw_text is intentionally excluded: it is a serialization artifact produced
// by Go's RenderFileContent, not editor-authorable content.
const SEMANTIC_FIELDS = [
  'id',
  'type',
  'depth',
  'status',
  'owner',
  'start_date',
  'due_date',
  'priority',
  'clean_text'
] as const

function expectSemanticEqual(a: ParsedBlock, b: ParsedBlock): void {
  for (const field of SEMANTIC_FIELDS) {
    expect(a[field], `block.${field} mismatch`).toBe(b[field])
  }
}

describe('blocksToDoc / docToBlocks pure conversion', () => {
  it('round-trips a single NOTE block', () => {
    const blocks = [mkBlock('NOTE', { clean_text: 'hello world', depth: 0 })]
    const doc = blocksToDoc(blocks)
    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expectSemanticEqual(back[0], blocks[0])
  })

  it('round-trips a single TASK block with full metadata', () => {
    const blocks = [
      mkBlock('TASK', {
        clean_text: 'ship the editor',
        status: 'DOING',
        owner: 'chris',
        start_date: '2026-06-14',
        due_date: '2026-06-20',
        priority: 1
      })
    ]
    const doc = blocksToDoc(blocks)
    const back = docToBlocks(doc)
    expectSemanticEqual(back[0], blocks[0])
  })

  it('round-trips a HEADER block', () => {
    const blocks = [mkBlock('HEADER', { clean_text: 'My Section', depth: 2 })]
    const doc = blocksToDoc(blocks)
    const back = docToBlocks(doc)
    expectSemanticEqual(back[0], blocks[0])
  })

  it('round-trips a nested structure (depth-based parenting)', () => {
    const blocks = [
      mkBlock('TASK', { clean_text: 'parent', depth: 0 }),
      mkBlock('NOTE', { clean_text: 'child a', depth: 1 }),
      mkBlock('NOTE', { clean_text: 'child b', depth: 1 }),
      mkBlock('NOTE', { clean_text: 'grandchild', depth: 2 })
    ]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back).toHaveLength(4)
    back.forEach((b, i) => expectSemanticEqual(b, blocks[i]))
  })

  it('round-trips all three task statuses', () => {
    for (const status of ['TODO', 'DOING', 'DONE'] as const) {
      const blocks = [mkBlock('TASK', { status, clean_text: `task ${status}` })]
      const back = docToBlocks(blocksToDoc(blocks))
      expect(back[0].status).toBe(status)
    }
  })

  it('round-trips all three bullet styles for notes', () => {
    for (const bullet of ['- ', '* ', '+ ']) {
      const blocks = [
        mkBlock('NOTE', { raw_text: `${bullet}note`, clean_text: 'note' })
      ]
      const doc = blocksToDoc(blocks)
      const noteNode = doc.content[0]
      expect(noteNode?.attrs?.bullet).toBe(bullet)
    }
  })

  it('round-trips a note with no bullet (plain prose)', () => {
    const blocks = [
      mkBlock('NOTE', { raw_text: 'just prose', clean_text: 'just prose' })
    ]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.attrs?.bullet).toBe('')
  })

  it('handles empty clean_text (placeholder block)', () => {
    const blocks = [mkBlock('NOTE', { clean_text: '' })]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back[0].clean_text).toBe('')
  })

  it('handles an empty block list by producing a single empty note node', () => {
    const doc = blocksToDoc([])
    expect(doc.content).toHaveLength(1)
    expect(doc.content[0].type).toBe('noteBlock')
    expect(doc.content[0].content).toEqual([])
  })

  it('defensively maps an unknown node type to NOTE', () => {
    const bogusDoc: DocJSON = {
      type: 'doc',
      content: [
        {
          type: 'somethingWeird',
          attrs: { id: 'abc', depth: 0 },
          content: [{ type: 'text', text: 'salvaged' }]
        }
      ]
    }
    const back = docToBlocks(bogusDoc)
    expect(back).toHaveLength(1)
    expect(back[0].type).toBe('NOTE')
    expect(back[0].clean_text).toBe('salvaged')
  })

  it('derives parent_id from depth using the stack-walk algorithm', () => {
    const blocks = [
      mkBlock('NOTE', { clean_text: 'root', depth: 0 }),
      mkBlock('NOTE', { clean_text: 'child', depth: 1 }),
      mkBlock('NOTE', { clean_text: 'sibling', depth: 1 }),
      mkBlock('NOTE', { clean_text: 'grand', depth: 2 })
    ]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back[0].parent_id).toBe('') // depth 0
    expect(back[1].parent_id).toBe(back[0].id) // depth 1 → root
    expect(back[2].parent_id).toBe(back[0].id) // depth 1 → root
    expect(back[3].parent_id).toBe(back[2].id) // depth 2 → sibling
  })

  it('assigns line numbers by doc order', () => {
    const blocks = [
      mkBlock('NOTE', { clean_text: 'a' }),
      mkBlock('NOTE', { clean_text: 'b' }),
      mkBlock('NOTE', { clean_text: 'c' })
    ]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back.map((b) => b.line_number)).toEqual([1, 2, 3])
  })
})

describe('doc JSON accepted by a real TipTap editor', () => {
  it('setContent(blocksToDoc(...)) and getJSON round-trips through the editor', () => {
    const editor = makeEditor()
    const blocks = [
      mkBlock('HEADER', { clean_text: 'Day Plan', depth: 1 }),
      mkBlock('TASK', {
        clean_text: 'review PRs',
        status: 'TODO',
        owner: 'chris',
        priority: 2
      }),
      mkBlock('NOTE', { clean_text: 'a quick thought', depth: 0 })
    ]
    const doc = blocksToDoc(blocks)
    editor.commands.setContent(doc)

    const fromEditor = editor.getJSON() as DocJSON
    const back = docToBlocks(fromEditor)
    expect(back).toHaveLength(3)
    back.forEach((b, i) => expectSemanticEqual(b, blocks[i]))
    editor.destroy()
  })

  it('the editor preserves node attrs through a no-op edit cycle', () => {
    const editor = makeEditor()
    const block = mkBlock('TASK', {
      clean_text: 'do thing',
      status: 'DOING',
      owner: 'sam',
      due_date: '2026-07-01',
      priority: 1
    })
    editor.commands.setContent(blocksToDoc([block]))
    // No edits — just read back.
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expectSemanticEqual(back[0], block)
    editor.destroy()
  })
})

describe('uniqueIdPlugin', () => {
  it('assigns a UUID to a block that arrives without one', () => {
    const editor = makeEditor()
    // Insert a note node with no id attr. The plugin should mint one.
    editor.commands.setContent({
      type: 'doc',
      content: [
        {
          type: 'noteBlock',
          attrs: { id: null, depth: 0, bullet: '- ' },
          content: [{ type: 'text', text: 'untitled' }]
        }
      ]
    })
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back[0].id).toBeTruthy()
    expect(back[0].id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
    )
    editor.destroy()
  })

  it('does NOT change an existing unique UUID', () => {
    const editor = makeEditor()
    const stableId = '11111111-1111-4111-8111-111111111111'
    editor.commands.setContent({
      type: 'doc',
      content: [
        {
          type: 'noteBlock',
          attrs: { id: stableId, depth: 0, bullet: '- ' },
          content: [{ type: 'text', text: 'stable' }]
        }
      ]
    })
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back[0].id).toBe(stableId)
    editor.destroy()
  })

  it('mints a fresh UUID for a pasted/duplicated block id', () => {
    const editor = makeEditor()
    const dupId = '22222222-2222-4222-8222-222222222222'
    // Two nodes with the SAME id → the second must be rewritten.
    editor.commands.setContent({
      type: 'doc',
      content: [
        {
          type: 'noteBlock',
          attrs: { id: dupId, depth: 0, bullet: '- ' },
          content: [{ type: 'text', text: 'first' }]
        },
        {
          type: 'noteBlock',
          attrs: { id: dupId, depth: 0, bullet: '- ' },
          content: [{ type: 'text', text: 'second (duplicate)' }]
        }
      ]
    })
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back).toHaveLength(2)
    expect(back[0].id).toBe(dupId)
    expect(back[1].id).not.toBe(dupId)
    expect(back[1].id).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
    )
    editor.destroy()
  })
})
