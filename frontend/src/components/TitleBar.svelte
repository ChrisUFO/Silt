<script lang="ts">
  import { onMount } from 'svelte'
  import logo from '../assets/logo.svg'
  import {
    WindowMinimise,
    WindowToggleMaximise,
    WindowIsMaximised,
    Quit
  } from '../../wailsjs/runtime/runtime.js'

  interface Props {
    sidebarCollapsed: boolean
    sidebarWidth?: number
    onSearchClick: () => void
    children?: import('svelte').Snippet
  }

  let {
    sidebarCollapsed = $bindable(),
    sidebarWidth = 256,
    onSearchClick,
    children
  }: Props = $props()

  let maximised = $state(false)

  // Platform detection (#61): on macOS, Wails auto-injects the native
  // traffic-light buttons, so we hide our in-app controls and reserve a
  // left inset for them. Detection via navigator.userAgent is safe (the
  // guard only activates on Mac; detection failure → show controls).
  let isMac = $state(false)

  async function syncMaximised() {
    try {
      maximised = await WindowIsMaximised()
    } catch {
      // runtime not ready (e.g. during SSR/check); leave as-is
    }
  }

  onMount(() => {
    syncMaximised()
    isMac = /mac/i.test(navigator.platform || navigator.userAgent)
    // Maximize/restore triggers a viewport resize; re-sync the icon then.
    const onResize = () => {
      syncMaximised()
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  })

  // macOS reserves 80px for traffic lights (collapsed or not); other platforms
  // use 48px. Hoisted so the inner ternary isn't evaluated twice.
  let trafficPx = $derived(isMac ? 80 : 48)
  let brandZoneWidth = $derived(
    sidebarCollapsed ? trafficPx : trafficPx + sidebarWidth
  )

  function handleToggleMax() {
    WindowToggleMaximise()
    // Optimistic flip; resize listener will correct it if the platform
    // refuses the toggle.
    maximised = !maximised
  }
</script>

<header
  class="drag-region bg-void flex justify-between items-center h-14 w-full z-50 fixed top-0 border-b border-border-muted select-none"
>
  <!-- Left: brand zone (matches sidebar width) + sidebar toggle at the boundary -->
  <div class="flex items-center min-w-0 h-full flex-grow">
    <!-- Brand strip aligns over the sidebar; collapses when sidebar does -->
    <div
      class="flex items-center gap-2 h-full flex-shrink-0 transition-all duration-200 ease-out overflow-hidden"
      style:width={brandZoneWidth + 'px'}
      style:padding-left={isMac && !sidebarCollapsed ? '80px' : undefined}
      class:px-4={!sidebarCollapsed && !isMac}
      class:px-3={sidebarCollapsed && !isMac}
    >
      {#if !isMac || !sidebarCollapsed}
        <div
          class="relative logo-container flex items-center gap-2 group cursor-pointer"
          class:justify-center={sidebarCollapsed}
          class:w-full={sidebarCollapsed}
        >
          <div
            class="relative logo-shimmer flex-shrink-0 w-6 h-6 rounded-md overflow-hidden"
          >
            <img
              src={logo}
              alt="Silt"
              class="w-full h-full logo-img transition-all duration-300"
            />
            <div
              class="absolute inset-0 logo-shimmer-sweep pointer-events-none"
            ></div>
          </div>
          {#if !sidebarCollapsed}
            <span
              class="font-headline-md text-headline-md text-text-primary font-bold tracking-tight whitespace-nowrap group-hover:text-accent-primary-start transition-colors duration-300"
              >Silt</span
            >
            {#if isMac}
              <!-- Add a tiny dot next to the wordmark on macOS to fill space if needed, or leave it clean -->
            {/if}
          {/if}
        </div>
      {/if}
    </div>

    {#if children}
      <div class="h-full flex items-end flex-grow min-w-0">
        {@render children()}
      </div>
    {/if}
  </div>

  <!-- Right: search + window controls -->
  <div class="flex items-center gap-2 flex-shrink-0 h-full pr-2">
    <button
      onclick={onSearchClick}
      aria-label="Search"
      title="Search (Ctrl+Shift+F)"
      class="flex items-center justify-center h-9 w-9 rounded-lg text-text-muted hover:text-text-primary hover:bg-hover transition-colors cursor-pointer border-none bg-transparent focus:outline-none"
    >
      <span class="material-symbols-outlined text-[20px]">search</span>
    </button>

    <div class="w-px h-6 bg-border-muted mx-1"></div>

    <!-- Window controls (hidden on macOS — Wails injects native traffic lights) -->
    {#if !isMac}
      <div class="flex items-center h-full">
        <button
          onclick={() => WindowMinimise()}
          aria-label="Minimize"
          title="Minimize"
          class="h-full w-11 flex items-center justify-center text-text-muted hover:text-text-primary hover:bg-hover transition-colors border-none bg-transparent cursor-pointer focus:outline-none"
        >
          <span class="material-symbols-outlined text-[18px]">remove</span>
        </button>
        <button
          onclick={handleToggleMax}
          aria-label={maximised ? 'Restore' : 'Maximize'}
          title={maximised ? 'Restore' : 'Maximize'}
          class="h-full w-11 flex items-center justify-center text-text-muted hover:text-text-primary hover:bg-hover transition-colors border-none bg-transparent cursor-pointer focus:outline-none"
        >
          <span class="material-symbols-outlined text-[18px]"
            >{maximised ? 'fullscreen_exit' : 'crop_square'}</span
          >
        </button>
        <button
          onclick={() => Quit()}
          aria-label="Close"
          title="Close"
          class="h-full w-11 flex items-center justify-center text-text-muted hover:bg-error hover:text-white transition-colors border-none bg-transparent cursor-pointer focus:outline-none"
        >
          <span class="material-symbols-outlined text-[18px]">close</span>
        </button>
      </div>
    {/if}
  </div>
</header>

<style>
  .drag-region {
    --wails-draggable: drag;
  }
  /* Interactive children stay clickable while empty header space drags the window. */
  .drag-region :global(button),
  .drag-region :global(nav),
  .drag-region :global(input),
  .drag-region :global(a) {
    --wails-draggable: no-drag;
  }

  .logo-container:hover .logo-img {
    filter: drop-shadow(0 0 6px var(--color-accent-primary-start))
      brightness(1.1);
    transform: scale(1.05);
  }

  .logo-shimmer-sweep {
    background: linear-gradient(
      90deg,
      transparent,
      rgba(255, 255, 255, 0.25),
      transparent
    );
    left: -150%;
    width: 50%;
    height: 100%;
    transform: skewX(-20deg);
    transition: none;
  }

  .logo-container:hover .logo-shimmer-sweep {
    animation: logo-sweep 1.2s cubic-bezier(0.16, 1, 0.3, 1);
  }

  @keyframes logo-sweep {
    0% {
      left: -150%;
    }
    100% {
      left: 150%;
    }
  }
</style>
