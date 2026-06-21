<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import FormatToolbar from './FormatToolbar.svelte'
  import ViewModeToggle from './ViewModeToggle.svelte'
  import { settings } from '../../settings/store.svelte'
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
  let isDark = $derived(isSystemDark())
  let colorEnabled = $derived(
    settings.config?.ui?.formatting?.color_enabled !== false
  )
</script>

{#if viewMode === 'edit' && showFormatToolbar}
  <div class="unified-utility-bar">
    <div class="flex items-center">
      <FormatToolbar {editor} {activeMarks} {isDark} {colorEnabled} />
    </div>
    <div class="flex items-center">
      <ViewModeToggle mode={viewMode} onToggle={onToggleViewMode} />
    </div>
  </div>
{:else}
  <div class="unified-utility-bar justify-end">
    <ViewModeToggle mode={viewMode} onToggle={onToggleViewMode} />
  </div>
{/if}

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
