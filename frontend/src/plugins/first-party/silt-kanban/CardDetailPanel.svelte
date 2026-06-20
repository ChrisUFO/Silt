<script lang="ts">
  import { fly } from 'svelte/transition'
  import type { PluginContext, TaskStatus } from '../../sdk'

  // Mirrors the KanbanCard shape from Kanban.svelte. Re-declared (and
  // exported) here so the panel is self-documenting and type-checks without
  // a circular import back into the parent component.
  export interface KanbanCard {
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
    pinned: boolean
    progress: number
    comments_count: number
    links_count: number
    // Pipe-delimited raw tag paths from a GROUP_CONCAT subquery; absent
    // when the card has no tags.
    tags?: string
  }

  interface Props {
    card: KanbanCard | null
    ctx: PluginContext
    onClose: () => void
    // Called after a successful updateTaskMeta so the parent board can
    // re-query and reflect the new pin/progress on the card. Without this,
    // the board's lanes hold stale data until the next unrelated reload.
    onMetaChanged?: () => void
  }

  let { card, ctx, onClose, onMetaChanged }: Props = $props()

  const PRIORITY_LABELS: Record<number, string> = {
    1: 'Critical',
    2: 'Normal',
    3: 'Low'
  }

  function statusLabel(s: TaskStatus): string {
    return s === 'TODO' ? 'To Do' : s === 'DOING' ? 'In Progress' : 'Done'
  }
  function statusChipClass(s: TaskStatus): string {
    if (s === 'TODO') return 'text-text-muted border-border-muted bg-surface'
    if (s === 'DOING')
      return 'text-accent-secondary-start border-accent-secondary-start/30 bg-accent-secondary-glow'
    return 'text-accent-primary-start border-accent-primary-start/30 bg-accent-primary-glow'
  }

  let tagList = $derived(card?.tags ? card.tags.split('|').filter(Boolean) : [])

  // Local optimistic mirrors for the two mutable metadata fields (pin +
  // progress). The panel is the only writer for these while open, so an
  // optimistic update + revert-on-failure matches the board's `commitMove`
  // contract: the UI reflects the change immediately, and if the markdown
  // write fails (focus lock held, disk error) we revert + surface the
  // reason in an aria-live region instead of silently drifting.
  let pinState = $state(false)
  let progressState = $state(0)
  let metaError = $state('')
  // Pending flags disable the control while an IPC write is in-flight.
  // This serializes user interactions so two rapid pin toggles (or slider
  // changes) can't race on the Go side — LockFileWrite serializes writes
  // per file but preserves Go's IPC arrival order, not JS dispatch order,
  // so concurrent in-flight calls can land out-of-order and leave the disk
  // (last writer) out of sync with the optimistic UI state.
  let pinPending = $state(false)
  let progressPending = $state(false)
  $effect(() => {
    // Read the individual fields so Svelte 5's fine-grained reactivity
    // tracks them as deps — if the parent ever mutates card.pinned or
    // card.progress on the same object identity, the effect re-runs.
    void card?.pinned
    void card?.progress
    pinState = card?.pinned ?? false
    progressState = card?.progress ?? 0
    metaError = ''
  })

  async function togglePin() {
    if (!card || pinPending) return
    const prev = pinState
    pinState = !pinState
    pinPending = true
    metaError = ''
    try {
      await ctx.updateTaskMeta(card.id, { pinned: pinState })
      onMetaChanged?.()
    } catch (e) {
      pinState = prev
      metaError = e instanceof Error ? e.message : String(e)
    } finally {
      pinPending = false
    }
  }

  // Monotonic token so a failed earlier slider write can't revert over a
  // successful later one. With the slider disabled during writes, the two
  // can't overlap, but the guard is retained as defense-in-depth.
  let progressSeq = 0
  function onProgressChange(e: Event) {
    if (!card || progressPending) return
    const v = Number((e.target as HTMLInputElement).value)
    const prev = progressState
    const my = ++progressSeq
    progressState = v
    progressPending = true
    metaError = ''
    void (async () => {
      try {
        await ctx.updateTaskMeta(card.id, { progress: v })
        onMetaChanged?.()
      } catch (err) {
        if (my !== progressSeq) return
        progressState = prev
        metaError = err instanceof Error ? err.message : String(err)
      } finally {
        progressPending = false
      }
    })()
  }

  function openInEditor() {
    if (!card) return
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

  function onWindowKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && card) {
      e.preventDefault()
      onClose()
    }
  }

  // Esc-to-close listener is bound only while the panel is open (card
  // is non-null). When closed, no global keydown listener intercepts
  // Esc presses that other handlers (Settings, command palette) may need.
  $effect(() => {
    if (!card) return
    window.addEventListener('keydown', onWindowKeydown)
    return () => window.removeEventListener('keydown', onWindowKeydown)
  })
</script>

{#if card}
  <!-- Click-away scrim: a click on the area beside the panel closes it. -->
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-30 bg-black/30"
    aria-hidden="true"
    onclick={onClose}
  ></div>
  <div
    transition:fly={{ x: 320, duration: 200 }}
    class="fixed right-0 top-14 h-[calc(100vh-56px)] w-96 bg-panel border-l border-border-muted z-40 overflow-y-auto custom-scrollbar"
    role="dialog"
    aria-modal="true"
    aria-labelledby="card-detail-title"
  >
    <!-- Header -->
    <div
      class="flex items-start justify-between gap-2 px-5 py-4 border-b border-border-muted sticky top-0 bg-panel"
    >
      <div class="flex flex-col gap-1.5 min-w-0">
        {#if card.priority && card.priority <= 3}
          <span
            class="self-start px-1.5 py-0.5 border rounded-sm font-label-sm text-[9px] uppercase tracking-wide w-fit {statusChipClass(
              card.status
            )}"
          >
            {PRIORITY_LABELS[card.priority] ?? 'Normal'}
          </span>
        {/if}
        <h2
          id="card-detail-title"
          class="font-headline-md text-headline-md text-text-primary break-words"
        >
          {card.clean_content}
        </h2>
      </div>
      <button
        type="button"
        onclick={onClose}
        aria-label="Close detail panel"
        class="text-text-muted hover:text-text-primary transition-colors shrink-0"
      >
        <span class="material-symbols-outlined">close</span>
      </button>
    </div>

    <div class="px-5 py-4 space-y-6">
      {#if metaError}
        <div
          class="flex items-start gap-2 px-3 py-2 rounded border border-error-border bg-error-bg text-error text-[12px] font-body-md"
          role="alert"
        >
          <span class="material-symbols-outlined text-[14px] shrink-0"
            >error</span
          >
          <span>Couldn't save: {metaError}</span>
        </div>
      {/if}
      <!-- Metadata -->
      <section>
        <h3
          class="font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted mb-3"
        >
          Metadata
        </h3>
        <dl class="flex flex-col gap-2.5 text-[12px] font-label-sm">
          <div class="flex items-center justify-between">
            <dt class="text-text-muted">Status</dt>
            <dd
              class="flex items-center gap-1 px-2 py-0.5 border rounded-sm {statusChipClass(
                card.status
              )}"
            >
              {#if card.status === 'DOING'}
                <span class="material-symbols-outlined text-[14px]"
                  >radio_button_checked</span
                >
              {/if}
              {statusLabel(card.status)}
            </dd>
          </div>
          <div class="flex items-center justify-between">
            <dt class="text-text-muted">Owner</dt>
            <dd
              class="px-2 py-0.5 border rounded-sm text-text-primary border-border-muted bg-surface"
            >
              {card.owner || '—'}
            </dd>
          </div>
          <div class="flex items-center justify-between">
            <dt class="text-text-muted">Priority</dt>
            <dd class="text-text-primary">
              {card.priority
                ? (PRIORITY_LABELS[card.priority] ?? 'Normal')
                : '—'}
            </dd>
          </div>
          <div class="flex items-center justify-between">
            <dt class="text-text-muted">Due date</dt>
            <dd class="text-text-primary">{card.due_date || '—'}</dd>
          </div>
          <div class="flex items-center justify-between">
            <dt class="text-text-muted">Start date</dt>
            <dd class="text-text-primary">{card.start_date || '—'}</dd>
          </div>
          {#if tagList.length > 0}
            <div class="flex items-start justify-between gap-2">
              <dt class="text-text-muted shrink-0 pt-0.5">Tags</dt>
              <dd class="flex flex-wrap gap-1 justify-end">
                {#each tagList as tg (tg)}
                  <span
                    class="px-1.5 py-0.5 border rounded-sm text-[10px] text-accent-secondary-start border-accent-secondary-start/30 bg-accent-secondary-glow"
                    >{tg}</span
                  >
                {/each}
              </dd>
            </div>
          {/if}
        </dl>
      </section>

      <!-- Pin toggle -->
      <section>
        <button
          type="button"
          onclick={togglePin}
          disabled={pinPending}
          class="w-full flex items-center justify-between px-3 py-2 rounded border border-border-muted bg-surface hover:bg-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          aria-pressed={pinState}
        >
          <span
            class="flex items-center gap-2 text-[12px] font-label-sm text-text-primary"
          >
            <span class="material-symbols-outlined text-[16px]">push_pin</span>
            {pinState ? 'Pinned' : 'Pin'}
          </span>
          {#if pinState}
            <span
              class="material-symbols-outlined text-[16px] text-accent-primary-start"
              >check</span
            >
          {/if}
        </button>
      </section>

      <!-- Progress slider -->
      <section>
        <div class="flex items-center justify-between mb-2">
          <h3
            class="font-label-sm-bold uppercase tracking-widest text-[10px] text-text-muted"
          >
            Progress
          </h3>
          <span class="text-[11px] font-label-sm text-text-primary"
            >{progressState}%</span
          >
        </div>
        <input
          type="range"
          min="0"
          max="100"
          value={progressState}
          oninput={(e) => {
            if (!progressPending)
              progressState = Number(
                (e.currentTarget as HTMLInputElement).value
              )
          }}
          onchange={onProgressChange}
          disabled={progressPending}
          aria-label="Task progress"
          class="w-full accent-accent-secondary-start disabled:opacity-50"
        />
        <div
          class="mt-2 h-1 bg-surface border border-border-muted rounded overflow-hidden"
        >
          <div
            class="h-full bg-accent-secondary-start transition-all"
            style="width: {progressState}%"
          ></div>
        </div>
      </section>

      <!-- Counts -->
      <section class="flex items-center gap-4">
        <div class="flex items-center gap-1.5 text-text-muted">
          <span class="material-symbols-outlined text-[16px]">chat_bubble</span>
          <span class="text-[12px] font-label-sm">{card.comments_count}</span>
          <span class="text-[10px] font-label-sm text-text-muted">comments</span
          >
        </div>
        <div class="flex items-center gap-1.5 text-text-muted">
          <span class="material-symbols-outlined text-[16px]">link</span>
          <span class="text-[12px] font-label-sm">{card.links_count}</span>
          <span class="text-[10px] font-label-sm text-text-muted">links</span>
        </div>
      </section>

      <!-- Open in editor -->
      <section>
        <button
          type="button"
          onclick={openInEditor}
          class="w-full flex items-center justify-center gap-2 px-3 py-2 rounded border border-accent-primary-start/30 bg-accent-primary-glow text-accent-primary-start hover:brightness-110 transition-all font-label-sm-bold"
        >
          <span class="material-symbols-outlined text-[16px]">open_in_new</span>
          Open in editor
        </button>
      </section>

      <!-- Source context breadcrumb -->
      <section class="pt-2 border-t border-border-muted">
        <p class="text-[10px] font-label-sm text-text-muted break-all">
          {card.notebook} › {card.section} › {card.page}
        </p>
      </section>
    </div>
  </div>
{/if}
