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

  interface Props {
    surface: PluginSurface
    /** A context proxy the bridge calls into (the live PluginContext). */
    ctxProxy?: Record<string, (...args: any[]) => any>
  }
  let { surface, ctxProxy = {} }: Props = $props()

  let iframeEl: HTMLIFrameElement | undefined = $state()

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
        if (!msg || msg.__siltSurface !== 'response') return;
        const resolver = pending.get(msg.seq);
        if (resolver) {
          resolver(msg.ok ? msg.result : Promise.reject(new Error(msg.error)));
          pending.delete(msg.seq);
        }
      });
      // PluginContext proxy: every method becomes a postMessage request.
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
    return `:root { ${decls} } body { margin: 0; font-family: var(--font-body, system-ui, sans-serif); color: var(--text-primary, #e4e4e7); background: var(--bg-panel, #161619); }`
  }

  const srcdoc = $derived(
    `<html><head><style>${themeCss()}</style></head><body>${surface.html}${bridgeScript}</body></html>`
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
    'on',
    'queryByTag',
    'queryByDateRange',
    'fullTextSearch',
    'getBacklinks',
    'getEmbeds',
    'createBlock',
    'deleteBlock',
    'moveBlock',
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
        '*'
      )
      return
    }
    const method = ctxProxy[msg.method]
    Promise.resolve()
      .then(() => method(...(msg.args ?? [])))
      .then((result) => {
        iframeEl?.contentWindow?.postMessage(
          { __siltSurface: 'response', seq: msg.seq, ok: true, result },
          '*'
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
          '*'
        )
      })
  }

  onMount(() => {
    window.addEventListener('message', handleRequest)
  })
  onDestroy(() => {
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
