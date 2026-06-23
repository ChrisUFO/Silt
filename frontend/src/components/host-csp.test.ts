// Host-webview CSP tests (#237, F2).
//
// The host webview (frontend/index.html) must carry a strict CSP so a
// malicious plugin in the main webview cannot exfiltrate vault data via
// <img>, fetch, WebSocket, etc., and so a future TipTap/@html sanitizer
// regression has a backstop. The plugin-surface iframe has its own CSP
// (plugin-surface-csp.ts); this test guards the HOST CSP.
//
// The CSP value is imported from the same shared module the index.html
// value must match exactly. The test parses index.html + asserts every
// directive so drift between the HTML and the documented policy is caught.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { HOST_CSP, HOST_CSP_META } from './host-csp'

const here = dirname(fileURLToPath(import.meta.url))
const indexHtml = readFileSync(resolve(here, '../../index.html'), 'utf8')

describe('Host-webview CSP (#237, F2)', () => {
  it('HOST_CSP covers every required directive', () => {
    // The CSP MUST cover at minimum the directives enumerated in F2.
    const requiredDirectives = [
      'default-src',
      'script-src',
      'style-src',
      'img-src',
      'font-src',
      'connect-src',
      'frame-src',
      'worker-src',
      'object-src',
      'base-uri',
      'form-action',
      'frame-ancestors'
    ]
    for (const d of requiredDirectives) {
      expect(HOST_CSP, `HOST_CSP must include ${d}`).toContain(d)
    }
  })

  it("script-src does NOT include 'unsafe-inline' or 'unsafe-eval'", () => {
    // 'unsafe-inline' in script-src MUST be avoided (F2 caveat). If the
    // build needs it, find and fix the source rather than relaxing the CSP.
    const scriptSrc = HOST_CSP.match(/script-src ([^;]+)/)?.[1] ?? ''
    expect(scriptSrc).not.toContain("'unsafe-inline'")
    expect(scriptSrc).not.toContain("'unsafe-eval'")
    // 'wasm-unsafe-eval' is tightly scoped and does NOT enable eval() of
    // JS; 'blob:' is required for plugin ESM imports (loader.ts:104).
    expect(scriptSrc).toContain("'wasm-unsafe-eval'")
    expect(scriptSrc).toContain('blob:')
    expect(scriptSrc).toContain("'self'")
  })

  it("style-src includes 'unsafe-inline' (theme/editor injectors require it)", () => {
    // The theme injector (<style id="silt-theme">) and editor-token
    // injector (<style id="silt-editor">) inject <style> elements at
    // runtime; Svelte components may also emit scoped styles inline.
    const styleSrc = HOST_CSP.match(/style-src ([^;]+)/)?.[1] ?? ''
    expect(styleSrc).toContain("'self'")
    expect(styleSrc).toContain("'unsafe-inline'")
  })

  it("connect-src is 'self' only (no Google Fonts / external endpoints)", () => {
    // Wails v2 IPC is browser-internal (window.chrome.webview.postMessage /
    // window.webkit.messageHandlers.*) and NOT subject to connect-src.
    // The /wails/ipc.js + /wails/runtime.js scripts are same-origin.
    // So 'self' is sufficient and tighter than an ipc: / ipc.localhost
    // allowlist (which would be a misconception — the IPC bridge is not
    // an HTTP fetch).
    const connectSrc = HOST_CSP.match(/connect-src ([^;]+)/)?.[1] ?? ''
    expect(connectSrc.trim()).toBe("'self'")
  })

  it("font-src is 'self' data: only (no Google Fonts CDN)", () => {
    // Fonts are bundled via @fontsource/* (woff2 self-hosted by Vite).
    // No https://fonts.gstatic.com allowlist — Silt is local-first.
    const fontSrc = HOST_CSP.match(/font-src ([^;]+)/)?.[1] ?? ''
    expect(fontSrc).toContain("'self'")
    expect(fontSrc).toContain('data:')
    expect(fontSrc).not.toContain('fonts.gstatic.com')
    expect(fontSrc).not.toContain('fonts.googleapis.com')
  })

  it("object-src, base-uri, form-action, frame-ancestors are 'none'", () => {
    expect(HOST_CSP).toMatch(/object-src 'none'/)
    expect(HOST_CSP).toMatch(/base-uri 'none'/)
    expect(HOST_CSP).toMatch(/form-action 'none'/)
    expect(HOST_CSP).toMatch(/frame-ancestors 'none'/)
  })

  it('HOST_CSP_META is well-formed', () => {
    expect(HOST_CSP_META).toContain('Content-Security-Policy')
    expect(HOST_CSP_META).toContain(HOST_CSP)
    expect(HOST_CSP_META.startsWith('<meta')).toBe(true)
  })

  it('index.html contains the CSP meta tag as the first child of <head>', () => {
    // The CSP meta MUST be the first child of <head> so it is enforced
    // before any other resource loads.
    const headOpen = indexHtml.indexOf('<head>')
    const headClose = indexHtml.indexOf('</head>')
    expect(headOpen).toBeGreaterThan(-1)
    expect(headClose).toBeGreaterThan(headOpen)
    const head = indexHtml.slice(headOpen, headClose)

    // The CSP meta is present inside <head>.
    expect(head).toContain('Content-Security-Policy')

    // Pin the literal CSP value, not just the directive name, so the
    // index.html ↔ host-csp.ts pair cannot silently drift. The header of
    // host-csp.ts advertises itself as the shared source of truth; this
    // assertion is what actually enforces that contract.
    expect(indexHtml).toContain(HOST_CSP)

    // And it is the first <meta> tag in <head> (after the charset, which
    // the HTML spec requires to be first; the CSP comes immediately after).
    const charsetIdx = head.indexOf('<meta charset')
    const cspIdx = head.indexOf('Content-Security-Policy')
    expect(charsetIdx).toBeGreaterThan(-1)
    expect(cspIdx).toBeGreaterThan(charsetIdx)

    // No other resource <link> precedes the CSP meta (the favicon data:
    // link is fine but should not precede the CSP).
    const firstLinkIdx = head.indexOf('<link')
    expect(firstLinkIdx).toBeGreaterThan(cspIdx)
  })

  it('index.html no longer references Google Fonts CDN', () => {
    // F2 contracts: Google Fonts is bundled via @fontsource, not loaded
    // from the CDN. The <link> tags MUST be gone.
    expect(indexHtml).not.toContain('fonts.googleapis.com')
    expect(indexHtml).not.toContain('fonts.gstatic.com')
  })
})
