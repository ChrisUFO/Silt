import { describe, it, expect } from 'vitest'
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
  insertTable
} from './index'
import {
  EmbedNode,
  BlockReferenceNode,
  CalloutBlock,
  CodeBlock
} from './schema'
import {
  blocksToDoc,
  docToBlocks,
  embedBlockMarker,
  parseEmbedBlockMarker,
  tokenizeInline
} from './converters'
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
    file_date: '2026-06-14',
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
      ...SiltInlineMarkExtensions,
      ...SiltColorMarkExtensions,
      EmbedNode,
      BlockReferenceNode,
      UniqueBlockIds
    ]
  })
}

// A TipTap editor wired with the FULL Sprint 14 schema (callout, code,
// details, table) — used to prove the new node types survive TipTap's
// setContent → getJSON normalization without dropping semantic data. This is
// the gap the pure-conversion tests leave open: they prove the JSON shape we
// produce, but not that TipTap accepts and re-emits it unchanged.
function makeEditorWithNewBlocks() {
  return new Editor({
    extensions: [
      StarterKit.configure({
        // paragraph enabled: the Table extension fills cells with paragraphs
        // and hard-depends on schema.nodes.paragraph (mirrors the live editor).
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
  'clean_text',
  'file_date'
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

  it('round-trips numbered list prefixes for notes', () => {
    for (const bullet of ['1. ', '1) ', '10. ', '99) ']) {
      const blocks = [
        mkBlock('NOTE', { raw_text: `${bullet}note`, clean_text: 'note' })
      ]
      const doc = blocksToDoc(blocks)
      const noteNode = doc.content[0]
      expect(noteNode?.attrs?.bullet).toBe(bullet)
      const back = docToBlocks(doc)
      expect(back[0].raw_text).toBe(`${bullet}note`)
      expect(back[0].clean_text).toBe('note')
    }
  })

  it('round-trips a note with no bullet (plain prose)', () => {
    const blocks = [
      mkBlock('NOTE', { raw_text: 'just prose', clean_text: 'just prose' })
    ]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.attrs?.bullet).toBe('')
  })

  it('round-trips a blockquote (`> `) note — flat and nested (#188)', () => {
    // Flat quote: the `> ` prefix is detected on load and re-emitted on save.
    const blocks = [
      mkBlock('NOTE', {
        raw_text: '> quoted text',
        clean_text: '> quoted text'
      })
    ]
    const doc = blocksToDoc(blocks)
    const noteNode = doc.content[0]
    expect(noteNode?.attrs?.quote).toBe('> ')
    expect(noteNode?.attrs?.bullet).toBe('')
    // Body is the text without the marker.
    expect(noteNode?.content).toEqual([{ type: 'text', text: 'quoted text' }])
    // Save: the marker is re-prepended so the on-disk line is `> quoted text`.
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe('> quoted text')
    expect(back[0].raw_text).toBe('> quoted text')

    // Nested quote (`>> `): the full `>` run round-trips verbatim.
    const nested = [
      mkBlock('NOTE', {
        raw_text: '>> deep quote',
        clean_text: '>> deep quote'
      })
    ]
    const nestedDoc = blocksToDoc(nested)
    expect(nestedDoc.content[0]?.attrs?.quote).toBe('>> ')
    expect(docToBlocks(nestedDoc)[0].clean_text).toBe('>> deep quote')
  })

  it('a quote note with alignment round-trips both markers (#188 + #173)', () => {
    // `> text <!-- silt-align: center -->` — quote prefix + align marker.
    const blocks = [
      mkBlock('NOTE', {
        raw_text: '> quoted',
        clean_text: '> quoted <!-- silt-align: center -->'
      })
    ]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.attrs?.quote).toBe('> ')
    expect(doc.content[0]?.attrs?.align).toBe('center')
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe('> quoted <!-- silt-align: center -->')
  })

  it('round-trips all 7 callout variants (#180)', () => {
    const variants = [
      'note',
      'info',
      'tip',
      'warning',
      'danger',
      'success',
      'quote'
    ]
    for (const v of variants) {
      const blocks = [
        mkBlock('NOTE', {
          raw_text: `> [!${v}] message`,
          clean_text: `> [!${v}] message`
        })
      ]
      const doc = blocksToDoc(blocks)
      const node = doc.content[0]
      expect(node?.type).toBe('calloutBlock')
      expect(node?.attrs?.variant).toBe(v)
      expect(node?.content).toEqual([{ type: 'text', text: 'message' }])
      // Save: re-emits the Obsidian `> [!variant] message` line.
      const back = docToBlocks(doc)
      expect(back[0].type).toBe('NOTE')
      expect(back[0].clean_text).toBe(`> [!${v}] message`)
      expect(back[0].raw_text).toBe(`> [!${v}] message`)
    }
  })

  it('a callout without a message round-trips the bare marker (#180)', () => {
    const blocks = [
      mkBlock('NOTE', {
        raw_text: '> [!warning]',
        clean_text: '> [!warning]'
      })
    ]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.type).toBe('calloutBlock')
    expect(doc.content[0]?.attrs?.variant).toBe('warning')
    expect(doc.content[0]?.content).toEqual([])
    expect(docToBlocks(doc)[0].clean_text).toBe('> [!warning]')
  })

  it('callout detection takes precedence over plain quote (#180)', () => {
    // `> [!tip] x` is a callout, NOT a quote note.
    const blocks = [
      mkBlock('NOTE', { raw_text: '> [!tip] x', clean_text: '> [!tip] x' })
    ]
    expect(blocksToDoc(blocks).content[0]?.type).toBe('calloutBlock')
  })

  it('callout variants are matched case-insensitively (#180)', () => {
    // Obsidian commonly writes [!NOTE] / [!Tip] (uppercase/mixed). Detection is
    // case-insensitive; the variant is normalized to lowercase for the NodeView
    // lookup and the on-disk emit.
    for (const [input, expected] of [
      ['[!NOTE]', 'note'],
      ['[!Warning]', 'warning'],
      ['[!TIP]', 'tip']
    ] as const) {
      const blocks = [
        mkBlock('NOTE', {
          raw_text: `> ${input} hi`,
          clean_text: `> ${input} hi`
        })
      ]
      const node = blocksToDoc(blocks).content[0]
      expect(node?.type).toBe('calloutBlock')
      expect(node?.attrs?.variant).toBe(expected)
      // Emits the canonical lowercase marker.
      expect(docToBlocks(blocksToDoc(blocks))[0].clean_text).toBe(
        `> [!${expected}] hi`
      )
    }
  })

  it('round-trips a multi-line CODE block with language (#189)', () => {
    const code = 'func main() {\n\tprintln("hi")\n}'
    const blocks = [
      mkBlock('CODE', {
        clean_text: code,
        language: 'go',
        raw_text: ''
      })
    ]
    const doc = blocksToDoc(blocks)
    const node = doc.content[0]
    expect(node?.type).toBe('codeBlock')
    expect(node?.attrs?.language).toBe('go')
    expect(node?.content).toEqual([{ type: 'text', text: code }])
    // Save: a CODE ParsedBlock whose clean_text is the verbatim code.
    const back = docToBlocks(doc)
    expect(back[0].type).toBe('CODE')
    expect(back[0].clean_text).toBe(code)
    expect(back[0].language).toBe('go')
  })

  it('round-trips a CODE block with blank lines + a literal pipe (#189)', () => {
    const code = 'first\n\nthird\n| col'
    const blocks = [mkBlock('CODE', { clean_text: code, language: '' })]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back[0].type).toBe('CODE')
    expect(back[0].clean_text).toBe(code)
  })

  it('round-trips an empty CODE block (#189)', () => {
    const blocks = [mkBlock('CODE', { clean_text: '', language: 'ts' })]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.type).toBe('codeBlock')
    expect(doc.content[0]?.content).toEqual([])
    expect(docToBlocks(doc)[0].clean_text).toBe('')
  })

  it('round-trips a foldable <details> section (#183)', () => {
    // On-disk form: a run of opaque NOTE blocks the converter regroups.
    const id = '11111111-1111-1111-1111-111111111111'
    const blocks = [
      mkBlock('NOTE', { id, clean_text: '<details>' }),
      mkBlock('NOTE', { clean_text: '<summary>Title</summary>' }),
      mkBlock('NOTE', { clean_text: 'inner note' }),
      mkBlock('NOTE', { clean_text: '</details>' })
    ]
    const doc = blocksToDoc(blocks)
    const node = doc.content[0]
    expect(node?.type).toBe('details')
    const summary = (node?.content || []).find(
      (c) => c.type === 'detailsSummary'
    )
    const content = (node?.content || []).find(
      (c) => c.type === 'detailsContent'
    )
    expect(summary?.content).toEqual([{ type: 'text', text: 'Title' }])
    // The inner note became a child noteBlock inside detailsContent.
    const child = content?.content?.[0]
    expect(child?.type).toBe('noteBlock')

    // Save: re-emits the `<details>` run as opaque NOTE blocks.
    const back = docToBlocks(doc)
    expect(back.map((b) => b.clean_text)).toEqual([
      '<details>',
      '<summary>Title</summary>',
      'inner note',
      '</details>'
    ])
  })

  it('handles an unterminated <details> gracefully (#183)', () => {
    // No closing tag → the opener stays a plain NOTE (no crash, no loss).
    const blocks = [mkBlock('NOTE', { clean_text: '<details>' })]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.type).toBe('noteBlock')
  })

  it('empty <details> round-trips without a blank inner line (#183)', () => {
    // An empty details section must not inflate the file with a placeholder
    // NOTE line — <details><summary>…</summary></details> with no body content.
    const blocks = [
      mkBlock('NOTE', { clean_text: '<details>' }),
      mkBlock('NOTE', { clean_text: '<summary>T</summary>' }),
      mkBlock('NOTE', { clean_text: '</details>' })
    ]
    const doc = blocksToDoc(blocks)
    // buildDetailsNode seeds an empty noteBlock placeholder for TipTap; the
    // save path must strip it so the file stays 3 lines, not 4.
    const back = docToBlocks(doc).map((b) => b.clean_text)
    expect(back).toEqual(['<details>', '<summary>T</summary>', '</details>'])
  })

  it('parses a <summary> with HTML attributes (#183)', () => {
    // External HTML often carries attributes on <summary>; parsing should be
    // lenient (the save path emits attribute-free <summary>).
    const blocks = [
      mkBlock('NOTE', { clean_text: '<details>' }),
      mkBlock('NOTE', { clean_text: '<summary class="t">Title</summary>' }),
      mkBlock('NOTE', { clean_text: 'body' }),
      mkBlock('NOTE', { clean_text: '</details>' })
    ]
    const doc = blocksToDoc(blocks)
    const summary = (doc.content[0]?.content || []).find(
      (c) => c.type === 'detailsSummary'
    )
    expect(summary?.content).toEqual([{ type: 'text', text: 'Title' }])
  })

  it('round-trips a GFM table (#172)', () => {
    const id = '22222222-2222-2222-2222-222222222222'
    const blocks = [
      mkBlock('NOTE', { clean_text: '| Name | Status |' }),
      mkBlock('NOTE', { clean_text: '| --- | --- |' }),
      mkBlock('NOTE', { id, clean_text: '| Alice | Active |' })
    ]
    const doc = blocksToDoc(blocks)
    const node = doc.content[0]
    expect(node?.type).toBe('table')
    // 2 rows: header (tableHeader cells) + 1 data row (tableCell cells).
    const rows = (node?.content || []).filter((c) => c.type === 'tableRow')
    expect(rows).toHaveLength(2)
    expect(
      (rows[0]?.content || []).every((c) => c.type === 'tableHeader')
    ).toBe(true)
    expect((rows[1]?.content || []).every((c) => c.type === 'tableCell')).toBe(
      true
    )
    // Save: re-emits the GFM run; the block id lands on the last row. Column
    // widths are auto-padded for readability (a deterministic normalization),
    // so we assert on cell content + emit-stability rather than exact bytes.
    const back = docToBlocks(doc)
    expect(back).toHaveLength(3)
    expect(back[2].id).toBe(id)
    // Re-parsing the emitted run yields the same cells (semantic identity).
    const reparsed = blocksToDoc(back)
    const rrows = (reparsed.content[0]?.content || []).filter(
      (c) => c.type === 'tableRow'
    )
    const headerCells = (rrows[0]?.content || []).map((c) =>
      (c.content || []).map((n) => n.text).join('')
    )
    const dataCells = (rrows[1]?.content || []).map((c) =>
      (c.content || []).map((n) => n.text).join('')
    )
    expect(headerCells).toEqual(['Name', 'Status'])
    expect(dataCells).toEqual(['Alice', 'Active'])
    // Emit is byte-stable across a second pass (canonical form reached).
    const back2 = docToBlocks(blocksToDoc(back))
    expect(back2.map((b) => b.clean_text)).toEqual(
      back.map((b) => b.clean_text)
    )
  })

  it('a run without a separator is NOT a table (#172)', () => {
    // `| a | b |` with no `| --- |` separator → plain NOTEs.
    const blocks = [
      mkBlock('NOTE', { clean_text: '| a | b |' }),
      mkBlock('NOTE', { clean_text: 'plain text' })
    ]
    const doc = blocksToDoc(blocks)
    expect(doc.content[0]?.type).toBe('noteBlock')
    expect(doc.content[1]?.type).toBe('noteBlock')
  })

  it('escapes and round-trips a literal pipe inside a cell (#172)', () => {
    const blocks = [
      mkBlock('NOTE', { clean_text: '| a | b |' }),
      mkBlock('NOTE', { clean_text: '| --- | --- |' }),
      mkBlock('NOTE', { clean_text: '| x \\| y | z |' })
    ]
    const doc = blocksToDoc(blocks)
    const rows = (doc.content[0]?.content || []).filter(
      (c) => c.type === 'tableRow'
    )
    const dataCells = (rows[1]?.content || []).map((c) =>
      (c.content || []).map((n) => n.text).join('')
    )
    expect(dataCells).toEqual(['x | y', 'z'])
    // Save re-escapes the literal pipe; the emitted form is stable.
    const back = docToBlocks(doc)
    const back2 = docToBlocks(blocksToDoc(back))
    expect(back2.map((b) => b.clean_text)).toEqual(
      back.map((b) => b.clean_text)
    )
    // And the literal pipe survives the re-parse (still one cell "x | y").
    const reparsedData = (blocksToDoc(back).content[0]?.content || [])
      .filter((c) => c.type === 'tableRow')[1]
      ?.content?.map((c) => (c.content || []).map((n) => n.text).join(''))
    expect(reparsedData).toEqual(['x | y', 'z'])
  })

  it('round-trips a cell with backslashes (#172)', () => {
    // Backslashes must be escaped (\\) so a path like C:\x next to a pipe
    // delimiter can't be misread as an escaped pipe on re-parse.
    const blocks = [
      mkBlock('NOTE', { clean_text: '| a | b |' }),
      mkBlock('NOTE', { clean_text: '| --- | --- |' }),
      mkBlock('NOTE', { clean_text: '| C:\\path\\x | y\\|z |' })
    ]
    const doc = blocksToDoc(blocks)
    const rows = (doc.content[0]?.content || []).filter(
      (c) => c.type === 'tableRow'
    )
    const dataCells = (rows[1]?.content || []).map((c) =>
      (c.content || []).map((n) => n.text).join('')
    )
    // Cell 1: "C:\path\x"; cell 2: "y|z" (backslash-pipe → pipe).
    expect(dataCells).toEqual(['C:\\path\\x', 'y|z'])
    // Emit is byte-stable, and a second parse recovers the same cells.
    const back = docToBlocks(doc)
    const back2 = docToBlocks(blocksToDoc(back))
    expect(back2.map((b) => b.clean_text)).toEqual(
      back.map((b) => b.clean_text)
    )
    const reparsed = (blocksToDoc(back).content[0]?.content || [])
      .filter((c) => c.type === 'tableRow')[1]
      ?.content?.map((c) => (c.content || []).map((n) => n.text).join(''))
    expect(reparsed).toEqual(['C:\\path\\x', 'y|z'])
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

  it('callout survives TipTap setContent → getJSON (#180)', () => {
    const editor = makeEditorWithNewBlocks()
    const blocks = [mkBlock('NOTE', { clean_text: '> [!warning] Watch out' })]
    editor.commands.setContent(blocksToDoc(blocks))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back).toHaveLength(1)
    expect(back[0].clean_text).toBe('> [!warning] Watch out')
    editor.destroy()
  })

  it('code block survives TipTap setContent → getJSON (#189)', () => {
    const editor = makeEditorWithNewBlocks()
    const blocks = [
      mkBlock('CODE', {
        clean_text: 'func main() {\n\treturn\n}',
        language: 'go'
      })
    ]
    editor.commands.setContent(blocksToDoc(blocks))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back[0].type).toBe('CODE')
    expect(back[0].clean_text).toBe('func main() {\n\treturn\n}')
    expect(back[0].language).toBe('go')
    editor.destroy()
  })

  it('foldable details survives TipTap setContent → getJSON (#183)', () => {
    const editor = makeEditorWithNewBlocks()
    const blocks = [
      mkBlock('NOTE', { clean_text: '<details>' }),
      mkBlock('NOTE', { clean_text: '<summary>Title</summary>' }),
      mkBlock('NOTE', { clean_text: 'body text' }),
      mkBlock('NOTE', { clean_text: '</details>' })
    ]
    editor.commands.setContent(blocksToDoc(blocks))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    // A trailing noteBlock is appended after the (cursor-trapping) details node
    // so the user can click below it; the details run itself is preserved intact.
    expect(back.slice(0, 4).map((b) => b.clean_text)).toEqual([
      '<details>',
      '<summary>Title</summary>',
      'body text',
      '</details>'
    ])
    editor.destroy()
  })

  it('GFM table survives TipTap setContent → getJSON (#172)', () => {
    const editor = makeEditorWithNewBlocks()
    const blocks = [
      mkBlock('NOTE', { clean_text: '| Name | Role |' }),
      mkBlock('NOTE', { clean_text: '| --- | --- |' }),
      mkBlock('NOTE', { clean_text: '| Alice | Engineer |' })
    ]
    editor.commands.setContent(blocksToDoc(blocks))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    // The table re-emits as a GFM run; assert the cell text survives TipTap's
    // cell-content normalization (cells contain paragraphs in the editor) and
    // that the structure is recognised as a table, not fragmented NOTEs.
    const joined = back.map((b) => b.clean_text).join('\n')
    expect(joined).toContain('Name')
    expect(joined).toContain('Role')
    expect(joined).toContain('Alice')
    expect(joined).toContain('Engineer')
    // At least the header + separator + one data row (3 GFM lines minimum).
    expect(back.length).toBeGreaterThanOrEqual(3)
    editor.destroy()
  })

  it('insertTable produces a real editable table node (#172 regression)', () => {
    // Regression guard: insertTable must build valid cells (content 'block+').
    // When paragraph was disabled, cells were created empty/invalid and the
    // table silently failed to insert — both the /table slash command and the
    // toolbar button did nothing.
    const editor = makeEditorWithNewBlocks()
    expect(insertTable(editor, 2, 2)).toBe(true)
    const doc = editor.getJSON() as DocJSON
    const tableNode = doc.content.find((n) => n.type === 'table')
    expect(tableNode, 'expected a table node in the doc').toBeTruthy()
    const rows = (tableNode?.content || []).filter((c) => c.type === 'tableRow')
    expect(rows).toHaveLength(2)
    // Each cell carries a paragraph child (the valid block content).
    const firstCell = rows[0]?.content?.[0]
    expect(
      firstCell?.type === 'tableHeader' || firstCell?.type === 'tableCell'
    ).toBe(true)
    expect(firstCell?.content?.[0]?.type).toBe('paragraph')
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

  // -- Smart Graph (#85) ----------------------------------------------------

  const UUID_A = '11111111-1111-4111-8111-111111111111'
  const UUID_B = '22222222-2222-4222-8222-222222222222'

  it('tokenizes {{embed:uuid}} in clean_text as an embedNode (#85)', () => {
    const block = mkBlock('NOTE', {
      clean_text: `See {{embed:${UUID_A}}} for context.`
    })
    const doc = blocksToDoc([block])
    const noteNode = doc.content[0] as any
    const embeds = noteNode.content.filter((c: any) => c.type === 'embedNode')
    expect(embeds).toHaveLength(1)
    expect(embeds[0].attrs.uuid).toBe(UUID_A)
    // Text before and after the embed.
    const texts = noteNode.content
      .filter((c: any) => c.type === 'text')
      .map((c: any) => c.text)
      .join('')
    expect(texts).toBe('See  for context.')
  })

  it('tokenizes ((uuid)) in clean_text as a blockReferenceNode (#85)', () => {
    const block = mkBlock('NOTE', {
      clean_text: `Linked: ((${UUID_A})) and ((${UUID_B})).`
    })
    const doc = blocksToDoc([block])
    const noteNode = doc.content[0] as any
    const refs = noteNode.content.filter(
      (c: any) => c.type === 'blockReferenceNode'
    )
    expect(refs).toHaveLength(2)
    expect(refs[0].attrs.uuid).toBe(UUID_A)
    expect(refs[1].attrs.uuid).toBe(UUID_B)
  })

  it('round-trips embeds and refs through docToBlocks (#85)', () => {
    const block = mkBlock('NOTE', {
      clean_text: `Pre {{embed:${UUID_A}}} mid ((${UUID_B})) post`
    })
    const doc = blocksToDoc([block])
    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expect(back[0].clean_text).toBe(block.clean_text)
  })

  it('block-level embedNode (top-level) round-trips as a note carrying the token', () => {
    // Direct doc construction: a top-level embedNode is preserved through
    // docToBlocks as a NOTE block whose clean_text is the embed token.
    const doc: DocJSON = {
      type: 'doc',
      content: [
        {
          type: 'embedNode',
          attrs: { id: UUID_A, uuid: UUID_A }
        } as any
      ]
    }
    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expect(back[0].clean_text).toBe(`{{embed:${UUID_A}}}`)
    expect(back[0].raw_text).toBe(`{{embed:${UUID_A}}}`)
    expect(back[0].type).toBe('NOTE')
    expect(back[0].id).toBe(UUID_A)
  })

  it('sole-embed NOTE block becomes a top-level embedNode with id (#85)', () => {
    // A NOTE whose entire clean_text is {{embed:uuid}} must become a
    // top-level embedNode (group: 'block'), not an inline child of a
    // noteBlock (content: 'inline*'). The block's id is preserved as the
    // embedNode's id attr.
    const blockId = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    const embedUuid = '11111111-2222-4333-8444-555555555555'
    const block = mkBlock('NOTE', {
      id: blockId,
      clean_text: `{{embed:${embedUuid}}}`
    })
    const doc = blocksToDoc([block])
    expect(doc.content).toHaveLength(1)
    expect(doc.content[0]?.type).toBe('embedNode')
    expect(doc.content[0]?.attrs?.id).toBe(blockId)
    expect(doc.content[0]?.attrs?.uuid).toBe(embedUuid)

    // Round-trips back to the same NOTE block.
    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expect(back[0].type).toBe('NOTE')
    expect(back[0].id).toBe(blockId)
    expect(back[0].clean_text).toBe(`{{embed:${embedUuid}}}`)
  })

  it('sole-embed note survives a real TipTap editor round-trip', () => {
    const editor = makeEditor()
    const embedUuid = '33333333-4444-4555-8666-777777777777'
    const block = mkBlock('NOTE', {
      clean_text: `{{embed:${embedUuid}}}`
    })
    editor.commands.setContent(blocksToDoc([block]))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back).toHaveLength(1)
    expect(back[0].clean_text).toBe(`{{embed:${embedUuid}}}`)
    expect(back[0].id).toBeTruthy()
    editor.destroy()
  })

  it('round-trips a generic embedBlock node through the silt-embed marker (#110)', () => {
    const editor = new Editor({
      // @ts-ignore — minimal schema for doc round-trip; the converter functions
      // operate on JSON, not the live editor, so a bare doc is sufficient.
      extensions: [StarterKit.configure({ paragraph: true })]
    })
    const marker = embedBlockMarker({
      embedType: 'attachment',
      src: 'attachments/report.pdf',
      caption: 'Q3 Report',
      openable: true,
      pluginID: 'silt-attachments'
    })
    // A NOTE block carrying the marker → blockToNode → embedBlock node.
    const block = mkBlock('NOTE', { clean_text: marker })
    const doc = blocksToDoc([block])
    expect(doc.content![0].type).toBe('embedBlock')
    expect((doc.content![0].attrs as any).embedType).toBe('attachment')
    expect((doc.content![0].attrs as any).src).toBe('attachments/report.pdf')

    // embedBlock node → docToBlocks → NOTE block with the marker preserved.
    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expect(back[0].type).toBe('NOTE')
    expect(back[0].clean_text).toBe(marker)

    // The marker survives a parse → emit → parse cycle byte-for-byte.
    const reparsed = parseEmbedBlockMarker(back[0].clean_text)
    expect(reparsed).not.toBeNull()
    expect(reparsed!.embedType).toBe('attachment')
    expect(reparsed!.openable).toBe(true)
    editor.destroy()
  })

  it('round-trips a generic embedBlock node with notebook attr (#101 review)', () => {
    // The marker must carry the originating notebook so the embedBlock NodeView
    // can resolve the relative src path when the user clicks to open the
    // attachment, even if they have navigated to a different page/notebook.
    const marker = embedBlockMarker({
      embedType: 'attachment',
      src: 'attachments/report.pdf',
      caption: 'Q3 Report',
      openable: true,
      pluginID: 'silt-attachments',
      notebook: 'Work'
    })
    const block = mkBlock('NOTE', { clean_text: marker })
    const doc = blocksToDoc([block])
    expect((doc.content![0].attrs as any).notebook).toBe('Work')

    const back = docToBlocks(doc)
    expect(back).toHaveLength(1)
    expect(back[0].clean_text).toBe(marker)

    const reparsed = parseEmbedBlockMarker(back[0].clean_text)
    expect(reparsed).not.toBeNull()
    expect(reparsed!.notebook).toBe('Work')
  })
})

describe('inline mark round-trips (#168)', () => {
  // Helper: verify a clean_text string round-trips through the converter
  // (blocksToDoc → docToBlocks) byte-for-byte.
  function expectRoundTrip(cleanText: string): void {
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text, `round-trip of "${cleanText}"`).toBe(cleanText)
  }

  describe('individual marks', () => {
    it('round-trips bold (**...**)', () => {
      expectRoundTrip('this is **bold** text')
    })
    it('round-trips bold (__...__ normalizes to **)', () => {
      // The parser accepts __ but the serializer emits ** (canonical form).
      const block = mkBlock('NOTE', { clean_text: 'this is __bold__ text' })
      const back = docToBlocks(blocksToDoc([block]))
      expect(back[0].clean_text).toBe('this is **bold** text')
    })
    it('round-trips italic (*...*)', () => {
      expectRoundTrip('this is *italic* text')
    })
    it('normalizes italic (_..._ → *...*)', () => {
      // The parser accepts _ but the serializer emits * (canonical form).
      const block = mkBlock('NOTE', { clean_text: 'this is _italic_ text' })
      const back = docToBlocks(blocksToDoc([block]))
      expect(back[0].clean_text).toBe('this is *italic* text')
    })
    it('round-trips strikethrough (~~...~~)', () => {
      expectRoundTrip('this is ~~struck~~ text')
    })
    it('round-trips inline code (`...`)', () => {
      expectRoundTrip('this is `code` text')
    })
    it('round-trips highlight (==...==)', () => {
      expectRoundTrip('this is ==highlighted== text')
    })
    it('round-trips underline (<u>...</u>)', () => {
      expectRoundTrip('this is <u>underlined</u> text')
    })
    it('round-trips subscript (<sub>...</sub>)', () => {
      expectRoundTrip('this is H<sub>2</sub>O text')
    })
    it('round-trips superscript (<sup>...</sup>)', () => {
      expectRoundTrip('this is E=mc<sup>2</sup> text')
    })
    it('round-trips a link ([text](url))', () => {
      expectRoundTrip('see [the docs](https://example.com) for more')
    })
  })

  describe('nested marks', () => {
    it('round-trips bold+italic (***...***)', () => {
      expectRoundTrip('this is ***both*** together')
    })
    it('round-trips bold with italic inside', () => {
      expectRoundTrip('**bold and *italic* together**')
    })
    it('round-trips a link with bold inside', () => {
      expectRoundTrip('click [**bold link**](https://x.com) now')
    })
    it('round-trips underline with italic inside', () => {
      expectRoundTrip('<u>under *italic* line</u>')
    })
    it('round-trips highlight with bold inside', () => {
      expectRoundTrip('==**bold hl**==')
    })
  })

  describe('edge cases', () => {
    it('leaves unclosed ** as literal text', () => {
      expectRoundTrip('**not closed')
    })
    it('leaves unclosed * as literal text', () => {
      expectRoundTrip('*also not closed')
    })
    it('leaves unclosed ~~ as literal text', () => {
      expectRoundTrip('~~strike')
    })
    it('code shields content from further parsing', () => {
      // The **stars** inside the code span are literal, not bold.
      expectRoundTrip('run `const x = **stars**` here')
    })
    it('intraword underscores are NOT italic', () => {
      // my_var_name should NOT be parsed as italic "var"
      expectRoundTrip('my_var_name stays plain')
    })
    it('multiple marks on one line', () => {
      expectRoundTrip('**bold** and *italic* and `code` mixed')
    })
    it('adjacent marks of different types', () => {
      expectRoundTrip('**bold***italic*')
    })
    it('plain text with no marks is unchanged', () => {
      expectRoundTrip('just a plain note with nothing special')
    })
    it('literal asterisks in code', () => {
      expectRoundTrip('multiply `a * b` for result')
    })
  })

  describe('Smart Graph + marks interaction', () => {
    const UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    it('marks alongside a block reference', () => {
      expectRoundTrip(`**bold** before ((${UUID})) after`)
    })
    it('marks alongside an embed', () => {
      expectRoundTrip(`before *italic* and {{embed:${UUID}}} end`)
    })
    it('marks around a block reference', () => {
      expectRoundTrip(`**bold ((${UUID})) ref**`)
    })
  })

  describe('editor-backed round-trip (setContent → getJSON)', () => {
    // These verify the TipTap editor accepts and preserves the marks.
    function expectEditorRoundTrip(cleanText: string): void {
      const editor = makeEditor()
      const block = mkBlock('NOTE', { clean_text: cleanText })
      editor.commands.setContent(blocksToDoc([block]))
      const back = docToBlocks(editor.getJSON() as DocJSON)
      expect(back[0].clean_text, `editor round-trip of "${cleanText}"`).toBe(
        cleanText
      )
      editor.destroy()
    }

    it('bold survives the editor', () => {
      expectEditorRoundTrip('**bold** text')
    })
    it('italic survives the editor', () => {
      expectEditorRoundTrip('*italic* text')
    })
    it('code survives the editor', () => {
      expectEditorRoundTrip('`code` text')
    })
    it('highlight survives the editor', () => {
      expectEditorRoundTrip('==highlight== text')
    })
    it('underline survives the editor', () => {
      expectEditorRoundTrip('<u>underline</u> text')
    })
    it('subscript survives the editor', () => {
      expectEditorRoundTrip('H<sub>2</sub>O')
    })
    it('superscript survives the editor', () => {
      expectEditorRoundTrip('E=mc<sup>2</sup>')
    })
    it('link survives the editor', () => {
      expectEditorRoundTrip('[click](https://x.com)')
    })
    it('nested bold+italic survives the editor', () => {
      expectEditorRoundTrip('***both*** here')
    })
  })
})

describe('block alignment round-trips (#173)', () => {
  it('round-trips center alignment on a NOTE', () => {
    const cleanText = 'centered text <!-- silt-align: center -->'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const doc = blocksToDoc([block])
    expect((doc.content![0].attrs as any).align).toBe('center')
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('round-trips right alignment on a HEADER', () => {
    const cleanText = 'Right Title <!-- silt-align: right -->'
    const block = mkBlock('HEADER', { clean_text: cleanText, depth: 1 })
    const doc = blocksToDoc([block])
    expect((doc.content![0].attrs as any).align).toBe('right')
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('left alignment is the default (no marker)', () => {
    const block = mkBlock('NOTE', { clean_text: 'plain text' })
    const doc = blocksToDoc([block])
    expect((doc.content![0].attrs as any).align).toBe('left')
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe('plain text')
  })

  it('TASK blocks never emit an alignment marker', () => {
    // Even if the align attr is somehow set on a taskBlock, docToBlocks
    // must NOT emit the marker.
    const doc: DocJSON = {
      type: 'doc',
      content: [
        {
          type: 'taskBlock',
          attrs: {
            id: 'test-task',
            depth: 0,
            status: 'TODO',
            align: 'center',
            priority: 3
          },
          content: [{ type: 'text', text: 'task text' }]
        }
      ]
    }
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe('task text')
    expect(back[0].clean_text).not.toContain('silt-align')
  })

  it('all four alignment values round-trip', () => {
    for (const align of ['center', 'right', 'justify']) {
      const cleanText = `text <!-- silt-align: ${align} -->`
      const block = mkBlock('NOTE', { clean_text: cleanText })
      const back = docToBlocks(blocksToDoc([block]))
      expect(back[0].clean_text, `align=${align}`).toBe(cleanText)
    }
  })

  it('alignment survives the editor round-trip', () => {
    const editor = makeEditor()
    const cleanText = 'centered <!-- silt-align: center -->'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    editor.commands.setContent(blocksToDoc([block]))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back[0].clean_text).toBe(cleanText)
    editor.destroy()
  })
})

describe('color mark round-trips (#170)', () => {
  it('round-trips text color', () => {
    const cleanText = 'this is <span style="color: #dc2626">red</span> text'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('round-trips background color', () => {
    const cleanText =
      'this is <span style="background-color: #facc15">highlighted</span> text'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('round-trips nested text color inside bold', () => {
    const cleanText = '**bold <span style="color: #dc2626">red</span> bold**'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('spans without color style are ignored', () => {
    const cleanText = '<span>plain</span> text'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    // The span without a color style is treated as literal text
    expect(back[0].clean_text).toBe('<span>plain</span> text')
  })

  it('adversarial color values with quotes degrade gracefully', () => {
    // A color value containing " breaks the converter regex on re-parse.
    // Verify it doesn't corrupt surrounding content — the span is just
    // treated as literal text on the round-trip (not silent data loss).
    const cleanText = 'before <span style="color: ab"cd">bad</span> after'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    // The malformed span doesn't match the parser regex, so it stays as
    // literal text — no content is lost, no crash occurs.
    expect(back[0].clean_text).toContain('before')
    expect(back[0].clean_text).toContain('after')
  })

  it('javascript: scheme links are not parsed as links (inert literal text)', () => {
    const cleanText = 'click [here](javascript:alert(1)) now'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const doc = blocksToDoc([block])
    // Verify the text node has NO link mark (the scheme was rejected).
    const textNode = doc.content![0].content?.[0]
    expect(textNode?.marks?.some((m: any) => m.type === 'link')).toBeFalsy()
    // The text round-trips byte-for-byte as inert literal text.
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('data:text/html scheme links are not parsed as links', () => {
    const cleanText = '[x](data:text/html,<script>alert(1)</script>)'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const doc = blocksToDoc([block])
    const textNode = doc.content![0].content?.[0]
    expect(textNode?.marks?.some((m: any) => m.type === 'link')).toBeFalsy()
    const back = docToBlocks(doc)
    expect(back[0].clean_text).toBe(cleanText)
  })

  it('safe schemes (https, mailto, #anchor) round-trip correctly', () => {
    for (const text of [
      'see [docs](https://example.com) for more',
      'email [me](mailto:a@b.com) please',
      'jump to [section](#section-1)'
    ]) {
      const block = mkBlock('NOTE', { clean_text: text })
      const back = docToBlocks(blocksToDoc([block]))
      expect(back[0].clean_text, `round-trip of "${text}"`).toBe(text)
    }
  })

  it('span with onmouseover attribute is stripped on round-trip', () => {
    const cleanText =
      '<span style="color: #ff0000" onmouseover="alert(1)">red</span>'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text).toContain('color: #ff0000')
    expect(back[0].clean_text).not.toContain('onmouseover')
    expect(back[0].clean_text).not.toContain('alert')
  })

  it('text color survives the editor round-trip', () => {
    const editor = makeEditor()
    const cleanText = '<span style="color: #dc2626">red text</span>'
    const block = mkBlock('NOTE', { clean_text: cleanText })
    editor.commands.setContent(blocksToDoc([block]))
    const back = docToBlocks(editor.getJSON() as DocJSON)
    expect(back[0].clean_text).toBe(cleanText)
    editor.destroy()
  })
})

describe('cross-feature round-trip (#168, #170, #173)', () => {
  // A single test exercising multiple features together to verify they compose.
  it('bold + italic + color + alignment all round-trip together', () => {
    const blocks = [
      mkBlock('HEADER', {
        clean_text: 'Title **bold** <!-- silt-align: center -->',
        depth: 1
      }),
      mkBlock('NOTE', {
        clean_text:
          '***bold italic*** and <span style="color: #dc2626">red</span> text',
        depth: 0
      }),
      mkBlock('TASK', {
        clean_text: 'task with `code` and ==highlight==',
        status: 'TODO'
      })
    ]
    const back = docToBlocks(blocksToDoc(blocks))
    expect(back).toHaveLength(3)
    back.forEach((b, i) => expectSemanticEqual(b, blocks[i]))
  })

  it('Smart Graph tokens coexist with all mark types', () => {
    const UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    const cleanText = `**bold** ((${UUID})) and <span style="background-color: #facc15">hl</span>`
    const block = mkBlock('NOTE', { clean_text: cleanText })
    const back = docToBlocks(blocksToDoc([block]))
    expect(back[0].clean_text).toBe(cleanText)
  })
})

// --- tokenize / validate pipeline (#198) -----------------------------------
// The new typed Token model exposes the tokenize + validate stages
// independently of the legacy NodeJSON serializer. These tests pin the
// shape of the Token[] output and the security contract of validate.
describe('tokenize / validate pipeline (#198)', () => {
  it('returns an empty Token[] for empty input', () => {
    expect(tokenizeInline('')).toEqual([])
  })

  it('emits a single TextToken for plain prose', () => {
    const tokens = tokenizeInline('just plain text')
    expect(tokens).toHaveLength(1)
    expect(tokens[0].kind).toBe('text')
    if (tokens[0].kind === 'text') {
      expect(tokens[0].text).toBe('just plain text')
      expect(tokens[0].marks).toEqual([])
    }
  })

  it('emits a MarkToken wrapping a TextToken for bold', () => {
    const tokens = tokenizeInline('**bold**')
    expect(tokens).toHaveLength(1)
    expect(tokens[0].kind).toBe('mark')
    if (tokens[0].kind === 'mark') {
      expect(tokens[0].markType).toBe('bold')
      expect(tokens[0].children).toHaveLength(1)
      expect(tokens[0].children[0].kind).toBe('text')
    }
  })

  it('emits an EmbedToken for {{embed:uuid}}', () => {
    const UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    const tokens = tokenizeInline(`text {{embed:${UUID}}} more`)
    const embeds = tokens.filter((t) => t.kind === 'embed')
    expect(embeds).toHaveLength(1)
    if (embeds[0].kind === 'embed') {
      expect(embeds[0].uuid).toBe(UUID)
    }
  })

  it('emits a BlockReferenceToken for ((uuid))', () => {
    const UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    const tokens = tokenizeInline(`text ((${UUID})) more`)
    const refs = tokens.filter((t) => t.kind === 'blockReference')
    expect(refs).toHaveLength(1)
    if (refs[0].kind === 'blockReference') {
      expect(refs[0].uuid).toBe(UUID)
    }
  })

  it('validate drops links with disallowed schemes (javascript:)', () => {
    const tokens = tokenizeInline('[click](javascript:alert(1))')
    // After validate, the link mark is flattened — text survives as plain
    // TextToken, no MarkToken with type 'link' is present.
    const linkMarks = tokens.filter(
      (t) => t.kind === 'mark' && t.markType === 'link'
    )
    expect(linkMarks).toHaveLength(0)
    const textTokens = tokens.filter((t) => t.kind === 'text')
    const joined = textTokens.map((t) => (t as any).text).join('')
    expect(joined).toContain('click')
  })

  it('validate drops data:text/html links', () => {
    const tokens = tokenizeInline('[x](data:text/html,<script>1</script>)')
    const linkMarks = tokens.filter(
      (t) => t.kind === 'mark' && t.markType === 'link'
    )
    expect(linkMarks).toHaveLength(0)
  })

  it('validate drops vbscript: links', () => {
    const tokens = tokenizeInline('[x](vbscript:msgbox(1))')
    const linkMarks = tokens.filter(
      (t) => t.kind === 'mark' && t.markType === 'link'
    )
    expect(linkMarks).toHaveLength(0)
  })

  it('validate preserves safe http(s) links', () => {
    const tokens = tokenizeInline('[ok](https://example.com)')
    const linkMarks = tokens.filter(
      (t) => t.kind === 'mark' && t.markType === 'link'
    )
    expect(linkMarks.length).toBeGreaterThan(0)
  })

  it('color span strips extra attributes (onmouseover absorbed by [^>]*)', () => {
    // The tokenize regex [^>]* absorbs onmouseover so the extracted color
    // attr is the only thing the MarkToken carries. The serializer then
    // emits clean <span style="color: X"> on round-trip.
    const dirty = '<span style="color: #ff0000" onmouseover="evil()">red</span>'
    const block = mkBlock('NOTE', { clean_text: dirty })
    const back = docToBlocks(blocksToDoc([block]))
    // Round-trip preserves the color span (with clean attrs only) — the
    // onmouseover is dropped, not the whole span.
    expect(back[0].clean_text).toBe('<span style="color: #ff0000">red</span>')
    expect(back[0].clean_text).not.toContain('onmouseover')
    expect(back[0].clean_text).not.toContain('evil')
  })

  it('tokenize preserves nested mark structure (**bold with *italic* inside**)', () => {
    const tokens = tokenizeInline('**bold with *italic* inside**')
    // Walk the tree counting all MarkTokens — the outer bold + inner italic
    // both count, even though only the bold is at the top level.
    function countMarks(ts: ReturnType<typeof tokenizeInline>): number {
      let n = 0
      for (const t of ts) {
        if (t.kind === 'mark') {
          n += 1 + countMarks(t.children)
        }
      }
      return n
    }
    expect(countMarks(tokens)).toBeGreaterThanOrEqual(2)
  })
})
