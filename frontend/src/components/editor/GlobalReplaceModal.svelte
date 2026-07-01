<script lang="ts">
  import { untrack } from 'svelte'
  import {
    SearchBlocksPaged,
    FetchPageBlocks,
    SaveFileBlocks
  } from '../../../wailsjs/go/main/App.js'
  import type { config, parser } from '../../../wailsjs/go/models.js'
  import {
    buildMatcher,
    applyReplace,
    type MatcherOptions
  } from '../../lib/editor/search/globalReplaceMatcher'
  import {
    getAllEditors,
    getEditor
  } from '../../lib/editor/editorRegistry.svelte'
  import { pushNotification } from '../../notifications/store.svelte'

  interface Props {
    onClose: () => void
    initialQuery?: string
  }
  let { onClose, initialQuery = '' }: Props = $props()

  let findText = $state(untrack(() => initialQuery))
  let replaceText = $state('')
  let caseSensitive = $state(false)
  let wholeWord = $state(false)
  let regexp = $state(false)

  interface Match {
    blockId: string
    snippet: string // the matched snippet (before)
    after: string // the proposed replacement preview
    accepted: boolean
  }
  interface PageGroup {
    key: string
    notebook: string
    section: string
    page: string
    // Block root ('vault' | 'linked:<id>'), threaded from SearchBlocksPaged
    // (#343). Global replace is vault-scoped; a non-'vault' source here is a
    // defense-in-depth refusal — the server filter already excludes linked
    // hits, but this guards against a future filter relaxation.
    source: string
    matches: Match[]
    accepted: boolean // per-page accept-all toggle
  }

  interface BatchEntry {
    notebook: string
    section: string
    page: string
    blocks: parser.ParsedBlock[]
  }

  let groups = $state<PageGroup[]>([])
  let loading = $state(false)
  let applying = $state(false)
  let statusMessage = $state('')
  // True after the user edits find/replace inputs, marking the displayed
  // m.after values as out of date until Preview is clicked again.
  let previewStale = $state(false)
  // Full match count when SearchBlocksPaged truncated the preview (0 otherwise).
  let truncatedCount = $state(0)
  // Session revert log: one entry per Apply batch; each batch holds the
  // original blocks of every page touched in that batch. Undo restores a
  // batch. The batch is appended to the log on success OR on mid-batch
  // failure (with whatever pages were already persisted) so a partial
  // Apply still leaves an undo path for the written pages.
  let revertLog = $state<BatchEntry[][]>([])

  /** Preview: search FTS5 for the find term, group matches by page, compute
   *  before/after. Linked-notebook pages are flagged read-only (skipped on apply). */
  async function preview(): Promise<void> {
    const matcher = buildMatcher({
      findText,
      caseSensitive,
      wholeWord,
      regexp
    })
    if (!findText.trim() || !matcher) {
      groups = []
      truncatedCount = 0
      return
    }
    loading = true
    statusMessage = ''
    truncatedCount = 0
    try {
      const res = await SearchBlocksPaged(findText, 0, 200, {
        notebook: '',
        section: '',
        tag: '',
        type: '',
        sort: '',
        vaultOnly: true
      })
      const results = res.results || []
      // res.total is the full match count; results is capped at the page limit.
      if (res.total > results.length) {
        truncatedCount = res.total
      }
      const byPage = new Map<string, PageGroup>()
      for (const r of results) {
        const key = `${r.notebook}\x00${r.section}\x00${r.page}`
        if (!byPage.has(key)) {
          byPage.set(key, {
            key,
            notebook: r.notebook,
            section: r.section,
            page: r.page,
            source: r.source ?? 'vault',
            matches: [],
            accepted: true
          })
        }
        const grp = byPage.get(key)!
        // Re-derive the before/after from the snippet's marked-up text.
        const before = (r.snippet || '').replace(/<\/?mark>/g, '')
        const after = applyReplace(before, matcher, replaceText)
        grp.matches.push({
          blockId: r.id,
          snippet: before,
          after,
          accepted: true
        })
      }
      groups = [...byPage.values()]
      previewStale = false
    } catch (e) {
      statusMessage = `Preview failed: ${e}`
    } finally {
      loading = false
    }
  }

  function togglePageAccept(grp: PageGroup): void {
    grp.accepted = !grp.accepted
    for (const m of grp.matches) m.accepted = grp.accepted
  }

  /** Apply approved replacements: per affected page, FetchPageBlocks → replace in
   *  clean_text → SaveFileBlocks. Records originals for the Undo action. */
  async function apply(): Promise<void> {
    applying = true
    statusMessage = ''
    const matcher = buildMatcher({
      findText,
      caseSensitive,
      wholeWord,
      regexp
    })
    if (!matcher) {
      applying = false
      return
    }

    // Flush every mounted editor whose page is a target BEFORE writing, so
    // the replace reads the real current content (including unsaved edits)
    // instead of stale disk content. Without this, an editor's pending
    // autosave would silently clobber the replace — or the reload would
    // discard the user's unsaved edits (#345).
    const targetKeys = new Set(
      groups
        .filter(
          (g) => g.source === 'vault' && g.matches.some((m) => m.accepted)
        )
        .map((g) => g.key)
    )
    const dirtyEditors = getAllEditors().filter(
      (e) => targetKeys.has(e.key) && e.isDirty()
    )
    const unflushable = new Set<string>()
    const flushedAny = dirtyEditors.length > 0
    if (flushedAny) {
      const results = await Promise.all(
        dirtyEditors.map(async (e) => ({ key: e.key, clean: await e.flush() }))
      )
      for (const r of results) {
        if (!r.clean) unflushable.add(r.key)
      }
    }

    let pagesChanged = 0
    let replacements = 0
    let openPagesTouched = 0
    let skippedUnflushable = 0
    const newLog: BatchEntry[] = []
    try {
      for (const grp of groups) {
        const acceptedMatches = grp.matches.filter((m) => m.accepted)
        if (acceptedMatches.length === 0) continue
        // Defense in depth (#343): refuse linked-notebook pages. The server
        // VaultOnly filter already excludes them from the preview, but this
        // guarantees a future filter relaxation can't silently let a replace
        // touch a read-only external mount.
        if (grp.source !== 'vault') {
          continue
        }
        // A dirty editor that couldn't flush (save error) is skipped: writing
        // it from disk content would silently discard the user's unsaved edits.
        if (unflushable.has(grp.key)) {
          skippedUnflushable++
          continue
        }
        // Fetch the page's full block list (the search result is a subset).
        const blocks = await FetchPageBlocks(
          grp.notebook,
          grp.section,
          grp.page
        )
        const matchIds = new Set(acceptedMatches.map((m) => m.blockId))
        // Snapshot ORIGINAL blocks ONCE before any mutation so the revert log
        // captures the true pre-edit state — not a partially-mutated one.
        const originalBlocks = blocks.map((bb) => ({ ...bb }))
        let changed = false
        for (const b of blocks) {
          if (!matchIds.has(b.id)) continue
          const before = b.clean_text ?? ''
          const after = applyReplace(before, matcher, replaceText)
          if (before !== after) {
            b.clean_text = after
            // Do NOT overwrite raw_text — the renderer (RenderFileContent)
            // re-derives the bullet/checkbox prefix from the original raw_text.
            // Overwriting it with the clean replacement strips the prefix →
            // silent data corruption (list bullets vanish on save).
            changed = true
            replacements++
          }
        }
        if (changed) {
          // Arm the editor's one-shot external-reload flag BEFORE the write.
          // SaveFileBlocks emits `block:changed` (and returns) before the
          // frontend reload runs, but the flag must already be set when the
          // editor's sync $effect fires so it bypasses the focused-edit guard.
          // Setting it after the await would let the reload consume/clear an
          // absent flag, then re-arm a flag with no matching reload — leaking
          // until a LATER unrelated block:changed clobbered the user's edits.
          const editor = getEditor(grp.key)
          if (editor) {
            editor.forceExternalReload()
            openPagesTouched++
          }
          await SaveFileBlocks(grp.notebook, grp.section, grp.page, blocks)
          // Record the page only after it has persisted, so a mid-batch
          // failure leaves newLog holding exactly the written pages.
          newLog.push({
            notebook: grp.notebook,
            section: grp.section,
            page: grp.page,
            blocks: originalBlocks
          })
          pagesChanged++
        }
      }
      statusMessage = `Replaced ${replacements} across ${pagesChanged} page${
        pagesChanged === 1 ? '' : 's'
      }${
        skippedUnflushable > 0
          ? `; skipped ${skippedUnflushable} page${
              skippedUnflushable === 1 ? '' : 's'
            } with unsaved edits that couldn't be saved first`
          : ''
      }`
      // Surface when the replace touched a page the user has open, so the
      // reload is never a silent surprise (#345). Only claim edits were saved
      // first when a flush actually ran.
      if (openPagesTouched > 0) {
        pushNotification({
          kind: 'info',
          message: `Replaced ${replacements} match${
            replacements === 1 ? '' : 'es'
          } in ${openPagesTouched} open page${
            openPagesTouched === 1 ? '' : 's'
          }.${flushedAny ? ' Your unsaved edits were saved first.' : ''}${
            skippedUnflushable > 0
              ? ` ${skippedUnflushable} page${
                  skippedUnflushable === 1 ? '' : 's'
                } skipped (unsaved edits couldn't be saved).`
              : ''
          }`
        })
      }
    } catch (e) {
      statusMessage = `Apply failed: ${e}`
    } finally {
      // Commit the batch (full or partial) before clearing applying. If
      // SaveFileBlocks threw mid-loop, newLog already holds the pages that
      // were persisted — Undo must stay available for them.
      if (newLog.length > 0) revertLog = [...revertLog, newLog]
      applying = false
    }
  }

  /** Revert the last Apply batch by restoring the snapshotted original blocks
   *  for every page that was touched in that batch. */
  async function undo(): Promise<void> {
    if (revertLog.length === 0) return
    applying = true
    try {
      const lastBatch = revertLog[revertLog.length - 1]
      for (const entry of lastBatch) {
        await SaveFileBlocks(
          entry.notebook,
          entry.section,
          entry.page,
          entry.blocks
        )
      }
      revertLog = revertLog.slice(0, -1)
      statusMessage = `Reverted last apply (${lastBatch.length} page${
        lastBatch.length === 1 ? '' : 's'
      }).`
    } catch (e) {
      statusMessage = `Undo failed: ${e}`
    } finally {
      applying = false
    }
  }

  const totalAccepted = $derived(
    groups.reduce((n, g) => n + g.matches.filter((m) => m.accepted).length, 0)
  )
  const canPreview = $derived(
    !!findText.trim() &&
      !!buildMatcher({ findText, caseSensitive, wholeWord, regexp })
  )

  // Invalidate the displayed m.after values when the user edits any input that
  // the replacements are derived from. findText must be tracked too: Apply
  // rebuilds the matcher from the live value, so retyping Find without
  // re-previewing would otherwise write replacements the user never saw.
  // groups.length is read untracked so that populating or clearing the list
  // itself does not flip the stale flag.
  $effect(() => {
    findText
    replaceText
    caseSensitive
    wholeWord
    regexp
    untrack(() => {
      if (groups.length > 0) previewStale = true
    })
  })
</script>

<div
  class="fixed inset-0 bg-black/50 z-[150] flex items-start justify-center pt-20"
>
  <button
    tabindex="-1"
    aria-label="Close"
    onclick={onClose}
    class="absolute inset-0 cursor-default border-none p-0 bg-transparent"
  ></button>
  <div
    role="dialog"
    aria-modal="true"
    aria-label="Find and replace across vault"
    tabindex="-1"
    class="relative w-full max-w-3xl glass-palette border border-border-zinc rounded-xl shadow-2xl overflow-hidden flex flex-col max-h-[600px]"
    style="background: color-mix(in srgb, var(--color-panel) 95%, transparent);"
  >
    <div class="px-4 py-3 border-b border-border-muted bg-void/30 space-y-2">
      <div class="flex items-center gap-2">
        <input
          bind:value={findText}
          type="text"
          placeholder="Find across vault"
          aria-label="Find"
          autocomplete="off"
          spellcheck="false"
          class="flex-1 bg-transparent border border-border-muted rounded-lg px-3 py-1.5 text-text-primary text-[14px] font-body-md focus:outline-none focus:border-accent-primary-start/60"
        />
        <button
          type="button"
          onclick={preview}
          disabled={!canPreview || loading}
          class="px-3 py-1.5 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start text-[13px] font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >{loading ? 'Searching…' : 'Preview'}</button
        >
      </div>
      <div class="flex items-center gap-2">
        <input
          bind:value={replaceText}
          type="text"
          placeholder="Replace with"
          aria-label="Replace with"
          autocomplete="off"
          spellcheck="false"
          class="flex-1 bg-transparent border border-border-muted rounded-lg px-3 py-1.5 text-text-primary text-[14px] font-body-md focus:outline-none focus:border-accent-primary-start/60"
        />
        <label
          class="flex items-center gap-1 text-[12px] text-text-muted cursor-pointer"
          ><input
            type="checkbox"
            bind:checked={caseSensitive}
            class="accent-[#10b981]"
          />Aa</label
        >
        <label
          class="flex items-center gap-1 text-[12px] text-text-muted cursor-pointer"
          ><input
            type="checkbox"
            bind:checked={wholeWord}
            class="accent-[#10b981]"
          />ab</label
        >
        <label
          class="flex items-center gap-1 text-[12px] text-text-muted cursor-pointer"
          ><input
            type="checkbox"
            bind:checked={regexp}
            class="accent-[#10b981]"
          />.*</label
        >
        <button
          type="button"
          onclick={onClose}
          class="ml-auto text-text-muted hover:text-text-primary cursor-pointer px-2"
          aria-label="Close">✕</button
        >
      </div>
      <p class="text-[11px] text-text-muted font-body-md px-1">
        Replaces every occurrence in accepted blocks, not just the previewed
        snippet.
      </p>
    </div>

    <div class="flex-1 overflow-y-auto custom-scrollbar">
      {#if truncatedCount > 0}
        <div
          class="px-4 py-2 text-[12px] text-text-muted bg-void/20 border-b border-border-muted/60"
          role="status"
          aria-live="polite"
        >
          Showing first {groups.length} pages — {truncatedCount} total matches. Refine
          your search to replace all.
        </div>
      {/if}
      {#if groups.length === 0}
        <div
          class="text-text-muted text-center py-10 font-body-md select-none text-[13px]"
        >
          {loading
            ? 'Searching…'
            : findText.trim()
              ? 'Click Preview to find matches.'
              : 'Enter text to find across the vault.'}
        </div>
      {:else}
        {#each groups as grp (grp.key)}
          <div class="border-b border-border-muted/60">
            <div class="flex items-center gap-2 px-4 py-2 bg-void/20">
              <input
                type="checkbox"
                checked={grp.accepted}
                onchange={() => togglePageAccept(grp)}
                class="accent-[#10b981]"
                aria-label="Accept all on this page"
              />
              <span
                class="text-[11px] uppercase tracking-widest font-label-sm-bold text-text-muted truncate"
              >
                {grp.notebook} › {grp.section || '—'} › {grp.page}
              </span>
              <span class="ml-auto text-[11px] text-text-muted"
                >{grp.matches.length}</span
              >
            </div>
            {#each grp.matches as m (m.blockId)}
              <div class="px-4 py-1.5 flex items-start gap-2 text-[13px]">
                <input
                  type="checkbox"
                  bind:checked={m.accepted}
                  class="accent-[#10b981] mt-0.5"
                  aria-label="Accept this match"
                />
                <div class="flex-1 min-w-0">
                  <div class="text-text-muted line-through truncate">
                    {m.snippet}
                  </div>
                  <div class="text-accent-primary-start truncate">
                    {m.after}
                  </div>
                </div>
              </div>
            {/each}
          </div>
        {/each}
      {/if}
    </div>

    {#if statusMessage}
      <div
        class="px-4 py-2 text-[12px] font-body-md text-text-muted border-t border-border-muted bg-void/20"
        role="status"
        aria-live="polite"
      >
        {statusMessage}
      </div>
    {/if}

    <div
      class="flex items-center justify-between gap-2 px-4 py-3 border-t border-border-muted bg-surface/10"
    >
      <span class="text-[12px] text-text-muted"
        >{totalAccepted} match{totalAccepted === 1 ? '' : 'es'} selected</span
      >
      <div class="flex items-center gap-2">
        {#if revertLog.length > 0}
          {@const lastBatch = revertLog[revertLog.length - 1]}
          <button
            type="button"
            onclick={undo}
            disabled={applying}
            class="px-3 py-1.5 rounded-lg text-text-muted hover:text-text-primary text-[13px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40"
          >
            Restore last apply ({lastBatch.length}
            {lastBatch.length === 1 ? 'page' : 'pages'})
          </button>
        {/if}
        {#if previewStale}
          <span
            class="text-[11px] text-text-muted italic"
            role="status"
            aria-live="polite"
          >
            Preview is stale — click Preview to refresh
          </span>
        {/if}
        <button
          type="button"
          onclick={apply}
          disabled={totalAccepted === 0 || applying || previewStale}
          class="px-4 py-1.5 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start text-[13px] font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >{applying
            ? 'Applying…'
            : `Replace ${totalAccepted || ''}`.trim()}</button
        >
      </div>
    </div>
  </div>
</div>
