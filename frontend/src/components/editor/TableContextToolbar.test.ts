import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import { tick } from 'svelte'
import TableContextToolbar from './TableContextToolbar.svelte'

// Minimal editor mock matching the surface TableContextToolbar uses:
//   editor.on/off('selectionUpdate' | 'transaction', handler)
//   editor.can().addRowBefore?.() … → boolean
//   editor.chain().focus().addRowBefore().run() … / .run()  (Escape)
function makeMockEditor(overrides: Record<string, boolean> = {}) {
  const canMap: Record<string, boolean> = {
    addRowBefore: true,
    addRowAfter: true,
    addColumnBefore: true,
    addColumnAfter: true,
    deleteRow: true,
    deleteColumn: true,
    ...overrides
  }
  const handlers: Record<string, Array<() => void>> = {}
  const focusReturnSpy = vi.fn()
  return {
    on: vi.fn((event: string, handler: () => void) => {
      ;(handlers[event] ||= []).push(handler)
    }),
    off: vi.fn((event: string, handler: () => void) => {
      handlers[event] = (handlers[event] || []).filter((h) => h !== handler)
    }),
    can: () => ({
      addRowBefore: () => canMap.addRowBefore,
      addRowAfter: () => canMap.addRowAfter,
      addColumnBefore: () => canMap.addColumnBefore,
      addColumnAfter: () => canMap.addColumnAfter,
      deleteRow: () => canMap.deleteRow,
      deleteColumn: () => canMap.deleteColumn
    }),
    chain: () => ({
      focus: () => ({
        addRowBefore: () => ({ run: vi.fn() }),
        addRowAfter: () => ({ run: vi.fn() }),
        addColumnBefore: () => ({ run: vi.fn() }),
        addColumnAfter: () => ({ run: vi.fn() }),
        deleteRow: () => ({ run: vi.fn() }),
        deleteColumn: () => ({ run: vi.fn() }),
        run: focusReturnSpy
      })
    }),
    _canMap: canMap,
    _emit(event: string) {
      ;(handlers[event] || []).forEach((h) => h())
    },
    _focusReturnSpy: focusReturnSpy
  }
}

describe('TableContextToolbar', () => {
  it('renders six operation buttons with data-tb', async () => {
    const editor = makeMockEditor() as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const buttons = container.querySelectorAll('[data-tb]')
    expect(buttons.length).toBe(6)
  })

  it('moves focus forward on Arrow Right', async () => {
    const editor = makeMockEditor() as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const toolbar = container.querySelector('[role="toolbar"]')!
    const buttons = toolbar.querySelectorAll<HTMLButtonElement>('[data-tb]')
    fireEvent.keyDown(toolbar, { key: 'ArrowRight' })
    await tick()
    expect(document.activeElement).toBe(buttons[1])
  })

  it('skips a disabled button on Arrow Right', async () => {
    const editor = makeMockEditor({ deleteRow: false }) as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const toolbar = container.querySelector('[role="toolbar"]')!
    const buttons = toolbar.querySelectorAll<HTMLButtonElement>('[data-tb]')
    // Navigate to index 3 (col-right) — first three are always enabled.
    for (let k = 0; k < 3; k++) {
      fireEvent.keyDown(toolbar, { key: 'ArrowRight' })
      await tick()
    }
    expect(document.activeElement).toBe(buttons[3])
    // Arrow Right should skip index 4 (del-row, disabled) → land on 5 (del-col).
    fireEvent.keyDown(toolbar, { key: 'ArrowRight' })
    await tick()
    expect(document.activeElement).toBe(buttons[5])
  })

  it('moves focus to last button on End', async () => {
    const editor = makeMockEditor() as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const toolbar = container.querySelector('[role="toolbar"]')!
    const buttons = toolbar.querySelectorAll<HTMLButtonElement>('[data-tb]')
    fireEvent.keyDown(toolbar, { key: 'End' })
    await tick()
    expect(document.activeElement).toBe(buttons[5])
  })

  it('returns focus to editor on Escape', async () => {
    const editor = makeMockEditor() as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const toolbar = container.querySelector('[role="toolbar"]')!
    fireEvent.keyDown(toolbar, { key: 'Escape' })
    expect(editor._focusReturnSpy).toHaveBeenCalled()
  })

  it('re-clamps tabindex off a button that becomes disabled', async () => {
    const editor = makeMockEditor() as any
    const { container } = render(TableContextToolbar, { props: { editor } })
    await tick()
    const toolbar = container.querySelector('[role="toolbar"]')!
    const buttons = toolbar.querySelectorAll<HTMLButtonElement>('[data-tb]')
    // Navigate to index 4 (del-row).
    for (let k = 0; k < 4; k++) {
      fireEvent.keyDown(toolbar, { key: 'ArrowRight' })
      await tick()
    }
    expect(buttons[4].tabIndex).toBe(0)
    // Disable del-row and fire a selection update so ops re-derive.
    editor._canMap.deleteRow = false
    editor._emit('selectionUpdate')
    await tick()
    // The re-clamp effect should have moved the Tab-stop forward to del-col.
    expect(buttons[4].tabIndex).toBe(-1)
    expect(buttons[5].tabIndex).toBe(0)
  })
})
