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
    injectTokens({ '--bg-void': '#0c0c0e' })
    const el = document.getElementById(STYLE_ID)
    expect(el).toBeTruthy()
    expect(el!.tagName).toBe('STYLE')
  })

  it('reuses the same element on subsequent calls (no duplicate)', () => {
    injectTokens({ '--bg-void': '#0c0c0e' })
    injectTokens({ '--bg-void': '#101010' })
    const els = document.querySelectorAll(`#${STYLE_ID}`)
    expect(els.length).toBe(1)
  })

  it('emits every token as a CSS custom property on :root', () => {
    injectTokens({
      '--bg-void': '#0c0c0e',
      '--accent-primary-start': '#2dd4bf'
    })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    expect(el.textContent).toContain(':root{')
    expect(el.textContent).toContain('--bg-void:#0c0c0e;')
    expect(el.textContent).toContain('--accent-primary-start:#2dd4bf;')
  })

  it('skips empty / null / undefined values', () => {
    injectTokens({
      '--bg-void': '#0c0c0e',
      '--empty': '',
      '--null': null as unknown as string,
      '--undef': undefined as unknown as string
    })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    expect(el.textContent).toContain('--bg-void:#0c0c0e;')
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
    injectTokens({ '--bg-void': '#0c0c0e' })
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
    injectTokens({ '--bg-void': '#101010' })
    expect(calls).toHaveLength(1)
    expect(calls[0]).toContain('--bg-void:#101010;')
  })

  it('round-trips through readToken', () => {
    injectTokens({ '--bg-void': '#abcdef' })
    expect(readToken('--bg-void').trim()).toBe('#abcdef')
  })

  it('does not inject tokens that contain no characters between : and ;', () => {
    // Regression guard for the empty-skip rule.
    injectTokens({ '--bg-void': 'value', '--empty': '' })
    const el = document.getElementById(STYLE_ID) as HTMLStyleElement
    // No dangling ';' preceded by an empty value segment.
    expect(el.textContent).not.toMatch(/--empty:;/)
  })

  it('applying a new theme changes --bg-void WITHOUT remounting the style element (#50)', () => {
    // The same-tick-repaint / no-remount contract is the core #46/#50
    // guarantee: switching the active theme rewrites the SAME <style>
    // element's textContent (one DOM write -> one recalc) rather than
    // creating a new element. Assert both halves: (1) exactly one
    // element exists after two applies, and (2) the resolved computed
    // value reflects the LATEST injection (proving the rewrite carried
    // the new value, not a stale copy).
    injectTokens({ '--bg-void': '#0c0c0e' })
    const firstEl = document.getElementById(STYLE_ID)

    injectTokens({ '--bg-void': '#101010' })
    const els = document.querySelectorAll(`#${STYLE_ID}`)
    expect(els.length).toBe(1)
    // The element instance is reused (same node), not recreated.
    expect(els[0]).toBe(firstEl)
    // The live computed value reflects the second injection.
    expect(readToken('--bg-void')).toBe('#101010')
  })
})
