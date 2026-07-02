<script lang="ts">
  // FormattingFirstRunTip — a one-time discoverability banner (#168).
  // Shows once on the first formatting-eligible action if the user hasn't
  // dismissed it. Dismissal persists in settings.config.ui.dismissed_tips
  // (per-vault). role="status" aria-live="polite" so screen readers announce it.

  interface Props {
    dismissed: boolean
    onDismiss: () => void
  }

  let { dismissed, onDismiss }: Props = $props()
</script>

{#if !dismissed}
  <div class="first-run-tip" role="status" aria-live="polite">
    <span class="material-symbols-outlined tip-icon" aria-hidden="true"
      >tips_and_updates</span
    >
    <span class="tip-text">
      Tip: select text and press <kbd>Ctrl+B</kbd> to make it bold. Type
      <kbd>/</kbd> for more options.
    </span>
    <button
      type="button"
      class="tip-dismiss"
      onclick={onDismiss}
      aria-label="Dismiss tip"
    >
      Got it
    </button>
  </div>
{/if}

<style>
  .first-run-tip {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    margin-bottom: 4px;
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
    color: var(--color-text-primary, #e6e6e6);
    font-size: 0.8rem;
  }

  .tip-icon {
    font-size: 18px;
    color: var(--color-accent-primary-glow, #6fa3ff);
    flex-shrink: 0;
  }

  .tip-text {
    flex: 1;
    line-height: 1.4;
  }

  .tip-text kbd {
    display: inline-block;
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--color-panel, #252830);
    border: 1px solid var(--color-border-muted, #3a3f4b);
    color: var(--color-text-primary, #e6e6e6);
    font-family: var(--font-mono, monospace);
    font-size: 0.7rem;
  }

  .tip-dismiss {
    flex-shrink: 0;
    padding: 3px 10px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #8b95a3);
    font-size: 0.75rem;
    cursor: pointer;
    transition:
      background 0.1s,
      color 0.1s;
  }

  .tip-dismiss:hover {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 15%,
      transparent
    );
    color: var(--color-text-primary, #e6e6e6);
  }
</style>
