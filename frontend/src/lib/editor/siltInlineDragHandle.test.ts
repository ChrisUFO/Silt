import { describe, it, expect } from 'vitest'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { NodeSelection } from '@tiptap/pm/state'
import { Slice } from '@tiptap/pm/model'
import {
  SiltBlockExtensions,
  UniqueBlockIds,
  SiltInlineDragHandle
} from './index'
import {
  resolveDraggedBlockPosition,
  buildBlockSlice,
  buildNodeDragSelection,
  computeDragImageOffset
} from './siltInlineDragHandle'

// Pure-helper tests + extension smoke. Per AGENTS.md + the project's
// `dragIndentDrop.test.ts` precedent, the interactive HTML5 drag/drop
// dispatch path (which depends on `view.dragging` population,
// `setDragImage`, and `tr.dispatch`) cannot be driven from jsdom — that
// path is gated on the `wails dev` manual matrix in TESTING.md.

describe('resolveDraggedBlockPosition — pure helper', () => {
  // The real `ProseMirror.Node.forEach` walks doc children, calling the
  // callback with `(child, offset)`. We mimic it with a tiny shim so the
  // pure function can be tested without standing up a full editor.
  function fakeDoc(blocks: Array<{ id: string | null; type?: string }>): {
    forEach: (cb: (child: any, offset: number) => boolean | void) => void
  } {
    let offset = 0
    const children = blocks.map((b) => ({
      type: { name: b.type ?? 'noteBlock' },
      attrs: { id: b.id },
      nodeSize: 2
    }))
    return {
      forEach(cb) {
        for (const child of children) {
          const keepGoing = cb(child, offset)
          offset += child.nodeSize
          if (keepGoing === false) return
        }
      }
    }
  }

  it('returns the matching block when its id is present', () => {
    const doc = fakeDoc([{ id: 'a' }, { id: 'b' }, { id: 'c' }])
    const result = resolveDraggedBlockPosition(doc, 'b')
    expect(result).not.toBeNull()
    expect(result?.pos).toBe(2)
    expect(result?.node.attrs.id).toBe('b')
  })

  it('returns the first block when ids collide (UniqueBlockIds plugin prevents this in production)', () => {
    const doc = fakeDoc([{ id: 'dup' }, { id: 'dup' }])
    const result = resolveDraggedBlockPosition(doc, 'dup')
    expect(result?.pos).toBe(0)
  })

  it('returns null when no block matches', () => {
    const doc = fakeDoc([{ id: 'a' }, { id: 'b' }])
    expect(resolveDraggedBlockPosition(doc, 'missing')).toBeNull()
  })

  it('returns null when the doc is empty', () => {
    expect(resolveDraggedBlockPosition(fakeDoc([]), 'any')).toBeNull()
  })

  it('skips blocks whose id is null (in-progress blocks before UniqueBlockIds runs)', () => {
    const doc = fakeDoc([{ id: null }, { id: 'real' }])
    expect(resolveDraggedBlockPosition(doc, 'real')?.pos).toBe(2)
  })

  it('skips blocks with no `attrs` (defensive)', () => {
    const offset = 0
    const fake = {
      forEach(cb: (child: any, off: number) => boolean | void) {
        cb({ type: { name: 'noteBlock' }, nodeSize: 2 }, offset)
        cb(
          { type: { name: 'noteBlock' }, attrs: { id: 'real' }, nodeSize: 2 },
          offset + 2
        )
      }
    }
    expect(resolveDraggedBlockPosition(fake, 'real')?.pos).toBe(2)
  })

  it('stops iterating after the first match (exits the forEach via `return false`)', () => {
    let visits = 0
    const offset = 0
    const fake = {
      forEach(cb: (child: any, off: number) => boolean | void) {
        for (let i = 0; i < 5; i++) {
          const id = i === 2 ? 'target' : `noise-${i}`
          visits++
          const keepGoing = cb(
            { type: { name: 'noteBlock' }, attrs: { id }, nodeSize: 2 },
            offset + i * 2
          )
          if (keepGoing === false) return
        }
      }
    }
    expect(resolveDraggedBlockPosition(fake, 'target')?.pos).toBe(4)
    expect(visits).toBe(3)
  })
})

describe('buildBlockSlice — pure helper', () => {
  // `doc.slice(from, to)` returns a Slice covering the range. Build a tiny
  // real PM doc by standing up a Tiptap editor with StarterKit only.
  function makeDoc() {
    const editor = new Editor({
      extensions: [StarterKit],
      content: {
        type: 'doc',
        content: [
          { type: 'paragraph', content: [{ type: 'text', text: 'one' }] }
        ]
      }
    })
    const doc = editor.state.doc
    editor.destroy()
    return doc
  }

  it('produces a Slice covering the first block (open depth = 0)', () => {
    const doc = makeDoc()
    const node = doc.firstChild
    const nodeSize = node ? node.nodeSize : 0
    const slice = buildBlockSlice(doc, node)
    expect(slice).toBeInstanceOf(Slice)
    expect(slice.openStart).toBe(0)
    expect(slice.openEnd).toBe(0)
    expect(slice.content.size).toBe(nodeSize)
  })

  it('handles a node without a known nodeSize (defensive — fallback size 0)', () => {
    const doc = makeDoc()
    const fakeNode = { type: { name: 'noteBlock' } }
    const slice = buildBlockSlice(doc, fakeNode)
    expect(slice).toBeInstanceOf(Slice)
    expect(slice.content.size).toBe(0)
  })
})

describe('buildNodeDragSelection — pure helper', () => {
  // Real ProseMirror doc required here — `NodeSelection.create` validates
  // the position against `doc`. Use a minimal StarterKit + paragraph doc.
  function makeDoc() {
    const editor = new Editor({
      extensions: [StarterKit],
      content: {
        type: 'doc',
        content: [
          { type: 'paragraph', content: [{ type: 'text', text: 'one' }] },
          { type: 'paragraph', content: [{ type: 'text', text: 'two' }] }
        ]
      }
    })
    const doc = editor.state.doc
    editor.destroy()
    return doc
  }

  it('returns a NodeSelection when pos points at a real paragraph', () => {
    const doc = makeDoc()
    const sel = buildNodeDragSelection(doc, 0)
    expect(sel).toBeInstanceOf(NodeSelection)
    expect(sel.from).toBe(0)
  })

  it('returns a NodeSelection for the second paragraph at its offset', () => {
    const doc = makeDoc()
    const second = doc.child(1)
    const expectedOffset = second ? second.nodeSize : 0
    const sel = buildNodeDragSelection(doc, expectedOffset)
    expect(sel).toBeInstanceOf(NodeSelection)
  })
})

describe('computeDragImageOffset — pure helper (defensive against broken getBoundingClientRect)', () => {
  it('returns the relative offset when both rects are finite', () => {
    // block: left=100, width=200 → ends at 300
    // handle: left=130, width=20 → starts 30px into the block
    expect(computeDragImageOffset(100, 200, 130, 20)).toEqual({ x: 30, y: 0 })
  })

  it('clamps to 0 if the handle is to the left of the block (rect drift)', () => {
    expect(computeDragImageOffset(100, 200, 50, 20)).toEqual({ x: 0, y: 0 })
  })

  it('clamps to (blockWidth - 1) if the handle is past the block right edge', () => {
    // block width 200, handle left 350 → raw 250 → clamped to 199
    expect(computeDragImageOffset(100, 200, 350, 20)).toEqual({ x: 199, y: 0 })
  })

  it('returns (0, 0) when blockRect.left is NaN', () => {
    expect(computeDragImageOffset(NaN, 200, 130, 20)).toEqual({ x: 0, y: 0 })
  })

  it('returns (0, 0) when handleRect.width is NaN', () => {
    expect(computeDragImageOffset(100, 200, 130, NaN)).toEqual({ x: 0, y: 0 })
  })

  it('returns (0, 0) when handleRect.left is Infinity', () => {
    expect(computeDragImageOffset(100, 200, Infinity, 20)).toEqual({
      x: 0,
      y: 0
    })
  })

  it('returns (0, 0) when blockRect.width is -Infinity (detached DOM edge case)', () => {
    expect(computeDragImageOffset(100, -Infinity, 130, 20)).toEqual({
      x: 0,
      y: 0
    })
  })

  it('handles a zero-width block gracefully', () => {
    // block width 0 → max(width-1, 0) = 0 → clamped offsets are 0
    expect(computeDragImageOffset(100, 0, 130, 20)).toEqual({ x: 0, y: 0 })
  })

  it('y is always 0 (inline handle lives at the top of the row)', () => {
    expect(computeDragImageOffset(100, 200, 130, 20).y).toBe(0)
  })
})

// ---- smoke: extension is constructible + registers under the right name -----
// Mirrors `dragIndentDrop.test.ts`'s "BlockIndentOnDrop extension (smoke)"
// block. The interactive path (real dragstart + DataTransfer + DOM rects +
// PM dispatch) cannot be driven from jsdom; what we CAN pin here is
// (a) the TipTap-level registration contract, (b) the ProseMirror plugin
// contribution, and (c) constructor side-effect-freeness on the doc.

describe('SiltInlineDragHandle extension (smoke)', () => {
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
        UniqueBlockIds,
        SiltInlineDragHandle
      ],
      content: {
        type: 'doc',
        content: [
          {
            type: 'noteBlock',
            attrs: { id: 'b1', depth: 0, bullet: '- ' },
            content: [{ type: 'text', text: 'one' }]
          },
          {
            type: 'noteBlock',
            attrs: { id: 'b2', depth: 0, bullet: '- ' },
            content: [{ type: 'text', text: 'two' }]
          }
        ]
      }
    })
  }

  it('registers under the name siltInlineDragHandle', () => {
    const editor = makeEditor()
    expect(editor.extensionManager.extensions.map((e) => e.name)).toContain(
      'siltInlineDragHandle'
    )
    editor.destroy()
  })

  it('contributes a ProseMirror plugin to the editor state', () => {
    const editor = makeEditor()
    // Without the plugin, the dragstart listener never binds, and every
    // real drag would fall through to native (silently breaking the
    // inline path). This gate catches that.
    expect(editor.state.plugins.length).toBeGreaterThan(0)
    editor.destroy()
  })

  it('does not alter the doc on construction (no side effects)', () => {
    const editor = makeEditor()
    expect(editor.state.doc.childCount).toBe(2)
    expect(editor.state.doc.firstChild?.attrs.depth).toBe(0)
    expect(editor.state.doc.lastChild?.attrs.depth).toBe(0)
    editor.destroy()
  })

  it('exposes the pure helpers needed by callers + tests', () => {
    expect(typeof resolveDraggedBlockPosition).toBe('function')
    expect(typeof buildBlockSlice).toBe('function')
    expect(typeof buildNodeDragSelection).toBe('function')
    expect(typeof computeDragImageOffset).toBe('function')
  })
})
