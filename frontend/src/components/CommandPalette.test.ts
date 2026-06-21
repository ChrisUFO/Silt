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

  it('ranks label matches higher than description matches', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    const { container } = render(CommandPalette, {
      props: { onSelect, onClose, query: 'h' }
    })

    const buttons = container.querySelectorAll('button')
    const labels = Array.from(buttons).map((btn) => {
      const span = btn.querySelector('.font-label-sm-bold')
      return span ? span.textContent : ''
    })

    // "Heading 1" starts with "h", so it should rank higher than "Italic" (whose description contains "h")
    const h1Index = labels.indexOf('Heading 1')
    const italicIndex = labels.indexOf('Italic')

    expect(h1Index).toBeGreaterThan(-1)
    expect(italicIndex).toBeGreaterThan(-1)
    expect(h1Index).toBeLessThan(italicIndex)
  })

  // Contract: the root element must carry the `.glass-palette` class.
  // TipTapEditor.svelte's onDocumentClick guard checks for this class to
  // avoid clobbering slashMenuDismissed after a click-selection. If this
  // class is renamed, the guard in onDocumentClick must be updated too.
  it('root element carries the glass-palette class for click-outside guard', () => {
    const { container } = render(CommandPalette, {
      props: { onSelect: vi.fn(), onClose: vi.fn() }
    })
    const root = container.firstElementChild as HTMLElement
    expect(root.classList.contains('glass-palette')).toBe(true)
  })

  // Regression: after selecting a slash command via mouse click, the
  // document-level click listener (onDocumentClick in TipTapEditor) must
  // NOT dismiss/clobber the slash menu state. The guard early-returns when
  // the click target is inside `.glass-palette`. This test simulates that
  // guard logic and verifies clicks on palette items are ignored.
  it('document click inside glass-palette does not trigger dismissal', () => {
    const { container } = render(CommandPalette, {
      props: { onSelect: vi.fn(), onClose: vi.fn() }
    })

    let dismissed = false
    const onDocumentClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement | null
      if (!target) return
      if (
        target.closest('.ProseMirror') ||
        target.closest('.selection-bubble') ||
        target.closest('.glass-palette')
      )
        return
      dismissed = true
    }
    document.addEventListener('click', onDocumentClick)

    // Click a command button inside the palette
    const btn = container.querySelector('button') as HTMLButtonElement
    btn.click()

    expect(dismissed).toBe(false)

    document.removeEventListener('click', onDocumentClick)
  })
})
