<script lang="ts">
  // PluginNoteBanners — host for the 'note-banner' surface kind (#215).
  // Renders registered banners at the top of the note view, above the TipTap
  // editor content, in registration order. Mirrors FormattingFirstRunTip's
  // theming + dismissal UX (role="status", aria-live, accessible close).
  // Third-party banners render via PluginSurfaceFrame (sandboxed iframe).
  import { onDestroy } from 'svelte'
  import {
    getSurfaces,
    onSurfacesChanged,
    unregisterSurface,
    type PluginSurface
  } from '../../plugins/surfaces'
  import PluginSurfaceFrame from '../PluginSurfaceFrame.svelte'
  import { makePluginContext } from '../../plugins/context'

  let surfaces = $state<PluginSurface[]>(getSurfaces('note-banner'))

  // Cache contexts per pluginID so a surfaces-list change doesn't rebuild
  // the context for every banner on every render (avoids needless iframe
  // srcdoc rebuilds in PluginSurfaceFrame). Invalidated for pluginIDs that
  // leave the surfaces list (disable/enable issues a fresh session token).
  const ctxCache = new Map<string, any>()

  // host→iframe post closures per surface.id, handed back by each
  // PluginSurfaceFrame via onBridgeReady (#355). Used to notify a plugin its
  // banner was dismissed so it can persist dismissal state
  // (ctx.updatePluginSetting('dismissed_notes', [...])) BEFORE the surface is
  // torn down. Entries are dropped when a surface leaves the list.
  const postFns = new Map<string, (msg: any) => void>()

  const off = onSurfacesChanged((all) => {
    surfaces = all.filter((s) => s.kind === 'note-banner')
    // Evict cached contexts + post closures for surfaces no longer present —
    // their session tokens are revoked on teardown, so a stale ctx would fail
    // server-side, and a stale post closure would target a torn-down iframe.
    const activeIDs = new Set(surfaces.map((s) => s.id))
    for (const id of [...postFns.keys()]) {
      if (!activeIDs.has(id)) postFns.delete(id)
    }
    const activePluginIDs = new Set(surfaces.map((s) => s.pluginID))
    for (const id of ctxCache.keys()) {
      if (!activePluginIDs.has(id)) ctxCache.delete(id)
    }
  })

  onDestroy(() => off())

  function ctxFor(pluginID: string): any {
    let ctx = ctxCache.get(pluginID)
    if (!ctx) {
      ctx = makePluginContext(pluginID) as any
      ctxCache.set(pluginID, ctx)
    }
    return ctx
  }

  // Dismiss a banner. Before removing the surface we send a host→iframe
  // 'dismiss' event (#355) so the plugin can persist its dismissal state
  // (recommended: ctx.updatePluginSetting('dismissed_notes', [...])).
  // updatePluginSetting is now in the surface bridge's allowedMethods, so the
  // documented pattern is finally reachable. `persistent` is false for the
  // default close ("Dismiss for now"); a plugin may treat the event however it
  // likes (the protocol carries the flag for future "Don't show again" UI).
  //
  // A 400ms timeout fallback guarantees the surface is removed even if a
  // plugin's dismiss handler hangs — no banner can wedge the host.
  //
  // Focus management (#215 a11y): the close button lives inside the banner, so
  // removing the banner destroys the focused element. Before removal, move
  // focus to the next banner's close button (or, if none, to the container so
  // focus doesn't fall to <body>).
  const DISMISS_TIMEOUT_MS = 400
  let dismissedThisTick: string | null = null

  function dismiss(surface: PluginSurface, closeBtn: HTMLButtonElement) {
    if (dismissedThisTick === surface.id) return // idempotent on double-click
    dismissedThisTick = surface.id

    // Signal the plugin first (host→iframe), then tear down after a grace
    // window so its updatePluginSetting call can land before the iframe is gone.
    // The notify is best-effort: if the post throws (e.g. the iframe is already
    // gone, or an environment quirk), dismissal MUST still proceed — the host
    // never wedges on an unresponsive/unreachable plugin (#355 fallback).
    const post = postFns.get(surface.id)
    try {
      post?.({
        __siltSurface: 'event',
        type: 'dismiss',
        payload: { surfaceId: surface.id, persistent: false }
      })
    } catch {
      /* best-effort notify — teardown below is the guarantee */
    }

    const doRemove = () => {
      // Reset the debounce guard so a plugin re-enabled and re-registered with
      // the same surface.id can be dismissed again (the guard is only meant to
      // debounce a single click during the grace window).
      if (dismissedThisTick === surface.id) dismissedThisTick = null
      const idx = surfaces.findIndex((s) => s.id === surface.id)
      const next = surfaces[idx + 1]
      unregisterSurface(surface.id)
      // Defer so the DOM updates before we focus.
      queueMicrotask(() => {
        if (next) {
          const nextBtn = document.querySelector<HTMLButtonElement>(
            `[data-banner-close="${next.id}"]`
          )
          nextBtn?.focus()
        } else {
          // No more banners — return focus to the container (Tab will move
          // into the editor on the next press).
          containerEl?.focus()
        }
      })
    }

    // Give the plugin a chance to persist, but never hang the host.
    window.setTimeout(doRemove, DISMISS_TIMEOUT_MS)
  }

  let containerEl: HTMLDivElement | null = $state(null)

  // Collapse affordance (#358): when more than 2 banners stack, the default
  // collapses them into a single summary to avoid pushing the editor down.
  // The user expands to see all; dismissing one while expanded drops back
  // below the threshold automatically.
  const COLLAPSE_THRESHOLD = 2
  let collapsed = $state(true)
  let showCollapse = $derived(surfaces.length > COLLAPSE_THRESHOLD)
  // Visible banners: none while collapsed (the summary takes their place);
  // all of them when expanded or when under the threshold.
  let visibleSurfaces = $derived(showCollapse && collapsed ? [] : surfaces)
</script>

{#if surfaces.length > 0}
  <!-- Stacking: predictable order (registration order), max-height + overflow
       so several banners coexist without pushing the editor out of view. The
       custom-scrollbar class styles the overflow per the app convention. -->
  <div
    bind:this={containerEl}
    class="plugin-note-banners custom-scrollbar"
    role="region"
    aria-label="Plugin banners"
    tabindex="-1"
  >
    {#if showCollapse}
      <button
        type="button"
        class="banner-collapse-toggle"
        aria-expanded={!collapsed}
        aria-controls="banner-stack"
        onclick={() => (collapsed = !collapsed)}
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >{collapsed ? 'expand' : 'compress'}</span
        >
        {surfaces.length} plugin {surfaces.length === 1 ? 'banner' : 'banners'} —
        {collapsed ? 'show' : 'hide'}
      </button>
    {/if}

    {#if showCollapse && collapsed}
      <!-- Collapsed state removes the per-banner role=status live regions, so a
           screen reader would not learn when a new banner arrives (the toggle
           button text change is not itself announced). This visually-hidden
           polite region announces the count + the latest label so arrivals and
           departures are spoken without a visible affordance. -->
      <div class="sr-only" aria-live="polite" aria-atomic="true">
        {surfaces.length} plugin {surfaces.length === 1 ? 'banner' : 'banners'} active.
        Latest: {surfaces[surfaces.length - 1]?.label ?? 'unknown'}.
      </div>
    {/if}

    <div id="banner-stack" class="banner-stack">
      {#each visibleSurfaces as surface (surface.id)}
        <div
          class="note-banner"
          role="status"
          aria-live="polite"
          aria-label={surface.label}
        >
          <span class="material-symbols-outlined banner-icon" aria-hidden="true"
            >{surface.icon || 'campaign'}</span
          >
          <div class="banner-frame-wrapper">
            <PluginSurfaceFrame
              {surface}
              ctxProxy={ctxFor(surface.pluginID)}
              onBridgeReady={(post) => postFns.set(surface.id, post)}
            />
          </div>
          <button
            type="button"
            class="banner-dismiss"
            data-banner-close={surface.id}
            onclick={(e) => dismiss(surface, e.currentTarget)}
            aria-label="Dismiss {surface.label}"
            title="Dismiss {surface.label}"
          >
            <span class="material-symbols-outlined" aria-hidden="true"
              >close</span
            >
          </button>
        </div>
      {/each}
    </div>
  </div>
{/if}

<style>
  /* Visually hidden but available to assistive tech (the collapsed-stack live
     region). Standard visually-hidden pattern; not globalized because no other
     component needs it yet. */
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  .plugin-note-banners {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 4px;
    max-height: 30vh;
    overflow-y: auto;
  }

  .banner-stack {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  /* Banner chrome theming is aligned with FormattingFirstRunTip (12% / 30%
     accent-glow mixes) so dismissible highlight regions share a look. The
     ratios live here as the single source for the note-banner variant. */
  .note-banner {
    display: flex;
    align-items: stretch;
    gap: 6px;
    padding: 6px 10px;
    border-radius: 8px;
    background: color-mix(
      in srgb,
      var(--color-accent-primary-glow, #6fa3ff) 12%,
      var(--color-surface, #1a1d24)
    );
    border: 1px solid
      color-mix(
        in srgb,
        var(--color-accent-primary-glow, #6fa3ff) 30%,
        transparent
      );
  }

  .banner-collapse-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    border-radius: 8px;
    border: 1px solid
      color-mix(
        in srgb,
        var(--color-accent-primary-glow, #6fa3ff) 30%,
        transparent
      );
    background: color-mix(
      in srgb,
      var(--color-accent-primary-glow, #6fa3ff) 12%,
      var(--color-surface, #1a1d24)
    );
    color: var(--color-text-primary, #e6e6e6);
    font-size: 12px;
    cursor: pointer;
    transition:
      background 0.1s,
      color 0.1s;
  }

  .banner-collapse-toggle:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-glow, #6fa3ff) 18%,
      var(--color-surface, #1a1d24)
    );
  }

  .banner-collapse-toggle .material-symbols-outlined {
    font-size: 16px;
  }

  .banner-icon {
    font-size: 18px;
    color: var(--color-accent-primary-glow, #6fa3ff);
    flex-shrink: 0;
    align-self: flex-start;
    margin-top: 2px;
  }

  .banner-frame-wrapper {
    flex: 1;
    min-width: 0;
    /* The iframe content is sandboxed; constrain its height so it doesn't
       blow out the banner's compact layout. Truncated banner text is
       scrollable (hidden auto) rather than silently clipped (#358). */
    max-height: 120px;
    overflow: hidden auto;
    border-radius: 4px;
  }

  .banner-dismiss {
    flex-shrink: 0;
    align-self: flex-start;
    margin-top: 2px;
    padding: 2px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    cursor: pointer;
    transition:
      background 0.1s,
      color 0.1s;
    line-height: 0;
  }

  .banner-dismiss:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
    color: var(--color-text-primary, #e6e6e6);
  }

  .banner-dismiss .material-symbols-outlined {
    font-size: 18px;
  }
</style>
