<script lang="ts">
  // Default NodeView for the generic embedBlock node (#110). When `openable`
  // is true (e.g. attachments), the card is fully interactive: click or
  // Enter/Space opens the file in the OS native handler; the delete button
  // removes the block; the drag handle is keyboard-accessible (#101).
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'
  import { OpenAttachment } from '../../../wailsjs/go/main/App.js'
  import { getActiveLocation } from '../../plugins/location.svelte'

  let { node, deleteNode, selected }: NodeViewProps = $props()
  const attrs = $derived(node.attrs as Record<string, any>)
  const cardClass = $derived(
    selected
      ? 'border-accent-primary-start/60 bg-accent-primary-glow'
      : 'border-border-muted bg-surface/60'
  )
  // Read the live active location so the click-to-open path always has a
  // notebook to pass to OpenAttachment, even if the user has navigated away
  // from the page that owns the embed block. The marker also carries the
  // originating notebook (#101 review) as a defence-in-depth fallback.
  const loc = getActiveLocation()
  const resolvedNotebook = $derived((attrs.notebook as string) || loc.notebook)
  const isOpenable = $derived(!!attrs.openable && !!attrs.src)

  async function open() {
    if (!isOpenable) return
    try {
      await OpenAttachment(resolvedNotebook, attrs.src)
    } catch (e) {
      console.error('[embed-block] open failed:', e)
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (isOpenable && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault()
      open()
    }
  }
</script>

<NodeViewWrapper data-id={node.attrs.id}>
  <!--
    Card is always a <div> with an explicit role: role="button" when openable
    (so it has a nonnegative tabindex and the click-to-open handler), or
    role="img" when read-only (so a11y_no_noninteractive_tabindex is satisfied
    and AT treats it as a non-interactive figure). The inner delete is a real
    <button> so the native semantics compose without nesting interactive
    elements. Svelte's a11y rule a11y_no_noninteractive_tabindex cannot
    statically verify a dynamic role; we therefore render two separate
    branches (one per role) so the linter can verify each path.
  -->
  {#if isOpenable}
    <div
      class="group embed-block-default my-2 p-3 rounded-lg border transition-colors flex items-center gap-3 {cardClass}"
      role="button"
      tabindex="0"
      aria-label={attrs.caption || `${attrs.embedType}: ${attrs.src}`}
      data-embed-type={attrs.embedType}
      data-openable="true"
      onclick={open}
      onkeydown={handleKeydown}
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
      <button
        type="button"
        onclick={(e) => {
          e.stopPropagation()
          deleteNode()
        }}
        title="Remove block"
        aria-label="Remove block"
        class="text-text-muted hover:text-status-danger border-none bg-transparent cursor-pointer p-1 rounded transition-colors"
      >
        <span class="material-symbols-outlined text-[18px]">delete</span>
      </button>
      <span
        class="silt-drag-handle-inline material-symbols-outlined text-text-muted text-[16px] opacity-0 group-hover:opacity-100 transition-opacity duration-150"
        title="Drag to reorder"
        aria-label="Drag handle"
        spellcheck="false"
        draggable="true"
        aria-hidden="true"
        data-drag-handle
      >
        drag_indicator
      </span>
    </div>
  {:else}
    <div
      class="group embed-block-default my-2 p-3 rounded-lg border transition-colors flex items-center gap-3 {cardClass}"
      role="img"
      aria-label={attrs.caption || `${attrs.embedType}: ${attrs.src}`}
      data-embed-type={attrs.embedType}
      data-openable="false"
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
      <button
        type="button"
        onclick={(e) => {
          e.stopPropagation()
          deleteNode()
        }}
        title="Remove block"
        aria-label="Remove block"
        class="text-text-muted hover:text-status-danger border-none bg-transparent cursor-pointer p-1 rounded transition-colors"
      >
        <span class="material-symbols-outlined text-[18px]">delete</span>
      </button>
      <span
        class="silt-drag-handle-inline material-symbols-outlined text-text-muted text-[16px] opacity-0 group-hover:opacity-100 transition-opacity duration-150"
        title="Drag to reorder"
        aria-label="Drag handle"
        spellcheck="false"
        draggable="true"
        aria-hidden="true"
        data-drag-handle
      >
        drag_indicator
      </span>
    </div>
  {/if}
</NodeViewWrapper>
