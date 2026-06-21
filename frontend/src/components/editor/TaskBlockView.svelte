<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node, updateAttributes, selected }: NodeViewProps = $props()

  const status = $derived(node.attrs.status || 'TODO')
  let isEmpty = $derived(
    node.content.size === 0 || node.textContent.trim() === ''
  )

  function cycleStatus() {
    const s = status.toUpperCase()
    const next = s === 'TODO' ? 'DOING' : s === 'DOING' ? 'DONE' : 'TODO'
    updateAttributes({ status: next })
  }

  function priorityLabel(p: number): string {
    if (p === 1) return '! CRITICAL'
    if (p === 2) return '! HIGH'
    return ''
  }
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
  class="task-block flex items-start gap-3 py-1 min-h-[32px]"
  data-depth={depth}
>
  <!-- Drag handle -->
  <span
    class="material-symbols-outlined text-text-muted/30 hover:text-primary transition-colors cursor-move mt-0.5 select-none text-[18px]"
    class:opacity-0={isEmpty}
    data-drag-handle
  >
    drag_indicator
  </span>

  <!-- Checkbox -->
  {#if status === 'TODO'}
    <button
      onclick={cycleStatus}
      aria-label="Mark task as doing"
      class="w-5 h-5 mt-0.5 rounded todo-check flex-shrink-0 cursor-pointer focus:outline-none"
    ></button>
  {:else if status === 'DOING'}
    <button
      onclick={cycleStatus}
      aria-label="Mark task as done"
      class="w-5 h-5 mt-0.5 rounded doing-check flex-shrink-0 flex items-center justify-center cursor-pointer focus:outline-none"
    >
      <div
        class="w-2 h-2 bg-accent-secondary-end doing-indicator rounded-full"
      ></div>
    </button>
  {:else if status === 'DONE'}
    <button
      onclick={cycleStatus}
      aria-label="Mark task as todo"
      class="w-5 h-5 mt-0.5 rounded done-check flex-shrink-0 flex items-center justify-center cursor-pointer focus:outline-none"
    >
      <span
        class="material-symbols-outlined text-accent-primary-start text-[14px] font-bold select-none"
      >
        check
      </span>
    </button>
  {/if}

  <!-- Content -->
  <div class="flex-1 flex flex-wrap items-center gap-2 min-w-0">
    <NodeViewContent
      class="flex-1 whitespace-pre-wrap break-words min-h-[22px] min-w-[150px] focus:outline-none"
    />

    <!-- Meta badges -->
    {#if status !== 'DONE'}
      {#if node.attrs.owner}
        <span
          class="bg-accent-secondary-glow border border-accent-secondary-start/30 text-accent-secondary-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          [{node.attrs.owner}]
        </span>
      {/if}
      {#if node.attrs.due_date}
        <span
          class="bg-accent-primary-glow border border-accent-primary-start/30 text-accent-primary-start px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          {node.attrs.due_date}
        </span>
      {/if}
      {#if priorityLabel(node.attrs.priority)}
        <span
          class="bg-error-bg border border-error-border text-error px-2 py-0.5 rounded text-[11px] font-label-sm select-none"
        >
          {priorityLabel(node.attrs.priority)}
        </span>
      {/if}
    {/if}
  </div>
</NodeViewWrapper>
