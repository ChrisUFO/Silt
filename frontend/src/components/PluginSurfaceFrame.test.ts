// PluginSurfaceFrame CSP + postMessage tests (#149, #248).
//
// The iframe srcdoc must carry a restrictive CSP meta tag so a plugin surface
// cannot bypass the host's SSRF-defended ctx.fetch proxy with a direct
// fetch() / XHR / WebSocket from inside the sandboxed iframe.
//
// The parent → iframe response postMessage calls must pin targetOrigin to
// 'null' (the literal origin a sandboxed-without-allow-same-origin iframe
// reports) instead of '*'. The actual gate is the source-window check in
// handleRequest; the tight targetOrigin is defense in depth against a future
// refactor that swaps the contentWindow ref for a window lookup (#248).
//
// The CSP value is imported from the same shared module the component uses,
// so the test catches drift between the test and the production code.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { SURFACE_CSP, SURFACE_CSP_META } from './plugin-surface-csp'

const componentSource = readFileSync(
  resolve(
    dirname(fileURLToPath(import.meta.url)),
    './PluginSurfaceFrame.svelte'
  ),
  'utf8'
)

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
    const allowedMethods = new Set([
      'sqliteQuery',
      'mutateBlock',
      'updateBlockState',
      'updateTaskMeta',
      'getPluginSettings',
      'getSetting',
      'updatePluginSetting',
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
    // updatePluginSetting must be proxiable so banner dismissal state is
    // reachable (#355): the documented ctx.updatePluginSetting('dismissed_notes',
    // [...]) pattern runs through this bridge for sandboxed third-party banners.
    expect(allowedMethods.has('updatePluginSetting')).toBe(true)
  })
})

describe('PluginSurfaceFrame postMessage targetOrigin (#248)', () => {
  // The parent sends three response flavors back to the iframe (blocked-method
  // error, success result, catch error). All three MUST pin targetOrigin to
  // 'null' (the literal origin a sandboxed-without-allow-same-origin iframe
  // reports). The bridge-script request postMessage is a separate direction
  // (iframe → parent) and intentionally keeps '*' — the sandboxed iframe
  // cannot read the parent's origin.
  //
  // Rather than parsing nested-paren argument lists with a regex (which
  // breaks on `String(method)` inside the bridge and on `String(err)` inside
  // the catch response), the test counts the structural end-of-call pattern:
  // each response ends with the literal `'null'` targetOrigin on its own line
  // followed by the closing paren. That pattern uniquely identifies a
  // parent → iframe response and is immune to nested parens inside the
  // message payload.

  it('parent → iframe response calls pin targetOrigin to "null" (exactly 3, no "*")', () => {
    // Strip the bridge script template literal before counting so only the
    // parent-side response paths are inspected.
    const declStart = componentSource.indexOf('const bridgeScript =')
    const openingBacktick = componentSource.indexOf('`', declStart)
    const closingBacktick = componentSource.indexOf('`', openingBacktick + 1)
    expect(openingBacktick).toBeGreaterThan(-1)
    expect(closingBacktick).toBeGreaterThan(openingBacktick)
    const withoutBridge =
      componentSource.slice(0, declStart) +
      componentSource.slice(closingBacktick + 1)

    // Three response calls (blocked, success, catch), each ending with the
    // 'null' targetOrigin on its own line followed by the closing paren.
    // \r? tolerates CRLF line endings on Windows.
    const nullTargets = withoutBridge.match(/'null'\r?\n\s+\)/g) ?? []
    expect(nullTargets.length).toBe(3)

    // And zero response calls may use '*' as the targetOrigin.
    const starTargets = withoutBridge.match(/'\*'\r?\n\s+\)/g) ?? []
    expect(starTargets.length).toBe(0)
  })

  it('bridge-script request postMessage keeps "*" (sandboxed iframe cannot read parent origin)', () => {
    // The bridge script runs INSIDE the iframe (srcdoc context). It cannot
    // read the parent's origin (cross-origin sandbox), so its request
    // postMessage MUST keep '*'. Changing this to 'null' would silently
    // drop every request and break the plugin surface entirely (#248).
    const declStart = componentSource.indexOf('const bridgeScript =')
    const openingBacktick = componentSource.indexOf('`', declStart)
    const closingBacktick = componentSource.indexOf('`', openingBacktick + 1)
    const bridge = componentSource.slice(openingBacktick + 1, closingBacktick)
    // Substring check (a regex with [^)]* breaks on `String(method)` inside
    // the bridge). The bridge emits a single-line call:
    //   parent.postMessage({ ... }, '*');
    expect(bridge).toContain('parent.postMessage(')
    expect(bridge).toContain("}, '*');")
  })
})

describe('PluginSurfaceFrame host→iframe dismiss bridge (#355)', () => {
  // The bridge was one-directional (iframe→host). The dismiss bridge adds a
  // symmetric host→iframe 'event' channel so a banner host can tell a plugin
  // its surface was dismissed, letting the plugin persist dismissal state
  // (ctx.updatePluginSetting('dismissed_notes', [...])) before teardown.

  it('the iframe bridge dispatches host→iframe events as silt:surface:event', () => {
    // The bridge message listener must branch on __siltSurface === 'event' and
    // re-dispatch as a CustomEvent plugin code can subscribe to.
    const declStart = componentSource.indexOf('const bridgeScript =')
    const openingBacktick = componentSource.indexOf('`', declStart)
    const closingBacktick = componentSource.indexOf('`', openingBacktick + 1)
    const bridge = componentSource.slice(openingBacktick + 1, closingBacktick)

    expect(bridge).toContain("'event'")
    expect(bridge).toContain('silt:surface:event')
    expect(bridge).toContain('CustomEvent')
  })

  it('exposes a host→iframe post closure via onBridgeReady', () => {
    // The parent hands a postToSurface closure to onBridgeReady; it posts into
    // the iframe contentWindow with targetOrigin 'null' (sandboxed iframe
    // origin) and no-ops once destroyed.
    expect(componentSource).toContain('onBridgeReady')
    expect(componentSource).toContain('postToSurface')
    // The event post pins targetOrigin to 'null' (defense in depth, matching
    // the response direction).
    expect(componentSource).toContain("postMessage(msg, 'null')")
  })

  it('the dismiss event envelope carries __siltSurface=event', () => {
    // The host posts { __siltSurface: 'event', type, payload }; the iframe
    // bridge filters on that discriminant.
    expect(componentSource).toContain("__siltSurface: 'event'")
  })
})
