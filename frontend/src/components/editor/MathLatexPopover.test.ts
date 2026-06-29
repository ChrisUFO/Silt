// Component coverage for the LaTeX equation popover (Phase 5 / #328).
// The popover owns three contracts that only a rendered test can verify:
// the live-preview pipeline (renderKatex → {@html} / aria-live error), the
// keyboard map (Escape cancels, Ctrl/Cmd+Enter commits, plain Enter inserts a
// newline, Tab focus-traps), and the Commit-enabled gate. renderKatex is
// mocked via vi.hoisted so no real KaTeX bundle loads.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte'

const katexMock = vi.hoisted(() => ({
  renderKatex: vi.fn()
}))

vi.mock('../../lib/editor/useKatex', () => ({
  renderKatex: katexMock.renderKatex
}))

import MathLatexPopover from './MathLatexPopover.svelte'

// The component's preview pipeline is async (renderKatex → setState). A
// setTimeout(0) flushes both the promise microtask queue and the rAF used for
// autofocus; tick() flushes Svelte's effect scheduler.
async function flush(): Promise<void> {
  await tick()
  await new Promise((r) => setTimeout(r, 0))
  await tick()
}

describe('MathLatexPopover', () => {
  beforeEach(() => {
    katexMock.renderKatex.mockReset()
  })
  afterEach(() => {
    cleanup()
  })

  it('pre-fills the textarea with the initial latex and autofocuses it', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    render(MathLatexPopover, {
      props: {
        latex: 'a^2',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    const textarea = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(textarea.value).toBe('a^2')

    // Autofocus fires from a rAF callback.
    await new Promise((r) => setTimeout(r, 16))
    expect(document.activeElement).toBe(textarea)
  })

  it('renders the live preview html from renderKatex on open', async () => {
    katexMock.renderKatex.mockResolvedValue({
      html: '<span class="katex">preview</span>',
      error: null
    })
    render(MathLatexPopover, {
      props: {
        latex: '\\sum',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    await flush()

    expect(katexMock.renderKatex).toHaveBeenCalledWith('\\sum', true)
    expect(screen.getByText('preview')).toBeTruthy()
  })

  it('shows the parse error inside the aria-live region when renderKatex fails', async () => {
    katexMock.renderKatex.mockResolvedValue({
      html: '',
      error: 'KaTeX parse error: Unexpected character'
    })
    render(MathLatexPopover, {
      props: {
        latex: '@@@',
        displayMode: false,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    await flush()

    const err = screen.getByText(/KaTeX parse error/i)
    expect(err).toBeTruthy()
    // The error renders inside the preview pane, which is the live region.
    expect(err.closest('[aria-live]')?.getAttribute('aria-live')).toBe('polite')
  })

  it('updates the preview after the debounce window when the text changes', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    render(MathLatexPopover, {
      props: {
        latex: 'a',
        displayMode: false,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })
    await flush() // flush the immediate initial render
    katexMock.renderKatex.mockClear()
    katexMock.renderKatex.mockResolvedValue({
      html: '<span class="katex">new</span>',
      error: null
    })

    const textarea = screen.getByRole('textbox')
    await fireEvent.input(textarea, { target: { value: 'ab' } })

    // While inside the debounce window, no re-render has happened yet.
    expect(katexMock.renderKatex).not.toHaveBeenCalled()

    // Past the 150ms window the debounced render fires.
    await new Promise((r) => setTimeout(r, 180))
    await flush()

    expect(katexMock.renderKatex).toHaveBeenCalledWith('ab', false)
    expect(screen.getByText('new')).toBeTruthy()
  })

  it('disables Commit when the source is empty and enables it once typed', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    render(MathLatexPopover, {
      props: {
        latex: '',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    const commit = screen.getByRole('button', { name: 'Commit' })
    expect(commit).toBeDisabled()

    const textarea = screen.getByRole('textbox')
    await fireEvent.input(textarea, { target: { value: 'x+1' } })
    await tick()

    expect(commit).not.toBeDisabled()
  })

  it('Ctrl+Enter commits the trimmed source via onCommit', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    const onCommit = vi.fn()
    render(MathLatexPopover, {
      props: {
        latex: '  a^2  ',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit,
        onCancel: () => {}
      }
    })

    await fireEvent.keyDown(screen.getByRole('textbox'), {
      key: 'Enter',
      ctrlKey: true
    })

    expect(onCommit).toHaveBeenCalledTimes(1)
    expect(onCommit).toHaveBeenCalledWith('a^2')
  })

  it('Cmd+Enter (mac) also commits', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    const onCommit = vi.fn()
    render(MathLatexPopover, {
      props: {
        latex: 'x',
        displayMode: false,
        coords: { left: 10, top: 10 },
        onCommit,
        onCancel: () => {}
      }
    })

    await fireEvent.keyDown(screen.getByRole('textbox'), {
      key: 'Enter',
      metaKey: true
    })

    expect(onCommit).toHaveBeenCalledWith('x')
  })

  it('plain Enter does not commit (LaTeX is multi-line)', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    const onCommit = vi.fn()
    render(MathLatexPopover, {
      props: {
        latex: 'a',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit,
        onCancel: () => {}
      }
    })

    await fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })

    expect(onCommit).not.toHaveBeenCalled()
  })

  it('Escape calls onCancel', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    const onCancel = vi.fn()
    render(MathLatexPopover, {
      props: {
        latex: 'a',
        displayMode: false,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel
      }
    })

    await fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' })

    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('Cancel and Commit buttons invoke the right callback', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    const onCommit = vi.fn()
    const onCancel = vi.fn()
    render(MathLatexPopover, {
      props: {
        latex: 'x',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit,
        onCancel
      }
    })

    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onCommit).not.toHaveBeenCalled()

    await fireEvent.click(screen.getByRole('button', { name: 'Commit' }))
    expect(onCommit).toHaveBeenCalledWith('x')
  })

  it('is a modal dialog with a mode-specific accessible name', () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    render(MathLatexPopover, {
      props: {
        latex: '',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    const dialog = screen.getByRole('dialog')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.getAttribute('aria-label')).toBe('Edit block equation')
  })

  it('traps focus: Tab on the last focusable wraps back to the textarea', async () => {
    katexMock.renderKatex.mockResolvedValue({ html: '', error: null })
    render(MathLatexPopover, {
      props: {
        latex: 'x',
        displayMode: true,
        coords: { left: 10, top: 10 },
        onCommit: () => {},
        onCancel: () => {}
      }
    })

    const textarea = screen.getByRole('textbox')
    const commit = screen.getByRole('button', { name: 'Commit' })
    commit.focus()
    expect(document.activeElement).toBe(commit)

    await fireEvent.keyDown(commit, { key: 'Tab' })

    expect(document.activeElement).toBe(textarea)
  })
})
