<script lang="ts">
  import type { Editor } from '@tiptap/core'
  import {
    suggest,
    ignoreWordSession
  } from '../../lib/editor/spellcheck/dictionary'
  import { requestSpellcheckRecheck } from '../../lib/editor/spellcheck/SpellcheckExtension'
  import { customDictionary } from '../../lib/editor/spellcheck/customDictionary.svelte'

  let {
    editor,
    word,
    range,
    anchor,
    onClose
  }: {
    editor: Editor
    word: string
    range: { from: number; to: number }
    anchor: { x: number; y: number }
    onClose: () => void
  } = $props()

  const suggestions = $derived(suggest(word))
  let items = $state<HTMLButtonElement[]>([])
  let focusedIndex = $state(0)

  // Recompute focus when suggestions change.
  $effect(() => {
    void suggestions
    focusedIndex = 0
  })

  function apply(suggestion: string): void {
    // Replace the misspelled word with the chosen suggestion in one tx.
    editor
      .chain()
      .focus()
      .deleteRange(range)
      .insertContentAt(range.from, { type: 'text', text: suggestion })
      .run()
    onClose()
  }

  async function addToDictionary(): Promise<void> {
    await customDictionary.add(word)
    // The config:changed event refreshes the editor's $effect (which calls
    // setCustomWords + recheck), so the word un-flags immediately. No reload.
    onClose()
  }

  function ignore(): void {
    ignoreWordSession(word)
    requestSpellcheckRecheck(editor)
    onClose()
  }

  function handleKeydown(e: KeyboardEvent): void {
    const total = suggestions.length + 2 // suggestions + Add + Ignore
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      focusedIndex = (focusedIndex + 1) % total
      items[focusedIndex]?.focus()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      focusedIndex = (focusedIndex - 1 + total) % total
      items[focusedIndex]?.focus()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  let menuEl = $state<HTMLDivElement | null>(null)
  // Clamp the menu to the viewport so right-clicks near the screen edge don't
  // push it off-screen. The $effect reads `anchor` reactively + measures the
  // element after render to compute the clamped position.
  let clampedAnchor = $state({ x: -9999, y: -9999 })

  $effect(() => {
    if (!menuEl) return
    const { x: ax, y: ay } = anchor
    const rect = menuEl.getBoundingClientRect()
    let x = ax
    let y = ay
    if (x + rect.width > window.innerWidth) {
      x = window.innerWidth - rect.width - 8
    }
    if (y + rect.height > window.innerHeight) {
      y = window.innerHeight - rect.height - 8
    }
    clampedAnchor = { x: Math.max(8, x), y: Math.max(8, y) }
  })
</script>

<svelte:window onkeydown={handleKeydown} />

<div
  bind:this={menuEl}
  class="spell-menu"
  role="menu"
  aria-label="Spelling suggestions"
  style="left:{Math.round(clampedAnchor.x)}px; top:{Math.round(
    clampedAnchor.y
  )}px;"
  tabindex="-1"
>
  {#if suggestions.length === 0}
    <button type="button" class="menu-item disabled" role="menuitem" disabled
      >No suggestions</button
    >
  {:else}
    {#each suggestions as s, i}
      <button
        type="button"
        bind:this={items[i]}
        class="menu-item"
        role="menuitem"
        aria-label="Replace with {s}"
        onclick={() => apply(s)}>{s}</button
      >
    {/each}
  {/if}
  <div class="menu-separator"></div>
  <button
    type="button"
    bind:this={items[suggestions.length]}
    class="menu-item"
    role="menuitem"
    onclick={addToDictionary}>Add to dictionary</button
  >
  <button
    type="button"
    bind:this={items[suggestions.length + 1]}
    class="menu-item"
    role="menuitem"
    onclick={ignore}>Ignore</button
  >
</div>

<style>
  .spell-menu {
    position: fixed;
    z-index: 100;
    min-width: 180px;
    padding: 4px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #333);
    border-radius: 8px;
    box-shadow: var(--shadow-md, 0 8px 24px rgba(0, 0, 0, 0.45));
    font-size: 13px;
  }
  .menu-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 0.375rem 0.625rem;
    background: transparent;
    color: var(--color-text-primary, #e6e6e6);
    border: none;
    border-radius: 4px;
    cursor: pointer;
    font: inherit;
  }
  .menu-item:hover:not(.disabled),
  .menu-item:focus-visible {
    background: var(--color-hover, rgba(255, 255, 255, 0.08));
    outline: none;
  }
  .menu-item.disabled {
    color: var(--color-text-muted, #888);
    cursor: default;
  }
  .menu-separator {
    height: 1px;
    margin: 4px 0;
    background: var(--color-border-muted, #333);
  }
</style>
