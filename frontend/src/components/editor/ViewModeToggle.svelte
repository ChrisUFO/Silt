<script lang="ts">
  // ViewModeToggle — Edit / Source radio toggle (#171).
  // Sits in the page chrome. role="radiogroup" with arrow-key navigation.
  // aria-checked reflects the current mode. aria-keyshortcuts announces Ctrl+E.

  interface Props {
    mode: 'edit' | 'source'
    onToggle: () => void
  }

  let { mode, onToggle }: Props = $props()

  function handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault()
      onToggle()
    }
  }
</script>

<div
  class="view-mode-toggle"
  role="radiogroup"
  aria-label="View mode"
  tabindex="0"
  onkeydown={handleKeydown}
>
  <button
    type="button"
    class="mode-btn"
    class:active={mode === 'edit'}
    role="radio"
    aria-checked={mode === 'edit'}
    aria-keyshortcuts="Ctrl+E"
    onclick={() => mode !== 'edit' && onToggle()}
  >
    Edit
  </button>
  <button
    type="button"
    class="mode-btn"
    class:active={mode === 'source'}
    role="radio"
    aria-checked={mode === 'source'}
    aria-keyshortcuts="Ctrl+E"
    onclick={() => mode !== 'source' && onToggle()}
  >
    Source
  </button>
</div>

<style>
  .view-mode-toggle {
    display: inline-flex;
    align-items: center;
    border-radius: 6px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    overflow: hidden;
  }

  .view-mode-toggle:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #4f7cff);
    outline-offset: 1px;
  }

  .mode-btn {
    padding: 2px 10px;
    border: none;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    font-size: 0.75rem;
    cursor: pointer;
    transition: background 0.1s, color 0.1s;
  }

  .mode-btn.active {
    background: color-mix(in srgb, var(--color-accent-primary-glow, #6fa3ff) 20%, transparent);
    color: var(--color-accent-primary-glow, #6fa3ff);
  }

  .mode-btn:hover:not(.active) {
    color: var(--color-text-primary, #e6e6e6);
  }

  @media (prefers-reduced-motion: reduce) {
    .mode-btn {
      transition: none;
    }
  }
</style>
