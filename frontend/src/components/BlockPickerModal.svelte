<script lang="ts">
  import { onMount } from 'svelte'
  import { SearchBlocks } from '../../wailsjs/go/main/App.js'

  interface Props {
    onPick: (blockId: string) => void
    onClose: () => void
  }

  let { onPick, onClose }: Props = $props()

  let query = $state('')
  let results = $state<any[]>([])
  let selectedIdx = $state(0)
  let loading = $state(false)
  let inputEl = $state<HTMLInputElement | null>(null)

  let debounceTimer: any = null

  async function runSearch() {
    if (query.trim() === '') {
      results = []
      return
    }
    loading = true
    try {
      results = await SearchBlocks(query)
      selectedIdx = 0
    } catch (e) {
      console.error('BlockPicker search failed:', e)
      results = []
    } finally {
      loading = false
    }
  }

  function onInput() {
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(runSearch, 180)
  }

  function pick(res: any) {
    onPick(res.id)
    onClose()
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      selectedIdx = Math.min(selectedIdx + 1, results.length - 1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      selectedIdx = Math.max(selectedIdx - 1, 0)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (results[selectedIdx]) pick(results[selectedIdx])
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  onMount(() => {
    inputEl?.focus()
  })
</script>

<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  onclick={onClose}
  class="fixed inset-0 bg-black/60 backdrop-blur-sm z-[170] flex items-start justify-center pt-32"
>
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    onclick={(e) => e.stopPropagation()}
    class="w-full max-w-2xl glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex flex-col max-h-[500px]"
    style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--bg-panel) 92%, transparent);"
  >
    <div class="px-5 py-3 border-b border-border-muted">
      <h2 class="font-headline-md text-headline-md text-text-primary">
        Embed a block
      </h2>
      <p class="text-text-muted text-[12px] font-body-md mt-0.5">
        Search for the block to embed live.
      </p>
    </div>
    <div class="flex items-center gap-3 px-4 py-3 border-b border-border-muted">
      <span class="material-symbols-outlined text-text-muted text-[22px]"
        >search</span
      >
      <input
        bind:this={inputEl}
        bind:value={query}
        oninput={onInput}
        onkeydown={handleKeydown}
        type="text"
        placeholder="Search blocks to embed…"
        class="bg-transparent border-none outline-none text-text-primary text-[15px] font-body-md w-full focus:ring-0 placeholder:text-text-muted"
      />
      {#if loading}
        <span
          class="material-symbols-outlined text-accent-primary-start animate-spin text-[20px]"
          >sync</span
        >
      {/if}
    </div>
    <div class="flex-1 overflow-y-auto custom-scrollbar py-2">
      {#if query.trim() === ''}
        <div class="text-text-muted text-center py-10 font-body-md">
          Type to search for a block…
        </div>
      {:else if results.length === 0 && !loading}
        <div class="text-text-muted text-center py-10 font-body-md">
          No blocks found.
        </div>
      {:else}
        {#each results as res, idx (res.id)}
          <button
            onclick={() => pick(res)}
            class="w-full px-5 py-3 border-none flex flex-col gap-1 text-left cursor-pointer transition-colors focus:outline-none"
            class:bg-accent-primary-glow={idx === selectedIdx}
          >
            <div
              class="flex items-center gap-1.5 text-[10px] text-text-muted uppercase tracking-widest font-label-sm-bold"
            >
              <span>{res.notebook}</span>
              <span class="material-symbols-outlined text-[10px]"
                >chevron_right</span
              >
              <span>{res.section}</span>
              <span class="material-symbols-outlined text-[10px]"
                >chevron_right</span
              >
              <span>{res.page}</span>
            </div>
            <div class="font-body-md text-sm text-text-primary truncate">
              {res.clean_content}
            </div>
          </button>
        {/each}
      {/if}
    </div>
  </div>
</div>
