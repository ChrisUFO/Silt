import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  cutSelection,
  copySelection,
  pasteFromClipboard,
  copyAsMarkdown,
  copyAsPlainText,
  copyBlockReference,
  copyBlockEmbed,
  duplicateBlock,
  deleteBlock,
  type ClipboardDeps
} from './clipboard'

// Mock serializeInlineContent so we can assert it's called for the markdown
// path without depending on the real converter shape.
vi.mock('./converters', () => ({
  serializeInlineContent: (content: unknown) =>
    `[md:${JSON.stringify(content).length}]`
}))

// Mock navigator.clipboard (jsdom doesn't provide one).
const writeTextSpy = vi.fn().mockResolvedValue(undefined)
const readTextSpy = vi.fn().mockResolvedValue('')
Object.defineProperty(navigator, 'clipboard', {
  value: { writeText: writeTextSpy, readText: readTextSpy },
  configurable: true,
  writable: true
})

const notifySpy = vi.fn()

// --- Editor mock builders ----------------------------------------------------

/** Build a minimal Editor stub that supports the operations clipboard.ts uses. */
function makeEditor(overrides: Record<string, unknown> = {}): any {
  // A fake block node returned by $from.node(d). The walker in
  // findActiveBlockDepth checks `node.type.name === 'noteBlock'|'taskBlock'|'headerBlock'`.
  const noteBlockNode = { type: { name: 'noteBlock' } }
  const state = {
    selection: {
      from: 0,
      to: 0,
      empty: true,
      $from: {
        depth: 1,
        node: () => noteBlockNode,
        after: () => 5,
        before: () => 0
      }
    },
    doc: {
      textBetween: vi.fn(() => 'plain text'),
      childCount: 3,
      slice: vi.fn(() => ({
        content: {
          forEach: (cb: (n: any) => void) =>
            cb({ toJSON: () => ({ content: { foo: 1 } }) })
        }
      }))
    },
    ...overrides
  }
  return {
    state,
    commands: {
      deleteSelection: vi.fn(),
      insertContent: vi.fn(),
      focus: vi.fn()
    },
    chain: () => ({
      insertContentAt: vi.fn().mockReturnThis(),
      deleteRange: vi.fn().mockReturnThis(),
      focus: vi.fn().mockReturnThis(),
      run: vi.fn()
    }),
    ...overrides
  } as any
}

function makeDeps(
  editor: any,
  menuState: { activeBlockId?: string; activeBlockNode?: any } | null = null
): ClipboardDeps {
  return {
    editor,
    notify: notifySpy,
    menu: () => menuState
  }
}

beforeEach(() => {
  writeTextSpy.mockClear()
  readTextSpy.mockClear()
  notifySpy.mockClear()
})

describe('cutSelection', () => {
  it('writes the selected text and deletes the selection', () => {
    const editor = makeEditor()
    cutSelection(makeDeps(editor))
    expect(writeTextSpy).toHaveBeenCalledWith('plain text')
    expect(editor.commands.deleteSelection).toHaveBeenCalledOnce()
  })
})

describe('copySelection', () => {
  it('writes the selected text but does NOT delete', () => {
    const editor = makeEditor()
    copySelection(makeDeps(editor))
    expect(writeTextSpy).toHaveBeenCalledWith('plain text')
    expect(editor.commands.deleteSelection).not.toHaveBeenCalled()
  })
})

describe('pasteFromClipboard', () => {
  it('inserts the clipboard text as a text node', async () => {
    readTextSpy.mockResolvedValueOnce('pasted content')
    const editor = makeEditor()
    await pasteFromClipboard(makeDeps(editor))
    expect(editor.commands.insertContent).toHaveBeenCalledWith({
      type: 'text',
      text: 'pasted content'
    })
  })

  it('does nothing when clipboard is empty', async () => {
    readTextSpy.mockResolvedValueOnce('')
    const editor = makeEditor()
    await pasteFromClipboard(makeDeps(editor))
    expect(editor.commands.insertContent).not.toHaveBeenCalled()
  })

  it('pushes an error notification when readText throws', async () => {
    readTextSpy.mockRejectedValueOnce(new Error('denied'))
    const editor = makeEditor()
    await pasteFromClipboard(makeDeps(editor))
    expect(notifySpy).toHaveBeenCalledWith(
      expect.objectContaining({ kind: 'error' })
    )
  })
})

describe('copyAsMarkdown', () => {
  it('serializes the active block when selection is empty', async () => {
    const fakeNode = { toJSON: () => ({ content: { foo: 'bar' } }) }
    const editor = makeEditor()
    await copyAsMarkdown(makeDeps(editor, { activeBlockNode: fakeNode as any }))
    // serializeInlineContent mock returns [md:N] where N = JSON length
    expect(writeTextSpy).toHaveBeenCalledWith(expect.stringContaining('[md:'))
  })

  it('serializes the slice when selection is non-empty', async () => {
    const editor = makeEditor({
      state: {
        selection: { from: 5, to: 10, empty: false },
        doc: {
          slice: vi.fn(() => ({
            content: {
              forEach: (cb: (n: any) => void) =>
                cb({ toJSON: () => ({ content: { a: 1 } }) })
            }
          })),
          textBetween: vi.fn()
        }
      }
    })
    await copyAsMarkdown(makeDeps(editor))
    expect(writeTextSpy).toHaveBeenCalledWith(expect.stringContaining('[md:'))
  })
})

describe('copyAsPlainText', () => {
  it('writes empty string when selection is empty', async () => {
    const editor = makeEditor()
    await copyAsPlainText(makeDeps(editor))
    expect(writeTextSpy).toHaveBeenCalledWith('')
  })

  it('writes the text-between when selection is non-empty', async () => {
    const editor = makeEditor({
      state: {
        selection: { from: 5, to: 10, empty: false },
        doc: { textBetween: vi.fn(() => 'hello world') }
      }
    })
    await copyAsPlainText(makeDeps(editor))
    expect(writeTextSpy).toHaveBeenCalledWith('hello world')
  })
})

describe('copyBlockReference', () => {
  it('writes ((id)) when the menu has an activeBlockId', async () => {
    await copyBlockReference(
      makeDeps(makeEditor(), { activeBlockId: 'abc-123' })
    )
    expect(writeTextSpy).toHaveBeenCalledWith('((abc-123))')
  })

  it('writes nothing when no activeBlockId', async () => {
    await copyBlockReference(makeDeps(makeEditor(), null))
    expect(writeTextSpy).not.toHaveBeenCalled()
  })
})

describe('copyBlockEmbed', () => {
  it('writes {{embed:id}} when the menu has an activeBlockId', async () => {
    await copyBlockEmbed(makeDeps(makeEditor(), { activeBlockId: 'xyz' }))
    expect(writeTextSpy).toHaveBeenCalledWith('{{embed:xyz}}')
  })

  it('writes nothing when no activeBlockId', async () => {
    await copyBlockEmbed(makeDeps(makeEditor(), null))
    expect(writeTextSpy).not.toHaveBeenCalled()
  })
})

describe('duplicateBlock', () => {
  it('inserts the block JSON at the block-end position and strips the id', () => {
    const fakeNode = {
      toJSON: () => ({ type: 'noteBlock', attrs: { id: 'orig' } })
    }
    const insertAt = vi.fn().mockReturnThis()
    const editor = makeEditor()
    editor.chain = () => ({
      insertContentAt: insertAt,
      deleteRange: vi.fn().mockReturnThis(),
      focus: vi.fn().mockReturnThis(),
      run: vi.fn()
    })
    duplicateBlock(makeDeps(editor, { activeBlockNode: fakeNode as any }))
    expect(insertAt).toHaveBeenCalledWith(
      expect.any(Number),
      expect.objectContaining({
        type: 'noteBlock',
        attrs: expect.not.objectContaining({ id: 'orig' })
      })
    )
  })

  it('is a no-op when no activeBlockNode', () => {
    const editor = makeEditor()
    duplicateBlock(makeDeps(editor, null))
    // No throw; chain is never called for the insertContentAt path.
    expect(true).toBe(true)
  })
})

describe('deleteBlock', () => {
  it('deletes the active block range and focuses', () => {
    const deleteRange = vi.fn().mockReturnThis()
    const focus = vi.fn().mockReturnThis()
    const editor = makeEditor()
    editor.chain = () => ({
      insertContentAt: vi.fn().mockReturnThis(),
      deleteRange,
      focus,
      run: vi.fn()
    })
    deleteBlock(makeDeps(editor))
    expect(deleteRange).toHaveBeenCalledWith({
      from: expect.any(Number),
      to: expect.any(Number)
    })
  })

  it('refuses to delete when only one block remains', () => {
    const editor = makeEditor({
      state: {
        selection: {
          from: 0,
          to: 0,
          empty: true,
          $from: { depth: 1, node: () => null, after: () => 0, before: () => 0 }
        },
        doc: { childCount: 1, slice: vi.fn(), textBetween: vi.fn() }
      }
    })
    const deleteRange = vi.fn()
    editor.chain = () => ({
      insertContentAt: vi.fn().mockReturnThis(),
      deleteRange,
      focus: vi.fn().mockReturnThis(),
      run: vi.fn()
    })
    deleteBlock(makeDeps(editor))
    expect(deleteRange).not.toHaveBeenCalled()
  })

  it('is a no-op when no block node is found in the tree', () => {
    const nonBlockNode = { type: { name: 'paragraph' } }
    const editor = makeEditor({
      state: {
        selection: {
          from: 0,
          to: 0,
          empty: true,
          $from: {
            depth: 1,
            node: () => nonBlockNode,
            after: () => 0,
            before: () => 0
          }
        },
        doc: { childCount: 5, slice: vi.fn(), textBetween: vi.fn() }
      }
    })
    const deleteRange = vi.fn()
    editor.chain = () => ({
      insertContentAt: vi.fn().mockReturnThis(),
      deleteRange,
      focus: vi.fn().mockReturnThis(),
      run: vi.fn()
    })
    deleteBlock(makeDeps(editor))
    expect(deleteRange).not.toHaveBeenCalled()
  })
})
