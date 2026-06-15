// Registry invariants for the preloaded font picker (#82). The registry is
// the single source of truth for both the General-tab picker and the
// Appearance-tab typography indicator, so its shape is pinned here: the
// defaults exist, system fallbacks exist, ids are unique, and the helper
// selectors return the right slices.

import { describe, expect, it } from 'vitest'
import {
  FONT_REGISTRY,
  DEFAULT_BODY_ID,
  DEFAULT_MONO_ID,
  DEFAULT_HEADLINE_ID,
  bundledByCategory,
  systemFonts,
  findByCssFamily,
  displayNameForCssFamily
} from './fonts'

describe('font registry (#82)', () => {
  it('bundles the full ~26-family set across sans/mono/display/serif + system fallbacks', () => {
    const ids = new Set(FONT_REGISTRY.map((f) => f.id))
    const bundled = FONT_REGISTRY.filter((f) => f.source === 'bundled')
    // 26 bundled families (12 sans + 6 mono + 4 display + 4 serif).
    expect(bundled).toHaveLength(26)
    expect(bundledByCategory('sans')).toHaveLength(12)
    expect(bundledByCategory('mono')).toHaveLength(6)
    expect(bundledByCategory('display')).toHaveLength(4)
    expect(bundledByCategory('serif')).toHaveLength(4)
    // A representative sample from each category is present.
    for (const id of ['plus-jakarta-sans', 'inter', 'geist', 'atkinson-hyperlegible', 'dm-sans']) {
      expect(ids.has(id), `expected sans ${id}`).toBe(true)
    }
    for (const id of ['jetbrains-mono', 'geist-mono', 'martian-mono']) {
      expect(ids.has(id), `expected mono ${id}`).toBe(true)
    }
    for (const id of ['hanken-grotesk', 'schibsted-grotesk', 'bricolage-grotesque']) {
      expect(ids.has(id), `expected display ${id}`).toBe(true)
    }
    for (const id of ['source-serif-4', 'newsreader', 'lora', 'crimson-pro']) {
      expect(ids.has(id), `expected serif ${id}`).toBe(true)
    }
    // System fallbacks are always present (offline).
    for (const id of ['system-ui', 'sans-serif', 'monospace']) {
      expect(ids.has(id), `expected system fallback ${id}`).toBe(true)
    }
  })

  it('the three defaults are bundled and resolve to the canonical families', () => {
    const body = findByCssFamily('Plus Jakarta Sans')
    const mono = findByCssFamily('JetBrains Mono')
    const headline = findByCssFamily('Hanken Grotesk')
    expect(body?.id).toBe(DEFAULT_BODY_ID)
    expect(mono?.id).toBe(DEFAULT_MONO_ID)
    expect(headline?.id).toBe(DEFAULT_HEADLINE_ID)
    // Defaults are bundled (not system) so they render offline.
    expect(body?.source).toBe('bundled')
    expect(mono?.source).toBe('bundled')
    expect(headline?.source).toBe('bundled')
  })

  it('the first-class theme pairings are all bundled (so every theme renders out of box)', () => {
    // Terra Noir: Source Serif 4 / IBM Plex Mono / Newsreader
    // Linen: Mulish / Fira Code / Sora
    // Stark: Atkinson Hyperlegible / Geist Mono
    // Graphite: Geist / Geist Mono / Schibsted Grotesk
    const requiredCssFamilies = [
      'Source Serif 4', 'IBM Plex Mono', 'Newsreader',
      'Mulish', 'Fira Code', 'Sora',
      'Atkinson Hyperlegible', 'Geist Mono',
      'Geist', 'Schibsted Grotesk'
    ]
    for (const family of requiredCssFamilies) {
      const entry = findByCssFamily(family)
      expect(entry, `theme-pairing family ${family} missing from registry`).toBeDefined()
      expect(entry?.source, `${family} must be bundled`).toBe('bundled')
    }
  })

  it('has unique ids and a non-empty cssFamily per entry', () => {
    const seen = new Set<string>()
    for (const f of FONT_REGISTRY) {
      expect(seen.has(f.id), `duplicate id ${f.id}`).toBe(false)
      seen.add(f.id)
      expect(f.cssFamily.length, `empty cssFamily for ${f.id}`).toBeGreaterThan(0)
      expect(f.displayName.length, `empty displayName for ${f.id}`).toBeGreaterThan(0)
    }
  })

  it('bundledByCategory returns only bundled entries of that category', () => {
    for (const category of ['sans', 'mono', 'display', 'serif'] as const) {
      const entries = bundledByCategory(category)
      expect(entries.every((f) => f.source === 'bundled' && f.category === category)).toBe(true)
      // System fallbacks are never returned by bundledByCategory.
      expect(entries.some((f) => f.source === 'system')).toBe(false)
      // Each category has at least one bundled family.
      expect(entries.length, `${category} should have bundled entries`).toBeGreaterThan(0)
    }
  })

  it('systemFonts returns only system-source entries', () => {
    const sys = systemFonts()
    expect(sys.length).toBeGreaterThan(0)
    expect(sys.every((f) => f.source === 'system')).toBe(true)
  })

  it('displayNameForCssFamily falls back to the raw value for unknown families', () => {
    expect(displayNameForCssFamily('Plus Jakarta Sans')).toBe('Plus Jakarta Sans')
    // A hand-edited config value / extension-pack font not in the registry is
    // shown verbatim (the picker never blanks a value it doesn't curate).
    expect(displayNameForCssFamily('Some Custom Font')).toBe('Some Custom Font')
  })
})
