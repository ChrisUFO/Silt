// Vitest coverage for the runtime theme injector (#74). The injector
// rewrites a single <style id="silt-theme">:root{...} block so the
// shell repaints in the same paint frame; these tests pin that
// contract.

import { describe, expect, it, beforeEach, afterEach, vi } from 'vitest'
import { injectTokens, readToken } from './inject'

const STYLE_ID = 'silt-theme'

describe('injectTokens', () => {
  beforeEach(() => {
    // Each test starts with a clean <head>.
    document.head.innerHTML = ''
  })

  afterEach(() => {
    document.head.innerHTML = ''
  })

  it('creates a single <style id="silt-theme"> element on first call', () => {
    injectTokens({ '--color-void': '#0c0c0e' })
    const el = document.getElementById(STYLE_ID)
    expect(el).toBeTruthy()
    expect(el!.tagName).toBe('STYLE')
  })

  it('reuses the same element on subsequent calls (no duplicate)', () => {
    injectTokens({ '--color-void': '#0c0c0e' })
    injectTokens({ '--color-void': '#101010' })
    const els = document.querySelectorAll(`#${STYLE_ID}`)
    expect(els.length).toBe(1)
  })

  it('emits every token as a CSS custom property on :root', () => {
    injectTokens({
      '--color-void': '#0c0c0e',
      '--color-accent-primary-start': '#2dd4bf'
    })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    expect(el.textContent).toContain(':root{')
    expect(el.textContent).toContain('--color-void:#0c0c0e;')
    expect(el.textContent).toContain('--color-accent-primary-start:#2dd4bf;')
  })

  it('skips empty / null / undefined values', () => {
    injectTokens({
      '--color-void': '#0c0c0e',
      '--empty': '',
      '--null': null as unknown as string,
      '--undef': undefined as unknown as string
    })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    expect(el.textContent).toContain('--color-void:#0c0c0e;')
    expect(el.textContent).not.toContain('--empty')
    expect(el.textContent).not.toContain('--null')
    expect(el.textContent).not.toContain('--undef')
  })

  it('performs exactly one textContent assignment per call (same-tick repaint)', () => {
    // The same-tick repaint contract is one DOM write -> one style
    // recalculation. We can't measure recalc timing in jsdom, but we
    // can assert the single textContent assignment is what carries
    // the new value. (We replace the property via Object.defineProperty
    // since textContent is a DOMString accessor, not a plain field.)
    injectTokens({ '--color-void': '#0c0c0e' })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    const calls: string[] = []
    const original = el.textContent
    Object.defineProperty(el, 'textContent', {
      configurable: true,
      get() {
        return calls[calls.length - 1] ?? original
      },
      set(v: string) {
        calls.push(v)
      }
    })
    injectTokens({ '--color-void': '#101010' })
    expect(calls).toHaveLength(1)
    expect(calls[0]).toContain('--color-void:#101010;')
  })

  it('round-trips through readToken', () => {
    injectTokens({ '--color-void': '#abcdef' })
    expect(readToken('--color-void').trim()).toBe('#abcdef')
  })

  it('does not inject tokens that contain no characters between : and ;', () => {
    // Regression guard for the empty-skip rule.
    injectTokens({ '--color-void': 'value', '--empty': '' })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    // No dangling ';' preceded by an empty value segment.
    expect(el.textContent).not.toMatch(/--empty:;/)
  })

  it('applying a new theme changes --color-void WITHOUT remounting the style element (#50)', () => {
    // The same-tick-repaint / no-remount contract is the core #46/#50
    // guarantee: switching the active theme rewrites the SAME <style>
    // element's textContent (one DOM write -> one recalc) rather than
    // creating a new element. Assert both halves: (1) exactly one
    // element exists after two applies, and (2) the resolved computed
    // value reflects the LATEST injection (proving the rewrite carried
    // the new value, not a stale copy).
    injectTokens({ '--color-void': '#0c0c0e' })
    const firstEl = document.getElementById(STYLE_ID)

    injectTokens({ '--color-void': '#101010' })
    const els = document.querySelectorAll(`#${STYLE_ID}`)
    expect(els.length).toBe(1)
    // The element instance is reused (same node), not recreated.
    expect(els[0]).toBe(firstEl)
    // The live computed value reflects the second injection.
    expect(readToken('--color-void')).toBe('#101010')
  })

  it('token consolidation: --color-* keys are the single namespace Tailwind utilities read (#146)', () => {
    // Regression guard for #146: the injector must write --color-* keys
    // (the SAME custom properties Tailwind v4 @theme declares and generates
    // utilities from). If the keys drift back to unprefixed --* names, the
    // utilities will read the static @theme fallback instead of the live
    // theme value and the whole shell will stop repainting on theme switch.
    //
    // Inject Terra Noir's accent values (distinct from the Cyber Forest
    // @theme fallbacks) and verify every representative token resolves to
    // the injected value, NOT the fallback.
    injectTokens({
      '--color-void': '#1a1410',
      '--color-surface': '#241c16',
      '--color-panel': '#2a2018',
      '--color-border-muted': '#3d2e22',
      '--color-border-zinc': '#4a3828',
      '--color-text-primary': '#e8d5c0',
      '--color-text-muted': '#a89478',
      '--color-accent-primary-start': '#e07a3c',
      '--color-accent-primary-end': '#c05022',
      '--color-accent-primary-glow': 'rgba(224,122,60,0.15)',
      '--color-accent-secondary-start': '#8b5cf6',
      '--color-accent-secondary-end': '#a855f7',
      '--color-accent-secondary-glow': 'rgba(139,92,246,0.12)'
    })
    // Accent start is the most visible tell — Cyber Forest is teal (#2dd4bf),
    // Terra Noir is clay (#e07a3c). If this resolves to teal, the bypass is
    // back.
    expect(readToken('--color-accent-primary-start').trim()).toBe('#e07a3c')
    expect(readToken('--color-text-primary').trim()).toBe('#e8d5c0')
    expect(readToken('--color-void').trim()).toBe('#1a1410')
    // Prove the value is NOT the @theme fallback (which would indicate the
    // injector wrote the wrong key and the utility fell through).
    expect(readToken('--color-accent-primary-start').trim()).not.toBe('#2dd4bf')
  })
})
