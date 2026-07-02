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

  const off = onSurfacesChanged((all) => {
    surfaces = all.filter((s) => s.kind === 'note-banner')
    // Evict cached contexts for pluginIDs no longer present — their session
    // tokens are revoked on teardown, so a stale ctx would fail server-side.
    const activeIDs = new Set(surfaces.map((s) => s.pluginID))
    for (const id of ctxCache.keys()) {
      if (!activeIDs.has(id)) ctxCache.delete(id)
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

  // Dismiss a banner. The surface is removed from the registry immediately so
  // the banner disappears; PERSISTENT dismissal state is the plugin's
  // responsibility (recommended: updatePluginSetting('<id>', 'dismissed_notes',
  // [...])). The close button's accessible name is derived from the banner
  // label so a screen reader announces "Dismiss Summary" etc.
  //
  // Focus management (#215 a11y): the close button lives inside the banner, so
  // removing the banner destroys the focused element. Before removal, move
  // focus to the next banner's close button (or, if none, to the container so
  // focus doesn't fall to <body>).
  function dismiss(surface: PluginSurface, closeBtn: HTMLButtonElement) {
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
        // No more banners — return focus to the container (Tab will move into
        // the editor on the next press).
        containerEl?.focus()
      }
    })
  }

  let containerEl: HTMLDivElement | null = $state(null)
</script>

{#if surfaces.length > 0}
  <!-- Stacking: predictable order (registration order), max-height + overflow
       so several banners coexist without pushing the editor out of view. -->
  <div
    bind:this={containerEl}
    class="plugin-note-banners"
    role="region"
    aria-label="Plugin banners"
    tabindex="-1"
    style="max-height: 30vh; overflow-y: auto;"
  >
    {#each surfaces as surface (surface.id)}
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
          <PluginSurfaceFrame {surface} ctxProxy={ctxFor(surface.pluginID)} />
        </div>
        <button
          type="button"
          class="banner-dismiss"
          data-banner-close={surface.id}
          onclick={(e) => dismiss(surface, e.currentTarget)}
          aria-label="Dismiss {surface.label}"
          title="Dismiss {surface.label}"
        >
          <span class="material-symbols-outlined" aria-hidden="true">close</span
          >
        </button>
      </div>
    {/each}
  </div>
{/if}

<style>
  .plugin-note-banners {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 4px;
  }

  .note-banner {
    display: flex;
    align-items: stretch;
    gap: 6px;
    padding: 6px 10px;
    border-radius: 8px;
    background: color-mix(
      in srgb,
      var(--color-accent-primary-glow, #6fa3ff) 10%,
      var(--color-surface, #1a1d24)
    );
    border: 1px solid
      color-mix(
        in srgb,
        var(--color-accent-primary-glow, #6fa3ff) 25%,
        transparent
      );
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
       blow out the banner's compact layout. */
    max-height: 120px;
    overflow: hidden;
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
