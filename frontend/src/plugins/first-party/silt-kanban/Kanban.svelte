<script lang="ts">
  import { onMount } from 'svelte'
  import { flip } from 'svelte/animate'
  import { cubicOut } from 'svelte/easing'
  import type { PluginContext, PluginManifest, TaskStatus } from '../../sdk'
  import { settings } from '../../../settings/store.svelte'
  import { measureFrameBudget } from '../../../lib/perf/frame-budget'

  interface Props {
    ctx: PluginContext
    manifest: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  interface KanbanCard {
    id: string
    notebook: string
    section: string
    page: string
    file_date: string
    clean_content: string
    status: TaskStatus
    owner: string
    start_date: string
    due_date: string
    priority: number
  }

  type Scope = 'vault' | 'notebook' | 'section' | 'page'

  const ALL_STATUSES: TaskStatus[] = ['TODO', 'DOING', 'DONE']

  // Scope defaults to the most-specific active nav level; the user can
  // widen/narrow via the segmented control.
  function defaultScope(): Scope {
    if (ctx.activePage) return 'page'
    if (ctx.activeSection) return 'section'
    if (ctx.activeNotebook) return 'notebook'
    return 'vault'
  }

  let scope = $state<Scope>(defaultScope())
  let lanes = $state<Record<string, KanbanCard[]>>({})
  let loading = $state(true)
  let errorMsg = $state('')
  let moveError = $state('')

  // Columns come from config.yaml (plugins.plugin_settings.silt-kanban.columns),
  // falling back to the canonical TODO/DOING/DONE triple.
  let columns = $derived.by(() => {
    const cfgCols = settings.config?.plugins?.plugin_settings?.['silt-kanban']
      ?.columns as string[] | undefined
    const cols =
      cfgCols?.filter((c) => ALL_STATUSES.includes(c as TaskStatus)) ?? []
    return cols.length > 0 ? (cols as TaskStatus[]) : [...ALL_STATUSES]
  })

  let totalCards = $derived(
    Object.values(lanes).reduce((sum, lane) => sum + lane.length, 0)
  )

  // Breadcrumb showing the active scope context.
  let scopeCrumb = $derived.by(() => {
    switch (scope) {
      case 'vault':
        return 'All notebooks'
      case 'notebook':
        return ctx.activeNotebook || '—'
      case 'section':
        return `${ctx.activeNotebook || '—'} › ${ctx.activeSection || '—'}`
      case 'page':
        return `${ctx.activeNotebook || '—'} › ${ctx.activeSection || '—'} › ${ctx.activePage || '—'}`
    }
  })

  function buildQuery(s: Scope): { sql: string; params: unknown[] } {
    const baseSelect = `SELECT b.id, b.notebook, b.section, b.page, b.file_date, b.line_number,
           b.clean_content, t.status, t.owner, t.start_date, t.due_date, t.priority
    FROM blocks b JOIN tasks t ON b.id = t.block_id`
    const orderBy = ` ORDER BY t.priority DESC, COALESCE(t.due_date, '9999-12-31') ASC`
    switch (s) {
      case 'vault':
        return { sql: baseSelect + ' WHERE 1=1' + orderBy, params: [] }
      case 'notebook':
        return {
          sql: baseSelect + ' WHERE b.notebook = ?' + orderBy,
          params: [ctx.activeNotebook]
        }
      case 'section':
        return {
          sql: baseSelect + ' WHERE b.notebook = ? AND b.section = ?' + orderBy,
          params: [ctx.activeNotebook, ctx.activeSection]
        }
      case 'page':
        return {
          sql:
            baseSelect +
            ' WHERE b.notebook = ? AND b.section = ? AND b.page = ?' +
            orderBy,
          params: [ctx.activeNotebook, ctx.activeSection, ctx.activePage]
        }
    }
  }

  async function reload() {
    loading = true
    errorMsg = ''
    try {
      const { sql, params } = buildQuery(scope)
      const rows = await ctx.sqliteQuery(sql, params)
      const bucket: Record<string, KanbanCard[]> = {}
      for (const col of columns) bucket[col] = []
      for (const r of rows as unknown as KanbanCard[]) {
        if (bucket[r.status]) bucket[r.status].push(r)
      }
      lanes = bucket
    } catch (e) {
      errorMsg = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  // Reload whenever scope or the active nav changes. The effect fires on
  // mount too, so there's no separate onMount load.
  $effect(() => {
    void scope
    void ctx.activeNotebook
    void ctx.activeSection
    void ctx.activePage
    reload()
  })

  // --- Drag-and-drop state ---
  let dragCard: KanbanCard | null = null
  let dragFromStatus: TaskStatus | null = null
  let dragOverStatus: TaskStatus | null = null
  let dragOverIndex = -1
  let draggingId = $state<string | null>(null)

  // --- Keyboard status change (a11y) ---
  // ArrowLeft/Right directly move the focused card between lanes;
  // Enter/Space falls through to native button click (navigate to source).
  let liveMessage = $state('')

  function onDragStart(e: DragEvent, card: KanbanCard, fromStatus: TaskStatus) {
    dragCard = card
    dragFromStatus = fromStatus
    draggingId = card.id
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', card.id)
    }
  }

  function onLaneDragOver(e: DragEvent, status: TaskStatus) {
    if (!dragCard) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    dragOverStatus = status
    // Determine insertion index from the card under the cursor.
    const lane = e.currentTarget as HTMLElement
    const afterEl = Array.from(
      lane.querySelectorAll<HTMLElement>('[data-card]')
    ).find((el) => {
      const rect = el.getBoundingClientRect()
      return e.clientY < rect.top + rect.height / 2
    })
    dragOverIndex = afterEl
      ? Number(afterEl.dataset.index)
      : (lanes[status]?.length ?? 0)
  }

  function onLaneDrop(e: DragEvent, toStatus: TaskStatus) {
    e.preventDefault()
    if (!dragCard || !dragFromStatus) return
    const card = dragCard
    const from = dragFromStatus
    const targetIndex = dragOverStatus === toStatus ? dragOverIndex : -1
    cleanupDrag()

    if (from === toStatus) return
    void commitMove(card, from, toStatus, targetIndex)
  }

  function cleanupDrag() {
    dragCard = null
    dragFromStatus = null
    dragOverStatus = null
    dragOverIndex = -1
    draggingId = null
  }

  async function commitMove(
    card: KanbanCard,
    fromStatus: TaskStatus,
    toStatus: TaskStatus,
    targetIndex: number
  ) {
    moveError = ''
    // Snapshot for revert on failure.
    const prevLanes = { ...lanes }

    // Optimistic: remove from source, insert into target.
    const newLanes = { ...lanes }
    newLanes[fromStatus] = (newLanes[fromStatus] ?? []).filter(
      (c) => c.id !== card.id
    )
    const updatedCard: KanbanCard = { ...card, status: toStatus }
    const targetLane = [...(newLanes[toStatus] ?? [])]
    const insertAt = targetIndex >= 0 ? targetIndex : targetLane.length
    targetLane.splice(insertAt, 0, updatedCard)
    newLanes[toStatus] = targetLane
    measureFrameBudget('kanban-drop', () => {
      lanes = newLanes
    })

    liveMessage = `Task moved to ${toStatus === 'TODO' ? 'To Do' : toStatus === 'DOING' ? 'In Progress' : 'Done'}`

    try {
      await ctx.updateBlockState(card.id, toStatus)
    } catch (e) {
      moveError = e instanceof Error ? e.message : String(e)
      lanes = prevLanes
      liveMessage = 'Move failed — reverted.'
    }
  }

  // --- Keyboard status change (a11y) ---
  // ArrowLeft/Right move the card between lanes; Enter/Space navigates to
  // the source block (native button activation, no preventDefault needed).
  function onCardKeydown(
    e: KeyboardEvent,
    card: KanbanCard,
    fromStatus: TaskStatus
  ) {
    const idx = columns.indexOf(fromStatus)
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      const next = Math.min(idx + 1, columns.length - 1)
      if (next !== idx) {
        void commitMove(card, fromStatus, columns[next], -1)
      }
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      const prev = Math.max(idx - 1, 0)
      if (prev !== idx) {
        void commitMove(card, fromStatus, columns[prev], -1)
      }
    }
  }

  function openCard(card: KanbanCard) {
    window.dispatchEvent(
      new CustomEvent('navigate-to-block', {
        detail: {
          notebook: card.notebook,
          section: card.section,
          page: card.page,
          date: card.file_date,
          blockId: card.id
        }
      })
    )
  }

  const PRIORITY_LABELS: Record<number, string> = {
    1: 'Critical',
    2: 'Normal',
    3: 'Low'
  }
  function priorityClass(p: number): string {
    if (p <= 1) return 'text-error border-error/20 bg-error/10'
    if (p === 2)
      return 'text-accent-primary-start border-accent-primary-start/20 bg-accent-primary-glow'
    return 'text-text-muted border-border-muted bg-bg-surface'
  }
  function statusLabel(s: TaskStatus): string {
    return s === 'TODO' ? 'To Do' : s === 'DOING' ? 'In Progress' : 'Done'
  }
</script>

<div class="flex-1 flex flex-col min-h-0 overflow-hidden">
  <header
    class="px-6 py-4 border-b border-border-muted flex items-center gap-3 flex-wrap"
  >
    <span class="material-symbols-outlined text-accent-primary-start"
      >view_kanban</span
    >
    <h1 class="font-headline-lg text-headline-lg text-text-primary">
      {manifest.name}
    </h1>
    <!-- Scope selector (segmented control) -->
    <div
      class="flex items-center gap-0.5 bg-bg-surface border border-border-muted rounded-lg p-0.5 ml-2"
      role="radiogroup"
      aria-label="Board scope"
    >
      {#each ['vault', 'notebook', 'section', 'page'] as s}
        <button
          onclick={() => (scope = s as Scope)}
          role="radio"
          aria-checked={scope === s}
          class="px-2.5 py-1 rounded font-label-sm border-none cursor-pointer transition-colors"
          class:bg-bg-hover={scope === s}
          class:text-accent-primary-start={scope === s}
          class:text-text-muted={scope !== s}
        >
          {s === 'vault' ? 'Vault' : s[0].toUpperCase() + s.slice(1)}
        </button>
      {/each}
    </div>
    <span class="text-text-muted text-[12px] font-body-md ml-auto">
      {scopeCrumb} · {totalCards} task{totalCards === 1 ? '' : 's'}
    </span>
  </header>

  {#if moveError}
    <div
      class="px-6 py-2 bg-error-bg border-b border-error-border text-error text-[12px] font-body-md"
      role="alert"
    >
      Couldn't move task: {moveError}
    </div>
  {/if}

  <!-- aria-live region for drag/keyboard move announcements -->
  <div class="sr-only" aria-live="polite">{liveMessage}</div>

  <div class="flex-1 overflow-hidden">
    {#if loading}
      <div class="text-text-muted animate-pulse p-6">Loading board…</div>
    {:else if errorMsg}
      <div class="text-error p-6">{errorMsg}</div>
    {:else}
      <div class="h-full flex gap-4 p-4 overflow-x-auto custom-scrollbar">
        {#each columns as col}
          {@const laneCards = lanes[col] ?? []}
          <section
            class="flex flex-col min-w-[280px] flex-1 max-w-[400px] rounded-lg border border-border-muted bg-bg-surface/50"
            role="group"
            aria-label={statusLabel(col)}
            ondragover={(e) => onLaneDragOver(e, col)}
            ondrop={(e) => onLaneDrop(e, col)}
            ondragleave={() => {
              if (dragOverStatus === col) dragOverStatus = null
            }}
          >
            <div
              class="flex items-center justify-between px-3 py-2.5 border-b border-border-muted"
            >
              <div class="flex items-center gap-2">
                <span
                  class="w-2 h-2 rounded-full"
                  class:bg-text-muted={col === 'TODO'}
                  class:bg-accent-secondary-start={col === 'DOING'}
                  class:bg-accent-primary-start={col === 'DONE'}
                ></span>
                <h2
                  class="font-label-sm-bold uppercase tracking-widest text-[11px] text-text-muted"
                >
                  {statusLabel(col)}
                </h2>
                <span
                  class="bg-bg-hover text-text-muted text-[10px] px-1.5 py-0.5 rounded-sm font-label-sm"
                  >{laneCards.length}</span
                >
              </div>
            </div>
            <div
              class="flex-1 overflow-y-auto custom-scrollbar p-2 space-y-2 min-h-[100px]"
            >
              {#each laneCards as card, i (card.id)}
                <div
                  data-card
                  data-index={i}
                  role="button"
                  tabindex="0"
                  aria-grabbed={draggingId === card.id ? 'true' : 'false'}
                  aria-label={`${card.clean_content}, ${statusLabel(col)}${card.owner ? `, owner ${card.owner}` : ''}${card.due_date ? `, due ${card.due_date}` : ''}. Arrow keys change status.`}
                  draggable="true"
                  animate:flip={{ duration: 200, easing: cubicOut }}
                  class="group bg-bg-panel border border-border-muted rounded-lg p-3 cursor-grab transition-all duration-200 hover:bg-bg-hover hover:-translate-y-px hover:shadow-lg focus:outline-none focus:ring-2 focus:ring-accent-primary-start/40 {draggingId ===
                  card.id
                    ? 'opacity-40 rotate-2'
                    : ''}"
                  ondragstart={(e) => onDragStart(e, card, col)}
                  ondragend={cleanupDrag}
                  onkeydown={(e) => onCardKeydown(e, card, col)}
                  onclick={() => openCard(card)}
                >
                  <div class="flex justify-between items-start mb-2 gap-2">
                    {#if card.priority && card.priority <= 3}
                      <span
                        class="px-1.5 py-0.5 border rounded-sm font-label-sm text-[9px] uppercase tracking-wide {priorityClass(
                          card.priority
                        )}"
                      >
                        {PRIORITY_LABELS[card.priority] ?? 'Normal'}
                      </span>
                    {/if}
                    {#if col === 'DONE'}
                      <span
                        class="material-symbols-outlined text-accent-primary-start text-[16px] ml-auto"
                        >check_circle</span
                      >
                    {/if}
                  </div>
                  <p
                    class="text-[13px] font-body-md text-text-primary mb-2 {col ===
                    'DONE'
                      ? 'line-through opacity-60'
                      : ''}"
                  >
                    {card.clean_content}
                  </p>
                  <div class="flex justify-between items-center gap-2">
                    <div class="flex items-center gap-1.5">
                      {#if card.owner}
                        <span
                          class="text-[9px] text-accent-secondary-start bg-accent-secondary-glow border border-accent-secondary-start/30 rounded-sm px-1.5 py-0.5 font-label-sm"
                        >
                          [{card.owner}]
                        </span>
                      {/if}
                    </div>
                    {#if card.due_date}
                      <span
                        class="text-[9px] text-text-muted font-label-sm flex items-center gap-0.5"
                      >
                        <span class="material-symbols-outlined text-[12px]"
                          >schedule</span
                        >
                        {card.due_date}
                      </span>
                    {/if}
                  </div>
                </div>
              {/each}
              {#if laneCards.length === 0}
                <div
                  class="text-center text-text-muted text-[11px] font-body-md py-6 border border-dashed border-border-muted rounded-lg"
                >
                  No {statusLabel(col).toLowerCase()} tasks
                </div>
              {/if}
            </div>
          </section>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
