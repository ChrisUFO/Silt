<script lang="ts">
  import { flip } from 'svelte/animate'
  import { cubicOut } from 'svelte/easing'
  import { untrack, onMount, onDestroy } from 'svelte'
  import type { PluginContext, PluginManifest, TaskStatus } from '../../sdk'
  import { settings, updatePluginSetting } from '../../../settings/store.svelte'
  import { measureFrameBudget } from '../../../lib/perf/frame-budget'
  import { EventsOn } from '../../../../wailsjs/runtime/runtime.js'
  import FilterBar from './FilterBar.svelte'
  import CardDetailPanel from './CardDetailPanel.svelte'
  import type { KanbanCard, KanbanFilters, Scope } from './types'
  import { PRIORITY_LABELS, laneLabel, priorityClass } from './types'
  import { buildQuery } from './query'

  interface Props {
    ctx: PluginContext
    manifest: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  const ALL_STATUSES: TaskStatus[] = ['TODO', 'DOING', 'DONE']
  const SCOPES: Scope[] = ['vault', 'notebook', 'section', 'page']

  // Scope defaults to the most-specific active nav level; the user can
  // widen/narrow via the segmented control.
  function defaultScope(): Scope {
    if (ctx.activePage) return 'page'
    if (ctx.activeSection) return 'section'
    if (ctx.activeNotebook) return 'notebook'
    return 'vault'
  }

  function isScopeDisabled(s: string): boolean {
    if (s === 'notebook') return !ctx.activeNotebook
    if (s === 'section') return !ctx.activeSection
    if (s === 'page') return !ctx.activePage
    return false
  }

  // WAI-ARIA radiogroup keyboard pattern: ArrowLeft/Right move selection
  // between enabled options (wrapping), Home/End jump to the boundaries.
  // Roving tabindex (checked radio = 0, others = -1) ensures Tab enters
  // the group on the active option and leaves on the next Tab.
  function onScopeKeydown(e: KeyboardEvent) {
    if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(e.key)) return
    e.preventDefault()
    const dir = e.key === 'ArrowLeft' || e.key === 'End' ? -1 : 1
    let start: number
    if (e.key === 'Home') start = 0
    else if (e.key === 'End') start = SCOPES.length - 1
    else start = (SCOPES.indexOf(scope) + dir + SCOPES.length) % SCOPES.length
    for (let i = 0; i < SCOPES.length; i++) {
      const next =
        (((start + i * dir) % SCOPES.length) + SCOPES.length) % SCOPES.length
      if (!isScopeDisabled(SCOPES[next])) {
        setScope(SCOPES[next])
        ;(e.currentTarget as HTMLElement)
          .querySelector<HTMLElement>(`[data-scope="${SCOPES[next]}"]`)
          ?.focus()
        return
      }
    }
  }

  // --- Shared state (#323) ------------------------------------------------
  // Kanban.svelte and KanbanSidebar.svelte both read/write the SAME scope +
  // filters via the shared module. The debounced persistence path stays
  // here (UI concern), but the value it persists is whatever the shared
  // module holds. The scopeUserOverride invariant (#124) is preserved:
  // any user-initiated scope change flips the override flag; the nav-
  // tracking $effect only mutates scope via narrowScopeTo (which is a
  // no-op when the override is set).
  import {
    getKanbanState,
    setScope as setScopeShared,
    setFilters as setFiltersShared,
    narrowScopeTo,
    clearScopeOverride,
    initFromConfig as initSharedFromConfig
  } from './kanbanSharedState.svelte'
  let scope = $derived(getKanbanState().scope)
  let scopeUserOverride = $derived(getKanbanState().scopeUserOverride)

  // Read the persisted scope from the synchronous settings snapshot. If
  // the user previously picked a scope, it lives under
  // plugins.plugin_settings.silt-kanban.scope; otherwise default to
  // defaultScope() so the board auto-narrows on first mount.
  function initialScope(): Scope {
    const cfgScope = settings.config?.plugins?.plugin_settings?.['silt-kanban']
      ?.scope as Scope | undefined
    if (
      cfgScope === 'vault' ||
      cfgScope === 'notebook' ||
      cfgScope === 'section' ||
      cfgScope === 'page'
    ) {
      return cfgScope
    }
    return defaultScope()
  }

  // Mirror the shared filters into a local $state so the existing
  // `$effect(() => { ... filters ... reload() })` pattern keeps working
  // unchanged. Writes flow the other way: when the user toggles a chip
  // locally, we setFiltersShared() so the sidebar checkboxes update,
  // and we also update the local $state for immediate re-query.
  // Filters derive from the shared module so a write from the sidebar
  // propagates to the local $effect that triggers the reload. Writes
  // from the FilterBar (the existing chip toggle path) still go through
  // `handleFiltersChange()` which calls setFiltersShared().
  let filters = $derived(getKanbanState().filters)

  // Hydrate the shared module from the current settings snapshot on
  // mount. Running this in onMount (not at script-body top level) means
  // the sidebar's first render already sees the persisted scope/filters
  // rather than the defaults — avoids a brief flash of "Vault / All
  // Tasks" before the main view writes the real values (#323 hardening).
  // Hydration must run before any child component reads the shared
  // module, and onMount runs synchronously before children commit.
  let hydrated = $state(false)
  onMount(() => {
    if (hydrated) return
    hydrated = true
    initSharedFromConfig(
      initialScope(),
      initialFilters(),
      // The settings store doesn't persist the override flag itself, so
      // we infer it on mount: if the persisted scope doesn't match what
      // defaultScope() would have produced, the user must have
      // overridden.
      initialScope() !== defaultScope()
    )
  })

  // setScope is the single entry point for a USER-initiated scope change
  // (click or keyboard) — writes to the shared module so the sidebar's
  // scope radio updates instantly, AND sets scopeUserOverride so the
  // navigation-tracking $effect stops re-narrowing the board.
  function setScope(s: Scope) {
    setScopeShared(s)
  }

  function resetScopeToContext() {
    clearScopeOverride()
    untrack(() => {
      setScopeShared(defaultScope())
    })
  }
  let lanes = $state<Record<string, KanbanCard[]>>({})
  let loading = $state(true)
  let errorMsg = $state('')
  let moveError = $state('')
  // Config-write failures (column + filter persistence) surface here so a
  // silent saveConfig rejection can't leave the user believing their
  // board layout persisted when it didn't.
  let configError = $state('')
  // The Go-side PluginRawQuery caps results at maxPluginQueryRows (5000) —
  // a defense-in-depth memory safeguard, not a deliberate design limit.
  // When the result hits the cap we surface a non-blocking hint so the
  // user knows to narrow the scope (Vault → Notebook/Section/Page) rather
  // than silently missing tasks.
  let truncated = $state(false)
  // Raw row count from the last query, surfaced in the truncation banner
  // so the copy stays in sync with the Go-side cap (maxPluginQueryRows)
  // instead of hard-coding a literal that can drift if the cap is tuned.
  let loadedCount = $state(0)

  // --- Plugin settings: read vs write paths (#133) ---
  // READ path: ctx.getPluginSettings() resolves per-active-notebook — vault
  // notebooks get the vault-scoped config verbatim; linked notebooks get the
  // deep-merge of vault defaults + the co-located <root>/.system/config.yaml
  // (linked wins per-key). The initial mount reads synchronously from the
  // vault config store (below); the re-resolution effect handles switches.
  // WRITE path: always vault-scoped via updatePluginSetting (#120). Silt
  // NEVER writes the co-located file (it is read-only / user-authored).
  function initialColumns(): string[] {
    const cfgCols = settings.config?.plugins?.plugin_settings?.['silt-kanban']
      ?.columns as string[] | undefined
    return cfgCols && cfgCols.length ? [...cfgCols] : [...ALL_STATUSES]
  }
  let columns = $state<string[]>(initialColumns())

  // Filter state — persisted (debounced) to config so a board reload restores
  // the active filter selection. Same resolution pattern as columns (#133).
  function initialFilters(): KanbanFilters {
    const f = settings.config?.plugins?.plugin_settings?.['silt-kanban']
      ?.filters as Partial<KanbanFilters> | undefined
    return {
      owners: f?.owners ?? [],
      priorities: f?.priorities ?? [],
      dueDate: f?.dueDate ?? '',
      tags: f?.tags ?? []
    }
  }

  // Per-active-notebook plugin settings re-resolution (#133): on mount AND
  // on a vault ↔ linked switch (or a linked-config:changed event), re-read
  // the merged settings from ctx.getPluginSettings() and sync columns +
  // filters. The synchronous init above provides a fast first paint from
  // the vault config store; this async resolution then reconciles with the
  // per-notebook merge — critical for the case where the app opens directly
  // on a linked notebook, where the vault defaults are NOT the right columns.
  //
  // Write guard: the resolution only writes columns/filters when the values
  // actually differ from the current state. This prevents a spurious reload
  // when a vault notebook's async resolution returns the same data as the
  // synchronous init (same-source resolve = no-op for state), while still
  // applying overrides when the data differs (linked notebook at mount or
  // on switch).
  let settingsEpoch = $state(0)
  $effect(() => {
    void ctx.activeNotebook // track navigation
    void settingsEpoch // track linked-config:changed
    let cancelled = false
    ctx
      .getPluginSettings()
      .then((s) => {
        if (cancelled) return
        const cfgCols = s.columns as string[] | undefined
        const nextCols =
          cfgCols && cfgCols.length ? [...cfgCols] : [...ALL_STATUSES]
        if (JSON.stringify(nextCols) !== JSON.stringify(columns)) {
          columns = nextCols
        }
        const f = s.filters as Partial<KanbanFilters> | undefined
        const nextFilters: KanbanFilters = {
          owners: f?.owners ?? [],
          priorities: f?.priorities ?? [],
          dueDate: f?.dueDate ?? '',
          tags: f?.tags ?? []
        }
        if (JSON.stringify(nextFilters) !== JSON.stringify(getKanbanState().filters)) {
          // Do NOT write to the local `filters` $derived directly — that
          // is a no-op since `filters` is derived from the shared module
          // (#323). Writing to the shared module via setFiltersShared
          // propagates through the $derived and triggers the reload
          // $effect at line ~347.
          // Mirror into the shared module so the sidebar's checkboxes
          // pick up the per-notebook override (#133).
          setFiltersShared(nextFilters)
        }
      })
      .catch((err) => {
        // Fail loud: an unparseable co-located config rejects the IPC promise
        // (GetPluginSettingsForNotebook returns a real error, not a silent
        // default). Surface it via errorMsg so the user sees their broken file
        // instead of a half-loaded board with no signal.
        if (cancelled) return
        errorMsg = err instanceof Error ? err.message : String(err)
      })
    return () => {
      cancelled = true
    }
  })

  // Re-resolve when a co-located linked config is edited externally (#133).
  // The Go watcher emits linked-config:changed after invalidating the cache.
  $effect(() => {
    const off = EventsOn('linked-config:changed', () => {
      settingsEpoch++
    })
    return off
  })

  // Card selected for the slide-out detail panel (null = closed).
  let selectedCard = $state<KanbanCard | null>(null)

  let totalCards = $derived(
    Object.values(lanes).reduce((sum, lane) => sum + lane.length, 0)
  )

  // Distinct owner / tag option lists derived from the currently-loaded
  // cards (a single query feeds both the board and these option lists via
  // the GROUP_CONCAT tags subquery, so no extra round-trip is needed).
  let allOwners = $derived.by(() => {
    const set = new Set<string>()
    for (const lane of Object.values(lanes)) {
      for (const c of lane) if (c.owner) set.add(c.owner)
    }
    return [...set].sort()
  })
  let allTags = $derived.by(() => {
    const set = new Set<string>()
    for (const lane of Object.values(lanes)) {
      for (const c of lane) {
        if (c.tags) for (const t of c.tags.split('|')) if (t) set.add(t)
      }
    }
    return [...set].sort()
  })

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

  // Monotonic token so concurrent reload() calls can identify their own
  // response vs a later one. Without this, a slow page-scope query landing
  // after a fast vault-scope query would silently overwrite the fresh data.
  let loadSeq = 0

  async function reload() {
    const my = ++loadSeq
    loading = true
    errorMsg = ''
    try {
      const { sql, params } = buildQuery(scope, filters, {
        activeNotebook: ctx.activeNotebook,
        activeSection: ctx.activeSection,
        activePage: ctx.activePage,
        today: ctx.today
      })
      const { rows, truncated: wasTruncated } = await ctx.sqliteQuery(
        sql,
        params
      )
      // A newer reload (e.g. a rapid scope switch) has started; abandon
      // this stale response so it can't clobber the fresh data.
      if (my !== loadSeq) return
      const bucket: Record<string, KanbanCard[]> = {}
      for (const col of columns) bucket[col] = []
      for (const r of rows as unknown as KanbanCard[]) {
        // SQLite stores pinned as INTEGER (0/1); coerce to boolean so the
        // card shape matches the interface and `!card.pinned` toggles work.
        const card: KanbanCard = { ...r, pinned: !!r.pinned }
        if (bucket[card.status]) bucket[card.status].push(card)
      }
      lanes = bucket
      truncated = wasTruncated
      loadedCount = (rows as unknown[]).length
    } catch (e) {
      if (my !== loadSeq) return
      errorMsg = e instanceof Error ? e.message : String(e)
    } finally {
      if (my === loadSeq) loading = false
    }
  }

  // Reload whenever scope, the active nav, or any active filter changes. The
  // effect fires on mount too, so there's no separate onMount load.
  $effect(() => {
    void scope
    void ctx.activeNotebook
    void ctx.activeSection
    void ctx.activePage
    void filters.owners
    void filters.priorities
    void filters.dueDate
    void filters.tags
    reload()
  })

  // #124: auto-narrow the scope to follow the active nav level until the
  // user manually overrides it. narrowScopeTo is a no-op when scopeUserOverride
  // is set, so manual picks stick. When the override is set but the chosen
  // scope's nav level goes inactive (e.g. navigating off the page), the
  // shared module needs to reset to the new default so the board never shows
  // an empty, invalid scope — but only if the user already overrode (we
  // don't fight an auto-narrow mid-track).
  $effect(() => {
    void ctx.activeNotebook
    void ctx.activeSection
    void ctx.activePage
    void scopeUserOverride
    untrack(() => {
      if (!scopeUserOverride) {
        // Auto-narrow: writes via narrowScopeTo so the override flag stays
        // untouched. The next manual click will flip it.
        narrowScopeTo(defaultScope())
        return
      }
      if (isScopeDisabled(scope)) {
        // Override is set but the chosen scope is no longer valid (the user
        // navigated off the page, etc.) — fall back to the new default
        // and clear the override so subsequent nav re-narrows.
        clearScopeOverride()
        setScopeShared(defaultScope())
      }
    })
  })

  // --- Filter persistence (debounced) ---
  // Apply immediately to the board, but defer the config write so rapid
  // checkbox toggles don't hammer the plugin-setting write. 500ms of quiet commits.
  // Writes flow into the shared module first (so the sidebar's checkboxes
  // update instantly) and into config.yaml after the debounce.
  let saveFiltersTimer: ReturnType<typeof setTimeout> | null = null
  // Clear the pending debounce on unmount so a board torn down within the
  // 500ms window never fires a stale persistFilters against a dead component
  // (which would otherwise leak a write into the next-mounted board's test /
  // race a real freshly-opened notebook).
  onDestroy(() => {
    if (saveFiltersTimer) clearTimeout(saveFiltersTimer)
    if (saveScopeTimer) clearTimeout(saveScopeTimer)
  })
  function handleFiltersChange(f: KanbanFilters) {
    // Do NOT write to the local `filters` $derived directly — that is a
    // no-op since `filters` derives from the shared module (#323).
    // setFiltersShared propagates through the $derived and triggers the
    // reload $effect.
    setFiltersShared(f)
    if (saveFiltersTimer) clearTimeout(saveFiltersTimer)
    saveFiltersTimer = setTimeout(() => {
      void persistFilters(f)
    }, 500)
  }
  async function persistFilters(f: KanbanFilters) {
    if (!settings.config) return
    configError = ''
    // Atomic Go-side read-modify-write of just this plugin's setting: cannot
    // clobber a concurrent external config.yaml edit (#120).
    const ok = await updatePluginSetting('silt-kanban', 'filters', f)
    if (!ok) configError = settings.error || 'Failed to save filters'
  }

  // --- Column management ---
  let menuCol = $state<string | null>(null)
  let renamingCol = $state<string | null>(null)
  let renameValue = $state('')
  let colDragIndex = $state<number | null>(null)

  function toggleColMenu(col: string) {
    menuCol = menuCol === col ? null : col
  }
  function startRename(col: string) {
    renamingCol = col
    renameValue = col
    menuCol = null
  }
  function commitRename() {
    const old = renamingCol
    const v = renameValue.trim()
    renamingCol = null
    if (!old || !v || v === old || columns.includes(v)) return
    columns = columns.map((c) => (c === old ? v : c))
    void persistColumns()
  }
  function cancelRename() {
    renamingCol = null
  }
  async function addColumn() {
    const name = window.prompt('New column name')?.trim()
    if (!name || columns.includes(name)) return
    columns = [...columns, name]
    await persistColumns()
  }
  async function removeColumn(col: string) {
    menuCol = null
    if (
      !window.confirm(
        `Remove column "${laneLabel(col)}"? Cards keep their status.`
      )
    )
      return
    columns = columns.filter((c) => c !== col)
    await persistColumns()
  }
  async function persistColumns() {
    if (!settings.config) return
    configError = ''
    // Atomic Go-side read-modify-write of just this plugin's setting (#120).
    const ok = await updatePluginSetting('silt-kanban', 'columns', [...columns])
    if (!ok) configError = settings.error || 'Failed to save columns'
  }

  // Scope persistence (debounced, #323). The scope value lives in the
  // shared module; the board's setScope() flips the override flag AND
  // Scope persistence (debounced, #323). The scope value lives in the
  // shared module; the board's setScope() flips the override flag AND
  // schedules a debounced persist so a reload restores the user's pick
  // and the sidebar radio reflects the persisted value (#323 AC #4).
  //
  // The first-run guard mirrors Calendar.svelte's modeLoaded pattern
  // (Calendar.svelte:240-258): the $effect fires on mount before the
  // user has touched anything, and the persisted value is by definition
  // what we just loaded — writing it back is a wasted config.yaml
  // mutation. Skip the first run.
  let saveScopeTimer: ReturnType<typeof setTimeout> | null = null
  let scopePersisted = $state(false)
  $effect(() => {
    const s = scope
    if (!scopePersisted) {
      scopePersisted = true
      return
    }
    if (saveScopeTimer) clearTimeout(saveScopeTimer)
    saveScopeTimer = setTimeout(() => {
      void persistScope(s)
    }, 500)
  })
  async function persistScope(s: Scope) {
    if (!settings.config) return
    const ok = await updatePluginSetting('silt-kanban', 'scope', s)
    if (!ok) configError = settings.error || 'Failed to save scope'
  }

  // Column drag-reorder: a dedicated handle on each header sets the source
  // index; dropping on another header splices the array and persists. Kept
  // separate from card DnD (which keys off dragCard) so the two can't clash.
  function onColDragStart(e: DragEvent, i: number) {
    colDragIndex = i
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', `col:${i}`)
    }
  }
  function onColDragOver(e: DragEvent, i: number) {
    if (colDragIndex === null) return
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
  }
  function onColDrop(e: DragEvent, i: number) {
    if (colDragIndex === null) return
    e.preventDefault()
    const from = colDragIndex
    colDragIndex = null
    if (from === i) return
    const next = [...columns]
    const [moved] = next.splice(from, 1)
    next.splice(i, 0, moved)
    columns = next
    void persistColumns()
  }

  // --- Drag-and-drop state ---
  let dragCard: KanbanCard | null = null
  let dragFromStatus: TaskStatus | null = null
  let dragOverStatus: TaskStatus | null = null
  let dragOverIndex = -1
  let draggingId = $state<string | null>(null)

  // --- Keyboard status change (a11y) ---
  // ArrowLeft/Right directly move the focused card between lanes;
  // Enter/Space opens the detail panel (navigation lives behind the panel's
  // "Open in editor" button so the source jump is an explicit action).
  let liveMessage = $state('')

  function onDragStart(e: DragEvent, card: KanbanCard, fromStatus: TaskStatus) {
    dragCard = card
    dragFromStatus = fromStatus
    draggingId = card.id
    colDragIndex = null // card drag, not column drag
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', card.id)
    }
  }

  function onLaneDragOver(e: DragEvent, status: TaskStatus) {
    if (!dragCard) return
    // Custom (non-status) columns don't accept card drops — skip
    // preventDefault so the browser shows a "no-drop" cursor and the
    // drop event never fires. Matches the keyboard path's guard in
    // onCardKeydown (ALL_STATUSES.includes).
    if (!ALL_STATUSES.includes(status)) return
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
    // Defense-in-depth: onLaneDragOver already blocks custom columns,
    // but if a drop somehow fires, reject it here rather than sending
    // an invalid status to Go (which would reject + trigger a confusing
    // optimistic-move-then-revert error banner).
    if (!ALL_STATUSES.includes(toStatus)) {
      cleanupDrag()
      return
    }
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

  // Monotonic token so a failed earlier move can't revert over a later
  // optimistic move. Without this, rapid double-moves where call #1 fails
  // would restore prevLanes (captured before call #2's optimistic state),
  // wiping call #2's move as well. Mirrors loadSeq / progressSeq.
  let moveSeq = 0
  async function commitMove(
    card: KanbanCard,
    fromStatus: TaskStatus,
    toStatus: TaskStatus,
    targetIndex: number
  ) {
    const my = ++moveSeq
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

    liveMessage = `Task moved to ${laneLabel(toStatus)}`

    try {
      await ctx.updateBlockState(card.id, toStatus)
    } catch (e) {
      // A newer move started after this one; its optimistic state is
      // authoritative, so don't revert to the stale snapshot.
      if (my !== moveSeq) return
      moveError = e instanceof Error ? e.message : String(e)
      lanes = prevLanes
      liveMessage = 'Move failed — reverted.'
    }
  }

  // --- Keyboard navigation (a11y) ---
  // Cards are <div role="button">, so the browser does NOT fire onclick on
  // Enter/Space the way a real <button> would. We handle all three keys
  // explicitly here. (Pattern mirrors Calendar.svelte onCellKeydown.)
  function onCardKeydown(
    e: KeyboardEvent,
    card: KanbanCard,
    fromStatus: TaskStatus
  ) {
    const idx = columns.indexOf(fromStatus)
    if (e.key === 'ArrowRight') {
      e.preventDefault()
      const next = Math.min(idx + 1, columns.length - 1)
      if (next !== idx && ALL_STATUSES.includes(columns[next] as TaskStatus)) {
        void commitMove(card, fromStatus, columns[next] as TaskStatus, -1)
      }
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      const prev = Math.max(idx - 1, 0)
      if (prev !== idx && ALL_STATUSES.includes(columns[prev] as TaskStatus)) {
        void commitMove(card, fromStatus, columns[prev] as TaskStatus, -1)
      }
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      selectedCard = card
    }
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
    <!-- svelte-ignore a11y_no_static_element_interactions
         role="radiogroup" is a composite widget that handles arrow-key
         navigation for its radio children per WAI-ARIA APG. -->
    <div
      class="flex items-center gap-0.5 bg-surface border border-border-muted rounded-lg p-0.5 ml-2"
      role="radiogroup"
      aria-label="Board scope"
      tabindex="-1"
      onkeydown={onScopeKeydown}
    >
      {#each SCOPES as s}
        <button
          data-scope={s}
          onclick={() => setScope(s)}
          role="radio"
          aria-checked={scope === s}
          tabindex={scope === s ? 0 : -1}
          disabled={isScopeDisabled(s)}
          title={isScopeDisabled(s) ? `Select a ${String(s)} first` : undefined}
          class="px-2.5 py-1 rounded font-label-sm border-none cursor-pointer transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          class:bg-hover={scope === s}
          class:text-accent-primary-start={scope === s}
          class:text-text-muted={scope !== s}
        >
          {s === 'vault' ? 'Vault' : s[0].toUpperCase() + s.slice(1)}
        </button>
      {/each}
    </div>
    <!-- Add Column -->
    <button
      type="button"
      onclick={addColumn}
      class="flex items-center gap-1 px-2.5 py-1 rounded border border-border-muted bg-surface text-[12px] font-label-sm text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 transition-colors"
      aria-label="Add column"
    >
      <span class="material-symbols-outlined text-[16px]">add</span>
      <span>Column</span>
    </button>
    <span
      class="text-text-muted text-[12px] font-body-md ml-auto flex items-center gap-2"
    >
      <span>{scopeCrumb} · {totalCards} task{totalCards === 1 ? '' : 's'}</span>
      {#if scopeUserOverride}
        <button
          type="button"
          onclick={resetScopeToContext}
          aria-label="Reset board scope to follow navigation"
          title="Follow navigation"
          class="flex items-center gap-1 px-1.5 py-0.5 rounded border border-border-muted text-text-muted hover:text-accent-primary-start hover:border-accent-primary-start/40 transition-colors"
        >
          <span class="material-symbols-outlined text-[14px]">my_location</span>
          <span class="font-label-sm">Follow</span>
        </button>
      {/if}
    </span>
  </header>

  <FilterBar
    {filters}
    owners={allOwners}
    tags={allTags}
    onFiltersChange={handleFiltersChange}
  />

  {#if moveError}
    <div
      class="px-6 py-2 bg-error-bg border-b border-error-border text-error text-[12px] font-body-md"
      role="alert"
    >
      Couldn't move task: {moveError}
    </div>
  {/if}

  {#if configError}
    <div
      class="px-6 py-2 bg-yellow-500/10 border-b border-yellow-500/30 text-yellow-200 text-[12px] font-body-md flex items-center gap-2"
      role="status"
    >
      <span class="material-symbols-outlined text-[16px]">save</span>
      <span>Couldn't save board layout: {configError}</span>
    </div>
  {/if}

  {#if truncated}
    <div
      class="px-6 py-2 bg-yellow-500/10 border-b border-yellow-500/30 text-yellow-200 text-[12px] font-body-md flex items-center gap-2"
      role="status"
    >
      <span class="material-symbols-outlined text-[16px]">info</span>
      <span>
        Showing the first {loadedCount} tasks. Narrow the scope to a Notebook, Section,
        or Page to see tasks beyond the cap.
      </span>
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
        {#each columns as col, colIdx (col)}
          {@const laneCards = lanes[col] ?? []}
          <section
            class="flex flex-col min-w-[280px] flex-1 max-w-[400px] rounded-lg border border-border-muted bg-surface/50 {colDragIndex ===
            colIdx
              ? 'opacity-50'
              : ''}"
            role="group"
            aria-label={laneLabel(col)}
            ondragover={(e) => onLaneDragOver(e, col as TaskStatus)}
            ondrop={(e) => onLaneDrop(e, col as TaskStatus)}
            ondragleave={() => {
              if (dragOverStatus === (col as TaskStatus)) dragOverStatus = null
            }}
          >
            <!-- svelte-ignore a11y_no_static_element_interactions
                 Column drag-reorder is a pointer-only affordance; the header
                 exposes Rename/Remove via its menu button for keyboard users. -->
            <div
              class="flex items-center justify-between px-3 py-2.5 border-b border-border-muted"
              draggable="true"
              ondragstart={(e) => onColDragStart(e, colIdx)}
              ondragover={(e) => onColDragOver(e, colIdx)}
              ondrop={(e) => onColDrop(e, colIdx)}
              ondragend={() => (colDragIndex = null)}
            >
              <div class="flex items-center gap-2 min-w-0">
                <span
                  class="material-symbols-outlined text-[14px] text-text-muted cursor-grab active:cursor-grabbing shrink-0"
                  title="Drag to reorder"
                  spellcheck="false">drag_indicator</span
                >
                <span
                  class="w-2 h-2 rounded-full shrink-0"
                  class:bg-text-muted={col === 'TODO' ||
                    !ALL_STATUSES.includes(col as TaskStatus)}
                  class:bg-accent-secondary-start={col === 'DOING'}
                  class:bg-accent-primary-start={col === 'DONE'}
                ></span>
                {#if renamingCol === col}
                  <input
                    type="text"
                    bind:value={renameValue}
                    onkeydown={(e) => {
                      if (e.key === 'Enter') commitRename()
                      else if (e.key === 'Escape') cancelRename()
                    }}
                    onblur={commitRename}
                    class="bg-surface border border-accent-primary-start/40 rounded px-1.5 py-0.5 text-[11px] font-label-sm-bold uppercase tracking-widest text-text-primary outline-none w-28"
                    aria-label="Rename column"
                  />
                {:else}
                  <h2
                    class="font-label-sm-bold uppercase tracking-widest text-[11px] text-text-muted truncate cursor-text"
                    ondblclick={() => startRename(col)}
                    title="Double-click to rename"
                  >
                    {laneLabel(col)}
                  </h2>
                {/if}
                <span
                  class="bg-hover text-text-muted text-[10px] px-1.5 py-0.5 rounded-sm font-label-sm"
                  >{laneCards.length}</span
                >
              </div>
              <div class="relative shrink-0">
                <button
                  type="button"
                  onclick={() => toggleColMenu(col)}
                  aria-label="Column actions"
                  aria-expanded={menuCol === col}
                  aria-haspopup="true"
                  class="text-text-muted hover:text-text-primary transition-colors p-0.5"
                >
                  <span class="material-symbols-outlined text-[16px]"
                    >more_horiz</span
                  >
                </button>
                {#if menuCol === col}
                  <div
                    class="absolute right-0 top-full mt-1 z-50 min-w-[140px] bg-panel border border-border-muted rounded-lg shadow-xl py-1"
                    role="menu"
                  >
                    <button
                      type="button"
                      onclick={() => startRename(col)}
                      class="w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-hover text-[12px] font-label-sm text-text-primary"
                      role="menuitem"
                    >
                      <span class="material-symbols-outlined text-[14px]"
                        >edit</span
                      >
                      Rename
                    </button>
                    <button
                      type="button"
                      onclick={() => removeColumn(col)}
                      class="w-full text-left flex items-center gap-2 px-3 py-1.5 hover:bg-hover text-[12px] font-label-sm text-error"
                      role="menuitem"
                    >
                      <span class="material-symbols-outlined text-[14px]"
                        >delete</span
                      >
                      Remove
                    </button>
                  </div>
                {/if}
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
                  aria-label={`${card.clean_content}, ${laneLabel(col)}${card.owner ? `, owner ${card.owner}` : ''}${card.due_date ? `, due ${card.due_date}` : ''}${card.pinned ? ', pinned' : ''}. Arrow keys change status.`}
                  draggable="true"
                  animate:flip={{ duration: 200, easing: cubicOut }}
                  class="group relative bg-panel border border-border-muted rounded-lg p-3 cursor-grab transition-all duration-200 hover:bg-hover hover:-translate-y-px hover:shadow-lg focus:outline-none focus:ring-2 focus:ring-accent-primary-start/40 {card.status ===
                  'DOING'
                    ? 'border-l-2 border-l-accent-secondary-start'
                    : ''} {draggingId === card.id ? 'opacity-40 rotate-2' : ''}"
                  ondragstart={(e) => onDragStart(e, card, col as TaskStatus)}
                  ondragend={cleanupDrag}
                  onkeydown={(e) => onCardKeydown(e, card, col as TaskStatus)}
                  onclick={() => (selectedCard = card)}
                >
                  {#if card.pinned}
                    <span
                      class="material-symbols-outlined absolute top-2 right-2 text-[14px] text-accent-primary-start"
                      aria-label="pinned">push_pin</span
                    >
                  {/if}
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
                        class="material-symbols-outlined text-accent-primary-start text-[16px] {card.pinned
                          ? ''
                          : 'ml-auto'}">check_circle</span
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
                  {#if card.progress > 0}
                    <div class="h-0.5 bg-surface rounded overflow-hidden mb-2">
                      <div
                        class="h-full bg-accent-secondary-start transition-all"
                        style="width: {card.progress}%"
                      ></div>
                    </div>
                  {/if}
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
                    <div class="flex items-center gap-1.5">
                      {#if card.comments_count > 0}
                        <span
                          class="text-[9px] text-text-muted font-label-sm flex items-center gap-0.5"
                          title="{card.comments_count} comments"
                        >
                          <span class="material-symbols-outlined text-[12px]"
                            >chat_bubble</span
                          >
                          {card.comments_count}
                        </span>
                      {/if}
                      {#if card.links_count > 0}
                        <span
                          class="text-[9px] text-text-muted font-label-sm flex items-center gap-0.5"
                          title="{card.links_count} links"
                        >
                          <span class="material-symbols-outlined text-[12px]"
                            >link</span
                          >
                          {card.links_count}
                        </span>
                      {/if}
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
                </div>
              {/each}
              {#if laneCards.length === 0}
                <div
                  class="text-center text-text-muted text-[11px] font-body-md py-6 border border-dashed border-border-muted rounded-lg"
                >
                  No {laneLabel(col).toLowerCase()} tasks
                </div>
              {/if}
            </div>
          </section>
        {/each}
      </div>
    {/if}
  </div>
</div>

<CardDetailPanel
  card={selectedCard}
  {ctx}
  onClose={() => (selectedCard = null)}
  onMetaChanged={reload}
/>

{#if menuCol}
  <!-- Click-away for the column action menu. -->
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-40"
    aria-hidden="true"
    onclick={() => (menuCol = null)}
  ></div>
{/if}

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
