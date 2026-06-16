<script lang="ts">
  // Template picker modal (#55). A centered overlay that lets the user browse
  // all available templates (built-in + custom), preview them, fill in any
  // placeholders, and insert the rendered content — either as a NEW PAGE or
  // INSERTED AT THE CURSOR. Two modes are driven by the entry point:
  //   - 'new-page': shows a page-name field, calls CreatePageFromTemplate, then
  //     onCreatedPage(pageName) so the parent navigates to the new page.
  //   - 'insert': calls RenderTemplateBlocks, then onInsertBlocks(blocks) so the
  //     parent (TipTapEditor) does blocksToDoc → editor.insertContent.
  //
  // Mirrors AppearanceTab.svelte's chrome (roving tabindex, ARIA, Cyber-Ink
  // tokens) and SearchModal.svelte's overlay structure. No hard-coded colors.
  import { onMount, onDestroy } from 'svelte'
  import {
    GetTemplate,
    RenderTemplate,
    CreatePageFromTemplate,
    RenderTemplateBlocks
  } from '../../wailsjs/go/main/App.js'
  import type { templates as tpl } from '../../wailsjs/go/models'
  import {
    templatesState,
    loadTemplates,
    templateStatus,
    setTemplateStatus,
    clearTemplateStatus
  } from './store.svelte'
  import { pushNotification } from '../notifications/store.svelte'

  interface Props {
    mode: 'new-page' | 'insert'
    notebook?: string
    section?: string
    onClose: () => void
    onCreatedPage?: (page: string) => void
    onInsertBlocks?: (blocks: import('../../wailsjs/go/models').parser.ParsedBlock[]) => void
  }

  let {
    mode,
    notebook = '',
    section = '',
    onClose,
    onCreatedPage,
    onInsertBlocks
  }: Props = $props()

  let searchQuery = $state('')
  let selectedId = $state<string | null>(null)
  let placeholderValues = $state<Record<string, string>>({})
  let pageName = $state('')
  let preview = $state('')
  let previewLoading = $state(false)
  let creating = $state(false)
  let listRefs: HTMLButtonElement[] = $state([])
  let searchEl: HTMLInputElement | null = $state(null)
  let pageNameEl: HTMLInputElement | null = $state(null)
  let previouslyFocused: HTMLElement | null = null

  function defaultPageName(): string {
    const d = new Date()
    const yyyy = d.getFullYear()
    const mm = String(d.getMonth() + 1).padStart(2, '0')
    const dd = String(d.getDate()).padStart(2, '0')
    return `Page ${yyyy}-${mm}-${dd}`
  }

  // Flat filtered list (search applies to title, description, category,
  // placeholder names, and id). Used for both display (grouped) and keyboard
  // navigation (flat index).
  let filtered = $derived(
    (templatesState.items ?? []).filter((t) => {
      if (!searchQuery.trim()) return true
      const q = searchQuery.toLowerCase()
      const haystack = [
        t.title,
        t.description ?? '',
        t.category,
        t.id,
        ...(t.placeholders ?? []).map((p) => p.name)
      ]
        .join(' ')
        .toLowerCase()
      return haystack.includes(q)
    })
  )

  // Group the filtered list by category for display, preserving the (Category,
  // Title) sort the backend already applied.
  let grouped = $derived.by(() => {
    const groups: { category: string; items: tpl.TemplateSummary[] }[] = []
    let currentCat = ''
    for (const t of filtered) {
      if (t.category !== currentCat) {
        currentCat = t.category
        groups.push({ category: currentCat, items: [] })
      }
      groups[groups.length - 1].items.push(t)
    }
    return groups
  })

  // The selected template's full metadata (from the listing summary —
  // placeholders come from here). The full body is fetched lazily for preview.
  let selectedSummary = $derived(
    selectedId ? (templatesState.items ?? []).find((t) => t.id === selectedId) ?? null : null
  )

  // Auto-select the first template when the list loads.
  $effect(() => {
    if (!selectedId && filtered.length > 0) {
      selectedId = filtered[0].id
    }
  })

  // Re-render the preview whenever the selection or placeholder values change.
  // A 100ms debounce coalesces rapid keystrokes in placeholder fields into a
  // single Wails IPC call so the bridge is not flooded on every keypress.
  $effect(() => {
    const id = selectedId
    const vars = { ...placeholderValues }
    if (!id) {
      preview = ''
      return
    }
    previewLoading = true
    // Abort guard: if selection/values change before the IPC resolves, drop
    // the stale result (the next $effect run will pick up the new one). The
    // debounce timer is also cleared so only the latest keystroke burst fires.
    let cancelled = false
    const timer = setTimeout(() => {
      RenderTemplate(id, vars)
        .then((r) => {
          if (!cancelled) {
            preview = r
            previewLoading = false
          }
        })
        .catch((e) => {
          if (!cancelled) {
            preview = ''
            previewLoading = false
            console.error('TemplatePicker: RenderTemplate failed:', e)
          }
        })
    }, 100)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  })

  // Reset placeholder values when the selection changes.
  $effect(() => {
    const id = selectedId
    // Touch selectedId so the effect re-runs on change; then reset.
    void id
    placeholderValues = {}
  })

  function selectTemplate(id: string): void {
    selectedId = id
  }

  // Keyboard navigation on the flat filtered list.
  function focusIndex(idx: number): void {
    if (filtered.length === 0) return
    const clamped = ((idx % filtered.length) + filtered.length) % filtered.length
    listRefs[clamped]?.focus()
  }

  function handleListKeydown(e: KeyboardEvent, idx: number): void {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      focusIndex(idx + 1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      focusIndex(idx - 1)
    } else if (e.key === 'Home') {
      e.preventDefault()
      focusIndex(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      focusIndex(filtered.length - 1)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      void handleConfirm()
    }
  }

  // Tab focus trap: keeps Tab/Shift+Tab cycling within the modal so focus
  // never escapes into the background editor while the picker is open.
  function handleTabTrap(e: KeyboardEvent): void {
    if (e.key !== 'Tab') return
    const modal = document.querySelector('[role="dialog"]')
    if (!modal) return
    const focusable = modal.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  async function handleConfirm(): Promise<void> {
    if (!selectedId) return
    creating = true
    try {
      if (mode === 'new-page') {
        const name = pageName.trim()
        if (!name) {
          setTemplateStatus({ kind: 'error', message: 'Please enter a page name.' })
          return
        }
        if (!notebook) {
          setTemplateStatus({ kind: 'error', message: 'Open a notebook first.' })
          return
        }
        await CreatePageFromTemplate(notebook, section, name, '', selectedId, { ...placeholderValues })
        window.dispatchEvent(new CustomEvent('focus-page-title'))
        onCreatedPage?.(name)
        onClose()
      } else {
        const blocks = await RenderTemplateBlocks(selectedId, { ...placeholderValues })
        onInsertBlocks?.(blocks)
        onClose()
      }
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setTemplateStatus({ kind: 'error', message: msg })
      pushNotification({
        kind: 'error',
        message:
          mode === 'new-page'
            ? `Failed to create page from template: ${msg}`
            : `Failed to render template: ${msg}`
      })
    } finally {
      creating = false
    }
  }

  function handleOverlayKeydown(e: KeyboardEvent): void {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      void handleConfirm()
    } else if (e.key === 'Tab') {
      handleTabTrap(e)
    }
  }

  onMount(() => {
    previouslyFocused = document.activeElement as HTMLElement | null
    if (templatesState.items.length === 0 && !templatesState.loading) {
      void loadTemplates()
    }
    if (mode === 'new-page' && !pageName) {
      pageName = defaultPageName()
    }
    setTimeout(() => {
      if (mode === 'new-page') {
        pageNameEl?.focus()
        if (pageNameEl) pageNameEl.select()
      } else {
        searchEl?.focus()
      }
    }, 0)
  })

  onDestroy(() => {
    previouslyFocused?.focus?.()
    clearTemplateStatus()
  })

  const confirmLabel = $derived(mode === 'new-page' ? 'Create Page' : 'Insert')
</script>

<svelte:window on:keydown={handleOverlayKeydown} />

<!-- Overlay backdrop -->
<div
  class="fixed inset-0 z-[180] flex items-center justify-center bg-black/50 backdrop-blur-sm"
  role="presentation"
>
  <!-- Modal panel -->
  <div
    class="flex max-h-[85vh] w-[min(900px,92vw)] flex-col overflow-hidden rounded-xl border border-border-zinc bg-bg-surface shadow-2xl"
    role="dialog"
    aria-modal="true"
    aria-label="Template picker"
  >
    <!-- Header -->
    <div class="flex items-center gap-3 border-b border-border-muted px-5 py-3">
      <span class="material-symbols-outlined text-[22px] text-accent-primary-start">description</span>
      <h2 class="flex-1 text-sm font-semibold text-text-primary">
        {mode === 'new-page' ? 'New Page from Template' : 'Insert Template'}
      </h2>
      <button
        onclick={onClose}
        class="rounded p-1 text-text-muted transition-colors hover:bg-bg-hover hover:text-text-primary"
        aria-label="Close template picker"
      >
        <span class="material-symbols-outlined text-[20px]">close</span>
      </button>
    </div>

    <!-- Body: list (left) + preview/form (right) -->
    <div class="flex min-h-0 flex-1">
      <!-- Left: search + list -->
      <div class="flex w-[320px] shrink-0 flex-col border-r border-border-muted">
        <div class="border-b border-border-muted px-4 py-3">
          <input
            bind:this={searchEl}
            bind:value={searchQuery}
            type="text"
            placeholder="Search templates…"
            class="w-full rounded-lg border border-border-zinc bg-bg-void px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent-primary-start focus:outline-none"
            aria-label="Search templates"
          />
        </div>
        <!-- Template list -->
        <div class="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          {#if templatesState.loading && templatesState.items.length === 0}
            <div class="px-3 py-8 text-center">
              <p class="text-sm text-text-muted">Loading templates…</p>
            </div>
          {:else if filtered.length === 0}
            <div class="px-3 py-8 text-center">
              <p class="text-sm text-text-muted">
                {templatesState.items.length === 0
                  ? 'No templates found. Drop a .md file into'
                  : 'No templates match your search.'}
              </p>
              {#if templatesState.items.length === 0}
                <p class="mt-1 text-xs text-text-muted">
                  <code>&lt;vault&gt;/.system/templates/</code>
                </p>
                <p class="mt-2 text-xs text-accent-primary-start">
                  See the <span class="underline">docs/TEMPLATES.md</span> authoring guide.
                </p>
              {/if}
            </div>
          {/if}
          {#each grouped as group (group.category)}
            <div class="mb-1 px-2 pt-2 text-xs font-medium uppercase tracking-wide text-text-muted">
              {group.category}
            </div>
            {#each group.items as t (t.id)}
              {@const flatIdx = filtered.indexOf(t)}
              <button
                bind:this={listRefs[flatIdx]}
                onclick={() => selectTemplate(t.id)}
                onkeydown={(e) => handleListKeydown(e, flatIdx)}
                onfocus={() => selectTemplate(t.id)}
                role="option"
                aria-selected={selectedId === t.id}
                tabindex={selectedId === t.id ? 0 : -1}
                class="flex w-full items-start gap-2 rounded-lg px-3 py-2 text-left transition-colors {selectedId === t.id
                  ? 'bg-bg-active'
                  : 'hover:bg-bg-hover'}"
              >
                <span class="material-symbols-outlined mt-0.5 text-[18px] text-accent-secondary-start">
                  {t.icon || 'description'}
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium text-text-primary">{t.title}</span>
                  {#if t.description}
                    <span class="block truncate text-xs text-text-muted">{t.description}</span>
                  {/if}
                </span>
                {#if t.source === 'builtin'}
                  <span class="shrink-0 text-[10px] uppercase text-text-muted">built-in</span>
                {/if}
              </button>
            {/each}
          {/each}
        </div>
      </div>

      <!-- Right: preview + form + action -->
      <div class="flex min-h-0 flex-1 flex-col">
        {#if selectedSummary}
          <!-- Preview pane -->
          <div class="min-h-0 flex-1 overflow-y-auto border-b border-border-muted px-5 py-4">
            {#if previewLoading}
              <p class="text-sm text-text-muted">Rendering preview…</p>
            {/if}
            <pre class="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-text-primary">{preview}</pre>
          </div>

          <!-- Placeholder form + page-name -->
          <div class="shrink-0 space-y-3 px-5 py-4">
            {#if mode === 'new-page'}
              <div>
                <label for="tpl-page-name" class="mb-1 block text-xs font-medium text-text-muted">
                  Page name
                </label>
                <input
                  id="tpl-page-name"
                  bind:this={pageNameEl}
                  bind:value={pageName}
                  type="text"
                  placeholder="e.g. Sprint Planning"
                  class="w-full rounded-lg border border-border-zinc bg-bg-void px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent-primary-start focus:outline-none"
                  onkeydown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault()
                      void handleConfirm()
                    }
                  }}
                />
              </div>
            {/if}
            {#if selectedSummary.placeholders && selectedSummary.placeholders.length > 0}
              <div class="space-y-2">
                <p class="text-xs font-medium text-text-muted">Placeholders</p>
                {#each selectedSummary.placeholders as ph (ph.name)}
                  <div>
                    <label for="tpl-ph-{ph.name}" class="mb-0.5 block text-xs text-text-muted">
                      {ph.name}{ph.required ? ' *' : ''}
                      {#if ph.description}<span class="opacity-70"> — {ph.description}</span>{/if}
                    </label>
                    <input
                      id="tpl-ph-{ph.name}"
                      type="text"
                      value={placeholderValues[ph.name] ?? ''}
                      oninput={(e) => (placeholderValues[ph.name] = e.currentTarget.value)}
                      placeholder={ph.default || `{{${ph.name}}}`}
                      class="w-full rounded-lg border border-border-zinc bg-bg-void px-3 py-1.5 text-sm text-text-primary placeholder:text-text-muted focus:border-accent-primary-start focus:outline-none"
                    />
                  </div>
                {/each}
              </div>
            {/if}
            <!-- Default placeholders note -->
            <p class="text-xs text-text-muted">
              <code>{'{{date}}'}</code>, <code>{'{{time}}'}</code>, <code>{'{{weekday}}'}</code> auto-fill with the current date/time.
            </p>
          </div>
        {:else}
          <div class="flex flex-1 items-center justify-center px-5">
            <p class="text-sm text-text-muted">Select a template to preview.</p>
          </div>
        {/if}

        <!-- Status + action bar -->
        <div class="flex items-center justify-between gap-3 border-t border-border-muted px-5 py-3">
          <div class="flex-1 text-xs" role="status" aria-live="polite">
            {#if templateStatus.message}
              <span class={templateStatus.kind === 'error' ? 'text-status-danger' : 'text-text-muted'}>
                {templateStatus.message}
              </span>
            {/if}
          </div>
          <button
            onclick={onClose}
            class="rounded-lg px-4 py-2 text-sm text-text-muted transition-colors hover:bg-bg-hover hover:text-text-primary"
          >
            Cancel
          </button>
          <button
            onclick={() => void handleConfirm()}
            disabled={!selectedId || creating || (mode === 'new-page' && !pageName.trim())}
            title="Confirm (Enter or Ctrl+Enter)"
            class="rounded-lg bg-accent-primary-start px-4 py-2 text-sm font-medium text-bg-void transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {creating ? '…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  </div>
</div>

<style>
  /* Import the store for the status live-region. The $state proxy is reactive
     in the template above; this style block is just for scoped polish. */
</style>
