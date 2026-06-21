<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node }: NodeViewProps = $props()
  let isEmpty = $derived(!node.content.size || node.textContent.trim() === '')
  let align = $derived(node.attrs.align || 'left')
  let bullet = $derived(node.attrs.bullet || '')
  let depth = $derived(node.attrs.depth || 0)

  $effect(() => {
    const id = node.attrs.id
    if (id) {
      const el = document.querySelector(`[data-id="${id}"]`)
      if (el) {
        el.setAttribute('data-depth', String(depth))
      }
    }
  })
</script>

<NodeViewWrapper
  class="note-block flex items-start gap-3 py-1 min-h-[32px]"
  data-align={align}
  data-depth={depth}
>
  <span
    class="material-symbols-outlined text-text-muted/30 hover:text-primary transition-colors cursor-move mt-0.5 select-none text-[18px]"
    class:opacity-0={isEmpty}
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

  <div class="flex-1 min-w-0" style="text-align: {align}">
    <NodeViewContent
      class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none"
    />
  </div>
</NodeViewWrapper>
