<script lang="ts">
  // KanbanSidebar — the primary sidebar for the Kanban view (#323).
  // Renders three sections:
  //
  //   1. SAVED BOARDS — list of named board configurations. Click applies
  //      scope+filters via the shared state. "+ Save current…" captures
  //      the live scope+filters into a new board (debounced persist via
  //      updatePluginSetting, atomic #120).
  //   2. SCOPE — radio list mirroring the header segmented control.
  //      Roving tabindex + Arrow/Home/End + Space/Enter.
  //   3. ACTIVE FILTERS — checkboxes that mirror FilterBar state via the
  //      shared state. Toggling is instant on both sides.
  //
  // The sidebar derives its live state from kanbanSharedState.svelte so
  // every interaction is bidirectional: a filter toggle here updates the
  // FilterBar AND the board's SQL query; toggling a chip in the FilterBar
  // updates this sidebar's checkboxes.
  import { onMount, tick } from 'svelte'
  import type { PluginContext, PluginManifest } from '../../sdk'
  import { settings, updatePluginSetting } from '../../../settings/store.svelte'
  import type { SavedBoard, Scope, KanbanFilters } from './types'
  import {
    getKanbanState,
    setScope,
    setFilters,
    clearFilters,
    applySavedBoard
  } from './kanbanSharedState.svelte'
  import { PRIORITY_LABELS } from './types'

  interface Props {
    ctx: PluginContext
    manifest?: PluginManifest
  }

  let { ctx, manifest }: Props = $props()

  // Live shared state.
  let liveScope = $derived(getKanbanState().scope)
  let liveFilters = $derived(getKanbanState().filters)
  let liveOverride = $derived(getKanbanState().scopeUserOverride)

  // Saved boards — persisted under `plugins.plugin_settings.silt-kanban.boards[]`.
  // Cap the persisted set so the sidebar stays scannable and config.yaml
  // doesn't grow unbounded (#326 item 3). commitNewBoard no-ops at the cap
  // rather than silently evicting an old board.
  const MAX_BOARDS = 50
  let savedBoards = $state<SavedBoard[]>([])
  let atLimit = $derived(savedBoards.length >= MAX_BOARDS)
  let newBoardName = $state('')
  let newBoardComposing = $state(false)
  let saveError = $state('')
  // Auto-focus ref so the user can start typing the moment they click
  // "+ Save current…" (no extra click into the input). Mirrors the
  // Sidebar's notebook-create modal which auto-focuses its input.
  let newBoardInput = $state<HTMLInputElement | null>(null)

  // Owners and tags — board-scoped option lists bridged into shared state
  // by Kanban.svelte (#326 item 2). Reading them here as $derived means
  // the quick-toggles always match the current board (no vault-wide
  // query, no toggle that filters to nothing).
  let owners = $derived(getKanbanState().boardOwners)
  let tags = $derived(getKanbanState().boardTags)

  function loadFromSettings() {
    const raw = settings.config?.plugins?.plugin_settings?.['silt-kanban']
      ?.boards as SavedBoard[] | undefined
    if (!Array.isArray(raw)) {
      savedBoards = []
      return
    }
    // Validate on load: drop entries missing required fields (#323 hardening).
    // A malformed board (e.g. hand-edited YAML with a typo) would otherwise
    // be applied via applySavedBoard and write garbage into the shared
    // module. Re-seeding the valid set keeps the user's stored boards
    // self-healing on next persist.
    const valid = raw.filter(isValidBoard)
    const dropped = raw.length - valid.length
    if (dropped > 0) {
      // Best-effort re-persist so the YAML catches up to the in-memory set.
      void persistBoards(valid)
    }
    savedBoards = valid
  }

  // Stable fingerprint for a scope+filters pair (#323 polish). Used to
  // mark a saved board as "active" when its scope+filters match the live
  // shared state. Avoids per-render JSON.stringify on every board in the
  // list — that allocation is wasted when nothing has changed.
  function boardFingerprint(scope: Scope, filters: KanbanFilters): string {
    return [
      scope,
      filters.owners.join('|'),
      filters.priorities.join('|'),
      filters.dueDate,
      filters.tags.join('|')
    ].join('\u0000')
  }
  let liveFingerprint = $derived(boardFingerprint(liveScope, liveFilters))
  function isBoardActive(b: SavedBoard): boolean {
    return boardFingerprint(b.scope, b.filters) === liveFingerprint
  }

  function isValidBoard(b: unknown): b is SavedBoard {
    if (!b || typeof b !== 'object') return false
    const r = b as Record<string, unknown>
    if (typeof r.id !== 'string' || r.id.length === 0) return false
    if (typeof r.name !== 'string' || r.name.length === 0) return false
    if (
      r.scope !== 'vault' &&
      r.scope !== 'notebook' &&
      r.scope !== 'section' &&
      r.scope !== 'page'
    ) {
      return false
    }
    const f = r.filters
    if (!f || typeof f !== 'object') return false
    const fr = f as Record<string, unknown>
    if (!Array.isArray(fr.owners)) return false
    if (!Array.isArray(fr.priorities)) return false
    if (typeof fr.dueDate !== 'string') return false
    if (!Array.isArray(fr.tags)) return false
    return true
  }

  onMount(() => {
    loadFromSettings()
  })

  async function persistBoards(next: SavedBoard[]) {
    const ok = await updatePluginSetting('silt-kanban', 'boards', next)
    if (!ok) saveError = settings.error || 'Failed to save boards'
    else saveError = ''
  }

  async function commitNewBoard() {
    // Cap the persisted set (#326 item 3). No-op at the limit rather than
    // silently evicting an old board the user may still want.
    if (atLimit) return
    const name = newBoardName.trim()
    if (!name) {
      newBoardComposing = false
      return
    }
    const id =
      typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `board-${Date.now()}-${Math.random().toString(36).slice(2)}`
    const next: SavedBoard = {
      id,
      name,
      scope: liveScope,
      filters: { ...liveFilters }
    }
    const updated = [...savedBoards, next]
    savedBoards = updated
    await persistBoards(updated)
    newBoardName = ''
    newBoardComposing = false
  }

  function cancelNewBoard() {
    newBoardComposing = false
    newBoardName = ''
  }

  function activateBoard(b: SavedBoard) {
    applySavedBoard({ scope: b.scope, filters: b.filters })
  }

  async function deleteBoard(b: SavedBoard) {
    // Confirm before destroying a saved board — the user may have spent
    // time crafting a non-trivial scope+filter combination named for
    // a real workflow ("My Work", "Sprint 15"). One mis-click destroys
    // it; recovery means re-creating from scratch or hand-editing the
    // YAML. Matches Kanban.svelte's removeColumn UX (window.confirm).
    // Browser-native confirm() is acceptable here because delete is a
    // destructive single-occurrence action; richer UI (undo toast) is a
    // future polish opportunity once a toast system lands.
    if (!window.confirm(`Delete saved board "${b.name}"?`)) return
    const next = savedBoards.filter((x) => x.id !== b.id)
    savedBoards = next
    await persistBoards(next)
  }

  // --- Scope radio + filter quick-toggles -------------------------------

  const SCOPES: Scope[] = ['vault', 'notebook', 'section', 'page']

  // Roving tabindex cursor for the scope radiogroup. Reactive on
  // liveScope so external writes (e.g. Kanban.svelte's resetScopeToContext
  // → clearScopeOverride + setScopeShared) keep the focus cursor in sync
  // with the displayed selected radio. Without this, the user clicks
  // "Follow" in the board header → scope jumps to 'vault' visually but
  // the sidebar's Tab focus still lands on the previously-focused
  // (wrong) radio.
  let scopeFocusIdx = $derived(SCOPES.indexOf(liveScope))

  function isScopeDisabled(s: Scope): boolean {
    return s !== 'vault' && !ctx.activeNotebook
  }

  // Advance to the next enabled scope in the given direction. Skips
  // disabled scopes so the cursor never lands on an unreachable option
  // (e.g. when no notebook/section/page is active, 'notebook',
  // 'section', 'page' are all dimmed).
  function nextEnabledIdx(from: number, dir: 1 | -1): number {
    let i = from
    for (let n = 0; n < SCOPES.length; n++) {
      i = (i + dir + SCOPES.length) % SCOPES.length
      if (!isScopeDisabled(SCOPES[i] as Scope)) return i
    }
    return from // all disabled (shouldn't happen — vault is always enabled)
  }

  function onScopeKeydown(e: KeyboardEvent) {
    if (
      e.key === 'ArrowDown' ||
      e.key === 'ArrowRight' ||
      e.key === 'ArrowUp' ||
      e.key === 'ArrowLeft'
    ) {
      e.preventDefault()
      const dir: 1 | -1 =
        e.key === 'ArrowDown' || e.key === 'ArrowRight' ? 1 : -1
      const next = nextEnabledIdx(scopeFocusIdx, dir)
      const nextScope = SCOPES[next]
      if (nextScope)
        document
          .querySelector<HTMLElement>(`[data-scope-radio="${nextScope}"]`)
          ?.focus()
      return
    }
    if (e.key === 'Home') {
      e.preventDefault()
      const first = SCOPES.findIndex((s) => !isScopeDisabled(s))
      if (first >= 0)
        document
          .querySelector<HTMLElement>(`[data-scope-radio="${SCOPES[first]}"]`)
          ?.focus()
      return
    }
    if (e.key === 'End') {
      e.preventDefault()
      let last = -1
      for (let i = SCOPES.length - 1; i >= 0; i--) {
        if (!isScopeDisabled(SCOPES[i] as Scope)) {
          last = i
          break
        }
      }
      if (last >= 0)
        document
          .querySelector<HTMLElement>(`[data-scope-radio="${SCOPES[last]}"]`)
          ?.focus()
      return
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      // Activate the scope corresponding to the focused element (the
      // event's currentTarget), NOT the roving cursor. The cursor and
      // focus are normally in sync, but focus follows the cursor rather
      // than vice versa — and reading the event target is robust against
      // any cursor drift. Per WAI-ARIA APG radiogroup guidance, a
      // disabled radio must NOT respond to activation.
      const target = e.currentTarget as HTMLElement | null
      const targetScope = target?.getAttribute(
        'data-scope-radio'
      ) as Scope | null
      if (targetScope && !isScopeDisabled(targetScope)) setScope(targetScope)
      return
    }
  }

  function pickScope(s: Scope) {
    // pickScope is only called from the click handler, which is itself
    // guarded against disabled scopes. The internal call is therefore
    // safe without re-checking.
    setScope(s)
  }

  // Filter quick-toggles: each chip mirrors one entry in liveFilters.
  // Writes go through setFilters so the FilterBar updates.
  function toggleOwner(o: string) {
    const has = liveFilters.owners.includes(o)
    setFilters({
      ...liveFilters,
      owners: has
        ? liveFilters.owners.filter((x) => x !== o)
        : [...liveFilters.owners, o]
    })
  }

  function togglePriority(p: number) {
    const has = liveFilters.priorities.includes(p)
    setFilters({
      ...liveFilters,
      priorities: has
        ? liveFilters.priorities.filter((x) => x !== p)
        : [...liveFilters.priorities, p]
    })
  }

  function setDueDateChip(d: KanbanFilters['dueDate']) {
    setFilters({ ...liveFilters, dueDate: d })
  }

  function toggleTag(t: string) {
    const has = liveFilters.tags.includes(t)
    setFilters({
      ...liveFilters,
      tags: has
        ? liveFilters.tags.filter((x) => x !== t)
        : [...liveFilters.tags, t]
    })
  }

  // Columns footer — read-only column summary from the user's config.
  let columns = $derived(
    (settings.config?.plugins?.plugin_settings?.['silt-kanban']?.columns as
      | string[]
      | undefined) ?? ['TODO', 'DOING', 'DONE']
  )

  // Live-aria region announces filter changes (mirrors CalendarSidebar's
  // approach — single sr-only live region, updates only on real change).
  let liveMessage = $state('')
  let lastMsgJson = ''
  $effect(() => {
    const j = JSON.stringify({
      s: liveScope,
      f: liveFilters,
      b: savedBoards.length
    })
    if (j !== lastMsgJson) {
      lastMsgJson = j
      const f = liveFilters
      liveMessage = `Scope: ${liveScope}. Active filters: ${
        f.owners.length +
        f.priorities.length +
        (f.dueDate ? 1 : 0) +
        f.tags.length
      }. ${savedBoards.length} saved boards.`
    }
  })
</script>

<aside
  class="flex flex-col gap-4 px-3 py-3"
  aria-label="Kanban sidebar"
  data-test-kanban-sidebar
>
  <!-- SAVED BOARDS (#323 AC: list, highlight active, click applies, +
       Save current…) -->
  <section aria-labelledby="kanban-boards-heading">
    <h3
      id="kanban-boards-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Saved Boards
    </h3>
    <ul role="list" class="mt-1 space-y-0.5">
      {#each savedBoards as board (board.id)}
        {@const isActive = isBoardActive(board)}
        <li>
          <div
            class="flex items-center gap-1 px-1 py-0.5 rounded text-[12px] font-body-md cursor-pointer border border-transparent
              {isActive
              ? 'bg-accent-primary-glow border-accent-primary-start/30 text-accent-primary-start'
              : 'text-text-primary hover:bg-hover border-transparent'}"
            data-testid={`board-${board.id}`}
          >
            <button
              type="button"
              onclick={() => activateBoard(board)}
              aria-pressed={isActive}
              class="flex-1 text-left px-1.5 py-1 rounded cursor-pointer border-none bg-transparent"
            >
              {board.name}
            </button>
            <button
              type="button"
              onclick={() => deleteBoard(board)}
              aria-label="Delete board {board.name}"
              data-testid={`delete-board-${board.id}`}
              class="p-1 rounded text-text-muted hover:text-error border-none bg-transparent cursor-pointer"
            >
              <span class="material-symbols-outlined text-[12px]">delete</span>
            </button>
          </div>
        </li>
      {/each}
      {#if newBoardComposing}
        <li class="flex items-center gap-1 px-1 py-1">
          <input
            type="text"
            bind:this={newBoardInput}
            bind:value={newBoardName}
            placeholder="Board name…"
            data-testid="new-board-name"
            onkeydown={(e) => {
              if (e.key === 'Enter') void commitNewBoard()
              else if (e.key === 'Escape') cancelNewBoard()
            }}
            class="flex-1 px-1.5 py-1 rounded bg-surface border border-accent-primary-start/40 text-text-primary text-[12px] outline-none focus:border-accent-primary-start"
          />
          <button
            type="button"
            onclick={() => void commitNewBoard()}
            data-testid="commit-board"
            class="p-1 rounded text-accent-primary-start hover:bg-hover border-none bg-transparent cursor-pointer"
            aria-label="Save board"
          >
            <span class="material-symbols-outlined text-[12px]">check</span>
          </button>
          <button
            type="button"
            onclick={cancelNewBoard}
            data-testid="cancel-board"
            class="p-1 rounded text-text-muted hover:text-error border-none bg-transparent cursor-pointer"
            aria-label="Cancel"
          >
            <span class="material-symbols-outlined text-[12px]">close</span>
          </button>
        </li>
      {:else}
        <li>
          <button
            type="button"
            disabled={atLimit}
            aria-disabled={atLimit}
            title={atLimit
              ? 'Reached the 50-board limit — delete one to add another'
              : undefined}
            onclick={() => {
              if (atLimit) return
              newBoardComposing = true
              // Focus the input after Svelte commits the new DOM node.
              // tick() awaits the next microtask so the {#if} block has
              // already rendered the input by the time we focus it.
              void tick().then(() => newBoardInput?.focus())
            }}
            data-testid="new-board"
            class="w-full flex items-center justify-center gap-1 px-2 py-1 rounded text-[11px] font-label-sm text-text-muted hover:text-accent-primary-start cursor-pointer border border-dashed border-border-muted bg-transparent transition-colors disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:text-text-muted"
          >
            <span class="material-symbols-outlined text-[12px]">add</span>
            Save current…
          </button>
          {#if atLimit}
            <p
              class="px-2 mt-1 text-text-muted text-[10px] font-body-md"
              role="status"
              data-testid="board-limit-hint"
            >
              Reached the 50-board limit — delete one to add another
            </p>
          {/if}
        </li>
      {/if}
    </ul>
    {#if saveError}
      <p class="px-2 mt-1 text-error text-[10px] font-body-md" role="alert">
        {saveError}
      </p>
    {/if}
  </section>

  <!-- SCOPE (#323 AC: scope mirror stays in sync with header) -->
  <section aria-labelledby="kanban-scope-heading">
    <h3
      id="kanban-scope-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Scope
    </h3>
    <ul role="radiogroup" aria-label="Board scope" class="mt-1 space-y-0.5">
      {#each SCOPES as s, i (s)}
        {@const selected = liveScope === s}
        {@const disabled = !ctx.activeNotebook && s !== 'vault'}
        <li>
          <button
            type="button"
            role="radio"
            aria-checked={selected}
            aria-disabled={disabled}
            tabindex={i === scopeFocusIdx && !disabled ? 0 : -1}
            data-scope-radio={s}
            data-testid={`scope-${s}`}
            onclick={() => !disabled && pickScope(s)}
            onkeydown={onScopeKeydown}
            class="w-full flex items-center gap-2 px-2 py-1.5 rounded text-left text-[12px] font-body-md cursor-pointer border-none bg-transparent transition-colors
              {selected
              ? 'bg-accent-primary-glow text-accent-primary-start'
              : 'text-text-primary hover:bg-hover'}
              {disabled ? 'opacity-40 cursor-not-allowed' : ''}"
          >
            <span
              class="w-2 h-2 rounded-full"
              class:bg-accent-primary-start={selected}
              class:bg-text-muted={!selected}
            ></span>
            <span class="flex-1 capitalize">
              {s === 'vault'
                ? 'Vault'
                : s === 'notebook'
                  ? 'Notebook'
                  : s === 'section'
                    ? 'Section'
                    : 'Page'}
            </span>
            {#if selected && liveOverride}
              <span
                class="material-symbols-outlined text-[12px] text-accent-primary-start"
                title="Manual override — click Follow in the board header to track navigation"
                aria-label="Manual scope override">push_pin</span
              >
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  </section>

  <!-- ACTIVE FILTERS (#323 AC: bidirectional sync with FilterBar) -->
  <section aria-labelledby="kanban-filters-heading">
    <h3
      id="kanban-filters-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Active Filters
    </h3>
    <div class="mt-1 space-y-2">
      <!-- Owners -->
      {#if owners.length > 0}
        <div>
          <p
            class="px-2 text-[10px] font-label-sm-bold text-text-muted uppercase tracking-widest mb-1"
          >
            Owners
          </p>
          <ul class="space-y-0.5">
            {#each owners as o (o)}
              {@const checked = liveFilters.owners.includes(o)}
              <li>
                <label
                  class="flex items-center gap-2 px-2 py-1 rounded text-[12px] font-body-md cursor-pointer hover:bg-hover"
                >
                  <input
                    type="checkbox"
                    {checked}
                    onchange={() => toggleOwner(o)}
                    data-testid={`owner-${o}`}
                    class="rounded border-border-muted bg-surface"
                  />
                  <span class="text-text-primary">{o}</span>
                </label>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      <!-- Priorities -->
      <div>
        <p
          class="px-2 text-[10px] font-label-sm-bold text-text-muted uppercase tracking-widest mb-1"
        >
          Priority
        </p>
        <ul class="space-y-0.5">
          {#each [1, 2, 3] as p (p)}
            {@const checked = liveFilters.priorities.includes(p)}
            <li>
              <label
                class="flex items-center gap-2 px-2 py-1 rounded text-[12px] font-body-md cursor-pointer hover:bg-hover"
              >
                <input
                  type="checkbox"
                  {checked}
                  onchange={() => togglePriority(p)}
                  data-testid={`priority-${p}`}
                  class="rounded border-border-muted bg-surface"
                />
                <span class="text-text-primary"
                  >P{p} · {PRIORITY_LABELS[p] ?? 'Normal'}</span
                >
              </label>
            </li>
          {/each}
        </ul>
      </div>

      <!-- Due-date quick-pick -->
      <div>
        <p
          class="px-2 text-[10px] font-label-sm-bold text-text-muted uppercase tracking-widest mb-1"
        >
          Due
        </p>
        <ul
          role="radiogroup"
          aria-label="Due-date quick-pick"
          class="space-y-0.5"
        >
          {#each [{ v: '', l: 'All' }, { v: 'overdue', l: 'Overdue' }, { v: 'today', l: 'Today' }, { v: 'week', l: 'This Week' }, { v: 'none', l: 'No Date' }] as opt (opt.v)}
            <li>
              <button
                type="button"
                role="radio"
                aria-checked={liveFilters.dueDate === opt.v}
                data-testid={`due-${opt.v || 'all'}`}
                onclick={() =>
                  setDueDateChip(opt.v as KanbanFilters['dueDate'])}
                class="w-full flex items-center gap-2 px-2 py-1 rounded text-[12px] font-body-md cursor-pointer border-none bg-transparent text-left
                  {liveFilters.dueDate === opt.v
                  ? 'bg-accent-primary-glow text-accent-primary-start'
                  : 'text-text-primary hover:bg-hover'}"
              >
                <span
                  class="w-2 h-2 rounded-full"
                  class:bg-accent-primary-start={liveFilters.dueDate === opt.v}
                  class:bg-text-muted={liveFilters.dueDate !== opt.v}
                ></span>
                <span class="flex-1">{opt.l}</span>
              </button>
            </li>
          {/each}
        </ul>
      </div>

      <!-- Tags -->
      {#if tags.length > 0}
        <div>
          <p
            class="px-2 text-[10px] font-label-sm-bold text-text-muted uppercase tracking-widest mb-1"
          >
            Tags
          </p>
          <ul class="space-y-0.5">
            {#each tags as t (t)}
              {@const checked = liveFilters.tags.includes(t)}
              <li>
                <label
                  class="flex items-center gap-2 px-2 py-1 rounded text-[12px] font-body-md cursor-pointer hover:bg-hover"
                >
                  <input
                    type="checkbox"
                    {checked}
                    onchange={() => toggleTag(t)}
                    data-testid={`tag-${t}`}
                    class="rounded border-border-muted bg-surface"
                  />
                  <span class="text-text-primary">{t}</span>
                </label>
              </li>
            {/each}
          </ul>
        </div>
      {/if}

      <!-- Clear all -->
      {#if liveFilters.owners.length || liveFilters.priorities.length || liveFilters.dueDate || liveFilters.tags.length}
        <button
          type="button"
          onclick={clearFilters}
          data-testid="clear-filters"
          class="w-full flex items-center justify-center gap-1 px-2 py-1 rounded text-[11px] font-label-sm text-text-muted hover:text-error cursor-pointer border border-dashed border-border-muted bg-transparent transition-colors"
        >
          <span class="material-symbols-outlined text-[12px]">close</span>
          Clear all filters
        </button>
      {/if}
    </div>
  </section>

  <!-- Read-only column summary -->
  <section aria-labelledby="kanban-columns-heading">
    <h3
      id="kanban-columns-heading"
      class="px-2 font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
    >
      Columns
    </h3>
    <p class="px-2 mt-1 text-[11px] font-body-md text-text-muted">
      {columns.join(' · ')}
    </p>
  </section>

  <div class="sr-only" aria-live="polite">{liveMessage}</div>
</aside>
