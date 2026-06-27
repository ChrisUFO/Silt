// Unit coverage for the Silt-token → Shiki-theme mapper (#194). Pure and
// synchronous, so it needs no jsdom/reactivity — it pins the single place
// Shiki meets the Silt theme: mode flows through, token colors map to the
// right scopes, and missing tokens fall back to sane defaults.

import { describe, it, expect } from 'vitest'
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
