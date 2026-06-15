import { describe, expect, it } from 'vitest'
import { sanitizeFontFamilyCSS } from './sanitize'

describe('sanitizeFontFamilyCSS', () => {
  it('strips CSS declaration/block breakouts (; { })', () => {
    expect(sanitizeFontFamilyCSS('Inter')).toBe('Inter')
    expect(sanitizeFontFamilyCSS("'Plus Jakarta Sans', sans-serif")).toBe(
      "'Plus Jakarta Sans', sans-serif"
    )
    // The breakout chars the editor-token injector and themes.isValidFontFamily
    // defend against — none survive into a CSS context.
    expect(sanitizeFontFamilyCSS('Evil}; body{background:red}')).toBe('Evil bodybackground:red')
    expect(sanitizeFontFamilyCSS('a;b{c}d')).toBe('abcd')
  })

  it('is idempotent (running twice is a no-op)', () => {
    const once = sanitizeFontFamilyCSS('Hack}; x{')
    expect(sanitizeFontFamilyCSS(once)).toBe(once)
  })

  it('is lossless for legitimate font-family names', () => {
    // Real family names (bare, quoted, or stacks) never contain ; {} — the
    // strip never alters a valid value.
    for (const v of ['JetBrains Mono', "'Source Serif 4', serif", 'system-ui', 'monospace']) {
      expect(sanitizeFontFamilyCSS(v)).toBe(v)
    }
  })
})
