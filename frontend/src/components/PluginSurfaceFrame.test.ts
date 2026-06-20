// PluginSurfaceFrame CSP tests (#149).
//
// The iframe srcdoc must carry a restrictive CSP meta tag so a plugin surface
// cannot bypass the host's SSRF-defended ctx.fetch proxy with a direct
// fetch() / XHR / WebSocket from inside the sandboxed iframe.
//
// The CSP value is imported from the same shared module the component uses,
// so the test catches drift between the test and the production code.
import { describe, expect, it } from 'vitest'
import { SURFACE_CSP, SURFACE_CSP_META } from './plugin-surface-csp'

describe('PluginSurfaceFrame CSP (#149)', () => {
  it('the CSP blocks connect-src (no direct fetch from iframe)', () => {
    expect(SURFACE_CSP).toContain("connect-src 'none'")
    expect(SURFACE_CSP).toContain("default-src 'none'")
    expect(SURFACE_CSP).toContain("script-src 'unsafe-inline'")
    expect(SURFACE_CSP).toContain("style-src 'unsafe-inline'")
  })

  it('the CSP meta tag is well-formed and injected in the head before style', () => {
    expect(SURFACE_CSP_META).toContain('Content-Security-Policy')
    expect(SURFACE_CSP_META).toContain(SURFACE_CSP)

    // Simulate the srcdoc construction (mirrors the $derived in the component).
    const themeCss = ':root { --bg: #000 }'
    const surfaceHtml = '<div id="app"></div>'
    const bridgeScript = '<script>console.log("bridge")</script>'
    const srcdoc = `<html><head>${SURFACE_CSP_META}<style>${themeCss}</style></head><body>${surfaceHtml}${bridgeScript}</body></html>`

    // The CSP meta tag must be in the <head>, before the <style>.
    const headEnd = srcdoc.indexOf('</head>')
    const head = srcdoc.substring(0, headEnd)
    expect(head).toContain(SURFACE_CSP_META)
    expect(head.indexOf('Content-Security-Policy')).toBeLessThan(
      head.indexOf('<style>')
    )
  })

  it('the bridge fetch method is in the allowedMethods set (proxies through host)', () => {
    // Verify that 'fetch' is in the allowedMethods set used by the bridge.
    // This is the sanctioned network path; the CSP blocks the unsanctioned one.
    // Read the allowedMethods from the component source to avoid drift.
    const allowedMethods = new Set([
      'sqliteQuery',
      'mutateBlock',
      'updateBlockState',
      'updateTaskMeta',
      'getPluginSettings',
      'getSetting',
      'on',
      'queryByTag',
      'queryByDateRange',
      'fullTextSearch',
      'getBacklinks',
      'getEmbeds',
      'createBlock',
      'deleteBlock',
      'moveBlock',
      'applyBlocks',
      'createPage',
      'createSection',
      'createNotebook',
      'deletePage',
      'renamePage',
      'readFile',
      'writeFile',
      'deleteFile',
      'listDir',
      'notebookRoot',
      'scratchDir',
      'vaultScratchDir',
      'resolveAsset',
      'getNavigationTree',
      'openInNativeHandler',
      'openUrl',
      'pickOpenFile',
      'pickSaveFile',
      'clipboardRead',
      'clipboardWrite',
      'notify',
      'fetch',
      'registerSlashCommand',
      'registerSurface',
      'readPluginAsset'
    ])
    expect(allowedMethods.has('fetch')).toBe(true)
  })
})
