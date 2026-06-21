import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import HeadingLevelMenu from './HeadingLevelMenu.svelte'

function makeMockEditor(nodeType: string = 'noteBlock', depth: number = 0) {
  const mockNode = { type: { name: nodeType }, attrs: { depth, align: 'left' } }
  return {
    isActive: vi.fn(() => false),
    state: {
      selection: {
        $from: {
          depth: 1,
          node: () => mockNode
        }
      }
    }
  }
}

describe('HeadingLevelMenu', () => {
  it('renders the trigger button showing current block type', () => {
    const editor = makeMockEditor('noteBlock') as any
    const { getByRole } = render(HeadingLevelMenu, { props: { editor } })
    const trigger = getByRole('button')
    expect(trigger.textContent).toContain('Note')
  })

  it('shows H1 label for a headerBlock with depth 1', () => {
    const editor = makeMockEditor('headerBlock', 1) as any
    const { getByRole } = render(HeadingLevelMenu, { props: { editor } })
    const trigger = getByRole('button')
    expect(trigger.textContent).toContain('H1')
  })

  it('opens menu with 5 options on click', async () => {
    const editor = makeMockEditor('noteBlock') as any
    const { getByRole, getAllByRole } = render(HeadingLevelMenu, { props: { editor } })
    const trigger = getByRole('button')
    await fireEvent.click(trigger)
    const items = getAllByRole('menuitemradio')
    expect(items).toHaveLength(5)
  })

  it('dispatches silt:change-block-type on selection', async () => {
    const editor = makeMockEditor('noteBlock') as any
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')
    const { getByRole, getByText } = render(HeadingLevelMenu, { props: { editor } })
    await fireEvent.click(getByRole('button'))
    await fireEvent.click(getByText('Heading 2'))
    const lastCall = dispatchSpy.mock.calls[dispatchSpy.mock.calls.length - 1][0] as CustomEvent
    expect(lastCall.type).toBe('silt:change-block-type')
    expect(lastCall.detail.type).toBe('headerBlock')
    expect(lastCall.detail.depth).toBe(2)
    dispatchSpy.mockRestore()
  })
})
