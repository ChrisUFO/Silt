<script lang="ts">
  import { tick, untrack } from 'svelte'
  import { FetchPageBlocks, RenamePage } from '../../wailsjs/go/main/App.js'
  import { EventsOn } from '../../wailsjs/runtime/runtime.js'
  import TipTapEditor from './TipTapEditor.svelte'
  import type { ParsedBlock } from '../lib/editor'
  import type { Editor } from 'svelte-tiptap'
  import type { ViewMode } from '../lib/tabs'
  import EditorUtilityBar from './editor/EditorUtilityBar.svelte'
  import {
    settings,
    toggleFocusMode,
    toggleFormatToolbar
  } from '../settings/store.svelte'

  interface Props {
    notebook: string
    section: string
    page: string
    /** Editor view for this tab (#195). Owned by App.svelte's TabEntry. */
    viewMode: ViewMode
    /** Toggle this tab's view mode (floating button). */
    onToggleViewMode?: () => void
    targetBlockId?: string
    targetKey?: string
    onBlockFocus?: (blockId: string, ancestors: string[]) => void
    onBlockBlur?: () => void
    activeFocusedBlockAncestors?: string[]
    onPageRenamed?: (newName: string) => void
    onFirstEdit?: () => void
    isActive?: boolean
    /** Forwarded to TipTapEditor; surfaces save-state changes (#167). */
    onSaveStateChange?: (state: {
      dirty: boolean
      error: string | null
    }) => void
  }

  let {
    notebook,
    section,
    page,
    viewMode,
    onToggleViewMode,
    targetBlockId = '',
    targetKey = '',
    onBlockFocus,
    onBlockBlur,
    activeFocusedBlockAncestors = [],
    onPageRenamed,
    onFirstEdit,
    isActive = true,
    onSaveStateChange
  }: Props = $props()

  // Editor bindings
  let editorInstance = $state<Editor | null>(null)
  let activeMarks = $state<Set<string>>(new Set())

  let showFormatToolbar = $derived(
    settings.config?.ui?.show_format_toolbar !== false
  )

  let blocks = $state<ParsedBlock[]>([])
  let loading = $state(false)
  let loadError = $state('')
  let containerEl = $state<HTMLDivElement | null>(null)
  // hasFirstEdit is intentionally NOT reset: each VirtualScrollContainer
  // instance is bound to one tab for its lifetime (the display:none
  // architecture mounts a fresh component per tab). The one-shot semantics
  // ensure edit-to-pin promotion fires exactly once per tab mount.
  let hasFirstEdit = false
  let handledTargetKey = $state('')

  $effect(() => {
    if (notebook && page) {
      untrack(() => loadPage())
    }
  })

  $effect(() => {
    if (targetBlockId && targetKey !== handledTargetKey) {
      scrollToBlock(targetKey)
    }
  })

  // Subscribe to block:changed events (#64). When an external mutation
  // (embed edit, external edit) changes a block on the current page, reload
  // the block list so the editor sees the update. The editor's own $effect
  // handles applying the update when the user is not actively editing.
  $effect(() => {
    // Read props at the top of the effect so it re-subscribes when the user
    // navigates to a different page (#64). Without this, the EventsOn closure
    // would filter against stale values after navigation.
    const nb = notebook,
      sec = section,
      pg = page
    const off = EventsOn(
      'block:changed',
      (ev: { notebook: string; section: string; page: string }) => {
        if (ev.notebook === nb && ev.section === sec && ev.page === pg) {
          loadPage()
        }
      }
    )
    return () => off()
  })

  async function loadPage() {
    loading = true
    loadError = ''
    const reqNotebook = notebook
    const reqSection = section
    const reqPage = page
    try {
      const result = await FetchPageBlocks(reqNotebook, reqSection, reqPage)
      if (notebook !== reqNotebook || page !== reqPage) return
      blocks = result || []
    } catch (e) {
      if (notebook !== reqNotebook || page !== reqPage) return
      loadError = e instanceof Error ? e.message : String(e)
    } finally {
      loading = false
    }
  }

  async function scrollToBlock(key: string) {
    handledTargetKey = key
    await tick()
    if (targetBlockId) {
      const el = document.querySelector(`[data-id="${targetBlockId}"]`)
      if (el instanceof HTMLElement) {
        el.scrollIntoView({ block: 'center', behavior: 'smooth' })
      }
    }
  }

  function handleBlocksUpdated(updatedBlocks: ParsedBlock[]) {
    blocks = updatedBlocks
    // Fire onFirstEdit on the first content change — used by the tab strip
    // to promote a preview tab to pinned (edit-to-pin, #142).
    if (!hasFirstEdit) {
      hasFirstEdit = true
      onFirstEdit?.()
    }
  }

  function formatDate(d: string): string {
    const parsed = new Date(d + 'T00:00:00')
    if (isNaN(parsed.getTime())) return d
    return parsed.toLocaleDateString('en-US', {
      weekday: 'long',
      month: 'long',
      day: 'numeric',
      year: 'numeric'
    })
  }

  // --- Inline title editing (#83) ---
  let titleEl = $state<HTMLHeadingElement | null>(null)
  let renameTimer: ReturnType<typeof setTimeout> | null = null
  let lastRenamedFrom = ''
  // Track whether the title is actively focused so we can guard reactive
  // re-patching from the `page` prop (#259). Without this guard, Svelte
  // patches the `<h1>` text whenever `page` changes — which happens after
  // every debounced rename round-trip, collapsing the caret to position 0.
  let titleFocused = $state(false)
  let displayTitle = $state(untrack(() => page))

  // Sync displayTitle from the page prop ONLY when the user is not editing.
  // When focused, the DOM is the source of truth (the user's caret position
  // must be preserved across rename round-trips).
  $effect(() => {
    if (!titleFocused) {
      displayTitle = page
    }
  })

  function handleFocusTitle() {
    if (titleEl) {
      titleEl.focus()
      // Select all text so typing replaces "Untitled"
      const range = document.createRange()
      range.selectNodeContents(titleEl)
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
    }
  }

  // Listen for the focus-page-title event (from sidebar page creation/rename).
  $effect(() => {
    const handler = () => handleFocusTitle()
    window.addEventListener('focus-page-title', handler)
    return () => window.removeEventListener('focus-page-title', handler)
  })

  function handleTitleInput() {
    if (!titleEl) return
    const newName = titleEl.textContent?.trim() ?? ''
    if (newName === '' || newName === page) return
    // Debounce the rename (500ms after last keystroke).
    if (renameTimer) clearTimeout(renameTimer)
    renameTimer = setTimeout(() => doRename(newName), 500)
  }

  function handleTitleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault()
      titleEl?.blur()
    } else if (e.key === 'Escape') {
      e.preventDefault()
      displayTitle = page
      if (titleEl) titleEl.textContent = page
      titleEl?.blur()
    }
  }

  function handleTitleBlur() {
    titleFocused = false
    if (renameTimer) {
      clearTimeout(renameTimer)
      renameTimer = null
    }
    const newName = titleEl?.textContent?.trim() ?? ''
    if (newName === '' || newName === page) {
      displayTitle = page
      if (titleEl) titleEl.textContent = page
      return
    }
    doRename(newName)
  }

  async function doRename(newName: string) {
    if (newName === page || newName === lastRenamedFrom) return
    lastRenamedFrom = newName
    try {
      await RenamePage(notebook, section, page, newName)
      onPageRenamed?.(newName)
      window.dispatchEvent(new CustomEvent('refresh-navigation'))
    } catch (e) {
      console.error('RenamePage failed:', e)
      displayTitle = page
      if (titleEl) titleEl.textContent = page
      lastRenamedFrom = ''
    }
  }

  let pageDate = $derived.by(() => {
    const dates = blocks
      .map((b) => b.file_date)
      .filter((d): d is string => !!d)
      .sort()
    if (dates.length > 0) return dates[0]
    return new Date().toISOString().slice(0, 10)
  })
</script>

<div
  class="flex-1 flex flex-col min-h-0 h-full overflow-hidden bg-void relative"
>
  {#if viewMode === 'edit' && showFormatToolbar}
    <EditorUtilityBar editor={editorInstance} {activeMarks} />
  {/if}

  <div
    bind:this={containerEl}
    class="silt-texture-surface flex-1 overflow-y-auto px-12 py-10 custom-scrollbar bg-void flex flex-col min-h-0"
  >
    <div class="relative z-[1] flex flex-col flex-1">
      <nav
        class="mb-6 flex items-center gap-1.5 text-text-muted/60 text-[11px] font-medium tracking-wider uppercase font-body"
      >
        <span class="hover:text-text-primary transition-colors cursor-pointer"
          >{notebook}</span
        >
        {#if section}
          <span class="material-symbols-outlined text-[12px] text-text-muted/30"
            >chevron_right</span
          >
          <span class="hover:text-text-primary transition-colors cursor-pointer"
            >{section}</span
          >
        {/if}
        <span class="material-symbols-outlined text-[12px] text-text-muted/30"
          >chevron_right</span
        >
        <span
          class="text-accent-primary-start/90 tracking-normal normal-case font-semibold"
          >{displayTitle}</span
        >
      </nav>

      <header class="mb-8">
        <h1
          bind:this={titleEl}
          contenteditable="true"
          spellcheck="false"
          oninput={handleTitleInput}
          onkeydown={handleTitleKeydown}
          onblur={handleTitleBlur}
          onfocus={() => (titleFocused = true)}
          class="font-headline-lg text-headline-lg text-text-primary tracking-tight mb-1 outline-none rounded-sm transition-colors"
          style="border-bottom: 1px solid transparent; padding-bottom: 1px;"
          aria-label="Page title"
        >
          {displayTitle}
        </h1>
        <p class="text-text-muted/60 text-sm font-body-sm">
          {formatDate(pageDate)}
        </p>
      </header>

      <div class="max-w-4xl w-full flex-1 flex flex-col gap-4">
        {#if loadError}
          <div
            class="text-error py-8 text-center font-body-md border border-error-border bg-error-bg rounded-lg flex flex-col items-center gap-3"
          >
            <div>Failed to load page: {loadError}</div>
            <button
              onclick={() => loadPage()}
              class="px-4 py-1.5 rounded-lg bg-error/20 border border-error-border text-error font-label-sm-bold hover:brightness-110 transition-all cursor-pointer"
            >
              Retry
            </button>
          </div>
        {:else}
          <TipTapEditor
            {notebook}
            {section}
            {page}
            {blocks}
            {activeFocusedBlockAncestors}
            {onBlockFocus}
            {onBlockBlur}
            onUpdate={handleBlocksUpdated}
            bind:editorInstance
            bind:activeMarks
            {viewMode}
            {onSaveStateChange}
          />
        {/if}

        {#if loading}
          <div class="flex justify-center py-6">
            <span class="text-accent-primary-start font-body-md animate-pulse"
              >Loading...</span
            >
          </div>
        {/if}
      </div>
    </div>
  </div>

  <!-- Floating Editor Actions Bar -->
  <div
    class="absolute right-6 z-40 flex items-center gap-1 p-1 bg-panel/60 backdrop-blur-md border border-border-muted/50 rounded-full shadow-lg transition-all duration-300 opacity-60 hover:opacity-100 hover:scale-105"
    class:top-4={!(viewMode === 'edit' && showFormatToolbar)}
    class:top-14={viewMode === 'edit' && showFormatToolbar}
  >
    <!-- Focus Mode Toggle -->
    <button
      onclick={toggleFocusMode}
      class="h-8 w-8 flex items-center justify-center rounded-full transition-colors border-none bg-transparent cursor-pointer focus:outline-none hover:bg-hover"
      class:text-accent-primary-start={settings.config?.editor?.focus_mode ===
        true}
      class:text-text-muted={settings.config?.editor?.focus_mode !== true}
      title={settings.config?.editor?.focus_mode === true
        ? 'Exit Focus Mode (Ctrl+Shift+D)'
        : 'Enter Focus Mode (Ctrl+Shift+D)'}
      aria-label="Toggle Focus Mode"
    >
      <span class="material-symbols-outlined text-[18px]"
        >center_focus_strong</span
      >
    </button>

    <!-- Format Toolbar Toggle -->
    <button
      onclick={toggleFormatToolbar}
      class="h-8 w-8 flex items-center justify-center rounded-full transition-colors border-none bg-transparent cursor-pointer focus:outline-none hover:bg-hover"
      class:text-accent-primary-start={showFormatToolbar}
      class:text-text-muted={!showFormatToolbar}
      title={showFormatToolbar
        ? 'Hide Formatting Toolbar (Ctrl+Shift+F)'
        : 'Show Formatting Toolbar (Ctrl+Shift+F)'}
      aria-label="Toggle Formatting Toolbar"
    >
      <span class="material-symbols-outlined text-[18px]">text_format</span>
    </button>

    <div class="w-px h-4 bg-border-muted mx-0.5"></div>

    <!-- View Mode Toggle -->
    <button
      onclick={() => onToggleViewMode?.()}
      class="h-8 w-8 flex items-center justify-center rounded-full transition-colors border-none bg-transparent cursor-pointer focus:outline-none hover:bg-hover text-text-muted"
      title={viewMode === 'edit'
        ? 'View Markdown Source (Ctrl+Shift+V)'
        : 'View Rich Text (Ctrl+Shift+V)'}
      aria-label="Toggle View Mode"
      aria-pressed={viewMode === 'source'}
      aria-keyshortcuts="Ctrl+Shift+V"
    >
      <span class="material-symbols-outlined text-[18px]">
        {viewMode === 'edit' ? 'code' : 'menu_book'}
      </span>
    </button>
  </div>
</div>

<style>
  h1[contenteditable] {
    transition: border-bottom-color 0.25s ease-in-out;
  }
  h1[contenteditable]:hover {
    border-bottom-color: var(--color-border-muted) !important;
  }
  h1[contenteditable]:focus {
    border-bottom-color: var(--color-accent-primary-start) !important;
  }
  h1[contenteditable]:empty::before {
    content: 'Untitled';
    color: var(--color-text-muted);
    opacity: 0.4;
  }
</style>
