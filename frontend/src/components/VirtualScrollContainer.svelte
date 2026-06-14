<script lang="ts">
  import { tick, untrack } from 'svelte'
  import { FetchPageBlocks } from '../../wailsjs/go/main/App.js'
  import TipTapEditor from './TipTapEditor.svelte'
  import type { ParsedBlock } from '../lib/editor'

  interface Props {
    notebook: string
    section: string
    page: string
    targetBlockId?: string
    targetKey?: string
    onBlockFocus?: (blockId: string, ancestors: string[]) => void
    onBlockBlur?: () => void
    activeFocusedBlockAncestors?: string[]
  }

  let {
    notebook,
    section,
    page,
    targetBlockId = '',
    targetKey = '',
    onBlockFocus,
    onBlockBlur,
    activeFocusedBlockAncestors = []
  }: Props = $props()

  let blocks = $state<ParsedBlock[]>([])
  let loading = $state(false)
  let loadError = $state('')
  let containerEl = $state<HTMLDivElement | null>(null)
  let handledTargetKey = $state('')

  $effect(() => {
    if (notebook && page) {
      untrack(() => loadPage())
    }
  })

  $effect(() => {
    if (targetBlockId && targetKey !== handledTargetKey) {
      scrollToBlock(targetKey)
    }
  })

  async function loadPage() {
    loading = true
    loadError = ''
    const reqNotebook = notebook
    const reqSection = section
    const reqPage = page
    try {
      const result = await FetchPageBlocks(reqNotebook, reqSection, reqPage)
      if (notebook !== reqNotebook || page !== reqPage) return
      blocks = result || []
    } catch (e) {
      if (notebook !== reqNotebook || page !== reqPage) return
      loadError = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  async function scrollToBlock(key: string) {
    handledTargetKey = key
    await tick()
    if (targetBlockId) {
      const el = document.querySelector(`[data-id="${targetBlockId}"]`)
      if (el instanceof HTMLElement) {
        el.scrollIntoView({ block: 'center', behavior: 'smooth' })
      }
    }
  }

  function handleBlocksUpdated(updatedBlocks: ParsedBlock[]) {
    blocks = updatedBlocks
  }
</script>

<div
  bind:this={containerEl}
  class="flex-1 overflow-y-auto px-12 py-10 custom-scrollbar bg-void flex flex-col min-h-0"
>
  <nav
    class="mb-6 flex items-center gap-2 text-text-muted font-label-sm text-label-sm"
  >
    <span>{notebook}</span>
    {#if section}
      <span class="material-symbols-outlined text-[14px]">chevron_right</span>
      <span>{section}</span>
    {/if}
    <span class="material-symbols-outlined text-[14px]">chevron_right</span>
    <span class="text-accent-primary-start">{page}</span>
  </nav>

  <header class="mb-8">
    <h1
      class="font-headline-lg text-headline-lg text-text-primary tracking-tight mb-2"
    >
      {page}
    </h1>
    <div class="flex items-center gap-3">
      <span
        class="bg-border-muted/50 border border-accent-secondary-start/20 text-accent-secondary-start px-2 py-0.5 rounded text-[10px] font-label-sm-bold uppercase tracking-wider"
      >
        {notebook}
      </span>
      {#if section}
        <span
          class="bg-border-muted/50 border border-accent-primary-start/20 text-accent-primary-start px-2 py-0.5 rounded text-[10px] font-label-sm-bold uppercase tracking-wider"
        >
          {section}
        </span>
      {/if}
    </div>
  </header>

  <div class="max-w-4xl w-full flex-1 flex flex-col gap-4">
    {#if loadError}
      <div
        class="text-error py-8 text-center font-body-md border border-error-border bg-error-bg rounded-lg flex flex-col items-center gap-3"
      >
        <div>Failed to load page: {loadError}</div>
        <button
          onclick={() => loadPage()}
          class="px-4 py-1.5 rounded-lg bg-error/20 border border-error-border text-error font-label-sm-bold hover:brightness-110 transition-all cursor-pointer"
        >
          Retry
        </button>
      </div>
    {:else if blocks.length === 0 && !loading}
      <div
        class="text-text-muted py-12 text-center font-body-md border border-dashed border-border-muted rounded-lg"
      >
        No blocks recorded for this page yet. Start typing below to add your
        first note!
      </div>
    {:else}
      <TipTapEditor
        {notebook}
        {section}
        {page}
        {blocks}
        {activeFocusedBlockAncestors}
        {onBlockFocus}
        {onBlockBlur}
        onUpdate={handleBlocksUpdated}
      />
    {/if}

    {#if loading}
      <div class="flex justify-center py-6">
        <span class="text-accent-primary-start font-body-md animate-pulse"
          >Loading...</span
        >
      </div>
    {/if}
  </div>
</div>
