<script lang="ts">
  import { fly } from 'svelte/transition'
  import type { KanbanFilters } from './types'

  interface Props {
    filters: KanbanFilters
    owners: string[]
    tags: string[]
    onFiltersChange: (f: KanbanFilters) => void
  }

  let { filters, owners, tags, onFiltersChange }: Props = $props()

  type ChipKey = 'owner' | 'priority' | 'dueDate' | 'tags'
  let openChip = $state<ChipKey | null>(null)

  function toggleChip(k: ChipKey) {
    openChip = openChip === k ? null : k
  }
  function close() {
    openChip = null
  }

  const PRIORITIES = [
    { value: 1, label: 'Critical' },
    { value: 2, label: 'Normal' },
    { value: 3, label: 'Low' }
  ]
  const DUE_OPTIONS: { value: KanbanFilters['dueDate']; label: string }[] = [
    { value: '', label: 'All' },
    { value: 'overdue', label: 'Overdue' },
    { value: 'today', label: 'Today' },
    { value: 'week', label: 'This Week' },
    { value: 'none', label: 'No Date' }
  ]

  function toggleOwner(o: string) {
    const has = filters.owners.includes(o)
    onFiltersChange({
      ...filters,
      owners: has
        ? filters.owners.filter((x) => x !== o)
        : [...filters.owners, o]
    })
  }
  function togglePriority(p: number) {
    const has = filters.priorities.includes(p)
    onFiltersChange({
      ...filters,
      priorities: has
        ? filters.priorities.filter((x) => x !== p)
        : [...filters.priorities, p]
    })
  }
  function setDueDate(d: KanbanFilters['dueDate']) {
    onFiltersChange({ ...filters, dueDate: d })
  }
  function toggleTag(t: string) {
    const has = filters.tags.includes(t)
    onFiltersChange({
      ...filters,
      tags: has ? filters.tags.filter((x) => x !== t) : [...filters.tags, t]
    })
  }
  function clearAll() {
    onFiltersChange({ owners: [], priorities: [], dueDate: '', tags: [] })
  }

  // A chip counts as "active" if it has at least one selection; the Clear-all
  // affordance appears once any chip is active.
  let activeCount = $derived(
    (filters.owners.length ? 1 : 0) +
      (filters.priorities.length ? 1 : 0) +
      (filters.dueDate ? 1 : 0) +
      (filters.tags.length ? 1 : 0)
  )

  function dueLabel(): string {
    return DUE_OPTIONS.find((o) => o.value === filters.dueDate)?.label ?? 'All'
  }

  // Escape closes the open chip popover. Bound to window while a chip is
  // open so it works regardless of where focus lives inside the popover
  // (the click-away backdrop is tabindex="-1" and rarely receives keydown).
  $effect(() => {
    if (!openChip) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })
</script>

<div
  class="flex items-center gap-2 px-6 py-2 border-b border-border-muted flex-wrap relative"
>
  <!-- Owner chip -->
  <div class="relative">
    <button
      type="button"
      onclick={() => toggleChip('owner')}
      aria-expanded={openChip === 'owner'}
      aria-haspopup="true"
      class="flex items-center gap-1.5 px-2.5 py-1 rounded border border-border-muted bg-surface text-[12px] font-label-sm text-text-muted hover:bg-hover hover:text-text-primary transition-colors {openChip ===
        'owner' || filters.owners.length
        ? 'border-accent-primary-start/40 text-text-primary'
        : ''}"
    >
      <span class="material-symbols-outlined text-[14px]">person</span>
      <span
        >Owner{filters.owners.length ? ` (${filters.owners.length})` : ''}</span
      >
      <span class="material-symbols-outlined text-[12px]">expand_more</span>
    </button>
    {#if openChip === 'owner'}
      <div
        transition:fly={{ y: -4, duration: 100 }}
        class="absolute z-50 mt-1 min-w-[180px] bg-panel border border-border-muted rounded-lg shadow-xl py-1 max-h-64 overflow-y-auto custom-scrollbar"
        role="listbox"
        aria-label="Filter by owner"
      >
        {#if owners.length === 0}
          <div class="px-3 py-2 text-[11px] text-text-muted font-label-sm">
            No owners
          </div>
        {:else}
          {#each owners as o (o)}
            <label
              class="flex items-center gap-2 px-3 py-1.5 hover:bg-hover cursor-pointer text-[12px] font-label-sm text-text-primary"
            >
              <input
                type="checkbox"
                checked={filters.owners.includes(o)}
                onchange={() => toggleOwner(o)}
                class="accent-accent-primary-start"
              />
              <span class="truncate">{o}</span>
            </label>
          {/each}
        {/if}
      </div>
    {/if}
  </div>

  <!-- Priority chip -->
  <div class="relative">
    <button
      type="button"
      onclick={() => toggleChip('priority')}
      aria-expanded={openChip === 'priority'}
      aria-haspopup="true"
      class="flex items-center gap-1.5 px-2.5 py-1 rounded border border-border-muted bg-surface text-[12px] font-label-sm text-text-muted hover:bg-hover hover:text-text-primary transition-colors {openChip ===
        'priority' || filters.priorities.length
        ? 'border-accent-primary-start/40 text-text-primary'
        : ''}"
    >
      <span class="material-symbols-outlined text-[14px]">flag</span>
      <span
        >Priority{filters.priorities.length
          ? ` (${filters.priorities.length})`
          : ''}</span
      >
      <span class="material-symbols-outlined text-[12px]">expand_more</span>
    </button>
    {#if openChip === 'priority'}
      <div
        transition:fly={{ y: -4, duration: 100 }}
        class="absolute z-50 mt-1 min-w-[160px] bg-panel border border-border-muted rounded-lg shadow-xl py-1"
        role="listbox"
        aria-label="Filter by priority"
      >
        {#each PRIORITIES as p (p.value)}
          <label
            class="flex items-center gap-2 px-3 py-1.5 hover:bg-hover cursor-pointer text-[12px] font-label-sm text-text-primary"
          >
            <input
              type="checkbox"
              checked={filters.priorities.includes(p.value)}
              onchange={() => togglePriority(p.value)}
              class="accent-accent-primary-start"
            />
            <span>{p.label}</span>
          </label>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Due date chip -->
  <div class="relative">
    <button
      type="button"
      onclick={() => toggleChip('dueDate')}
      aria-expanded={openChip === 'dueDate'}
      aria-haspopup="listbox"
      class="flex items-center gap-1.5 px-2.5 py-1 rounded border border-border-muted bg-surface text-[12px] font-label-sm text-text-muted hover:bg-hover hover:text-text-primary transition-colors {openChip ===
        'dueDate' || filters.dueDate
        ? 'border-accent-primary-start/40 text-text-primary'
        : ''}"
    >
      <span class="material-symbols-outlined text-[14px]">schedule</span>
      <span>{filters.dueDate ? dueLabel() : 'Due date'}</span>
      <span class="material-symbols-outlined text-[12px]">expand_more</span>
    </button>
    {#if openChip === 'dueDate'}
      <div
        transition:fly={{ y: -4, duration: 100 }}
        class="absolute z-50 mt-1 min-w-[160px] bg-panel border border-border-muted rounded-lg shadow-xl py-1"
        role="listbox"
        aria-label="Filter by due date"
      >
        {#each DUE_OPTIONS as opt (opt.value)}
          <button
            type="button"
            onclick={() => setDueDate(opt.value)}
            class="w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-hover text-[12px] font-label-sm {filters.dueDate ===
            opt.value
              ? 'text-accent-primary-start'
              : 'text-text-primary'}"
          >
            <span>{opt.label}</span>
            {#if filters.dueDate === opt.value}
              <span class="material-symbols-outlined text-[14px] ml-auto"
                >check</span
              >
            {/if}
          </button>
        {/each}
      </div>
    {/if}
  </div>

  <!-- Tags chip -->
  <div class="relative">
    <button
      type="button"
      onclick={() => toggleChip('tags')}
      aria-expanded={openChip === 'tags'}
      aria-haspopup="true"
      class="flex items-center gap-1.5 px-2.5 py-1 rounded border border-border-muted bg-surface text-[12px] font-label-sm text-text-muted hover:bg-hover hover:text-text-primary transition-colors {openChip ===
        'tags' || filters.tags.length
        ? 'border-accent-primary-start/40 text-text-primary'
        : ''}"
    >
      <span class="material-symbols-outlined text-[14px]">label</span>
      <span>Tags{filters.tags.length ? ` (${filters.tags.length})` : ''}</span>
      <span class="material-symbols-outlined text-[12px]">expand_more</span>
    </button>
    {#if openChip === 'tags'}
      <div
        transition:fly={{ y: -4, duration: 100 }}
        class="absolute z-50 mt-1 min-w-[200px] bg-panel border border-border-muted rounded-lg shadow-xl py-1 max-h-64 overflow-y-auto custom-scrollbar"
        role="listbox"
        aria-label="Filter by tag"
      >
        {#if tags.length === 0}
          <div class="px-3 py-2 text-[11px] text-text-muted font-label-sm">
            No tags
          </div>
        {:else}
          {#each tags as t (t)}
            <label
              class="flex items-center gap-2 px-3 py-1.5 hover:bg-hover cursor-pointer text-[12px] font-label-sm text-text-primary"
            >
              <input
                type="checkbox"
                checked={filters.tags.includes(t)}
                onchange={() => toggleTag(t)}
                class="accent-accent-primary-start"
              />
              <span class="truncate">{t}</span>
            </label>
          {/each}
        {/if}
      </div>
    {/if}
  </div>

  {#if activeCount > 0}
    <button
      type="button"
      onclick={clearAll}
      class="flex items-center gap-1 px-2 py-1 text-[12px] font-label-sm text-text-muted hover:text-error transition-colors"
    >
      <span class="material-symbols-outlined text-[14px]">close</span>
      <span>Clear all</span>
    </button>
  {/if}

  {#if activeCount > 0}
    <span class="ml-auto text-[11px] text-text-muted font-label-sm">
      {activeCount} active filter{activeCount === 1 ? '' : 's'}
    </span>
  {/if}

  <!-- Click-away backdrop: closes whichever chip popover is open. Sits below
       the popovers (z-40) but above page content so any outside click closes. -->
  {#if openChip}
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div
      class="fixed inset-0 z-40"
      role="presentation"
      onclick={close}
      tabindex="-1"
      aria-hidden="true"
    ></div>
  {/if}
</div>
