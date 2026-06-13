<script lang="ts">
  import { tick, untrack } from 'svelte'
  import { FetchSectionTimeline } from '../../wailsjs/go/main/App.js'
  import BlockRenderer from './BlockRenderer.svelte'

  interface Props {
    notebook: string
    section: string
    targetDate?: string
    targetBlockId?: string
    targetKey?: string
    onBlockFocus?: (blockId: string, ancestors: string[]) => void
    onBlockBlur?: () => void
    activeFocusedBlockAncestors?: string[]
  }

  let {
    notebook,
    section,
    targetDate = '',
    targetBlockId = '',
    targetKey = '',
    onBlockFocus,
    onBlockBlur,
    activeFocusedBlockAncestors = []
  }: Props = $props()

  let visibleGroups = $state<any[]>([])
  let offset = $state(0)
  let limit = 5 // Page size (number of day groups to load per batch)
  let loading = $state(false)
  let hasMore = $state(true)
  let loadError = $state('')
  let containerEl = $state<HTMLDivElement | null>(null)
  let handledTargetKey = $state('')

  // Reload timeline when notebook or section changes.
  //
  // resetTimeline() is async and, through loadMoreDays(), reads reactive state
  // (loading/hasMore) synchronously. Running it inside the effect's tracking
  // scope would make those reads effect dependencies, re-triggering the effect
  // on every loading/hasMore flip and producing an infinite reset/refetch loop
  // (the "Loading logs..." hang). untrack() limits the effect to only
  // notebook/section.
  $effect(() => {
    if (notebook && section) {
      untrack(() => resetTimeline())
    }
  })

  $effect(() => {
    if (targetDate && targetBlockId && targetKey !== handledTargetKey) {
      loadTargetBlock(targetKey)
    }
  })

  async function resetTimeline() {
    visibleGroups = []
    offset = 0
    hasMore = true
    loadError = ''
    await loadMoreDays()
  }

  async function loadMoreDays(): Promise<number> {
    if (loading || !hasMore) return 0
    loading = true
    loadError = ''
    let loadedCount = 0
    let succeeded = false

    try {
      const newDays = await FetchSectionTimeline(
        notebook,
        section,
        offset,
        limit
      )
      succeeded = true
      if (!newDays || newDays.length === 0) {
        hasMore = false
      } else {
        loadedCount = newDays.length
        visibleGroups = [...visibleGroups, ...newDays]
        offset += newDays.length
        if (newDays.length < limit) {
          hasMore = false
        }
      }
    } catch (e) {
      // Surface the failure to the user instead of swallowing it. Halt
      // pagination so a persistent backend error cannot drive an unbounded
      // retry loop via the viewport-fill recursion below.
      loadError = e instanceof Error ? e.message : String(e)
      hasMore = false
    } finally {
      loading = false
      await tick()
      // Only keep filling the viewport after a successful load with more
      // data available. Errors must not trigger a retry cascade.
      if (
        succeeded &&
        containerEl &&
        containerEl.scrollHeight <= containerEl.clientHeight &&
        hasMore
      ) {
        void loadMoreDays()
      }
    }

    return loadedCount
  }

  async function loadTargetBlock(key: string) {
    handledTargetKey = key

    while (
      targetKey === key &&
      targetDate &&
      !visibleGroups.some((group) => group.date === targetDate) &&
      hasMore
    ) {
      if (loading) {
        await new Promise((resolve) => setTimeout(resolve, 25))
        continue
      }
      const loadedCount = await loadMoreDays()
      if (loadedCount === 0 && !hasMore) break
    }

    await tick()
    if (targetKey !== key || !targetBlockId) return

    const el = document.getElementById(`editable-${targetBlockId}`)
    if (el instanceof HTMLElement) {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' })
      el.focus()
    }
  }

  function handleScroll() {
    if (!containerEl) return
    const { scrollTop, scrollHeight, clientHeight } = containerEl
    // Load more days if we are within 250px of the bottom
    if (scrollHeight - scrollTop - clientHeight < 250) {
      loadMoreDays()
    }
  }

  // Handle local block updates (e.g. checkbox clicks or typing updates)
  function handleBlockUpdated(date: string, updatedBlocks: any[]) {
    visibleGroups = visibleGroups.map((g) => {
      if (g.date === date) {
        return { ...g, blocks: updatedBlocks }
      }
      return g
    })
  }
</script>

<div
  bind:this={containerEl}
  onscroll={handleScroll}
  class="flex-1 overflow-y-auto px-12 py-10 custom-scrollbar bg-void flex flex-col min-h-0"
>
  <!-- Header/Breadcrumbs -->
  <nav
    class="mb-6 flex items-center gap-2 text-text-muted font-label-sm text-label-sm"
  >
    <span>Notes</span>
    <span class="material-symbols-outlined text-[14px]">chevron_right</span>
    <span>{notebook}</span>
    <span class="material-symbols-outlined text-[14px]">chevron_right</span>
    <span class="text-accent-teal-start">{section}</span>
  </nav>

  <header class="mb-8">
    <h1
      class="font-headline-lg text-headline-lg text-text-primary tracking-tight mb-2"
    >
      {section} Timeline
    </h1>
    <div class="flex items-center gap-3">
      <span
        class="bg-[#1e1e23]/50 border border-accent-indigo-start/20 text-accent-indigo-start px-2 py-0.5 rounded text-[10px] font-label-sm-bold uppercase tracking-wider"
      >
        {notebook}
      </span>
      <span
        class="bg-[#1e1e23]/50 border border-accent-teal-start/20 text-accent-teal-start px-2 py-0.5 rounded text-[10px] font-label-sm-bold uppercase tracking-wider"
      >
        Active Stream
      </span>
    </div>
  </header>

  <div class="max-w-4xl w-full flex-1 flex flex-col gap-8">
    {#if loadError}
      <div
        class="text-error py-8 text-center font-body-md border border-error-border bg-error-bg rounded-lg flex flex-col items-center gap-3"
      >
        <div>Failed to load logs: {loadError}</div>
        <button
          onclick={() => resetTimeline()}
          class="px-4 py-1.5 rounded-lg bg-error/20 border border-error-border text-error font-label-sm-bold hover:brightness-110 transition-all cursor-pointer"
        >
          Retry
        </button>
      </div>
    {:else if visibleGroups.length === 0 && !loading}
      <div
        class="text-text-muted py-12 text-center font-body-md border border-dashed border-border-muted rounded-lg"
      >
        No logs recorded for this section yet. Start typing below to add your
        first note!
      </div>
    {:else}
      {#each visibleGroups as group (group.date)}
        <section
          class="mb-8 pl-4 relative group/day border-l border-border-muted"
        >
          <!-- Date Sticky Header -->
          <h2
            class="text-accent-teal-start font-bold text-headline-md font-headline-md mb-6 sticky top-0 bg-void py-2 z-10"
          >
            {group.formattedDate}
          </h2>

          <div class="space-y-1">
            {#each group.blocks as block, idx (block.id)}
              <BlockRenderer
                {block}
                {notebook}
                {section}
                fileDate={group.date}
                siblings={group.blocks}
                blockIndex={idx}
                {activeFocusedBlockAncestors}
                {onBlockFocus}
                {onBlockBlur}
                onUpdate={(newBlocks) =>
                  handleBlockUpdated(group.date, newBlocks)}
              />
            {/each}
          </div>
        </section>
      {/each}
    {/if}

    {#if loading}
      <div class="flex justify-center py-6">
        <span class="text-accent-teal-start font-body-md animate-pulse"
          >Loading logs...</span
        >
      </div>
    {/if}
  </div>
</div>
