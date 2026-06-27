// Component coverage for the Source view (#194 Shiki highlighting, #171 base).
// The Shiki call is mocked so the test is deterministic and never depends on
// WASM/grammar loading in jsdom; the contract under test is the fallback
// (plain text until the highlighter resolves + on error), theme-change
// re-highlight, the Copy button, line numbers, and the read-only ARIA role.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { tick } from 'svelte'
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor
} from '@testing-library/svelte'

const mocks = vi.hoisted(() => ({
  themeState: {
    mode: 'dark' as 'dark' | 'light' | 'system',
    darkTokens: { '--color-text-primary': '#eee', '--color-surface': '#111' },
    lightTokens: { '--color-text-primary': '#111', '--color-surface': '#eee' }
  },
  // The mock highlighter: resolves to a fixed span, or rejects, or never
  // resolves (pending) — each test picks the behaviour.
  highlight: vi.fn()
}))

vi.mock('../../theme/store.svelte', () => ({ themeState: mocks.themeState }))
vi.mock('../../lib/editor/useMarkdownHighlighter', () => ({
  // Forward both args so tests can assert the theme Shiki would receive.
  // tokensToShikiTheme runs for real (pure); only the async Shiki call is mocked.
  tokensToShikiTheme: (tokens: Record<string, string>, mode: string) => ({
    name: 'silt-source',
    type: mode,
    fg: tokens['--color-text-primary'] ?? '#eee',
    bg: tokens['--color-surface'] ?? '#111',
    colors: {},
    tokenColors: []
  }),
  highlightMarkdown: (code: string, theme: unknown) =>
    mocks.highlight(code, theme)
}))

import MarkdownSourceViewer from './MarkdownSourceViewer.svelte'
import type { ParsedBlock } from '../../lib/editor/types'

function mkBlock(
  text: string,
  opts: { depth?: number; clean?: string } = {}
): ParsedBlock {
  return {
    id: 'b-' + Math.random().toString(36).slice(2),
    parent_id: '',
    type: 'NOTE',
    depth: opts.depth ?? 0,
    raw_text: text,
    clean_text: opts.clean ?? text,
    line_number: 1
    // ParsedBlock carries many optional fields; only what the viewer reads
    // (raw_text, clean_text, depth) matters here.
  } as ParsedBlock
}

const BLOCKS: ParsedBlock[] = [
  mkBlock('# Heading'),
  mkBlock('**bold** and *italic*')
]

describe('MarkdownSourceViewer', () => {
  beforeEach(() => {
    mocks.highlight.mockReset()
    mocks.themeState.mode = 'dark'
  })
  afterEach(() => cleanup())

  it('renders the plain markdown as a fallback before the highlighter resolves', () => {
    // Never-resolving highlighter simulates the lazy grammar load window.
    mocks.highlight.mockReturnValue(new Promise(() => {}))
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'Work/Section/Page.md' }
    })
    const code = document.querySelector('.source-code')!
    // The raw markdown text is present verbatim (no spans yet).
    expect(code.textContent).toContain('# Heading')
    expect(code.textContent).toContain('**bold**')
  })

  it('renders highlighted HTML once the highlighter resolves', async () => {
    mocks.highlight.mockResolvedValue(
      '<span style="color:#abc"># Heading</span>'
    )
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'Work/Section/Page.md' }
    })
    await waitFor(() => {
      const span = document.querySelector('.source-code span')
      expect(span).not.toBeNull()
      expect(span!.getAttribute('style')).toContain('#abc')
    })
  })

  it('falls back to plain text when the highlighter errors', async () => {
    mocks.highlight.mockRejectedValue(new Error('grammar load failed'))
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'Work/Section/Page.md' }
    })
    // The raw text survives the error path (highlightMarkdown returns null).
    await tick()
    await tick()
    const code = document.querySelector('.source-code')!
    expect(code.textContent).toContain('**bold**')
    expect(code.querySelector('span')).toBeNull()
  })

  it('passes a theme whose type matches the active mode to the highlighter', async () => {
    mocks.highlight.mockResolvedValue('<span>x</span>')
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'p.md' }
    })
    await waitFor(() => expect(mocks.highlight).toHaveBeenCalled())
    // The component resolves mode='dark' → dark theme type.
    const theme = mocks.highlight.mock.calls[0][1] as { type: string }
    expect(theme.type).toBe('dark')
  })

  it('re-highlights when the source content changes', async () => {
    mocks.highlight.mockResolvedValue('<span>highlighted</span>')
    const { rerender } = render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'p.md' }
    })
    await waitFor(() => expect(mocks.highlight).toHaveBeenCalledTimes(1))
    const callsAfterFirst = mocks.highlight.mock.calls.length
    // New blocks → new markdown → the $effect re-runs and re-highlights.
    await rerender({ blocks: [mkBlock('# Other content')], filePath: 'p.md' })
    await waitFor(() =>
      expect(mocks.highlight.mock.calls.length).toBeGreaterThan(callsAfterFirst)
    )
    // The re-highlight saw the new source.
    const lastCall = mocks.highlight.mock.calls.at(-1)!
    expect(lastCall[0]).toContain('Other content')
  })

  it('renders a line-number gutter matching the line count', () => {
    mocks.highlight.mockReturnValue(new Promise(() => {}))
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'p.md' }
    })
    const nums = document.querySelectorAll('.line-num')
    // Two blocks → two reconstructed lines.
    expect(nums).toHaveLength(2)
    expect(nums[0].textContent).toBe('1')
    expect(nums[1].textContent).toBe('2')
  })

  it('copies the reconstructed markdown to the clipboard', async () => {
    mocks.highlight.mockReturnValue(new Promise(() => {}))
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'p.md' }
    })
    await fireEvent.click(
      screen.getByRole('button', { name: /copy as markdown/i })
    )
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText.mock.calls[0][0]).toContain('# Heading')
    expect(writeText.mock.calls[0][0]).toContain('**bold**')
  })

  it('exposes the source body as a read-only document landmark', () => {
    mocks.highlight.mockReturnValue(new Promise(() => {}))
    render(MarkdownSourceViewer, {
      props: { blocks: BLOCKS, filePath: 'Work/Page.md' }
    })
    const doc = screen.getByRole('document')
    expect(doc.getAttribute('aria-label')).toBe('Source view of Work/Page.md')
  })
})
