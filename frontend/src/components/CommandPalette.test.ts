import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import CommandPalette from './CommandPalette.svelte'

describe('CommandPalette', () => {
  it('renders commands matching the query', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    const { queryByText } = render(CommandPalette, {
      props: { onSelect, onClose, query: 'Heading 1' }
    })

    expect(queryByText('Heading 1')).toBeTruthy()
    expect(queryByText('Italic')).toBeNull()
  })

  it('navigates with keyboard and selects a command', async () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(CommandPalette, {
      props: { onSelect, onClose, query: 'Heading 1' }
    })

    await fireEvent.keyDown(window, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('h1')
  })

  it('closes on Escape key press', async () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(CommandPalette, {
      props: { onSelect, onClose }
    })

    await fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalled()
  })

  it('shows no matching commands when query matches nothing', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    const { getByText } = render(CommandPalette, {
      props: { onSelect, onClose, query: 'nonexistentcommand' }
    })

    expect(getByText('No matching commands')).toBeTruthy()
  })
})
