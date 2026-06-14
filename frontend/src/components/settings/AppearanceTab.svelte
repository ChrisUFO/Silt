<script lang="ts">
  // Settings → Appearance tab (#47, #48).
  //
  // Live, accessible theme picker + dark/light/system mode toggle +
  // custom-theme import/export. Fully data-driven from themesState (the
  // listing store populated by ListThemes); zero per-theme code
  // branches. Live preview on hover/focus is implemented via a local
  // previewTokens state that overrides the active theme's tokens for
  // the duration of the hover; pressing Esc or moving focus off the row
  // restores the active theme.
  import { onMount } from 'svelte'
  import {
    OnFileDrop,
    OnFileDropOff
  } from '../../../wailsjs/runtime/runtime.js'
  import { injectTokens } from '../../theme/inject'
  import {
    applyTheme,
    clearStatus,
    exportActiveTheme,
    importThemeFromPath,
    loadThemes,
    pickAndImportTheme,
    restoreActiveTheme,
    themeState,
    themesState,
    themeStatus,
    type ThemeMode,
    type ThemeStatus
  } from '../../theme/store.svelte'

  type Props = Record<string, never>

  let {}: Props = $props()

  // Per-row preview state. When non-null, the effect at the bottom of
  // the file injects the preview theme's tokens in place of the active
  // theme's; on blur / mouseleave / Esc, the row clears it.
  let previewId: string | null = $state(null)

  // Currently-focused row index (for roving tabindex). null = no row focused.
  let focusIndex: number | null = $state(null)
  let rowRefs: HTMLButtonElement[] = $state([])

  onMount(() => {
    // Initial load: if the listing hasn't been populated by App.svelte's
    // initThemes() yet (e.g. user opened Settings before the async load
    // completed), kick off a refresh here too.
    if (themesState.items.length === 0 && !themesState.loading) {
      void loadThemes()
    }
    // Drag-drop: a *.json file dropped anywhere on the tab is imported
    // through the same code path as the picker button. The Wails
    // OnFileDrop runtime gives OS file paths (no frontend FS access),
    // satisfying the issue #48 AC.
    const onDrop = (_x: number, _y: number, paths: string[]) => {
      for (const p of paths) {
        if (p.toLowerCase().endsWith('.json')) {
          void importThemeFromPath(p)
        }
      }
    }
    OnFileDrop(onDrop, true)
    return () => {
      OnFileDropOff()
      // Clear any in-flight preview so a navigated-away tab doesn't
      // leave the page in a non-active theme.
      previewId = null
    }
  })

  // --- Mode toggle ---------------------------------------------------------

  const modes: { id: ThemeMode; label: string; icon: string }[] = [
    { id: 'dark', label: 'Dark', icon: 'dark_mode' },
    { id: 'light', label: 'Light', icon: 'light_mode' },
    { id: 'system', label: 'System', icon: 'desktop_windows' }
  ]

  function setMode(mode: ThemeMode) {
    if (mode === themeState.mode) return
    void applyTheme(themeState.id || 'cyber_forest', mode)
  }

  // --- Theme picker --------------------------------------------------------

  function isActive(t: { id: string }): boolean {
    return themeState.id === t.id
  }

  function selectTheme(id: string) {
    if (id === themeState.id) return
    void applyTheme(id, themeState.mode)
  }

  function onRowEnter(id: string) {
    previewId = id
  }
  function onRowLeave() {
    previewId = null
  }
  function onRowKey(e: KeyboardEvent, index: number) {
    const last = themesState.items.length - 1
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault()
      const next = Math.min(last, index + 1)
      focusIndex = next
      rowRefs[next]?.focus()
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault()
      const prev = Math.max(0, index - 1)
      focusIndex = prev
      rowRefs[prev]?.focus()
    } else if (e.key === 'Home') {
      e.preventDefault()
      focusIndex = 0
      rowRefs[0]?.focus()
    } else if (e.key === 'End') {
      e.preventDefault()
      focusIndex = last
      rowRefs[last]?.focus()
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      const item = themesState.items[index]
      if (item) selectTheme(item.id)
    } else if (e.key === 'Escape') {
      // Esc cancels any in-flight preview (matches the live-preview AC).
      e.preventDefault()
      previewId = null
    }
  }

  // Window-level key handler: Esc also cancels preview when focus is
  // outside the list (e.g. on the mode toggle).
  function onWindowKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && previewId !== null) {
      previewId = null
    }
  }

  // --- Live preview injection ---------------------------------------------

  /**
   * The token map to inject for the current paint frame. When a preview
   * is active, the preview theme's tokens for the current mode are
   * used; otherwise the active theme's tokens. Falls back to the
   * active theme's map if the preview theme's tokens aren't available
   * (e.g. mid-import) so the picker never blanks the page.
   */
  let previewTokens: Record<string, string> | null = $derived.by(() => {
    if (previewId === null) return null
    const ft = themesState.flatTokens[previewId]
    if (!ft) return null
    if (themeState.mode === 'light') return ft.light
    if (themeState.mode === 'system') {
      const prefersLight =
        typeof window !== 'undefined' &&
        window.matchMedia &&
        window.matchMedia('(prefers-color-scheme: light)').matches
      return prefersLight ? ft.light : ft.dark
    }
    return ft.dark
  })

  // Side-effect: when previewTokens changes (hover/unhover) or the
  // active mode flips, re-inject the right token map. The injector
  // uses a single textContent rewrite so the repaint is same-tick.
  // The else branch is critical: when a preview ends (mouseleave,
  // blur, Esc), previewTokens goes null and we MUST re-inject the
  // active theme's tokens — otherwise the page stays visually locked
  // to the last-hovered theme until the user manually clicks one.
  $effect(() => {
    if (previewTokens !== null) {
      injectTokens(previewTokens)
    } else {
      restoreActiveTheme()
    }
  })

  // --- Helpers -------------------------------------------------------------

  function handleImport() {
    void pickAndImportTheme()
  }
  function handleExport() {
    void exportActiveTheme()
  }

  function statusAriaRole(s: ThemeStatus | null): 'status' | 'alert' | null {
    if (!s || !s.message) return null
    return s.kind === 'error' ? 'alert' : 'status'
  }

  // A class string driven by theme status, used for the live region
  // styling. Kept simple — bg/border + text colour, no per-status icon
  // (the status kind is conveyed by the aria role).
  function statusClasses(s: ThemeStatus | null): string {
    if (!s || !s.message) return ''
    if (s.kind === 'error') {
      return 'bg-error/10 border border-error/30 text-error'
    }
    if (s.kind === 'success') {
      return 'bg-accent-primary-start/10 border border-accent-primary-start/30 text-accent-primary-start'
    }
    return 'bg-bg-panel border border-border-muted text-text-muted'
  }
</script>

<svelte:window on:keydown={onWindowKey} />

<div class="p-6 max-w-3xl space-y-8">
  <!-- Mode toggle -->
  <section aria-labelledby="mode-heading">
    <h3
      id="mode-heading"
      class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
    >
      Mode
    </h3>
    <div
      role="radiogroup"
      aria-label="Color mode"
      class="inline-flex bg-bg-surface border border-border-muted rounded-lg p-1 gap-1"
    >
      {#each modes as m (m.id)}
        {@const active = themeState.mode === m.id}
        <button
          type="button"
          role="radio"
          aria-checked={active}
          onclick={() => setMode(m.id)}
          class="flex items-center gap-1.5 px-3 py-1.5 rounded-md font-label-sm text-label-sm motion-reduce:transition-none transition-colors border-none cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60"
          class:bg-bg-hover={active}
          class:text-accent-primary-start={active}
          class:text-text-muted={!active}
          class:hover:text-text-primary={!active}
        >
          <span class="material-symbols-outlined text-[16px]">{m.icon}</span>
          {m.label}
        </button>
      {/each}
    </div>
    <p class="text-text-muted text-[11px] font-label-sm mt-2">
      "System" follows your OS appearance preference. Switching mode does not
      change the active theme.
    </p>
  </section>

  <!-- Theme list -->
  <section aria-labelledby="theme-heading">
    <div class="flex items-center justify-between mb-3">
      <h3
        id="theme-heading"
        class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px]"
      >
        Theme
      </h3>
      <span class="text-text-muted text-[11px] font-label-sm">
        {themesState.items.length}
        {themesState.items.length === 1 ? 'theme' : 'themes'}
      </span>
    </div>

    {#if themesState.loading && themesState.items.length === 0}
      <div
        class="text-text-muted text-[12px] font-body-md animate-pulse py-8 text-center"
      >
        Loading themes…
      </div>
    {:else if themesState.loadError}
      <div
        class="flex items-start gap-2 p-3 rounded-lg bg-error/10 border border-error/30 text-error text-[12px] font-body-md"
        role="alert"
      >
        <span class="material-symbols-outlined text-[18px]">error</span>
        <span class="flex-1"
          >Failed to load themes: {themesState.loadError}</span
        >
      </div>
    {:else if themesState.items.length === 0}
      <div
        class="text-text-muted text-[12px] font-body-md py-8 text-center"
      >
        No themes available. Import a theme .json to get started.
      </div>
    {:else}
      <div
        role="listbox"
        aria-label="Available themes"
        class="space-y-2"
      >
        {#each themesState.items as theme, i (theme.id)}
          {@const active = isActive(theme)}
          <button
            type="button"
            id={`theme-row-${theme.id}`}
            role="option"
            aria-selected={active}
            tabindex={focusIndex === i || (focusIndex === null && i === 0)
              ? 0
              : -1}
            bind:this={rowRefs[i]}
            onclick={() => selectTheme(theme.id)}
            onmouseenter={() => onRowEnter(theme.id)}
            onmouseleave={onRowLeave}
            onfocus={() => {
              focusIndex = i
              onRowEnter(theme.id)
            }}
            onblur={onRowLeave}
            onkeydown={(e) => onRowKey(e, i)}
            class="w-full text-left flex items-center gap-3 px-3 py-2.5 rounded-lg border motion-reduce:transition-none transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60 cursor-pointer"
            class:bg-bg-surface={!active}
            class:border-border-muted={!active}
            class:hover:border-border-active={!active}
            class:border-l-4={active}
            class:border-l-accent-primary-start={active}
            class:bg-accent-primary-glow={active}
            class:border-accent-primary-start={active}
          >
            <!-- Swatches: data-driven from theme.Swatches; no per-theme branching. -->
            <div class="flex items-center gap-1.5 flex-shrink-0">
              <span
                aria-hidden="true"
                class="block w-4 h-8 rounded-sm border border-border-muted"
                style="background-color: {theme.swatches?.[0] ??
                  'var(--accent-primary-start)'}"
              ></span>
              <span
                aria-hidden="true"
                class="block w-4 h-8 rounded-sm border border-border-muted"
                style="background-color: {theme.swatches?.[1] ??
                  'var(--accent-secondary-start)'}"
              ></span>
            </div>
            <div class="flex-1 min-w-0">
              <div class="flex items-center gap-2">
                <span
                  class="text-text-primary text-[13px] font-body-md truncate"
                >
                  {theme.name}
                </span>
                {#if active}
                  <span
                    class="text-[10px] text-accent-primary-start font-label-sm-bold uppercase tracking-wider flex-shrink-0"
                  >
                    Active
                  </span>
                {/if}
              </div>
              {#if theme.author || theme.description}
                <div class="text-text-muted text-[11px] font-label-sm truncate">
                  {theme.author ? `by ${theme.author}` : ''}
                  {theme.author && theme.description ? ' · ' : ''}
                  {theme.description ?? ''}
                </div>
              {/if}
            </div>
            <span
              class="material-symbols-outlined text-text-muted text-[18px] flex-shrink-0"
            >
              {active ? 'check_circle' : 'chevron_right'}
            </span>
          </button>
        {/each}
      </div>
    {/if}
  </section>

  <!-- Custom theme import/export -->
  <section aria-labelledby="custom-heading">
    <h3
      id="custom-heading"
      class="font-label-sm-bold text-text-muted uppercase tracking-widest text-[10px] mb-3"
    >
      Custom themes
    </h3>
    <div class="flex flex-wrap items-center gap-2">
      <button
        type="button"
        onclick={handleImport}
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start font-label-sm-bold hover:brightness-110 motion-reduce:transition-none transition-all cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60"
      >
        <span class="material-symbols-outlined text-[18px]">upload</span>
        Import .json
      </button>
      <button
        type="button"
        onclick={handleExport}
        disabled={!themeState.id}
        class="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-bg-surface border border-border-muted text-text-primary font-label-sm-bold hover:border-accent-primary-start motion-reduce:transition-none transition-colors cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-accent-primary-start/60 disabled:opacity-40 disabled:cursor-not-allowed"
      >
        <span class="material-symbols-outlined text-[18px]">download</span>
        Export active
      </button>
    </div>
    <p class="text-text-muted text-[11px] font-label-sm mt-2">
      Drop a theme .json file anywhere in this tab to import. Imported themes
      are validated against the canonical schema and appear in the list above
      immediately.
    </p>
  </section>

  <!-- Live status region (a11y: aria-live="polite" for success/info, role="alert" for errors) -->
  {#if themeStatus.message}
    <div
      role={statusAriaRole(themeStatus) ?? undefined}
      aria-live={themeStatus.kind === 'error' ? 'assertive' : 'polite'}
      class="rounded-lg px-3 py-2 text-[12px] font-body-md {statusClasses(
        themeStatus
      )}"
    >
      <div class="flex items-start gap-2">
        <span class="material-symbols-outlined text-[16px] flex-shrink-0">
          {themeStatus.kind === 'error'
            ? 'error'
            : themeStatus.kind === 'success'
              ? 'check_circle'
              : 'info'}
        </span>
        <div class="flex-1 min-w-0">
          <div>{themeStatus.message}</div>
          {#if themeStatus.fields.length > 0}
            <ul class="mt-1.5 ml-4 list-disc space-y-0.5">
              {#each themeStatus.fields as f (f.field)}
                <li>
                  <code class="font-mono text-[11px]">{f.field}</code>: {f.message}
                </li>
              {/each}
            </ul>
          {/if}
          <button
            type="button"
            onclick={() => clearStatus()}
            class="mt-1.5 text-[11px] font-label-sm-bold underline opacity-70 hover:opacity-100 bg-transparent border-none cursor-pointer"
          >
            Dismiss
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>
