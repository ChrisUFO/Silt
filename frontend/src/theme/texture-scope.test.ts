// Regression guard: the decorative texture overlay must be scoped to the
// page writing surface, not the full viewport (#261). When the texture was
// on `body::before` with `position: fixed; inset: 0`, it textured the
// titlebar, sidebar, and toolbars instead of just the paper/page area.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const frontendSrc = resolve(__dirname, '..')

function readFile(relPath: string): string {
  return readFileSync(resolve(frontendSrc, relPath), 'utf-8')
}

describe('texture scope guard (#261)', () => {
  it('index.css does not apply the texture to body::before with position:fixed', () => {
    const css = readFile('index.css')

    // The texture must not be a fixed full-viewport body::before overlay.
    expect(css).not.toMatch(/body::before[^}]*position:\s*fixed/s)
  })

  it('index.css texture overlay uses sticky positioning (not absolute)', () => {
    const css = readFile('index.css')

    // The .silt-texture-surface::before must use position: sticky so the
    // texture stays pinned within the scroll viewport on long pages.
    // position: absolute would scroll out of view below the fold.
    expect(css).toMatch(/silt-texture-surface::before[^}]*position:\s*sticky/s)
    expect(css).not.toMatch(
      /silt-texture-surface::before[^}]*position:\s*absolute/s
    )
  })

  it('VirtualScrollContainer applies a texture surface class', () => {
    const svelte = readFile('components/VirtualScrollContainer.svelte')

    // The page scroll container must carry a texture surface class so the
    // texture overlay is scoped to the writing area, not the full app.
    expect(svelte).toMatch(/silt-texture-surface/)
  })

  it('index.css does not use a blanket child selector for content lifting', () => {
    const css = readFile('index.css')

    // A `.silt-texture-surface > *` rule would create a stacking context on
    // every direct child, trapping the editor's fixed-position overlays
    // (slash menu, color picker, link modal). Content must be lifted by a
    // single positioned wrapper in the component instead.
    expect(css).not.toMatch(/silt-texture-surface\s*>\s*\*/)
  })

  it('VirtualScrollContainer lifts content via a positioned wrapper', () => {
    const svelte = readFile('components/VirtualScrollContainer.svelte')

    // The scroll content (nav, header, editor) must sit inside a single
    // wrapper that establishes the stacking lift (relative z-[1]) so the
    // texture overlay paints below the content without trapping each child
    // in its own stacking context.
    expect(svelte).toMatch(/relative z-\[1\][^"]*flex flex-col/)
  })

  it('texture ::before uses viewport units for height/margin (not %)', () => {
    const css = readFile('index.css')

    // CSS spec resolves margin-bottom percentages against the container's
    // WIDTH, not height. So "height:100%; margin-bottom:-100%" over-cancels
    // on wide containers, pulling the content wrapper above the visible area
    // and hiding all text. Viewport units (vh) are real lengths that cancel
    // exactly regardless of aspect ratio.
    // Check the actual property declarations (not the explanatory comment).
    expect(css).toMatch(/^\s+height:\s*100vh;/m)
    expect(css).toMatch(/^\s+margin-bottom:\s*-100vh;/m)
  })
})
