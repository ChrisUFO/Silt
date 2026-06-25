<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import FormatToolbar from './FormatToolbar.svelte'
  import ViewModeToggle from './ViewModeToggle.svelte'
  import {
    settings,
    toggleFormatToolbar,
    toggleFocusMode
  } from '../../settings/store.svelte'
  import { isSystemDark } from '../../lib/systemTheme.svelte'

  // EditorUtilityBar — extracted from VirtualScrollContainer (#202).
  // Pure presentational component. Owns the settings/theme-derived values
  // (showFormatToolbar, isDark, colorEnabled) and the FormatToolbar +
  // ViewModeToggle render logic; the parent (VSC) just wires the four
  // editable concerns through props.

  interface Props {
    editor: Editor | null
    activeMarks: Set<string>
    viewMode: 'edit' | 'source'
    onToggleViewMode: () => void
  }

  let { editor, activeMarks, viewMode, onToggleViewMode }: Props = $props()

  let showFormatToolbar = $derived(
    settings.config?.ui?.show_format_toolbar !== false
  )
  let focusModeActive = $derived(settings.config?.editor?.focus_mode === true)
  let isDark = $derived(isSystemDark())
  let colorEnabled = $derived(
    settings.config?.ui?.formatting?.color_enabled !== false
  )

  function handleToggleFocus() {
    toggleFocusMode()
  }

  function handleToggleToolbar() {
    toggleFormatToolbar()
  }
</script>

<div
  class="unified-utility-bar"
  class:justify-end={viewMode !== 'edit' || !showFormatToolbar}
>
  {#if viewMode === 'edit' && showFormatToolbar}
    <div class="flex items-center">
      <FormatToolbar {editor} {activeMarks} {isDark} {colorEnabled} />
    </div>
  {/if}

  <div class="flex items-center gap-1">
    {#if viewMode === 'edit'}
      <!-- Focus Mode Toggle -->
      <button
        onclick={handleToggleFocus}
        class="h-7 w-7 flex items-center justify-center rounded transition-colors border-none bg-transparent cursor-pointer focus:outline-none hover:bg-hover"
        class:text-accent-primary-start={focusModeActive}
        class:text-text-muted={!focusModeActive}
        title="Toggle Focus Mode (Ctrl+Shift+D)"
        aria-label="Toggle Focus Mode"
      >
        <span class="material-symbols-outlined text-[18px]"
          >center_focus_strong</span
        >
      </button>

      <!-- Zen Mode / Toolbar Toggle -->
      <button
        onclick={handleToggleToolbar}
        class="h-7 w-7 flex items-center justify-center rounded transition-colors border-none bg-transparent cursor-pointer focus:outline-none hover:bg-hover"
        class:text-accent-primary-start={showFormatToolbar}
        class:text-text-muted={!showFormatToolbar}
        title="Toggle Formatting Toolbar (Ctrl+Shift+F)"
        aria-label="Toggle Formatting Toolbar"
      >
        <span class="material-symbols-outlined text-[18px]">text_format</span>
      </button>

      <div class="w-px h-4 bg-border-muted mx-1"></div>
    {/if}

    <ViewModeToggle mode={viewMode} onToggle={onToggleViewMode} />
  </div>
</div>

<style>
  .unified-utility-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 38px;
    padding: 0 16px;
    background: color-mix(
      in srgb,
      var(--color-surface, #1a1d24) 95%,
      transparent
    );
    backdrop-filter: blur(8px);
    border-bottom: 1px solid var(--color-border-muted, #2a2e36);
    flex-shrink: 0;
    z-index: 15;
  }

  .unified-utility-bar.justify-end {
    justify-content: flex-end;
  }
</style>
