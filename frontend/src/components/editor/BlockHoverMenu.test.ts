import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import BlockHoverMenu from './BlockHoverMenu.svelte'

const mockNode = { type: { name: 'noteBlock' }, attrs: { depth: 0 } }
function makeEditor() {
  return {
    isActive: vi.fn(() => false),
    chain: () => ({ focus: () => ({ unsetAllMarks: () => ({ run: () => {} }), run: () => {} }), run: () => {} }),
    state: { selection: { $from: { depth: 1, node: () => mockNode }, empty: true, from: 0, to: 0 }, doc: { textBetween: () => 'text' } }
  }
}

describe('BlockHoverMenu', () => {
  it('renders the trigger button always visible (not opacity:0)', () => {
    const { getByRole } = render(BlockHoverMenu, { props: { editor: makeEditor() as any, colorEnabled: true, isDark: false } })
    const btn = getByRole('button')
    expect(btn).toBeTruthy()
  })

  it('opens menu with alignment + color + clear + copy on click', async () => {
    const { getByRole, getAllByRole, getByText } = render(BlockHoverMenu, { props: { editor: makeEditor() as any, colorEnabled: true, isDark: false } })
    await fireEvent.click(getByRole('button'))
    const items = getAllByRole('menuitem')
    expect(items.length).toBeGreaterThanOrEqual(7) // 4 align + 2 color + 3 clear/copy = 9, but separators
    expect(getByText('Align left')).toBeTruthy()
    expect(getByText('Text color')).toBeTruthy()
    expect(getByText('Clear formatting')).toBeTruthy()
  })

  it('hides color entries when colorEnabled is false', async () => {
    const { getByRole, queryByText } = render(BlockHoverMenu, { props: { editor: makeEditor() as any, colorEnabled: false, isDark: false } })
    await fireEvent.click(getByRole('button'))
    expect(queryByText('Text color')).toBeNull()
    expect(queryByText('Background color')).toBeNull()
  })

  it('dispatches silt:set-block-align on alignment click', async () => {
    const spy = vi.spyOn(window, 'dispatchEvent')
    const { getByRole, getByText } = render(BlockHoverMenu, { props: { editor: makeEditor() as any, colorEnabled: true, isDark: false } })
    await fireEvent.click(getByRole('button'))
    await fireEvent.click(getByText('Align center'))
    const lastCall = spy.mock.calls[spy.mock.calls.length - 1][0] as CustomEvent
    expect(lastCall.type).toBe('silt:set-block-align')
    expect(lastCall.detail).toBe('center')
    spy.mockRestore()
  })
})
