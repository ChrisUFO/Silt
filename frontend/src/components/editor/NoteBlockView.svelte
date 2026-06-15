<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node }: NodeViewProps = $props()
  let isEmpty = $derived(!node.content.size || node.textContent.trim() === '')
</script>

<NodeViewWrapper class="note-block flex items-start gap-3 py-1 min-h-[32px]">
  <span
    class="material-symbols-outlined text-text-muted/30 hover:text-primary transition-colors cursor-move mt-0.5 select-none text-[18px]"
    class:opacity-0={isEmpty}
    data-drag-handle
  >
    drag_indicator
  </span>
  <div class="flex-1 min-w-0">
    <NodeViewContent
      class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none"
    />
  </div>
</NodeViewWrapper>
