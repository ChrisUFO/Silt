import { describe, it, expect } from 'vitest'
import { findActiveBlock, BLOCK_TYPES } from './keymaps'

/**
 * Minimal Editor stub that exposes only what findActiveBlock reads
 * (`state.selection.$from.depth` + `$from.node(d)`). Each test builds the
 * stub to mirror a ProseMirror node tree at a specific depth.
 */
function makeEditor(nodes: { typeName: string }[]): any {
  // nodes[0] = depth 1, nodes[1] = depth 2, ...
  return {
    state: {
      selection: {
        $from: {
          depth: nodes.length,
          node: (d: number) => ({ type: { name: nodes[d - 1].typeName } })
        }
      }
    }
  } as any
}

describe('BLOCK_TYPES', () => {
  it('contains the three Silt block types in canonical order', () => {
    expect([...BLOCK_TYPES]).toEqual(['taskBlock', 'noteBlock', 'headerBlock'])
  })
})

describe('findActiveBlock', () => {
  it('returns null when the walk hits no block type', () => {
    const editor = makeEditor([{ typeName: 'doc' }, { typeName: 'paragraph' }])
    expect(findActiveBlock(editor)).toBeNull()
  })

  it('returns the innermost block when the selection is directly inside one', () => {
    const editor = makeEditor([{ typeName: 'doc' }, { typeName: 'noteBlock' }])
    const result = findActiveBlock(editor)
    expect(result?.node.type.name).toBe('noteBlock')
    expect(result?.depth).toBe(2)
  })

  it('walks past non-block depths to find the enclosing block', () => {
    const editor = makeEditor([
      { typeName: 'doc' },
      { typeName: 'taskBlock' },
      { typeName: 'paragraph' }
    ])
    // depth 3 = paragraph (not a block), depth 2 = taskBlock (block).
    const result = findActiveBlock(editor)
    expect(result?.node.type.name).toBe('taskBlock')
    expect(result?.depth).toBe(2)
  })

  it('returns the FIRST (innermost) block when multiple block types nest', () => {
    const editor = makeEditor([
      { typeName: 'doc' },
      { typeName: 'noteBlock' },
      { typeName: 'taskBlock' }
    ])
    // depth 3 = taskBlock encountered first while walking up.
    const result = findActiveBlock(editor)
    expect(result?.node.type.name).toBe('taskBlock')
    expect(result?.depth).toBe(3)
  })

  it('handles headerBlock like any other block type', () => {
    const editor = makeEditor([
      { typeName: 'doc' },
      { typeName: 'headerBlock' }
    ])
    const result = findActiveBlock(editor)
    expect(result?.node.type.name).toBe('headerBlock')
    expect(result?.depth).toBe(2)
  })

  it('returns null when depth is 0 (no parent at all)', () => {
    const editor = {
      state: {
        selection: {
          $from: {
            depth: 0,
            node: () => ({ type: { name: 'doc' } })
          }
        }
      }
    } as any
    expect(findActiveBlock(editor)).toBeNull()
  })
})
