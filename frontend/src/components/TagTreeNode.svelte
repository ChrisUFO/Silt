<script lang="ts">
  // Self-importing recursive tag-tree node (Svelte 5 idiom; replaces
  // <svelte:self>). Renders one node + its children.
  import Self from './TagTreeNode.svelte'

  interface TagNode {
    name: string
    path: string
    count: number
    children: TagNode[]
  }

  interface Props {
    node: TagNode
    depth: number
    expanded: Set<string>
    activeTag: string
    onToggle: (path: string) => void
    onSelect: (path: string) => void
  }

  let { node, depth, expanded, activeTag, onToggle, onSelect }: Props = $props()

  let isOpen = $derived(expanded.has(node.path))
  let isActive = $derived(activeTag === node.path)
</script>

<div style="padding-left: {depth * 14}px">
  <div
    class="group flex items-center gap-1 px-2 py-1 rounded transition-colors"
    class:bg-accent-teal-glow={isActive}
  >
    {#if node.children.length > 0}
      <button
        onclick={() => onToggle(node.path)}
        aria-label={isOpen ? 'Collapse' : 'Expand'}
        class="text-text-muted hover:text-accent-teal-start border-none bg-transparent cursor-pointer p-0 w-4 flex-shrink-0"
      >
        <span
          class="material-symbols-outlined text-[14px] transition-transform inline-block"
          class:rotate-90={isOpen}
        >
          chevron_right
        </span>
      </button>
    {:else}
      <span class="w-4 flex-shrink-0"></span>
    {/if}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div
      role="button"
      tabindex="0"
      onclick={() => onSelect(node.path)}
      onkeydown={(e) => e.key === 'Enter' && onSelect(node.path)}
      class="flex items-center gap-1.5 flex-1 cursor-pointer rounded px-1 py-0.5 min-w-0"
      class:text-accent-teal-start={isActive}
      class:hover:text-text-primary={!isActive}
      class:text-text-primary={!isActive}
    >
      <span
        class="material-symbols-outlined text-[15px] text-accent-indigo-start/70"
        >label</span
      >
      <span class="font-body-md text-[13px] truncate" title={node.name}
        >{node.name}</span
      >
      <span
        class="text-[9px] font-label-sm text-text-muted bg-bg-panel border border-border-muted rounded-full px-1.5 py-0.5 ml-auto"
      >
        {node.count}
      </span>
    </div>
  </div>

  {#if isOpen && node.children.length > 0}
    {#each node.children as child (child.path)}
      <Self
        node={child}
        depth={depth + 1}
        {expanded}
        {activeTag}
        {onToggle}
        {onSelect}
      />
    {/each}
  {/if}
</div>
