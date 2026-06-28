<script lang="ts">
  // CalendarSidebar — the primary sidebar for the unified Calendar/Agenda
  // view (#322). Renders:
  //   1. Smart lists with live counts: Today, Upcoming, Overdue, Completed,
  //      All Tasks.
  //   2. A mini month-calendar with per-day task dot indicators and click-
  //      to-jump navigation that pushes `focusDate` into the shared state
  //      and dispatches `calendar:focus-date` so the main view scrolls.
  //   3. A "Clear filter" affordance when a smart list is active.
  //
  // A11Y: roving tabindex across both lists (Arrow keys + Home/End + Enter).
  // Counts re-query on `refresh-navigation` + `block:changed` (mirrors how
  // Sidebar + TagSidebarPanel refresh).
  import { onMount, onDestroy } from 'svelte'
  import type { PluginContext, PluginManifest } from '../../sdk'
  import { plusDaysISO, localToday } from '../../sdk'
  import {
    getFocusState,
    setActiveFilter,
    clearActiveFilter,
    setFocusDate,
    clearFocusDate
  } from './focusState.svelte'

  interface Props {
    ctx: PluginContext
    manifest?: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  interface Counts {
    today: number
    upcoming: number
    overdue: number
    completed: number
    all: number
  }

  let counts = $state<Counts>({
    today: 0,
    upcoming: 0,
    overdue: 0,
    completed: 0,
    all: 0
  })
  let byDate = $state<Record<string, number>>({})
  let loading = $state(true)
  let errorMsg = $state('')

  // Mini-calendar cursor (independent of the main view's cursor).
  let miniCursor = $state(new Date())

  // Roving tabindex for the smart-list and mini-cal-day keyboard nav.
  let listFocusIdx = $state(0)
  let miniFocusIdx = $state(0)

  async function reload() {
    loading = true
    errorMsg = ''
    try {
      const today = ctx.today || localToday()
      const tomorrow = plusDaysISO(today, 1)
      const weekAhead = plusDaysISO(today, 7)
      // The smart-list counts come from a single conditional-aggregate
      // query so we hit the index once per refresh. The query uses the
      // local-day anchor (#118) — NOT SQLite's UTC date('now') — for the
      // same reasons Kanban/query.ts binds the today parameter.
      const res = await ctx.sqliteQuery(
        `SELECT
            SUM(CASE WHEN t.status != 'DONE' AND (t.due_date = ? OR t.due_date < ?) THEN 1 ELSE 0 END) AS today,
            SUM(CASE WHEN t.status != 'DONE' AND t.due_date >= ? AND t.due_date <= ? THEN 1 ELSE 0 END) AS upcoming,
            SUM(CASE WHEN t.status != 'DONE' AND t.due_date < ? THEN 1 ELSE 0 END) AS overdue,
            SUM(CASE WHEN t.status = 'DONE' AND t.due_date = ? THEN 1 ELSE 0 END) AS completed,
            SUM(CASE WHEN t.status != 'DONE' THEN 1 ELSE 0 END) AS all
         FROM blocks b JOIN tasks t ON b.id = t.block_id`,
        [today, today, tomorrow, weekAhead, today, today]
      )
      const row = (res.rows?.[0] ?? {}) as Record<string, unknown>
      counts = {
        today: Number(row.today ?? 0),
        upcoming: Number(row.upcoming ?? 0),
        overdue: Number(row.overdue ?? 0),
        completed: Number(row.completed ?? 0),
        all: Number(row.all ?? 0)
      }
      // Mini-calendar dots: a per-day count for the visible month only,
      // to keep the query window tight. One query per refresh.
      const first = ymd(firstOfMonth(miniCursor))
      const last = ymd(lastOfMonth(miniCursor))
      const dayRes = await ctx.sqliteQuery(
        `SELECT t.due_date AS d, COUNT(*) AS c
         FROM blocks b JOIN tasks t ON b.id = t.block_id
         WHERE t.status != 'DONE'
           AND t.due_date IS NOT NULL AND t.due_date != ''
           AND t.due_date >= ? AND t.due_date <= ?
         GROUP BY t.due_date`,
        [first, last]
      )
      const bucket: Record<string, number> = {}
      for (const r of dayRes.rows as unknown as Array<{ d: string; c: number }>) {
        if (r.d) bucket[r.d] = r.c
      }
      byDate = bucket
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  let offBlock: (() => void) | undefined
  onMount(() => {
    reload()
    offBlock = ctx.on('block:changed', () => {
      reload()
    })
    const onRefresh = () => reload()
    window.addEventListener('refresh-navigation', onRefresh)
    return () => {
      window.removeEventListener('refresh-navigation', onRefresh)
      offBlock?.()
    }
  })
  onDestroy(() => {
    offBlock?.()
  })

  // Re-query when the mini-calendar cursor shifts months.
  $effect(() => {
    void miniCursor
    void reload()
  })

  // --- Date helpers (mirror Calendar.svelte's local helpers) --------------

  const MONTHS = [
    'January',
    'February',
    'March',
    'April',
    'May',
    'June',
    'July',
    'August',
    'September',
    'October',
    'November',
    'December'
  ]
  const DOW = ['S', 'M', 'T', 'W', 'T', 'F', 'S']

  function ymd(d: Date): string {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
      d.getDate()
    ).padStart(2, '0')}`
  }
  function firstOfMonth(d: Date): Date {
    return new Date(d.getFullYear(), d.getMonth(), 1)
  }
  function lastOfMonth(d: Date): Date {
    return new Date(d.getFullYear(), d.getMonth() + 1, 0)
  }
  function startOfWeek(d: Date): Date {
    const x = new Date(d)
    x.setDate(x.getDate() - x.getDay())
    x.setHours(0, 0, 0, 0)
    return x
  }
  function addDays(d: Date, n: number): Date {
    const x = new Date(d)
    x.setDate(x.getDate() + n)
    return x
  }

  let miniWeeks = $derived.by(() => {
    const first = startOfWeek(firstOfMonth(miniCursor))
    const last = lastOfMonth(miniCursor)
    const weeks: Date[][] = []
    let cur = first
    for (let w = 0; w < 6; w++) {
      const row: Date[] = []
      for (let i = 0; i < 7; i++) {
        row.push(cur)
        cur = addDays(cur, 1)
      }
      weeks.push(row)
      if (cur > last && w >= 3) break
    }
    return weeks
  })

  function prevMonth() {
    miniCursor = new Date(miniCursor.getFullYear(), miniCursor.getMonth() - 1, 1)
  }
  function nextMonth() {
    miniCursor = new Date(miniCursor.getFullYear(), miniCursor.getMonth() + 1, 1)
  }
  function pickDay(d: Date) {
    setFocusDate(ymd(d))
  }

  // --- Smart-list keyboard nav -------------------------------------------

  const smartLists = [
    { id: 'today', label: 'Today' },
    { id: 'upcoming', label: 'Upcoming' },
    { id: 'overdue', label: 'Overdue' },
    { id: 'completed', label: 'Completed' },
    { id: 'all', label: 'All Tasks' }
  ] as const

  function activateList(id: string) {
    if (id === 'all') {
      clearActiveFilter()
    } else {
      setActiveFilter(id as any)
    }
  }

  function onListKeydown(e: KeyboardEvent) {
    const max = smartLists.length - 1
    let nextIdx = listFocusIdx
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      nextIdx = Math.min(max, listFocusIdx + 1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      nextIdx = Math.max(0, listFocusIdx - 1)
    } else if (e.key === 'Home') {
      e.preventDefault()
      nextIdx = 0
    } else if (e.key === 'End') {
      e.preventDefault()
      nextIdx = max
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      const item = smartLists[listFocusIdx]
      if (item) activateList(item.id)
      return
    } else {
      return
    }
    listFocusIdx = nextIdx
    // Explicit focus — jsdom (and screen readers) need the active
    // element to actually move so aria-activedescendant + the visible
    // focus ring track the same option. Mirrors the Kanban scope radio
    // pattern in Kanban.svelte:45-64.
    const next = smartLists[nextIdx]
    if (next) {
      document
        .querySelector<HTMLElement>(`[data-testid="${next.id}"]`)
        ?.focus()
    }
  }

  function onDayKeydown(e: KeyboardEvent, flatIdx: number) {
    const total = miniWeeks.flat().length
    let next = flatIdx
    let handled = true
    if (e.key === 'ArrowRight') next = flatIdx + 1
    else if (e.key === 'ArrowLeft') next = flatIdx - 1
    else if (e.key === 'ArrowDown') next = flatIdx + 7
    else if (e.key === 'ArrowUp') next = flatIdx - 7
    else if (e.key === 'Home') next = flatIdx - (flatIdx % 7)
    else if (e.key === 'End') next = flatIdx + (6 - (flatIdx % 7))
    else if (e.key === 'Enter' || e.key === ' ') {
      const day = miniWeeks.flat()[flatIdx]
      if (day) pickDay(day)
      handled = true
    } else handled = false
    if (handled) {
      e.preventDefault()
      miniFocusIdx = Math.max(0, Math.min(total - 1, next))
      const el = document.querySelector<HTMLElement>(
        `[data-mini-day="${miniFocusIdx}"]`
      )
      el?.focus()
    }
  }

  function clearFilterAndFocus() {
    clearActiveFilter()
  }

  // Listen for the main view's "Clear filter" click (which dispatches
  // `calendar:clear-filter`) so the X icon there also clears our state.
  onMount(() => {
    const off = () => clearActiveFilter()
    window.addEventListener('calendar:clear-filter', off)
    return () => window.removeEventListener('calendar:clear-filter', off)
  })

  let liveMessage = $state('')
  let lastCountsJson = ''
  $effect(() => {
    const j = JSON.stringify(counts)
    if (j !== lastCountsJson) {
      lastCountsJson = j
      liveMessage = `Counts: ${counts.today} today, ${counts.upcoming} upcoming, ${counts.overdue} overdue, ${counts.completed} completed, ${counts.all} total.`
    }
  })

  let activeFilter = $derived(getFocusState().activeFilter)
  let activeFocusDate = $derived(getFocusState().focusDate)
</script>

<aside
  class="flex flex-col gap-4 px-2 py-1"
  aria-label="Calendar sidebar"
  data-test-calendar-sidebar
>
  <!-- Smart lists (#322 AC #3) -->
  <section aria-labelledby="cal-smart-lists-heading">
    <h3
      id="cal-smart-lists-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Smart Lists
    </h3>
    <ul role="listbox" aria-label="Smart lists" class="mt-1 space-y-0.5">
      {#each smartLists as item, i (item.id)}
        {@const selected = activeFilter === item.id}
        <li>
          <button
            type="button"
            role="option"
            aria-selected={selected}
            tabindex={i === listFocusIdx ? 0 : -1}
            data-testid={item.id}
            onclick={() => {
              listFocusIdx = i
              activateList(item.id)
            }}
            onkeydown={onListKeydown}
            onfocus={() => (listFocusIdx = i)}
            class="w-full flex items-center gap-2 px-2 py-1.5 rounded text-left text-[12px] font-body-md cursor-pointer border-none bg-transparent transition-colors
              {selected
              ? 'bg-accent-primary-glow text-accent-primary-start'
              : 'text-text-primary hover:bg-hover'}"
          >
            <span
              class="material-symbols-outlined text-[14px]"
              class:text-error={item.id === 'overdue'}
              class:text-accent-primary-start={item.id !== 'overdue'}
            >
              {item.id === 'today'
                ? 'today'
                : item.id === 'upcoming'
                  ? 'event_upcoming'
                  : item.id === 'overdue'
                    ? 'error'
                    : item.id === 'completed'
                      ? 'check_circle'
                      : 'list_alt'}
            </span>
            <span class="flex-1 truncate">{item.label}</span>
            <span
              class="text-[10px] text-text-muted bg-surface px-1.5 py-0.5 rounded-sm font-label-sm"
              aria-label="{counts[item.id as keyof Counts]} tasks"
              data-testid={`count-${item.id}`}
            >
              {counts[item.id as keyof Counts]}
            </span>
          </button>
        </li>
      {/each}
    </ul>
    {#if activeFilter !== 'all'}
      <button
        type="button"
        onclick={clearFilterAndFocus}
        data-testid="clear-filter"
        class="mt-1 w-full flex items-center justify-center gap-1 px-2 py-1 rounded text-[11px] font-label-sm text-text-muted hover:text-error cursor-pointer border border-dashed border-border-muted bg-transparent transition-colors"
      >
        <span class="material-symbols-outlined text-[12px]">close</span>
        Clear filter
      </button>
    {/if}
  </section>

  <!-- Mini calendar (#322 AC #4) -->
  <section aria-labelledby="cal-mini-heading">
    <h3
      id="cal-mini-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Jump to date
    </h3>
    <div class="mt-1 px-2">
      <div class="flex items-center justify-between mb-1">
        <button
          type="button"
          onclick={prevMonth}
          aria-label="Previous month"
          class="p-1 rounded hover:bg-hover text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer"
        >
          <span class="material-symbols-outlined text-[14px]">chevron_left</span>
        </button>
        <span class="text-text-primary text-[11px] font-label-sm-bold">
          {MONTHS[miniCursor.getMonth()]} {miniCursor.getFullYear()}
        </span>
        <button
          type="button"
          onclick={nextMonth}
          aria-label="Next month"
          class="p-1 rounded hover:bg-hover text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer"
        >
          <span class="material-symbols-outlined text-[14px]">chevron_right</span>
        </button>
      </div>
      <div class="grid grid-cols-7 gap-0.5" role="grid">
        {#each DOW as d}
          <div
            class="text-center text-[9px] uppercase tracking-widest font-label-sm-bold text-text-muted py-0.5"
          >
            {d}
          </div>
        {/each}
        {#each miniWeeks as week, wi (wi)}
          {#each week as day, di (di)}
            {@const inMonth = day.getMonth() === miniCursor.getMonth()}
            {@const key = ymd(day)}
            {@const count = byDate[key] ?? 0}
            {@const flatIdx = wi * 7 + di}
            <button
              type="button"
              role="gridcell"
              tabindex={flatIdx === miniFocusIdx ? 0 : -1}
              data-mini-day={flatIdx}
              data-mini-date={key}
              data-test-mini-day={key}
              onclick={() => pickDay(day)}
              onkeydown={(e) => onDayKeydown(e, flatIdx)}
              aria-label={`${key}${count ? ', ' + count + ' task' + (count === 1 ? '' : 's') : ''}`}
              aria-current={key === activeFocusDate ? 'date' : undefined}
              data-testid={`mini-day-${key}`}
              class="aspect-square flex flex-col items-center justify-center rounded text-[10px] font-label-sm cursor-pointer border-none bg-transparent
                {inMonth
                ? 'text-text-primary hover:bg-hover'
                : 'text-text-muted/50'}
                {key === activeFocusDate ? 'ring-1 ring-accent-primary-start bg-accent-primary-glow' : ''}"
            >
              <span>{day.getDate()}</span>
              {#if count > 0}
                <span
                  class="w-1 h-1 rounded-full bg-accent-primary-start"
                  aria-hidden="true"
                ></span>
              {/if}
            </button>
          {/each}
        {/each}
      </div>
      {#if activeFocusDate}
        <button
          type="button"
          onclick={() => clearFocusDate()}
          data-testid="clear-focus"
          class="mt-1 w-full flex items-center justify-center gap-1 px-2 py-1 rounded text-[11px] font-label-sm text-text-muted hover:text-error cursor-pointer border border-dashed border-border-muted bg-transparent transition-colors"
        >
          <span class="material-symbols-outlined text-[12px]">close</span>
          Clear jump date
        </button>
      {/if}
    </div>
  </section>

  <!-- aria-live region announces count changes -->
  <div class="sr-only" aria-live="polite">{liveMessage}</div>

  {#if errorMsg}
    <p class="px-2 text-error text-[11px] font-body-md" role="alert">
      {errorMsg}
    </p>
  {/if}
</aside>
