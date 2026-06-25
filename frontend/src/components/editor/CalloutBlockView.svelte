<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import {
    CALLOUT_VARIANTS,
    type CalloutVariant
  } from '../../lib/editor/schema'

  // Obsidian-style callout / admonition (#180). The variant drives the icon +
  // accent color; the inline content is the editable message. The `>` marker
  // is reconstructed on save by the converter, so it never appears here.
  let { node }: NodeViewProps = $props()
  let variant = $derived((node.attrs.variant as CalloutVariant) || 'note')
  let cfg = $derived(CALLOUT_VARIANTS[variant] ?? CALLOUT_VARIANTS.note)
</script>

<NodeViewWrapper
  class="silt-callout group flex items-start gap-2 py-1.5 my-1 min-h-[32px]"
  data-variant={variant}
>
  <span
    class="material-symbols-outlined silt-callout-icon select-none text-[20px] mt-0.5"
    style="color: {cfg.accent}"
    aria-hidden="true"
  >
    {cfg.icon}
  </span>
  <div class="flex-1 min-w-0" role={cfg.role} aria-label={cfg.label}>
    <NodeViewContent
      class="silt-callout-body break-words min-h-[22px] focus:outline-none"
    />
  </div>
</NodeViewWrapper>

<style>
  /* block+ content: paragraphs inside the callout body get compact spacing
     so multi-paragraph callouts don't look airy. Fully global because the
     paragraphs are rendered by TipTap's NodeViewContent at runtime. */
  :global(.silt-callout-body p) {
    margin: 0 0 0.25rem 0;
  }
  :global(.silt-callout-body p:last-child) {
    margin-bottom: 0;
  }
</style>
