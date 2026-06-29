<script lang="ts">
  import type { Editor } from '@tiptap/core'
  import {
    getMatchCount,
    getActiveMatchIndex,
    clearSearch
  } from '../../lib/editor/search/searchExtension'

  // The active tab's TipTap editor. FindBar operates on the live doc.
  let { editor, onClose }: { editor?: Editor; onClose: () => void } = $props()

  // User inputs.
  let query = $state('')
  let caseSensitive = $state(false)
  let wholeWord = $state(false)
  let regexp = $state(false)

  // Projections of the editor's match decorations, refreshed on every editor
  // update / selection change (typing, navigation, doc edit).
  let matchCount = $state(0)
  let activeIndex = $state(-1)
  let inputEl = $state<HTMLInputElement | null>(null)

  function refreshCounts(): void {
    if (!editor || !editor.isEditable) return
    matchCount = getMatchCount(editor)
    activeIndex = getActiveMatchIndex(editor)
  }

  function applyQuery(): void {
    if (!editor) return
    editor.commands.setSearchQuery({
      search: query,
      caseSensitive,
      wholeWord,
      regexp,
      replace: ''
    })
    // Jump to the nearest match so the counter reflects a real position.
    if (query) editor.commands.findNextInPage()
    refreshCounts()
  }

  // Re-apply whenever the query string or a toggle flips.
  $effect(() => {
    // Track the reactive inputs so the effect re-runs on each change.
    void query
    void caseSensitive
    void wholeWord
    void regexp
    applyQuery()
  })

  // Subscribe to editor updates so the counter stays accurate as the doc
  // changes (the user edits while the bar is open) or navigation moves the
  // selection. Cleaned up on destroy.
  $effect(() => {
    if (!editor) return
    const onUpdate = () => refreshCounts()
    editor.on('update', onUpdate)
    editor.on('selectionUpdate', onUpdate)
    return () => {
      editor.off('update', onUpdate)
      editor.off('selectionUpdate', onUpdate)
    }
  })

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault()
      close()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (!editor || !query) return
      if (e.shiftKey) editor.commands.findPrevInPage()
      else editor.commands.findNextInPage()
      refreshCounts()
    }
  }

  function next(): void {
    editor?.commands.findNextInPage()
    refreshCounts()
  }
  function prev(): void {
    editor?.commands.findPrevInPage()
    refreshCounts()
  }

  function close(): void {
    editor && clearSearch(editor)
    onClose()
  }

  function focusInput(): void {
    // Select-all on (re)open so re-typing replaces the previous query (VS Code).
    inputEl?.focus()
    inputEl?.select()
  }

  // Focus when mounted.
  $effect(() => {
    if (editor) focusInput()
  })

  const status = $derived(
    !query
      ? ''
      : matchCount === 0
        ? 'No results'
        : `${activeIndex + 1} of ${matchCount}`
  )
  const noResults = $derived(!!query && matchCount === 0)
</script>

<div class="find-bar" role="toolbar" aria-label="Find">
  <input
    bind:this={inputEl}
    type="search"
    aria-label="Find"
    aria-keyshortcuts="Ctrl+F"
    aria-describedby="find-status"
    placeholder="Find"
    autocomplete="off"
    spellcheck="false"
    bind:value={query}
    onkeydown={handleKeydown}
    class="find-input"
    class:no-results={noResults}
  />
  <span
    id="find-status"
    class="find-status"
    aria-live="polite"
    aria-atomic="true"
  >
    {status}
  </span>
  <div class="find-toggles">
    <button
      type="button"
      class="toggle"
      class:on={caseSensitive}
      aria-pressed={caseSensitive}
      aria-label="Case sensitive (Alt+C)"
      title="Case sensitive (Alt+C)"
      onclick={() => (caseSensitive = !caseSensitive)}>Aa</button
    >
    <button
      type="button"
      class="toggle"
      class:on={wholeWord}
      aria-pressed={wholeWord}
      aria-label="Whole word (Alt+W)"
      title="Whole word (Alt+W)"
      onclick={() => (wholeWord = !wholeWord)}>ab</button
    >
    <button
      type="button"
      class="toggle"
      class:on={regexp}
      aria-pressed={regexp}
      aria-label="Regular expression (Alt+R)"
      title="Regular expression (Alt+R)"
      onclick={() => (regexp = !regexp)}>.*</button
    >
  </div>
  <div class="find-nav">
    <button
      type="button"
      class="nav-btn"
      aria-label="Previous match (Shift+Enter)"
      aria-keyshortcuts="Shift+Enter"
      title="Previous match"
      disabled={!editor || matchCount === 0}
      onclick={prev}>↑</button
    >
    <button
      type="button"
      class="nav-btn"
      aria-label="Next match (Enter)"
      aria-keyshortcuts="Enter"
      title="Next match"
      disabled={!editor || matchCount === 0}
      onclick={next}>↓</button
    >
  </div>
  <button
    type="button"
    class="close-btn"
    aria-label="Close find bar (Esc)"
    aria-keyshortcuts="Escape"
    title="Close"
    onclick={close}>✕</button
  >
</div>

<svelte:window
  onkeydown={(e) => {
    // Toggle shortcuts active while the find bar is focused.
    if (e.altKey && (e.key === 'c' || e.key === 'C')) {
      e.preventDefault()
      caseSensitive = !caseSensitive
    } else if (e.altKey && (e.key === 'w' || e.key === 'W')) {
      e.preventDefault()
      wholeWord = !wholeWord
    } else if (e.altKey && (e.key === 'r' || e.key === 'R')) {
      e.preventDefault()
      regexp = !regexp
    }
  }}
/>

<style>
  .find-bar {
    position: absolute;
    top: 0.5rem;
    right: 1rem;
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 0.375rem;
    padding: 0.375rem 0.5rem;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border, #333);
    border-radius: 8px;
    box-shadow: var(--shadow-md, 0 4px 12px rgba(0, 0, 0, 0.35));
    font-size: 13px;
  }
  .find-input {
    width: 200px;
    padding: 0.25rem 0.5rem;
    background: var(--color-bg, #0c0c0e);
    color: var(--color-text-primary, #e6e6e6);
    border: 1px solid var(--color-border, #333);
    border-radius: 4px;
    font: inherit;
  }
  .find-input.no-results {
    border-color: var(--color-status-danger, #ef4444);
  }
  .find-input:focus {
    outline: none;
    border-color: var(--color-accent-primary-start, #10b981);
  }
  .find-status {
    min-width: 70px;
    color: var(--color-text-muted, #888);
    font-size: 12px;
    text-align: center;
  }
  .find-toggles,
  .find-nav {
    display: flex;
    gap: 0.125rem;
  }
  .toggle,
  .nav-btn,
  .close-btn {
    min-width: 26px;
    height: 26px;
    padding: 0 0.375rem;
    background: transparent;
    color: var(--color-text-muted, #888);
    border: 1px solid transparent;
    border-radius: 4px;
    cursor: pointer;
    font: inherit;
    line-height: 1;
  }
  .toggle:hover,
  .nav-btn:hover,
  .close-btn:hover {
    background: var(--color-surface-hover, rgba(255, 255, 255, 0.06));
    color: var(--color-text-primary, #e6e6e6);
  }
  .toggle.on {
    background: var(--color-accent-primary-start, #10b981);
    color: var(--color-bg, #0c0c0e);
    border-color: var(--color-accent-primary-start, #10b981);
  }
  .nav-btn:disabled,
  .close-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .toggle:focus-visible,
  .nav-btn:focus-visible,
  .close-btn:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #10b981);
    outline-offset: 1px;
  }
</style>
