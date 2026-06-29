<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  // SvelteNodeViewRenderer auto-applies a `node-{type.name}` (camelCase) class
  // to the wrapper, so we don't redeclare it here (#179).
  let { node }: NodeViewProps = $props()
  let level = $derived(node.attrs.depth || 1)
  let align = $derived(node.attrs.align || 'left')

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
    return `font-size: ${sizes[level as keyof typeof sizes] || '1em'}; font-weight: ${level <= 2 ? '700' : '600'}; text-align: ${align};`
  })
</script>

<NodeViewWrapper
  class="group flex items-start gap-3 py-1"
  data-align={align}
  data-depth={level}
  data-id={node.attrs.id}
>
  <span
    class="silt-drag-handle-inline material-symbols-outlined text-text-muted hover:text-primary transition-colors duration-150 mt-0.5 select-none text-[18px] opacity-0 group-hover:opacity-100"
    spellcheck="false"
    draggable="true"
    aria-hidden="true"
    title="Drag to move heading (Alt+Up/Down to move by keyboard)"
    data-drag-handle
  >
    drag_indicator
  </span>
  <div class="flex-1 min-w-0" style={headingStyle}>
    <NodeViewContent class="focus:outline-none" />
  </div>
</NodeViewWrapper>
