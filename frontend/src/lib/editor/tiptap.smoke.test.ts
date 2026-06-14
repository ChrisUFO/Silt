import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'

// Phase 1 smoke test: proves the TipTap v3 + ProseMirror engine boots and
// round-trips content inside the project's Vitest/jsdom environment. This is
// the regression gate for the dependency surface itself — if a future
// @tiptap/* or svelte-tiptap bump breaks basic editing, this fails first.
describe('TipTap engine smoke', () => {
  it('boots an editor with StarterKit and round-trips content', () => {
    const editor = new Editor({
      extensions: [StarterKit, Placeholder.configure({ placeholder: 'empty' })],
      content: '<p>hello</p>'
    })

    expect(editor.isEmpty).toBe(false)
    expect(editor.getText()).toBe('hello')

    // Insert content via the command API (exercises the ProseMirror transaction path).
    editor.commands.setContent('<p>world</p>')
    expect(editor.getText()).toBe('world')

    editor.destroy()
  })

  it('supports native cross-block selection across multiple paragraphs', () => {
    const editor = new Editor({
      extensions: [StarterKit],
      content: '<p>first</p><p>second</p><p>third</p>'
    })

    // The core capability issue #80 demands: selection that spans separate
    // block boundaries. ProseMirror stores the selection as doc-relative
    // positions, so a range from end-of-para-1 to start-of-para-3 selects
    // across nodes — the exact thing a per-block contenteditable cannot do.
    const { doc } = editor.state
    const endOfPara1 = 1 + 'first'.length // doc(0) > para(1) + text(5)
    const startOfPara3 = doc.content.size - ('third'.length + 1)
    editor.commands.setTextSelection({ from: endOfPara1, to: startOfPara3 })

    const { from, to } = editor.state.selection
    expect(to - from).toBeGreaterThan(0)
    // The selection spans more than one block (the selected text includes the
    // boundary between paragraphs).
    const selectedText = editor.state.doc.textBetween(from, to, '\n')
    expect(selectedText).toContain('second')

    editor.destroy()
  })

  it('exposes the ProseMirror schema and document model via @tiptap/pm', async () => {
    // The converter layer (Phase 2) builds ProseMirror nodes directly via
    // @tiptap/pm (the re-exported ProseMirror packages). Confirm the import
    // path resolves and the schema model is reachable.
    const pmSchema = await import('@tiptap/pm/model')
    expect(typeof pmSchema.Schema).toBe('function')
    expect(typeof pmSchema.Node).toBe('function')

    const editor = new Editor({ extensions: [StarterKit], content: '<p>x</p>' })
    const { schema } = editor
    expect(schema.nodes.doc).toBeTruthy()
    expect(schema.nodes.paragraph).toBeTruthy()
    expect(schema.nodes.text).toBeTruthy()
    editor.destroy()
  })
})
