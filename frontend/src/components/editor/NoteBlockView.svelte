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

  let dragHandleEl: HTMLElement | null = $state(null)

  $effect(() => {
    if (dragHandleEl) {
      const wrapper = dragHandleEl.closest('[data-node-view-wrapper]')
      const parentEl = wrapper?.parentElement
      if (parentEl) {
        parentEl.setAttribute('data-depth', String(depth))
        if (node.attrs.id) {
          parentEl.setAttribute('data-id', node.attrs.id)
        }
      }
    }
  })
</script>

<NodeViewWrapper
  class="group flex items-start gap-3 py-1 min-h-[32px]"
  data-align={align}
  data-depth={depth}
>
  <span
    bind:this={dragHandleEl}
    class="material-symbols-outlined text-text-muted/30 hover:text-primary transition-all duration-150 cursor-move mt-0.5 select-none text-[18px] opacity-0"
    class:group-hover:opacity-100={!isEmpty}
    spellcheck="false"
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

  <div
    class="flex-1 min-w-0"
    class:silt-quote={!!quote}
    data-quote={quote || undefined}
    style="text-align: {align}"
    role={quote ? 'blockquote' : undefined}
  >
    <NodeViewContent
      class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none"
    />
  </div>
</NodeViewWrapper>
