<script lang="ts">
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

  interface Props {
    onClose: () => void
  }
  let { onClose }: Props = $props()

  let findText = $state('')
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
    matches: Match[]
    accepted: boolean // per-page accept-all toggle
  }

  let groups = $state<PageGroup[]>([])
  let loading = $state(false)
  let applying = $state(false)
  let statusMessage = $state('')
  // Session revert log: original blocks per applied page, for the Undo action.
  let revertLog = $state<
    {
      notebook: string
      section: string
      page: string
      blocks: parser.ParsedBlock[]
    }[]
  >([])

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
      return
    }
    loading = true
    statusMessage = ''
    try {
      const res = await SearchBlocksPaged(findText, 0, 200, {
        notebook: '',
        section: '',
        tag: '',
        type: '',
        sort: '',
        vaultOnly: true
      })
      const byPage = new Map<string, PageGroup>()
      for (const r of res.results || []) {
        const key = `${r.notebook}\x00${r.section}\x00${r.page}`
        if (!byPage.has(key)) {
          byPage.set(key, {
            key,
            notebook: r.notebook,
            section: r.section,
            page: r.page,
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
    let pagesChanged = 0
    let replacements = 0
    const newLog: typeof revertLog = []
    try {
      for (const grp of groups) {
        const acceptedMatches = grp.matches.filter((m) => m.accepted)
        if (acceptedMatches.length === 0) continue
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
          newLog.push({
            notebook: grp.notebook,
            section: grp.section,
            page: grp.page,
            blocks: originalBlocks
          })
          await SaveFileBlocks(grp.notebook, grp.section, grp.page, blocks)
          pagesChanged++
        }
      }
      revertLog = [...revertLog, ...newLog]
      statusMessage = `Replaced ${replacements} across ${pagesChanged} page${
        pagesChanged === 1 ? '' : 's'
      }`
    } catch (e) {
      statusMessage = `Apply failed: ${e}`
    } finally {
      applying = false
    }
  }

  /** Revert the last applied batch by restoring the snapshotted original blocks. */
  async function undo(): Promise<void> {
    if (revertLog.length === 0) return
    applying = true
    try {
      const last = revertLog[revertLog.length - 1]
      await SaveFileBlocks(last.notebook, last.section, last.page, last.blocks)
      revertLog = revertLog.slice(0, -1)
      statusMessage = 'Reverted the last applied page.'
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
          <button
            type="button"
            onclick={undo}
            disabled={applying}
            class="px-3 py-1.5 rounded-lg text-text-muted hover:text-text-primary text-[13px] font-label-sm-bold transition-colors cursor-pointer disabled:opacity-40"
            >Undo last</button
          >
        {/if}
        <button
          type="button"
          onclick={apply}
          disabled={totalAccepted === 0 || applying}
          class="px-4 py-1.5 rounded-lg bg-accent-primary-start/20 border border-accent-primary-start/40 text-accent-primary-start text-[13px] font-label-sm-bold hover:brightness-110 transition-all cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
          >{applying
            ? 'Applying…'
            : `Replace ${totalAccepted || ''}`.trim()}</button
        >
      </div>
    </div>
  </div>
</div>
