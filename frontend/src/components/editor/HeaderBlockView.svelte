<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node }: NodeViewProps = $props()
  let level = $derived(node.attrs.depth || 1)

  // Inline styles avoid Tailwind JIT purging dynamically-constructed class names.
  let headingStyle = $derived.by(() => {
    const sizes = {
      1: '1.75em',
      2: '1.4em',
      3: '1.2em',
      4: '1.1em',
      5: '1.05em',
      6: '1em'
    }
    return `font-size: ${sizes[level as keyof typeof sizes] || '1em'}; font-weight: ${level <= 2 ? '700' : '600'};`
  })
</script>

<NodeViewWrapper class="header-block flex items-start gap-3 py-1">
  <span
    class="material-symbols-outlined text-text-muted/30 hover:text-primary transition-colors cursor-move mt-0.5 select-none text-[18px]"
    data-drag-handle
  >
    drag_indicator
  </span>
  <div class="flex-1 min-w-0" style={headingStyle}>
    <NodeViewContent class="focus:outline-none" />
  </div>
</NodeViewWrapper>
