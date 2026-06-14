<script lang="ts">
  import { onMount } from 'svelte'
  import { SearchBlocks } from '../../wailsjs/go/main/App.js'

  interface Props {
    onClose: () => void
    onJump: (res: any) => void
  }

  let { onClose, onJump }: Props = $props()

  let query = $state('')
  let results = $state<any[]>([])
  let selectedIdx = $state(0)
  let inputEl = $state<HTMLInputElement | null>(null)
  let loading = $state(false)

  // Re-run search whenever query text changes
  $effect(() => {
    const trimmed = query.trim()
    if (!trimmed) {
      results = []
      selectedIdx = 0
      loading = false
      return
    }

    const timeout = window.setTimeout(() => {
      performSearch(trimmed)
    }, 175)

    return () => window.clearTimeout(timeout)
  })

  async function performSearch(q: string) {
    const trimmed = q.trim()

    loading = true
    try {
      const hits = await SearchBlocks(trimmed)
      if (query.trim() === trimmed) {
        results = hits || []
        selectedIdx = 0
      }
    } catch (e) {
      console.error('Search query failed:', e)
    } finally {
      if (query.trim() === trimmed) {
        loading = false
      }
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (results.length > 0) {
        selectedIdx = (selectedIdx + 1) % results.length
      }
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (results.length > 0) {
        selectedIdx = (selectedIdx - 1 + results.length) % results.length
      }
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (results[selectedIdx]) {
        selectResult(results[selectedIdx])
      }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }

  function selectResult(res: any) {
    onJump(res)
    onClose()
  }

  onMount(() => {
    if (inputEl) {
      inputEl.focus()
    }

    // Attach keyboard listener
    window.addEventListener('keydown', handleKeyDown, true)
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true)
    }
  })
</script>

<!-- Backdrop overlay -->
<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
<div
  onclick={onClose}
  class="fixed inset-0 bg-black/60 backdrop-blur-sm z-[150] flex items-start justify-center pt-28"
>
  <!-- Modal Frame (Frosted Glass Panel) -->
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div
    onclick={(e) => e.stopPropagation()}
    class="w-full max-w-2xl glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex flex-col max-h-[500px]"
    style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--bg-panel) 80%, transparent);"
  >
    <!-- Search Input Area -->
    <div
      class="flex items-center gap-3 px-4 py-4 border-b border-border-muted bg-void/30"
    >
      <span
        class="material-symbols-outlined text-text-muted text-[22px] select-none"
        >search</span
      >
      <input
        bind:this={inputEl}
        bind:value={query}
        type="text"
        placeholder="Fuzzy search notebooks, sections, or task content..."
        class="bg-transparent border-none outline-none text-text-primary text-[15px] font-body-md w-full focus:ring-0 placeholder:text-text-muted"
      />
      {#if loading}
        <span
          class="material-symbols-outlined text-accent-primary-start animate-spin text-[20px] select-none"
        >
          sync
        </span>
      {/if}
    </div>

    <!-- Search Results List -->
    <div class="flex-1 overflow-y-auto custom-scrollbar py-2">
      {#if query.trim() === ''}
        <div class="text-text-muted text-center py-10 font-body-md select-none">
          Type queries to find headers, notes, or checklist items...
        </div>
      {:else if results.length === 0 && !loading}
        <div class="text-text-muted text-center py-10 font-body-md select-none">
          No matches found for "{query}"
        </div>
      {:else}
        {#each results as res, idx}
          <button
            onclick={() => selectResult(res)}
            class="w-full px-5 py-3 border-none flex flex-col gap-1 text-left cursor-pointer transition-colors focus:outline-none"
            class:bg-accent-primary-glow={idx === selectedIdx}
            class:border-l-2={idx === selectedIdx}
            class:border-accent-primary-start={idx === selectedIdx}
          >
            <!-- Breadcrumb metadata -->
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
              <span class="text-accent-primary-start">{res.file_date}</span>
            </div>

            <!-- Content preview -->
            <div
              class="font-body-md text-sm text-text-primary flex items-center gap-2"
            >
              {#if res.status}
                <span
                  class="material-symbols-outlined text-[16px] text-accent-primary-start select-none"
                >
                  {res.status === 'DONE'
                    ? 'check_circle'
                    : 'radio_button_unchecked'}
                </span>
              {/if}
              <span>{res.clean_content}</span>
            </div>
          </button>
        {/each}
      {/if}
    </div>
  </div>
</div>
