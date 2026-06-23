<script lang="ts">
  import { onMount } from 'svelte'
  import { SearchBlocksPaged } from '../../wailsjs/go/main/App.js'

  interface TaskResult {
    id: string
    notebook: string
    section: string
    page: string
    file_date: string
    clean_content: string
    status?: string
    snippet?: string
  }
  interface SearchResult {
    results: TaskResult[]
    total: number
    offset: number
    limit: number
    has_more: boolean
  }

  interface Props {
    onClose: () => void
    onJump: (res: TaskResult) => void
  }

  let { onClose, onJump }: Props = $props()

  let query = $state('')
  let results = $state<TaskResult[]>([])
  let selectedIdx = $state(0)
  let inputEl = $state<HTMLInputElement | null>(null)
  let listEl = $state<HTMLDivElement | null>(null)
  let loading = $state(false)
  let total = $state(0)
  let hasMore = $state(false)
  let offset = $state(0)
  const pageSize = 20

  // Re-run search whenever query text changes (debounced). Resets to the first
  // page so the modal always shows the top-ranked matches for the current text.
  $effect(() => {
    const trimmed = query.trim()
    if (!trimmed) {
      results = []
      selectedIdx = 0
      total = 0
      hasMore = false
      offset = 0
      loading = false
      return
    }

    const timeout = window.setTimeout(() => {
      offset = 0
      performSearch(trimmed, 0, /*replace=*/ true)
    }, 175)

    return () => window.clearTimeout(timeout)
  })

  async function performSearch(q: string, off: number, replace: boolean) {
    loading = true
    try {
      const res: SearchResult = await SearchBlocksPaged(q, off, pageSize)
      // Guard against a stale response landing after a newer query/offset.
      if (query.trim() !== q) return
      if (replace) {
        results = res.results || []
        selectedIdx = 0
      } else {
        results = [...results, ...(res.results || [])]
      }
      total = res.total
      hasMore = res.has_more
      offset = off
    } catch (e) {
      console.error('Search query failed:', e)
    } finally {
      if (query.trim() === q) loading = false
    }
  }

  function loadMore() {
    if (loading || !hasMore) return
    performSearch(query.trim(), offset + pageSize, false)
  }

  function handleListScroll() {
    if (!listEl || loading || !hasMore) return
    const { scrollTop, scrollHeight, clientHeight } = listEl
    // Trigger the next page when the user scrolls within ~120px of the bottom.
    if (scrollHeight - scrollTop - clientHeight < 120) {
      loadMore()
    }
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      if (results.length > 0) {
        selectedIdx = (selectedIdx + 1) % results.length
        scrollSelectedIntoView()
      }
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      if (results.length > 0) {
        selectedIdx = (selectedIdx - 1 + results.length) % results.length
        scrollSelectedIntoView()
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

  function scrollSelectedIntoView() {
    // Defer until after the selectedIdx class flips the DOM.
    queueMicrotask(() => {
      if (!listEl) return
      const el = listEl.querySelector(
        `[data-idx="${selectedIdx}"]`
      ) as HTMLElement | null
      el?.scrollIntoView({ block: 'nearest' })
    })
  }

  function selectResult(res: TaskResult) {
    onJump(res)
    onClose()
  }

  // sanitizeSnippet HTML-escapes the FTS5 snippet, then restores ONLY the
  // <mark>/</mark> highlight tags the snippet() function emits. This keeps
  // user-authored note text from injecting arbitrary HTML into the modal
  // while still rendering the relevance highlight.
  //
  // CSP context (#237, F2): the host-webview CSP does NOT enable
  // `require-trusted-types-for 'script'`, so this @html sink works without
  // a Trusted Types policy. If a future tightening enables Trusted Types,
  // wrap the returned string in a `policy.createHTML(...)` call (the
  // Svelte 5 compiler translates @html to element.innerHTML).
  function sanitizeSnippet(snip: string): string {
    if (!snip) return ''
    const esc = snip
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
    return esc.replace(/&lt;\/?mark&gt;/g, (m) =>
      m.includes('/') ? '</mark>' : '<mark>'
    )
  }

  onMount(() => {
    if (inputEl) {
      inputEl.focus()
    }

    window.addEventListener('keydown', handleKeyDown, true)
    return () => {
      window.removeEventListener('keydown', handleKeyDown, true)
    }
  })
</script>

<!-- Positioning wrapper (scrim + dialog as siblings per SettingsShell pattern) -->
<div
  class="fixed inset-0 bg-black/40 backdrop-blur-[2px] z-[150] flex items-start justify-center pt-28"
>
  <button
    tabindex="-1"
    aria-label="Close search"
    onclick={onClose}
    class="absolute inset-0 cursor-default border-none p-0 bg-transparent"
  ></button>
  <!-- Modal Frame (Frosted Glass Panel) -->
  <div
    role="dialog"
    aria-modal="true"
    aria-label="Search blocks"
    tabindex="-1"
    class="relative w-full max-w-2xl glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex flex-col max-h-[500px]"
    style="backdrop-filter: blur(16px) saturate(140%); background: color-mix(in srgb, var(--color-panel) 80%, transparent);"
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
        placeholder="Search notebooks, sections, or task content..."
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
    <div
      bind:this={listEl}
      onscroll={handleListScroll}
      class="flex-1 overflow-y-auto custom-scrollbar py-2"
    >
      {#if query.trim() === ''}
        <div class="text-text-muted text-center py-10 font-body-md select-none">
          Type queries to find headers, notes, or checklist items...
        </div>
      {:else if results.length === 0 && !loading}
        <div class="text-text-muted text-center py-10 font-body-md select-none">
          No matches found for "{query}"
        </div>
      {:else}
        {#each results as res, idx (res.id + idx)}
          <button
            data-idx={idx}
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
              <span>{res.page}</span>
              <span class="material-symbols-outlined text-[10px]"
                >chevron_right</span
              >
              <span class="text-accent-primary-start">{res.file_date}</span>
            </div>

            <!-- Content preview with FTS5 highlight snippet -->
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
              {#if res.snippet}
                <!-- Sanitized in-script: only <mark> tags from FTS5 survive. -->
                <span>{@html sanitizeSnippet(res.snippet)}</span>
              {:else}
                <span>{res.clean_content}</span>
              {/if}
            </div>
          </button>
        {/each}

        {#if hasMore}
          <div
            class="text-text-muted text-center py-3 text-[11px] font-body-md select-none"
          >
            {loading ? 'Loading more…' : 'Scroll for more results'}
          </div>
        {/if}
      {/if}
    </div>

    <!-- Result count footer -->
    {#if query.trim() !== '' && total > 0}
      <div
        class="px-4 py-2 border-t border-border-muted text-[10px] text-text-muted font-label-sm flex items-center justify-between bg-void/30"
      >
        <span>{total} match{total === 1 ? '' : 'es'}</span>
        <span class="opacity-60">↑↓ navigate · ⏎ open · esc close</span>
      </div>
    {/if}
  </div>
</div>

<style>
  :global(mark) {
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start) 30%,
      transparent
    );
    color: var(--color-text-primary);
    border-radius: 3px;
    padding: 0 2px;
  }
</style>
