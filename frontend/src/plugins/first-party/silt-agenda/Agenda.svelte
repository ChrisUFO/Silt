<script lang="ts">
  import { onMount } from 'svelte'
  import type { PluginContext, PluginManifest } from '../../sdk'

  interface Props {
    ctx: PluginContext
    manifest: PluginManifest
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

  function todayStr() {
    const d = new Date()
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
      d.getDate()
    ).padStart(2, '0')}`
  }
  function addDaysStr(days: number) {
    const d = new Date()
    d.setDate(d.getDate() + days)
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(
      d.getDate()
    ).padStart(2, '0')}`
  }

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

  let today = $derived(todayStr())
  let tomorrow = $derived(addDaysStr(1))

  // Roll-forward: overdue items are surfaced in the Overdue group so they
  // effectively appear on today's timeline.
  let overdue = $derived(items.filter((i) => i.due_date < today))
  let todayItems = $derived(items.filter((i) => i.due_date === today))
  let tomorrowItems = $derived(items.filter((i) => i.due_date === tomorrow))
  let upcoming = $derived(
    items
      .filter((i) => i.due_date > tomorrow)
      .sort((a, b) => a.due_date.localeCompare(b.due_date))
  )

  let markDoneError = $state('')

  async function markDone(item: AgendaItem) {
    markDoneError = ''
    try {
      await ctx.updateBlockState(item.id, 'DONE')
      // Only remove from the list once the backend confirmed the change;
      // otherwise the UI would drift from the index on failure.
      items = items.filter((i) => i.id !== item.id)
    } catch (e) {
      markDoneError = e instanceof Error ? e.message : String(e)
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

  onMount(() => {
    reload()
  })
</script>

<div class="flex-1 flex flex-col min-h-0 overflow-hidden">
  <header
    class="px-6 py-4 border-b border-border-muted flex items-center gap-3"
  >
    <span class="material-symbols-outlined text-accent-primary-start"
      >event_repeat</span
    >
    <h1 class="font-headline-lg text-headline-lg text-text-primary">
      {manifest.name}
    </h1>
    <span class="text-text-muted text-[12px] font-body-md ml-auto">
      {items.length} active task{items.length === 1 ? '' : 's'}
    </span>
  </header>

  {#if markDoneError}
    <div
      class="px-6 py-2 bg-error-bg border-b border-error-border text-error text-[12px] font-body-md"
    >
      Couldn't mark task done: {markDoneError}
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
      {#each [{ label: 'Overdue', list: overdue, tone: 'error' }, { label: 'Today', list: todayItems, tone: 'primary' }, { label: 'Tomorrow', list: tomorrowItems, tone: 'secondary' }, { label: 'Upcoming', list: upcoming, tone: 'muted' }] as group (group.label)}
        {#if group.list.length > 0}
          <section>
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
                  class="group flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-bg-hover transition-colors cursor-pointer"
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
