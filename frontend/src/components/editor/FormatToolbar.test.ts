import { describe, it, expect, vi } from 'vitest'
import { render, fireEvent } from '@testing-library/svelte'
import FormatToolbar from './FormatToolbar.svelte'

// Mock editor with the minimal interface FormatToolbar uses.
function makeMockEditor() {
  const marks = new Set<string>()
  const mockNode = { type: { name: 'noteBlock' }, attrs: { depth: 0, align: 'left' } }
  return {
    isActive: vi.fn((mark: string) => marks.has(mark)),
    chain: () => ({
      focus: () => ({ toggleMark: () => ({ run: () => {} }), unsetLink: () => ({ run: () => {} }), unsetAllMarks: () => ({ run: () => {} }), run: () => {} }),
      toggleMark: () => ({ run: () => {} }),
      unsetLink: () => ({ run: () => {} }),
      unsetAllMarks: () => ({ run: () => {} })
    }),
    state: {
      selection: {
        empty: false,
        $from: { depth: 1, node: () => mockNode }
      }
    },
    _marks: marks
  }
}

describe('FormatToolbar', () => {
  it('renders all 8 inline mark buttons', () => {
    const editor = makeMockEditor() as any
    const { getByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: true }
    })
    for (const label of ['Bold', 'Italic', 'Underline', 'Strikethrough', 'Inline code', 'Highlight', 'Subscript', 'Superscript']) {
      expect(getByLabelText(label)).toBeTruthy()
    }
  })

  it('renders link and clear-formatting buttons', () => {
    const editor = makeMockEditor() as any
    const { getByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: true }
    })
    expect(getByLabelText('Insert link')).toBeTruthy()
    expect(getByLabelText('Clear formatting')).toBeTruthy()
  })

  it('renders 4 alignment buttons', () => {
    const editor = makeMockEditor() as any
    const { getByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: true }
    })
    expect(getByLabelText('Align left')).toBeTruthy()
    expect(getByLabelText('Align center')).toBeTruthy()
    expect(getByLabelText('Align right')).toBeTruthy()
    expect(getByLabelText('Align justify')).toBeTruthy()
  })

  it('hides color pickers when colorEnabled is false', () => {
    const editor = makeMockEditor() as any
    const { queryByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: false }
    })
    expect(queryByLabelText('Text color')).toBeNull()
    expect(queryByLabelText('Background color')).toBeNull()
  })

  it('reflects aria-pressed for active marks', () => {
    const editor = makeMockEditor() as any
    const { getByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(['bold']), isDark: true, colorEnabled: true }
    })
    const boldBtn = getByLabelText('Bold') as HTMLButtonElement
    expect(boldBtn.getAttribute('aria-pressed')).toBe('true')
  })

  it('has role=toolbar with tabindex for keyboard navigation', () => {
    const editor = makeMockEditor() as any
    const { getByRole } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: true }
    })
    const toolbar = getByRole('toolbar')
    expect(toolbar).toBeTruthy()
    expect(toolbar.getAttribute('tabindex')).toBe('-1')
  })

  it('dispatches silt:set-block-align on alignment click', () => {
    const editor = makeMockEditor() as any
    const dispatchSpy = vi.spyOn(window, 'dispatchEvent')
    const { getByLabelText } = render(FormatToolbar, {
      props: { editor, activeMarks: new Set<string>(), isDark: true, colorEnabled: true }
    })
    fireEvent.click(getByLabelText('Align center'))
    const lastCall = dispatchSpy.mock.calls[dispatchSpy.mock.calls.length - 1][0] as CustomEvent
    expect(lastCall.type).toBe('silt:set-block-align')
    expect(lastCall.detail).toBe('center')
    dispatchSpy.mockRestore()
  })
})
