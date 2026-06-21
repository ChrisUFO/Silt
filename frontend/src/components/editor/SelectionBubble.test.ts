import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/svelte'
import SelectionBubble from './SelectionBubble.svelte'

describe('SelectionBubble', () => {
  it('does not render when selection is empty', () => {
    const { container } = render(SelectionBubble, {
      props: { editor: null, activeMarks: new Set<string>(), selectionEmpty: true, selectionCoords: null }
    })
    expect(container.querySelector('.selection-bubble')).toBeNull()
  })

  it('does not render when coords are null', () => {
    const { container } = render(SelectionBubble, {
      props: { editor: null, activeMarks: new Set<string>(), selectionEmpty: false, selectionCoords: null }
    })
    expect(container.querySelector('.selection-bubble')).toBeNull()
  })

  it('renders when selection is non-empty with coords', () => {
    const { container } = render(SelectionBubble, {
      props: { editor: null, activeMarks: new Set<string>(), selectionEmpty: false, selectionCoords: { left: 100, top: 100, bottom: 120 } }
    })
    expect(container.querySelector('.selection-bubble')).toBeTruthy()
  })

  it('renders 7 quick format buttons', () => {
    const { getByLabelText } = render(SelectionBubble, {
      props: { editor: null, activeMarks: new Set<string>(), selectionEmpty: false, selectionCoords: { left: 100, top: 100, bottom: 120 } }
    })
    for (const label of ['Bold', 'Italic', 'Strikethrough', 'Code', 'Highlight', 'Underline', 'Link']) {
      expect(getByLabelText(label)).toBeTruthy()
    }
  })

  it('reflects active marks via aria-checked', () => {
    const { getByLabelText } = render(SelectionBubble, {
      props: { editor: null, activeMarks: new Set<string>(['bold']), selectionEmpty: false, selectionCoords: { left: 100, top: 100, bottom: 120 } }
    })
    expect(getByLabelText('Bold').getAttribute('aria-checked')).toBe('true')
    expect(getByLabelText('Italic').getAttribute('aria-checked')).toBe('false')
  })
})
