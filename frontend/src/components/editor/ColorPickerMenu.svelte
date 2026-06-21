<script lang="ts">
  import type { Editor } from 'svelte-tiptap'
  import {
    DEFAULT_COLOR_PALETTE,
    resolveColor,
    type ColorEntry
  } from '../../lib/editor/colors'

  interface Props {
    editor: Editor | null
    markType: 'textColor' | 'backgroundColor'
    isDark: boolean
  }

  let { editor, markType, isDark }: Props = $props()

  let menuOpen = $state(false)
  let wrapperEl = $state<HTMLDivElement | null>(null)

  $effect(() => {
    if (!menuOpen) return
    const onClick = (e: MouseEvent) => {
      if (wrapperEl && !wrapperEl.contains(e.target as Node)) {
        menuOpen = false
      }
    }
    document.addEventListener('click', onClick)
    return () => document.removeEventListener('click', onClick)
  })

  function applyColor(entry: ColorEntry): void {
    if (!editor) return
    const hex = resolveColor(entry, isDark)
    editor.chain().focus().setMark(markType, { color: hex }).run()
    menuOpen = false
  }

  function applyCustom(event: Event): void {
    const input = event.target as HTMLInputElement
    if (input.value && editor) {
      editor.chain().focus().setMark(markType, { color: input.value }).run()
    }
    menuOpen = false
  }

  function removeColor(): void {
    if (!editor) return
    editor.chain().focus().unsetMark(markType).run()
    menuOpen = false
  }

  const triggerIcon = $derived(
    markType === 'textColor' ? 'format_color_text' : 'format_color_fill'
  )
  const triggerLabel = $derived(
    markType === 'textColor' ? 'Text color' : 'Background color'
  )
</script>

<div class="color-picker-wrapper" bind:this={wrapperEl}>
  <button
    type="button"
    class="color-trigger"
    aria-expanded={menuOpen}
    aria-haspopup="menu"
    aria-label={triggerLabel}
    onclick={() => (menuOpen = !menuOpen)}
  >
    <span class="material-symbols-outlined" aria-hidden="true"
      >{triggerIcon}</span
    >
  </button>

  {#if menuOpen}
    <div class="color-menu" role="menu" aria-label={triggerLabel}>
      <button
        type="button"
        class="color-action"
        role="menuitem"
        onclick={removeColor}
      >
        <span class="material-symbols-outlined" aria-hidden="true"
          >format_color_reset</span
        >
        <span>No color</span>
      </button>
      <div class="swatch-grid" role="group" aria-label="Color palette">
        {#each DEFAULT_COLOR_PALETTE as entry (entry.id)}
          <button
            type="button"
            class="swatch"
            style="background-color: {resolveColor(entry, isDark)}"
            aria-label={entry.label}
            role="menuitem"
            onclick={() => applyColor(entry)}
          >
          </button>
        {/each}
      </div>
      <label class="custom-color-row">
        <span class="custom-label">Custom</span>
        <input
          type="color"
          class="custom-input"
          onchange={applyCustom}
          aria-label="Custom color"
        />
      </label>
    </div>
  {/if}
</div>

<style>
  .color-picker-wrapper {
    position: relative;
    display: inline-flex;
  }

  .color-trigger {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    cursor: pointer;
    transition:
      background 0.1s,
      color 0.1s;
  }

  .color-trigger:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
    color: var(--color-text-primary, #e6e6e6);
  }

  .color-trigger .material-symbols-outlined {
    font-size: 18px;
  }

  .color-menu {
    position: absolute;
    top: 100%;
    left: 0;
    z-index: 50;
    min-width: 200px;
    padding: 6px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .color-action {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-primary, #e6e6e6);
    font-size: 0.75rem;
    text-align: left;
    cursor: pointer;
  }

  .color-action:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
  }

  .color-action .material-symbols-outlined {
    font-size: 16px;
    color: var(--color-text-muted, #8b95a3);
  }

  .swatch-grid {
    display: grid;
    grid-template-columns: repeat(6, 1fr);
    gap: 3px;
    padding: 4px 0;
  }

  .swatch {
    width: 24px;
    height: 24px;
    border: 2px solid transparent;
    border-radius: 5px;
    cursor: pointer;
    padding: 0;
    transition:
      border-color 0.1s,
      transform 0.1s;
  }

  .swatch:hover {
    border-color: var(--color-text-primary, #e6e6e6);
    transform: scale(1.1);
  }

  .custom-color-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 8px;
    font-size: 0.75rem;
    color: var(--color-text-muted, #8b95a3);
  }

  .custom-input {
    width: 28px;
    height: 22px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 4px;
    background: transparent;
    cursor: pointer;
    padding: 0;
  }

  @media (prefers-reduced-motion: reduce) {
    .swatch {
      transition: none;
    }
    .color-trigger {
      transition: none;
    }
  }
</style>
