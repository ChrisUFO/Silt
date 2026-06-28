// Regression tests for the inline atomic nodes' HTML identity. The converter
// (NodeJSON) path is covered in converters.test.ts, but the HTML clipboard /
// DnD path runs through ProseMirror's DOMParser, which matches each node's
// parseHTML() tag. Two nodes must NOT share a tag — a past review slipped a
// `data-type="mention"` collision onto BlockReferenceNode, which silently
// re-parsed every block-ref chip as a mention on paste. These tests pin the
// distinct tags via the real DOMParser so it can't recur.

import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { DOMParser as PmDOMParser } from '@tiptap/pm/model'
import { SiltBlockExtensions } from './index'
import { BlockReferenceNode, MentionNode } from './schema'

function schemaFor() {
  const ed = new Editor({
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
      BlockReferenceNode,
      MentionNode
    ]
  })
  const schema = ed.schema
  ed.destroy()
  return schema
}

// Parse an inline chip wrapped in a NOTE block (the parse context the editor
// uses) and return the first inline child of that block.
function parseInlineChip(
  schema: ReturnType<typeof schemaFor>,
  chipHtml: string
) {
  const dom = document.createElement('div')
  dom.innerHTML = `<div data-type="note">${chipHtml}</div>`
  const doc = PmDOMParser.fromSchema(schema).parse(dom)
  const note = doc.firstChild
  return note?.firstChild ?? null
}

describe('inline atomic node HTML identity (no data-type collision)', () => {
  it('parses a mention chip as a mentionNode, not a blockReferenceNode (#184)', () => {
    const node = parseInlineChip(
      schemaFor(),
      '<span data-type="mention" data-name="Alice"></span>'
    )
    expect(node?.type.name).toBe('mentionNode')
    expect(node?.attrs.name).toBe('Alice')
  })

  it('parses a block-reference chip as a blockReferenceNode, not a mentionNode (#85)', () => {
    const UUID = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
    const node = parseInlineChip(
      schemaFor(),
      `<span data-type="block-ref" data-uuid="${UUID}"></span>`
    )
    expect(node?.type.name).toBe('blockReferenceNode')
    expect(node?.attrs.uuid).toBe(UUID)
  })
})
