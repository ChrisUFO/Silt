<script lang="ts">
  // MathNodeView — KaTeX renderer for inline ($...$) and block ($$...$$) math
  // (#191). One component serves both nodes; displayMode follows the node type.
  // KaTeX is lazy-loaded via useKatex (kept out of the main bundle); while it
  // loads the raw LaTeX shows in mono so the slot is never blank. KaTeX's
  // htmlAndMathml output carries the MathML screen readers announce as the
  // math content, so the button uses a human aria-label (not role="math", which
  // conflicts with the interactive button role) and does not duplicate the raw
  // LaTeX in the label (AT reads that poorly). Parse errors render inline in
  // error color (visible, not silent).
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import { renderKatex } from '../../lib/editor/useKatex'
  import { settings } from '../../settings/store.svelte'

  let { node, updateAttributes }: NodeViewProps = $props()
  const latex = $derived((node.attrs.latex as string) || '')
  const displayMode = $derived(node.type.name === 'blockMathNode')
  // Per-vault opt-out (#191): when math is disabled, render the raw source as
  // plain text (no KaTeX, no click-to-edit) so the toggle actually removes the
  // math affordance rather than just the /math slash command.
  const mathEnabled = $derived(
    settings.config?.ui?.formatting?.math_enabled !== false
  )

  let html = $state('')
  let error = $state<string | null>(null)

  // Re-render (async) whenever the latex or display mode changes. KaTeX loads
  // on first use; thereafter renders are effectively synchronous.
  $effect(() => {
    const l = latex
    const dm = displayMode
    let cancelled = false
    renderKatex(l, dm).then((res) => {
      if (cancelled) return
      html = res.html
      error = res.error
    })
    return () => {
      cancelled = true
    }
  })

  // Click opens the LaTeX popover (Phase 5 / #328) pre-filled with the raw
  // source — both an edit affordance and a way to view/copy the LaTeX. The
  // popover itself is owned by TipTapEditor so it renders outside the editable
  // surface; this NodeView hands over its latex/displayMode, DOM-derived
  // coords, and an updateAttributes callback via the silt:edit-math event. An
  // empty equation opens the popover for fresh entry; deletion is via Backspace
  // on the node (the popover's Commit is disabled for empty source).
  function editLatex(e: MouseEvent): void {
    const target = e.currentTarget as HTMLElement | null
    const rect = target?.getBoundingClientRect()
    const coords = rect
      ? { left: rect.left, top: rect.bottom }
      : { left: 100, top: 100 }
    window.dispatchEvent(
      new CustomEvent('silt:edit-math', {
        detail: {
          latex,
          displayMode,
          coords,
          onCommit: (next: string) => updateAttributes({ latex: next })
        }
      })
    )
  }
</script>

<NodeViewWrapper as={displayMode ? 'div' : 'span'}>
  {#if !mathEnabled}
    <span class="silt-math-disabled" aria-label="LaTeX source (math disabled)">
      {displayMode ? `$$${latex}$$` : `$${latex}$`}
    </span>
  {:else if latex}
    <button
      type="button"
      class="silt-math"
      class:silt-math-block={displayMode}
      aria-label="Equation. Activate to edit."
      onclick={editLatex}
    >
      {#if error}
        <span class="silt-math-err" role="alert">{error}</span>
      {:else if html}
        {@html html}
      {:else}
        <span class="silt-math-pending" aria-hidden="true">{latex}</span>
      {/if}
    </button>
  {:else}
    <button
      type="button"
      class="silt-math-empty"
      class:silt-math-empty-block={displayMode}
      onclick={editLatex}
      aria-label="Add LaTeX equation"
    >
      Add LaTeX equation
    </button>
  {/if}
</NodeViewWrapper>

<style>
  /* The button provides interaction + a11y only; KaTeX supplies the visual +
     MathML semantics. Strip button chrome so it renders as bare inline math. */
  .silt-math {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    color: inherit;
    cursor: pointer;
    display: inline;
  }
  .silt-math-block {
    display: block;
  }
  .silt-math:focus-visible {
    outline: 2px solid var(--color-accent-primary-start, #4f7cff);
    outline-offset: 2px;
    border-radius: 3px;
  }
  .silt-math :global(.katex) {
    font-size: 1.05em;
  }
  .silt-math :global(.katex-display) {
    margin: 0.5em 0;
    text-align: center;
  }
  .silt-math-err {
    color: var(--color-error, #ef4444);
    font-family: var(--font-mono, monospace);
    font-size: 0.85em;
  }
  .silt-math-pending {
    font-family: var(--font-mono, monospace);
    color: var(--color-text-muted, #888);
    opacity: 0.7;
  }
  .silt-math-empty {
    display: inline-block;
    padding: 0.5em 1em;
    border: 1px dashed var(--color-border-muted, #444);
    border-radius: 6px;
    background: transparent;
    color: var(--color-text-muted, #888);
    cursor: pointer;
    font-family: inherit;
    font-size: 0.9em;
  }
  /* Centered affordance for an empty BLOCK equation (matches the populated
     block-math geometry instead of sitting left-aligned inline). */
  .silt-math-empty-block {
    display: block;
    margin: 0.5em auto;
    text-align: center;
  }
  /* math_enabled opt-out: render the raw source as plain mono text. */
  .silt-math-disabled {
    font-family: var(--font-mono, monospace);
    color: var(--color-text-primary, currentColor);
  }
  .silt-math-empty:hover {
    border-color: var(--color-accent-primary-start, #4f7cff);
    color: var(--color-accent-primary-start, #4f7cff);
  }
</style>
