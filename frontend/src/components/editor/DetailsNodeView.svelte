<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node, updateAttributes, editor }: NodeViewProps = $props()
  let summary = $derived(node.attrs.summary || '')
  let isOpen = $derived(node.attrs.open ? true : false)

  let summaryEl: HTMLSpanElement | null = $state(null)

  function toggleOpen(): void {
    updateAttributes({ open: !isOpen })
    editor.commands.focus()
  }

  function onSummaryInput(): void {
    if (summaryEl) {
      updateAttributes({ summary: summaryEl.textContent || '' })
    }
  }

  function onSummaryKeydown(e: KeyboardEvent): void {
    if (e.key === 'Enter') {
      e.preventDefault()
      summaryEl?.blur()
      editor.commands.focus()
    }
  }

  // The Ctrl+. toggle is bound via SiltBlockKeymaps (keymaps.ts) to avoid
  // per-instance listener duplication and collision with the superscript
  // shortcut. See the Mod-. handler in keymaps.ts.
</script>

<NodeViewWrapper
  class="my-2 rounded-lg border border-border overflow-hidden"
  data-type="silt-details"
  role="group"
  aria-label={summary || 'Foldable section'}
>
  <div class="flex items-stretch">
    <button
      class="flex items-center gap-2 px-3 py-2 bg-bg-interface/50 hover:bg-bg-interface transition-colors cursor-pointer select-none text-sm flex-shrink-0"
      onclick={toggleOpen}
      aria-expanded={isOpen}
      aria-label={isOpen ? 'Collapse section' : 'Expand section'}
    >
      <span
        class="material-symbols-outlined text-[18px] text-text-muted transition-transform duration-150"
        aria-hidden="true"
      >
        {isOpen ? 'expand_more' : 'chevron_right'}
      </span>
    </button>
    <div class="flex-1 flex items-center px-2 py-2 min-w-0">
      <span
        bind:this={summaryEl}
        class="text-text text-sm outline-none min-w-[20px]"
        class:opacity-50={!summary}
        contenteditable="true"
        role="textbox"
        tabindex="0"
        aria-label="Section summary"
        oninput={onSummaryInput}
        onkeydown={onSummaryKeydown}
      >
        {summary || ''}
      </span>
      {#if !summary}
        <span class="text-text-muted italic text-sm select-none ml-1"
          >Details</span
        >
      {/if}
    </div>
  </div>
  {#if isOpen}
    <div class="px-4 py-3 border-t border-border">
      <NodeViewContent
        class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none"
      />
    </div>
  {/if}
</NodeViewWrapper>
