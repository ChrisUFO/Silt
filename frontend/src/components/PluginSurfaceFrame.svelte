<script lang="ts">
  // Sandboxed iframe renderer for a plugin UI surface (#117). The plugin's HTML
  // runs inside <iframe srcdoc> with the `sandbox` attribute (allow-scripts but
  // NOT allow-same-origin, so the iframe cannot touch the parent DOM or cookies).
  // A postMessage bridge proxies PluginContext calls back to the host.
  //
  // Theme tokens are injected as CSS custom properties on the iframe's :root so
  // third-party UI matches the active theme/dark-mode.

  import { themeState } from '../theme/store.svelte'
  import { onMount, onDestroy } from 'svelte'
  import type { PluginSurface } from '../plugins/surfaces'
  import { SURFACE_CSP_META } from './plugin-surface-csp'

  interface Props {
    surface: PluginSurface
    /** A context proxy the bridge calls into (the live PluginContext). */
    ctxProxy?: Record<string, (...args: any[]) => any>
    /**
     * Called once on mount with a `postToSurface` closure the parent can use
     * to push host→iframe messages into the sandboxed surface (#355). The
     * bridge is otherwise one-directional (iframe→host requests only); this
     * opens the symmetric host→iframe channel for events like banner dismiss.
     * The closure is invalidated on destroy (becomes a no-op) so a stale
     * parent ref cannot post into a torn-down iframe.
     */
    onBridgeReady?: (postToSurface: (msg: SurfaceHostMessage) => void) => void
  }
  let { surface, ctxProxy = {}, onBridgeReady }: Props = $props()

  /** A host→iframe event envelope (the dismiss bridge, #355). */
  interface SurfaceHostMessage {
    __siltSurface: 'event'
    type: string
    payload?: unknown
  }

  let iframeEl: HTMLIFrameElement | undefined = $state()
  let alive = true

  // Build the srcdoc: the plugin's HTML + a small bridge script that listens
  // for requests, posts them to the parent, and relays the response. This is
  // the "one SDK, two transports" pattern (#117): the same PluginContext
  // contract, proxied over postMessage instead of direct closure calls.
  const bridgeScript = `
    <script>
      const pending = new Map();
      let seq = 0;
      window.addEventListener('message', (ev) => {
        const msg = ev.data;
        if (!msg || msg.__siltSurface !== 'response' && msg.__siltSurface !== 'event') return;
        if (msg.__siltSurface === 'response') {
          const resolver = pending.get(msg.seq);
          if (resolver) {
            resolver(msg.ok ? msg.result : Promise.reject(new Error(msg.error)));
            pending.delete(msg.seq);
          }
          return;
        }
        // Host→iframe event (#355): e.g. a banner dismiss notification so the
        // plugin can persist dismissal state (ctx.updatePluginSetting) before
        // the surface is torn down. Dispatched as a CustomEvent plugin HTML
        // subscribes to (silt:surface:event). Origin/source gating is the host
        // side's job — the iframe trusts its parent by construction.
        window.dispatchEvent(new CustomEvent('silt:surface:event', {
          detail: { type: msg.type, payload: msg.payload }
        }));
      });
      // PluginContext proxy: every method becomes a postMessage request.
      // targetOrigin '*' here is intentional: the sandboxed iframe cannot read
      // parent's origin (cross-origin), so it cannot pin the targetOrigin.
      // The parent's inbound gate (ev.source === iframeEl.contentWindow +
      // ev.origin === 'null') provides the security check; the response
      // direction uses 'null' (see handleRequest below, #248).
      const ctx = new Proxy({}, {
        get(_, method) {
          return (...args) => new Promise((resolve, reject) => {
            const s = ++seq;
            pending.set(s, resolve);
            parent.postMessage({ __siltSurface: 'request', seq: s, method: String(method), args }, '*');
          });
        }
      });
      window.__siltCtx = ctx;
      // Notify the plugin the context is ready.
      window.dispatchEvent(new CustomEvent('silt:ready', { detail: ctx }));
    <\/script>
  `

  // Theme tokens injected as CSS custom properties so the surface matches the
  // active theme. The $derived srcdoc below reads themeState reactively, so a
  // theme/mode change re-renders the iframe with fresh tokens.
  function themeCss(): string {
    const mode = themeState.mode === 'light' ? 'lightTokens' : 'darkTokens'
    const tokens = (themeState[mode] ?? {}) as Record<string, string>
    const decls = Object.entries(tokens)
      .map(([k, v]) => `${k}: ${v};`)
      .join(' ')
    return `:root { ${decls} } body { margin: 0; font-family: var(--font-body, system-ui, sans-serif); color: var(--color-text-primary, #e4e4e7); background: var(--color-panel, #161619); }`
  }

  // CSP meta tag from the shared constant (plugin-surface-csp.ts). See the
  // constant for the full directive rationale (#149).
  const cspMeta = SURFACE_CSP_META

  const srcdoc = $derived(
    `<html><head>${cspMeta}<style>${themeCss()}</style></head><body>${surface.html}${bridgeScript}</body></html>`
  )

  // Explicit allowlist of proxiable PluginContext method names. Anything not
  // in this set is rejected, so a future non-gated host-internal function can
  // never be invoked by a plugin surface (#117 hardening).
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

  function handleRequest(ev: MessageEvent) {
    const msg = ev.data
    if (!msg || msg.__siltSurface !== 'request') return
    // Only handle requests from our iframe. The sandbox="allow-scripts"
    // (without allow-same-origin) makes the iframe report 'null' as its
    // origin. Check both the source window and the origin for defense-in-
    // depth: a future refactor to a real src URL would widen the origin.
    // Fail-closed: if the iframe ref is unset (mount/teardown window) or
    // the source doesn't match, reject the message.
    if (!iframeEl || ev.source !== iframeEl.contentWindow) return
    if (ev.origin !== 'null' && ev.origin !== window.location.origin) return

    // targetOrigin 'null' is the literal origin string a sandboxed iframe
    // (allow-scripts WITHOUT allow-same-origin) reports. The actual security
    // gate is the `ev.source === iframeEl.contentWindow` check at the top of
    // handleRequest — the response is targeted at a specific contentWindow
    // reference, never broadcast. Pinning targetOrigin to 'null' (instead of
    // '*') is defense in depth: a careless future refactor that swapped the
    // contentWindow ref for a window lookup could not leak the response to an
    // unrelated frame. If surfaces ever move to a real src URL with
    // allow-same-origin, targetOrigin MUST be updated to the iframe's actual
    // origin — this comment makes that requirement explicit (#248).
    if (
      !allowedMethods.has(msg.method) ||
      typeof ctxProxy[msg.method] !== 'function'
    ) {
      iframeEl?.contentWindow?.postMessage(
        {
          __siltSurface: 'response',
          seq: msg.seq,
          ok: false,
          error: `Blocked or unknown method: ${msg.method}`
        },
        'null'
      )
      return
    }
    const method = ctxProxy[msg.method]
    Promise.resolve()
      .then(() => method(...(msg.args ?? [])))
      .then((result) => {
        iframeEl?.contentWindow?.postMessage(
          { __siltSurface: 'response', seq: msg.seq, ok: true, result },
          'null'
        )
      })
      .catch((err) => {
        iframeEl?.contentWindow?.postMessage(
          {
            __siltSurface: 'response',
            seq: msg.seq,
            ok: false,
            error: err instanceof Error ? err.message : String(err)
          },
          'null'
        )
      })
  }

  function postToSurface(msg: SurfaceHostMessage) {
    if (!alive) return // torn down — ignore stale parent ref (#355)
    // targetOrigin 'null': same rationale as the response path (a sandboxed
    // iframe without allow-same-origin reports 'null' as its origin).
    iframeEl?.contentWindow?.postMessage(msg, 'null')
  }

  onMount(() => {
    window.addEventListener('message', handleRequest)
    // Hand the parent a closure it can use to push host→iframe events (#355).
    onBridgeReady?.(postToSurface)
  })
  onDestroy(() => {
    alive = false
    window.removeEventListener('message', handleRequest)
  })
</script>

<iframe
  bind:this={iframeEl}
  title={surface.label}
  {srcdoc}
  sandbox="allow-scripts"
  class="w-full h-full border-none bg-transparent"
  loading="lazy"
></iframe>
