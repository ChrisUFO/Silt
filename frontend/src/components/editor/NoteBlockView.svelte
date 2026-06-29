<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  // SvelteNodeViewRenderer auto-applies a `node-{type.name}` (camelCase) class
  // to the wrapper, so we don't redeclare it here (#179).
  let { node }: NodeViewProps = $props()
  let isEmpty = $derived(!node.content.size || node.textContent.trim() === '')
  let align = $derived(node.attrs.align || 'left')
  let bullet = $derived(node.attrs.bullet || '')
  let quote = $derived(node.attrs.quote || '')
  let depth = $derived(node.attrs.depth || 0)
</script>

<NodeViewWrapper
  class="group flex items-start gap-3 py-1 min-h-[32px]"
  data-align={align}
  data-depth={depth}
  data-id={node.attrs.id}
>
  <span
    class="silt-drag-handle-inline material-symbols-outlined text-text-muted hover:text-primary transition-colors duration-150 mt-0.5 select-none text-[18px] opacity-0 group-hover:opacity-100"
    class:group-hover:opacity-100={!isEmpty}
    spellcheck="false"
    draggable="true"
    aria-hidden="true"
    title="Drag to move block (Alt+Up/Down to move by keyboard)"
    data-drag-handle
  >
    drag_indicator
  </span>

  {#if bullet && bullet !== ''}
    {#if /^\d+/.test(bullet)}
      <!-- Numbered marker -->
      <span
        class="text-text-muted/70 text-[14px] leading-[22px] select-none font-mono min-w-[18px] text-right"
        aria-hidden="true"
      >
        {bullet.trim()}
      </span>
    {:else}
      <!-- Bullet dot -->
      <div
        class="w-1.5 h-1.5 rounded-full bg-text-muted/50 mt-2.5 flex-shrink-0 select-none"
        aria-hidden="true"
      ></div>
    {/if}
  {/if}

  <!-- A quote renders as a native <blockquote> (implicit semantics: paragraph
       grouping, distinct SR announcement) rather than a synthetic role on a
       div. No aria-label is set on purpose — it would override the screen
       reader reading the quote's own content. -->
  <svelte:element
    this={quote ? 'blockquote' : 'div'}
    class="flex-1 min-w-0"
    class:silt-quote={!!quote}
    data-quote={quote || undefined}
    style="text-align: {align}"
  >
    <NodeViewContent
      class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none"
    />
  </svelte:element>
</NodeViewWrapper>
