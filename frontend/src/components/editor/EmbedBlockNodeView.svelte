<script lang="ts">
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  // Default NodeView for the generic embedBlock node (#110). Renders a minimal,
  // keyboard-operable card with the embed type, src, and caption. Plugins that
  // register a custom embed type (e.g. silt-attachments for type "attachment")
  // provide their own NodeView; this is the fallback for types without a
  // custom view.
  let { node }: NodeViewProps = $props()
  const attrs = $derived(node.attrs as Record<string, any>)
</script>

<NodeViewWrapper>
  <div
    class="embed-block-default my-2 p-3 rounded-lg border border-border-muted bg-bg-surface/60 flex items-center gap-3"
    role="img"
    aria-label={attrs.caption || `${attrs.embedType}: ${attrs.src}`}
    data-embed-type={attrs.embedType}
    data-openable={attrs.openable ? 'true' : 'false'}
  >
    <span
      class="material-symbols-outlined text-accent-primary-start/70 text-[28px]"
    >
      {attrs.embedType === 'image'
        ? 'image'
        : attrs.embedType === 'attachment'
          ? 'attach_file'
          : 'extension'}
    </span>
    <div class="flex-1 min-w-0">
      <div class="text-text-primary text-[13px] font-body-md truncate">
        {attrs.caption || attrs.src || attrs.embedType}
      </div>
      {#if attrs.src}
        <div class="text-text-muted text-[10px] font-label-sm truncate">
          {attrs.src}
        </div>
      {/if}
    </div>
    {#if attrs.pluginID}
      <span
        class="text-[9px] text-text-muted uppercase tracking-wider border border-border-muted rounded px-1.5 py-0.5"
      >
        {attrs.pluginID}
      </span>
    {/if}
  </div>
</NodeViewWrapper>
