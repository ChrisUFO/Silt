<script lang="ts">
  // AgendaList — the agenda-style grouped task view extracted from the
  // legacy Agenda.svelte plugin (#322). Renders inside Calendar.svelte
  // when the unified view mode is 'agenda'. Owns the same Overdue / Today /
  // Tomorrow / Upcoming grouping, the same markDone + openItem behavior,
  // and the same refresh-on-block:changed reactivity.
  //
  // The grouping compares against `today` (the SDK's local-day anchor,
  // #118) so the buckets match the user's local midnight, not UTC.
  import { onMount, onDestroy, tick } from 'svelte'
  import type { PluginContext, PluginManifest } from '../../sdk'
  import { plusDaysISO } from '../../sdk'

  interface Props {
    ctx: PluginContext
    manifest?: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  interface AgendaItem {
    id: string
    notebook: string
    section: string
    page: string
    file_date: string
    clean_content: string
    status: string
    owner: string
    start_date: string
    due_date: string
    priority: number
  }

  let items = $state<AgendaItem[]>([])
  let loading = $state(true)
  let errorMsg = $state('')
  let markDoneError = $state('')
  let markDoneTimer: ReturnType<typeof setTimeout> | null = null

  async function reload() {
    loading = true
    errorMsg = ''
    try {
      const { rows } = await ctx.sqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.file_date, b.line_number,
                b.clean_content, t.status, t.owner, t.start_date, t.due_date, t.priority
         FROM blocks b JOIN tasks t ON b.id = t.block_id
         WHERE t.status != 'DONE'
           AND (t.due_date IS NOT NULL AND t.due_date != '')
         ORDER BY t.due_date ASC, t.priority ASC
         LIMIT 500`
      )
      items = (rows as unknown as AgendaItem[]) ?? []
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  // The local-day anchors (`today` / `tomorrow` / `weekAhead`) drive
  // the bucket grouping below. They depend on `ctx.today` (an SDK
  // getter — see sdk.ts:82), but ctx.today is a plain getter that
  // re-evaluates only when its reactive deps change. To re-bucket
  // across midnight when the agenda view stays mounted, we maintain
  // a 60s `nowTick` and depend on it (mirrors Calendar.svelte:50-53's
  // pattern). Without this, a user with the agenda open at 23:59
  // would see today's bucket sit on yesterday's date until they
  // remounted.
  let nowTick = $state(0)
  let nowInterval: ReturnType<typeof setInterval> | undefined

  onMount(() => {
    nowInterval = setInterval(() => {
      nowTick++
    }, 60_000)
  })
  onDestroy(() => {
    if (nowInterval) clearInterval(nowInterval)
  })

  let today = $derived.by(() => {
    void nowTick
    return ctx.today
  })
  let tomorrow = $derived(plusDaysISO(ctx.today, 1))
  let weekAhead = $derived(plusDaysISO(ctx.today, 7))

  let overdue = $derived(items.filter((i) => i.due_date < today))
  let todayItems = $derived(items.filter((i) => i.due_date === today))
  let tomorrowItems = $derived(items.filter((i) => i.due_date === tomorrow))
  let upcoming = $derived(
    items
      .filter((i) => i.due_date > tomorrow)
      .sort((a, b) => a.due_date.localeCompare(b.due_date))
  )

  async function markDone(item: AgendaItem) {
    markDoneError = ''
    if (markDoneTimer) clearTimeout(markDoneTimer)
    try {
      await ctx.updateBlockState(item.id, 'DONE')
      // Only remove from the list once the backend confirmed the change;
      // otherwise the UI would drift from the index on failure.
      items = items.filter((i) => i.id !== item.id)
    } catch (e) {
      markDoneError = e instanceof Error ? e.message : String(e)
      // Auto-clear after 8s so the banner doesn't sit forever on a list
      // that's actually working (the user dismissed it without further
      // action). The user can also dismiss manually via the banner's
      // close button.
      markDoneTimer = setTimeout(() => {
        markDoneError = ''
        markDoneTimer = null
      }, 8_000)
    }
  }

  function openItem(item: AgendaItem) {
    window.dispatchEvent(
      new CustomEvent('navigate-to-block', {
        detail: {
          notebook: item.notebook,
          section: item.section,
          page: item.page,
          date: item.file_date,
          blockId: item.id
        }
      })
    )
  }

  // Reload when the plugin's typed event bus reports a block change so a
  // task that just got marked done (or that just got a new due date)
  // shows up / disappears without a manual refresh.
  let offBlockChanged: (() => void) | undefined
  onMount(() => {
    reload()
    offBlockChanged = ctx.on('block:changed', () => {
      reload()
    })
  })
  onDestroy(() => {
    offBlockChanged?.()
    if (markDoneTimer) clearTimeout(markDoneTimer)
  })

  // Reactive scroll: when the sidebar's focusDate or activeFilter changes
  // and the agenda groups are rendered, scroll the relevant group into
  // view. Uses tick() to ensure the DOM has settled before scrolling.
  import { getFocusState, clearActiveFilter } from './focusState.svelte'

  // Expose the active filter on the script body so the in-view banner
  // (rendered below the header) can read it without a redundant
  // getFocusState() call. The value is still reactive because it reads
  // a $state field under the hood.
  let activeFilter = $derived(getFocusState().activeFilter)

  $effect(() => {
    const { focusDate, activeFilter: filter } = getFocusState()
    if (!focusDate && filter === 'all') return
    void items
    void tick().then(() => {
      const target = focusDate || (filter === 'today' ? today : '')
      const sel = target
        ? `[data-group-date="${target}"]`
        : `[data-group="today"]`
      const el = document.querySelector(sel) as HTMLElement | null
      el?.scrollIntoView({ block: 'start', behavior: 'smooth' })
    })
  })

  // Dim tasks that don't match the active smart-list filter.
  function matchesFilter(item: AgendaItem): boolean {
    const { activeFilter } = getFocusState()
    if (activeFilter === 'all') return true
    if (activeFilter === 'overdue') return item.due_date < today
    // "Today" smart list = exactly due today. Overdue tasks are NOT
    // matched by the Today filter — they live in the separate Overdue
    // smart list. Matches the SQL bucket in CalendarSidebar.
    if (activeFilter === 'today') return item.due_date === today
    if (activeFilter === 'upcoming')
      return item.due_date >= today && item.due_date <= weekAhead
    if (activeFilter === 'completed') return false
    return true
  }
</script>

<div class="flex-1 flex flex-col min-h-0 overflow-hidden" data-agenda-list>
  <header
    class="px-6 py-4 border-b border-border-muted flex items-center gap-3"
  >
    <span class="material-symbols-outlined text-accent-primary-start"
      >event_repeat</span
    >
    <h1 class="font-headline-lg text-headline-lg text-text-primary">
      {manifest?.name ?? 'Agenda'}
    </h1>
    <span class="text-text-muted text-[12px] font-body-md ml-auto">
      {items.length} active task{items.length === 1 ? '' : 's'}
    </span>
  </header>

  {#if activeFilter !== 'all'}
    <div
      class="px-6 py-1.5 border-b border-border-muted bg-accent-primary-glow flex items-center gap-2 text-[12px] font-body-md"
      role="status"
      aria-live="polite"
      data-testid="agenda-filter-banner"
    >
      <span class="material-symbols-outlined text-[14px] text-accent-primary-start"
        >filter_alt</span
      >
      <span class="text-text-primary"
        >Focused on: <strong>{activeFilter}</strong></span
      >
      <button
        type="button"
        onclick={clearActiveFilter}
        aria-label="Clear filter"
        data-testid="agenda-clear-filter"
        class="ml-auto p-1 rounded hover:bg-hover text-text-muted hover:text-error border-none bg-transparent cursor-pointer"
      >
        <span class="material-symbols-outlined text-[14px]">close</span>
      </button>
    </div>
  {/if}

  {#if markDoneError}
    <div
      class="px-6 py-2 bg-error-bg border-b border-error-border text-error text-[12px] font-body-md flex items-center gap-2"
      role="alert"
      data-testid="mark-done-error"
    >
      <span class="flex-1">Couldn't mark task done: {markDoneError}</span>
      <button
        type="button"
        aria-label="Dismiss error"
        onclick={() => {
          markDoneError = ''
          if (markDoneTimer) {
            clearTimeout(markDoneTimer)
            markDoneTimer = null
          }
        }}
        data-testid="mark-done-error-dismiss"
        class="p-1 rounded hover:bg-hover text-text-muted hover:text-error border-none bg-transparent cursor-pointer"
      >
        <span class="material-symbols-outlined text-[14px]">close</span>
      </button>
    </div>
  {/if}

  <div
    class="flex-1 overflow-y-auto custom-scrollbar px-6 py-4 space-y-6 max-w-4xl w-full"
  >
    {#if loading}
      <div class="text-text-muted animate-pulse">Loading agenda…</div>
    {:else if errorMsg}
      <div class="text-error">Failed to load: {errorMsg}</div>
    {:else if items.length === 0}
      <div class="text-text-muted py-10 text-center font-body-md">
        Nothing scheduled. Add a due date to a task to see it here.
      </div>
    {:else}
      {#each [{ key: 'overdue', label: 'Overdue', list: overdue, tone: 'error', date: '' }, { key: 'today', label: 'Today', list: todayItems, tone: 'primary', date: today }, { key: 'tomorrow', label: 'Tomorrow', list: tomorrowItems, tone: 'secondary', date: tomorrow }, { key: 'upcoming', label: 'Upcoming', list: upcoming, tone: 'muted', date: '' }] as group (group.key)}
        {#if group.list.length > 0}
          <section
            data-group={group.key}
            data-group-date={group.date}
            aria-label={group.label}
          >
            <h2
              class="font-label-sm-bold uppercase tracking-widest text-[11px] mb-2 flex items-center gap-2"
              class:text-error={group.tone === 'error'}
              class:text-accent-primary-start={group.tone === 'primary'}
              class:text-accent-secondary-start={group.tone === 'secondary'}
              class:text-text-muted={group.tone === 'muted'}
            >
              {group.label}
              <span class="text-text-muted/60">{group.list.length}</span>
            </h2>
            <div class="space-y-1">
              {#each group.list as item (item.id)}
                <div
                  class="group flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-hover transition-colors cursor-pointer"
                  class:opacity-30={!matchesFilter(item)}
                  onclick={() => openItem(item)}
                  onkeydown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault()
                      openItem(item)
                    }
                  }}
                  role="button"
                  tabindex="0"
                >
                  <button
                    onclick={(e) => {
                      e.stopPropagation()
                      markDone(item)
                    }}
                    title="Mark done"
                    class="w-5 h-5 rounded todo-check flex-shrink-0 cursor-pointer hover:border-accent-primary-start"
                    aria-label="Mark done"
                  ></button>
                  <div class="flex-1 min-w-0">
                    <div
                      class="text-text-primary text-sm font-body-md truncate"
                    >
                      {item.clean_content}
                    </div>
                    <div
                      class="text-[10px] text-text-muted uppercase tracking-widest font-label-sm"
                    >
                      {item.notebook} › {item.section} › {item.page}
                    </div>
                  </div>
                  {#if item.owner}
                    <span
                      class="text-[10px] text-accent-secondary-start bg-accent-secondary-glow border border-accent-secondary-start/30 rounded px-1.5 py-0.5"
                      >[{item.owner}]</span
                    >
                  {/if}
                  <span
                    class="text-[10px] text-text-muted font-label-sm flex-shrink-0"
                    >{item.due_date}</span
                  >
                </div>
              {/each}
            </div>
          </section>
        {/if}
      {/each}
    {/if}
  </div>
</div>
