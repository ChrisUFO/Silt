<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import type { PluginContext, PluginManifest } from '../../sdk'

  interface Props {
    ctx: PluginContext
    manifest: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  interface CalItem {
    id: string
    notebook: string
    section: string
    page: string
    file_date: string
    clean_content: string
    status: string
    due_date: string
  }

  let mode = $state<'month' | 'week'>('month')
  // Anchor date for the visible window.
  let cursor = $state(new Date())
  let byDate = $state<Record<string, CalItem[]>>({})
  let loading = $state(true)
  let errorMsg = $state('')

  // Reactive "now" so the today-highlight updates if the calendar stays
  // mounted past midnight (ticks every 60s; only re-evaluates isToday).
  let nowTick = $state(0)
  let nowInterval: ReturnType<typeof setInterval> | undefined
  onMount(() => {
    reload()
    nowInterval = setInterval(() => {
      nowTick++
    }, 60_000)
  })
  onDestroy(() => {
    if (nowInterval) clearInterval(nowInterval)
  })

  const DOW = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
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

  function ymd(d: Date) {
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
      d.getDate()
    ).padStart(2, '0')}`
  }
  function startOfWeek(d: Date) {
    const x = new Date(d)
    x.setDate(x.getDate() - x.getDay())
    x.setHours(0, 0, 0, 0)
    return x
  }
  function startOfMonth(d: Date) {
    return new Date(d.getFullYear(), d.getMonth(), 1)
  }
  function endOfMonth(d: Date) {
    return new Date(d.getFullYear(), d.getMonth() + 1, 0)
  }
  function addMonths(d: Date, n: number) {
    return new Date(d.getFullYear(), d.getMonth() + n, 1)
  }
  function addDays(d: Date, n: number) {
    const x = new Date(d)
    x.setDate(x.getDate() + n)
    return x
  }
  function isSameDay(a: Date, b: Date) {
    return ymd(a) === ymd(b)
  }

  // Month grid: weeks of Sunday..Saturday covering the month.
  let monthWeeks = $derived.by(() => {
    if (mode !== 'month') return []
    const first = startOfWeek(startOfMonth(cursor))
    const weeks: Date[][] = []
    let cur = first
    const monthEnd = endOfMonth(cursor)
    // 6 rows covers any month.
    for (let w = 0; w < 6; w++) {
      const row: Date[] = []
      for (let i = 0; i < 7; i++) {
        row.push(cur)
        cur = addDays(cur, 1)
      }
      weeks.push(row)
      if (cur > monthEnd && w >= 3) break
    }
    return weeks
  })

  let weekDays = $derived.by(() => {
    if (mode !== 'week') return []
    const first = startOfWeek(cursor)
    return Array.from({ length: 7 }, (_, i) => addDays(first, i))
  })

  let windowStart = $derived(
    mode === 'month' ? startOfWeek(startOfMonth(cursor)) : startOfWeek(cursor)
  )
  let windowEnd = $derived(
    mode === 'month'
      ? addDays(startOfWeek(endOfMonth(cursor)), 6)
      : addDays(startOfWeek(cursor), 6)
  )

  let heading = $derived(
    mode === 'month'
      ? `${MONTHS[cursor.getMonth()]} ${cursor.getFullYear()}`
      : `${MONTHS[cursor.getMonth()]} ${startOfWeek(cursor).getDate()}–${addDays(startOfWeek(cursor), 6).getDate()}, ${cursor.getFullYear()}`
  )

  async function reload() {
    loading = true
    errorMsg = ''
    try {
      const s = ymd(windowStart)
      const e = ymd(windowEnd)
      const { rows } = await ctx.sqliteQuery(
        `SELECT b.id, b.notebook, b.section, b.page, b.file_date,
                b.clean_content, t.status, t.due_date
         FROM blocks b JOIN tasks t ON b.id = t.block_id
         WHERE t.due_date IS NOT NULL AND t.due_date != ''
           AND t.due_date >= ? AND t.due_date <= ?
         ORDER BY t.due_date ASC, t.priority ASC`,
        [s, e]
      )
      const bucket: Record<string, CalItem[]> = {}
      for (const r of rows as unknown as CalItem[]) {
        if (!r.due_date) continue
        ;(bucket[r.due_date] ||= []).push(r)
      }
      byDate = bucket
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  // Reload whenever the visible window shifts.
  $effect(() => {
    void windowStart
    void windowEnd
    reload()
  })

  function prev() {
    cursor = mode === 'month' ? addMonths(cursor, -1) : addDays(cursor, -7)
  }
  function next() {
    cursor = mode === 'month' ? addMonths(cursor, 1) : addDays(cursor, 7)
  }
  function goToday() {
    cursor = new Date()
  }

  function openItem(item: CalItem) {
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

  // Keyboard navigation across month cells: arrows move focus by day (clamping
  // to the grid), Enter opens the focused day's first task.
  function onCellKeydown(e: KeyboardEvent, day: Date) {
    const map: Record<string, number> = {
      ArrowRight: 1,
      ArrowLeft: -1,
      ArrowDown: 7,
      ArrowUp: -7
    }
    const delta = map[e.key]
    if (delta === undefined) return
    e.preventDefault()
    const grid = monthWeeks.flat()
    const idx = grid.findIndex((d) => ymd(d) === ymd(day))
    if (idx < 0) return
    const next = Math.min(Math.max(idx + delta, 0), grid.length - 1)
    const targetDt = grid[next]
    if (!targetDt) return
    const el = document.querySelector<HTMLElement>(
      `[data-celldate="${ymd(targetDt)}"]`
    )
    el?.focus()
  }

  // Reactive today string — re-evaluates when the 60s tick fires so the
  // today-highlight updates if the calendar stays mounted past midnight.
  let todayKey = $derived.by(() => {
    void nowTick
    return ymd(new Date())
  })
</script>

<div class="flex-1 flex flex-col min-h-0 overflow-hidden">
  <header
    class="px-6 py-4 border-b border-border-muted flex items-center gap-3 flex-wrap"
  >
    <span class="material-symbols-outlined text-accent-primary-start"
      >calendar_month</span
    >
    <h1 class="font-headline-lg text-headline-lg text-text-primary">
      {heading}
    </h1>
    <div class="flex items-center gap-1 ml-2">
      <button
        onclick={prev}
        class="p-1.5 rounded hover:bg-bg-hover text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer"
        aria-label="Previous"
      >
        <span class="material-symbols-outlined text-[18px]">chevron_left</span>
      </button>
      <button
        onclick={goToday}
        class="px-2.5 py-1 rounded border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 font-label-sm border bg-transparent cursor-pointer transition-colors"
        >Today</button
      >
      <button
        onclick={next}
        class="p-1.5 rounded hover:bg-bg-hover text-text-muted hover:text-accent-primary-start border-none bg-transparent cursor-pointer"
        aria-label="Next"
      >
        <span class="material-symbols-outlined text-[18px]">chevron_right</span>
      </button>
    </div>
    <div
      class="ml-auto flex items-center gap-0.5 bg-bg-surface border border-border-muted rounded-lg p-0.5"
    >
      <button
        onclick={() => (mode = 'month')}
        class="px-2.5 py-1 rounded font-label-sm border-none cursor-pointer transition-colors"
        class:bg-bg-hover={mode === 'month'}
        class:text-accent-primary-start={mode === 'month'}
        class:text-text-muted={mode !== 'month'}>Month</button
      >
      <button
        onclick={() => (mode = 'week')}
        class="px-2.5 py-1 rounded font-label-sm border-none cursor-pointer transition-colors"
        class:bg-bg-hover={mode === 'week'}
        class:text-accent-primary-start={mode === 'week'}
        class:text-text-muted={mode !== 'week'}>Week</button
      >
    </div>
  </header>

  <div class="flex-1 overflow-auto custom-scrollbar p-4">
    {#if loading}
      <div class="text-text-muted animate-pulse p-6">Loading…</div>
    {:else if errorMsg}
      <div class="text-error p-6">{errorMsg}</div>
    {:else if mode === 'month'}
      <!-- Month grid -->
      <div class="grid grid-cols-7 gap-1 min-w-[700px]">
        {#each DOW as d}
          <div
            class="text-center text-[10px] uppercase tracking-widest font-label-sm-bold text-text-muted py-1"
          >
            {d}
          </div>
        {/each}
        {#each monthWeeks as week}
          {#each week as day}
            {@const inMonth = day.getMonth() === cursor.getMonth()}
            {@const isToday = ymd(day) === todayKey}
            {@const items = byDate[ymd(day)] ?? []}
            <div
              role="gridcell"
              tabindex="0"
              data-celldate={ymd(day)}
              aria-label={`${day.toDateString()}${items.length ? ', ' + items.length + ' task' + (items.length === 1 ? '' : 's') : ''}`}
              onkeydown={(e) => {
                if (e.key === 'Enter' && items[0]) {
                  e.preventDefault()
                  openItem(items[0])
                } else {
                  onCellKeydown(e, day)
                }
              }}
              class="min-h-[88px] rounded-lg border p-1.5 flex flex-col gap-0.5 focus:outline-none focus:border-accent-primary-start focus:ring-1 focus:ring-accent-primary-start/40 {inMonth
                ? 'border-border-muted bg-bg-panel'
                : 'border-border-muted/30 bg-transparent'}"
            >
              <span
                class="text-[11px] font-label-sm-bold w-5 h-5 flex items-center justify-center rounded-full"
                class:bg-accent-primary-start={isToday}
                class:text-bg-void={isToday}
                class:text-text-muted={!isToday && !inMonth}
                class:text-text-primary={!isToday && inMonth}
                >{day.getDate()}</span
              >
              {#each items.slice(0, 3) as item (item.id)}
                <button
                  onclick={() => openItem(item)}
                  class="text-left text-[10px] truncate px-1 py-0.5 rounded bg-accent-primary-glow border border-accent-primary-start/20 text-accent-primary-start hover:brightness-110 transition-all cursor-pointer"
                  title={item.clean_content}>{item.clean_content}</button
                >
              {/each}
              {#if items.length > 3}
                <span class="text-[9px] text-text-muted px-1"
                  >+{items.length - 3} more</span
                >
              {/if}
            </div>
          {/each}
        {/each}
      </div>
    {:else}
      <!-- Week view: day columns -->
      <div class="grid grid-cols-7 gap-2 min-w-[700px]">
        {#each weekDays as day}
          {@const isToday = ymd(day) === todayKey}
          {@const items = byDate[ymd(day)] ?? []}
          <div class="flex flex-col gap-1.5">
            <div class="text-center pb-2 border-b border-border-muted">
              <div
                class="text-[10px] uppercase tracking-widest font-label-sm-bold text-text-muted"
              >
                {DOW[day.getDay()]}
              </div>
              <span
                class="inline-flex items-center justify-center w-7 h-7 rounded-full text-[13px] font-label-sm-bold mt-1"
                class:bg-accent-primary-start={isToday}
                class:text-bg-void={isToday}
                class:text-text-primary={!isToday}>{day.getDate()}</span
              >
            </div>
            {#each items as item (item.id)}
              <button
                onclick={() => openItem(item)}
                class="text-left text-[12px] px-2 py-1.5 rounded bg-bg-panel border border-border-muted hover:border-accent-primary-start/40 text-text-primary transition-all cursor-pointer"
                title={item.clean_content}>{item.clean_content}</button
              >
            {/each}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
