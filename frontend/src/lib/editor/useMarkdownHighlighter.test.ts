// Unit coverage for the Silt-token → Shiki-theme mapper (#194) and the
// highlight cache (#194 hardening). The mapper tests are pure/synchronous
// (no jsdom); the cache test mocks shiki's codeToHtml so it stays deterministic
// and never depends on WASM/grammar loading.

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { tokensToShikiTheme } from './useMarkdownHighlighter'

const DARK = {
  '--color-surface': '#111111',
  '--color-text-primary': '#eeeeee',
  '--color-text-muted': '#888888',
  '--color-accent-primary-start': '#4f7cff',
  '--color-accent-secondary-start': '#22d3ee',
  '--color-status-warn': '#f0a020'
}

const LIGHT = {
  '--color-surface': '#fafafa',
  '--color-text-primary': '#111111',
  '--color-text-muted': '#666666',
  '--color-accent-primary-start': '#2563eb',
  '--color-accent-secondary-start': '#0891b2',
  '--color-status-warn': '#b45309'
}

describe('tokensToShikiTheme (#194 mapper)', () => {
  it('threads the concrete mode (dark/light) through to the Shiki theme type', () => {
    expect(tokensToShikiTheme(DARK, 'dark').type).toBe('dark')
    expect(tokensToShikiTheme(LIGHT, 'light').type).toBe('light')
  })

  it('uses the surface/text tokens for bg/fg and editor.background', () => {
    const theme = tokensToShikiTheme(DARK, 'dark')
    expect(theme.bg).toBe('#111111')
    expect(theme.fg).toBe('#eeeeee')
    expect(theme.colors['editor.background']).toBe('#111111')
  })

  it('maps the dark vs light accent tokens distinctly (theme-aware highlight)', () => {
    const dark = tokensToShikiTheme(DARK, 'dark')
    const light = tokensToShikiTheme(LIGHT, 'light')
    const darkHeading = dark.tokenColors.find((t) =>
      (t.scope as string[]).includes('markup.heading')
    )!
    const lightHeading = light.tokenColors.find((t) =>
      (t.scope as string[]).includes('markup.heading')
    )!
    expect(darkHeading.settings.foreground).toBe('#4f7cff')
    expect(lightHeading.settings.foreground).toBe('#2563eb')
  })

  it('applies typographic fontStyle for bold/italic/strike/heading/quote', () => {
    const theme = tokensToShikiTheme(DARK, 'dark')
    const byScope = (s: string) =>
      theme.tokenColors.find((t) => (t.scope as string[]).includes(s))
    expect(byScope('markup.bold')!.settings.fontStyle).toBe('bold')
    expect(byScope('markup.italic')!.settings.fontStyle).toBe('italic')
    expect(byScope('markup.strike')!.settings.fontStyle).toBe('strikethrough')
    expect(byScope('markup.heading')!.settings.fontStyle).toBe('bold')
    expect(byScope('markup.quote')!.settings.fontStyle).toBe('italic')
  })

  it('falls back to built-in defaults when a token is missing', () => {
    // Empty token map (e.g. before initTheme resolves) must still produce a
    // usable theme — never throw, never produce undefined colors.
    const theme = tokensToShikiTheme({}, 'dark')
    expect(theme.type).toBe('dark')
    expect(theme.fg).toMatch(/^#/)
    expect(theme.bg).toMatch(/^#/)
    expect(theme.tokenColors.length).toBeGreaterThan(0)
    for (const t of theme.tokenColors) {
      // Every rule that declares a foreground must have a real color.
      if (t.settings.foreground !== undefined) {
        expect(t.settings.foreground).toMatch(/^#/)
      }
    }
  })
})

// The cache test needs highlightMarkdown, which calls shiki's codeToHtml. Mock
// it so the test never touches WASM/grammar loading and can assert call counts.
const shikiMock = vi.hoisted(() => ({ codeToHtml: vi.fn() }))
vi.mock('shiki', () => ({ codeToHtml: shikiMock.codeToHtml }))

describe('highlightMarkdown cache (#194 hardening)', () => {
  // Use a unique code per test run so the module-scoped cache (shared across
  // tests in this file) can't hand one test a hit populated by another.
  let n = 0
  function uniqueCode(): string {
    n += 1
    return `# cached heading ${n}`
  }

  beforeEach(() => {
    shikiMock.codeToHtml.mockReset()
    // Shiki wraps output as <pre><code>INNER</code></pre>; the highlighter
    // extracts INNER, so the mock returns that wrapper shape.
    shikiMock.codeToHtml.mockImplementation(
      (code: string) => `<pre class="shiki"><code>${code}</code></pre>`
    )
  })

  it('serves a repeat call from the cache (codeToHtml runs once)', async () => {
    const { highlightMarkdown } = await import('./useMarkdownHighlighter')
    const theme = tokensToShikiTheme(DARK, 'dark')
    const code = uniqueCode()

    await highlightMarkdown(code, theme)
    await highlightMarkdown(code, theme)

    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(1)
  })

  it('re-highlights when the theme changes (cache key includes the theme)', async () => {
    const { highlightMarkdown } = await import('./useMarkdownHighlighter')
    const code = uniqueCode()

    await highlightMarkdown(code, tokensToShikiTheme(DARK, 'dark'))
    await highlightMarkdown(code, tokensToShikiTheme(LIGHT, 'light'))

    // Two distinct themes → two highlights (no stale cache hit).
    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(2)
  })

  it('returns null on a Shiki failure and does not cache it', async () => {
    const { highlightMarkdown } = await import('./useMarkdownHighlighter')
    const theme = tokensToShikiTheme(DARK, 'dark')
    const code = uniqueCode()

    shikiMock.codeToHtml.mockImplementation(() => {
      throw new Error('grammar load failed')
    })
    const first = await highlightMarkdown(code, theme)
    expect(first).toBeNull()

    // A later successful highlight of the same code still runs (the failure
    // was not cached as null).
    shikiMock.codeToHtml.mockImplementation(
      (c: string) => `<pre class="shiki"><code>${c}</code></pre>`
    )
    const second = await highlightMarkdown(code, theme)
    expect(second).not.toBeNull()
    expect(shikiMock.codeToHtml).toHaveBeenCalledTimes(2)
  })
})
