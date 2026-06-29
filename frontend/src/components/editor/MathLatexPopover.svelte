<script lang="ts">
  // In-app LaTeX editor popover (Phase 5 / #328). Replaces window.prompt for
  // both the /math slash command (block create, owned by TipTapEditor) and
  // click-to-edit on an existing math node (dispatched via the silt:edit-math
  // window event). Multi-line mono input + debounced KaTeX live preview +
  // parse-error line. Mirrors the link/color/table popovers: position:fixed,
  // coord-driven, Commit/Cancel + Escape/Ctrl+Enter.
  import { untrack } from 'svelte'
  import { renderKatex } from '../../lib/editor/useKatex'

  interface Props {
    latex: string
    displayMode: boolean
    coords: { left: number; top: number }
    onCommit: (latex: string) => void
    onCancel: () => void
  }

  let { latex, displayMode, coords, onCommit, onCancel }: Props = $props()

  // Snapshot of the incoming latex: the popover is a one-shot editor, so the
  // local copy is meant to diverge from the prop as the user types (it must
  // not reset if the parent re-renders). untrack signals that intent.
  let text = $state(untrack(() => latex))
  let previewHtml = $state('')
  let previewError = $state<string | null>(null)
  let textareaEl = $state<HTMLTextAreaElement | null>(null)

  const PREVIEW_DEBOUNCE_MS = 150

  // Commit requires non-empty trimmed source. This mirrors the old prompt
  // guard: blanking a node via the popover is not allowed (Backspace on the
  // node deletes it), so a stray clear+OK can't silently wipe an equation.
  let canCommit = $derived(text.trim().length > 0)

  async function runPreview(value: string, dm: boolean): Promise<void> {
    if (!value) {
      previewHtml = ''
      previewError = null
      return
    }
    const res = await renderKatex(value, dm)
    previewHtml = res.html
    previewError = res.error
  }

  // Autofocus + select-all on open so the user can immediately retype or edit
  // the pre-filled source. rAF waits for the textarea to be in the DOM.
  $effect(() => {
    const el = textareaEl
    if (!el) return
    const id = requestAnimationFrame(() => {
      el.focus()
      el.select()
    })
    return () => cancelAnimationFrame(id)
  })

  // Live preview: render immediately on first mount (instant feedback, no
  // 150ms blank), then debounce subsequent keystrokes so KaTeX doesn't
  // re-render on every key. The cleanup clears the pending timer when text
  // changes again before the window elapses (classic debounce) and on unmount.
  let didInitialPreview = false
  $effect(() => {
    const value = text
    const dm = displayMode
    if (!didInitialPreview) {
      didInitialPreview = true
      void runPreview(value, dm)
      return
    }
    const timer = setTimeout(
      () => void runPreview(value, dm),
      PREVIEW_DEBOUNCE_MS
    )
    return () => clearTimeout(timer)
  })

  function commit(): void {
    const value = text.trim()
    if (!value) return
    onCommit(value)
  }

  // All keyboard handling lives on the dialog so it works regardless of which
  // child (textarea, button) holds focus. Escape cancels; Ctrl/Cmd+Enter
  // commits; plain Enter falls through so the textarea inserts a newline
  // (LaTeX is multi-line). Tab cycles focus within the dialog (aria-modal).
  function onDialogKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
      return
    }
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      commit()
      return
    }
    if (e.key === 'Tab') {
      const dialog = e.currentTarget as HTMLElement
      const focusable = Array.from(
        dialog.querySelectorAll<HTMLElement>(
          'textarea, button:not([disabled]), input'
        )
      )
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
  }
</script>

<div
  class="math-latex-popover"
  style="left:{coords.left}px; top:{coords.top}px"
  role="dialog"
  aria-modal="true"
  tabindex="-1"
  aria-label={displayMode ? 'Edit block equation' : 'Edit inline equation'}
  onkeydown={onDialogKeydown}
>
  <label class="mlp-label" for="math-latex-input">
    {displayMode ? 'LaTeX (block)' : 'LaTeX (inline)'}
  </label>
  <textarea
    id="math-latex-input"
    bind:this={textareaEl}
    class="mlp-input"
    rows="3"
    spellcheck="false"
    bind:value={text}></textarea>
  <div class="mlp-preview" aria-live="polite">
    {#if previewError}
      <span class="mlp-error" role="alert">{previewError}</span>
    {:else if previewHtml}
      {@html previewHtml}
    {:else}
      <span class="mlp-preview-empty">Preview</span>
    {/if}
  </div>
  <div class="mlp-actions">
    <button type="button" class="mlp-btn mlp-cancel" onclick={onCancel}>
      Cancel
    </button>
    <button
      type="button"
      class="mlp-btn mlp-commit"
      onclick={commit}
      disabled={!canCommit}
    >
      Commit
    </button>
  </div>
</div>

<style>
  /* Container mirrors the link/color/table popovers: fixed, coord-driven,
     same surface/border/shadow tokens so it reads as a native editor
     surface rather than a new visual language. */
  .math-latex-popover {
    position: fixed;
    z-index: 100;
    margin-top: 4px;
    padding: 8px;
    width: 300px;
    border-radius: 8px;
    background: var(--color-surface, #1e1e22);
    border: 1px solid var(--color-border-muted, #33333a);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .mlp-label {
    font-size: 10px;
    color: var(--color-text-muted, #8b95a3);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .mlp-input {
    width: 100%;
    padding: 6px 8px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 6px;
    background: var(--color-surface, #1a1d24);
    color: var(--color-text-primary, #e6e6e6);
    font-family: var(--font-mono, monospace);
    font-size: 0.85rem;
    line-height: 1.4;
    resize: vertical;
    outline: none;
  }
  .mlp-input:focus {
    border-color: var(--color-accent-primary-glow, #6fa3ff);
  }

  /* Preview pane: a subtly distinct background so it reads as rendered output
     rather than another input. Min-height avoids layout jump as the user types. */
  .mlp-preview {
    min-height: 44px;
    padding: 8px;
    border: 1px solid var(--color-border-muted, #33333a);
    border-radius: 6px;
    background: var(--color-hover, rgba(255, 255, 255, 0.04));
    color: var(--color-text-primary, #e6e6e6);
    overflow-x: auto;
  }
  .mlp-preview :global(.katex-display) {
    margin: 0;
  }
  .mlp-preview-empty {
    color: var(--color-text-muted, #8b95a3);
    font-size: 0.8rem;
  }
  .mlp-error {
    color: var(--color-error, #ef4444);
    font-family: var(--font-mono, monospace);
    font-size: 0.8rem;
  }

  .mlp-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
  }
  .mlp-btn {
    padding: 4px 12px;
    border: 1px solid var(--color-border-muted, #3a3f4b);
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-primary, #e6e6e6);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }
  .mlp-btn:hover {
    border-color: var(--color-text-muted, #8b95a3);
  }
  .mlp-commit {
    background: var(--color-accent-primary-start, #2dd4bf);
    border-color: transparent;
    color: #001813;
  }
  .mlp-commit:hover {
    filter: brightness(1.08);
    border-color: transparent;
  }
  .mlp-commit:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  @media (prefers-reduced-motion: reduce) {
    .mlp-commit:hover {
      filter: none;
    }
  }
</style>
